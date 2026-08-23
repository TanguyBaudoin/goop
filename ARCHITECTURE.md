# Architecture

This documents how goop is actually built, why it's built that way, and
what to know before touching it. See
[`package-manager-spec.md`](package-manager-spec.md) for the full
requirements this implements (referenced throughout as EXF-xx, TR-xx,
CPT-xx, A1–A5); this document is about the *implementation*, not the
requirements themselves.

## Architectural invariants

These come directly from the spec (§7) and are load-bearing — code that
violates them is a bug, not a style choice:

1. **Authentication is a transport-layer concern, never business logic.**
   `internal/auth.Transport` wraps the HTTP client; nothing else in the
   codebase touches a credential. A manifest or bucket can never carry
   or leak one, because nothing reads credentials from manifest/bucket
   content in the first place.
2. **The lockfile is produced by the resolver, never hand-written.**
   `internal/lockfile` only ever gets written from an installed app's
   own `Record` (`Lock()` in `internal/installer/sync.go`) — never
   constructed from user input directly.
3. **The shim knows nothing about buckets, networks, or credentials.**
   `internal/shim` reads a sidecar `.shim` file and execs. That's the
   entire contract. This is deliberate: it's the most-invoked, most
   exposed component (hundreds of calls per build), so it stays trivial
   enough to reason about completely.
4. **The `pwsh` bridge is isolated: no business logic transits through
   it.** `internal/pwsh` only knows how to run a script with a fixed set
   of bound variables and a compat-function prelude; it has no idea
   what "installing a package" means. `internal/installer` decides
   *when* to call it and *what* to do with the result.

## Package map and data flow

```
cmd/goop (CLI) ──┬─→ internal/bucket ──→ internal/manifest (decode)
                  │        │
                  │        └─→ git.exe | internal/downloader+internal/archive (Git-less)
                  │
                  ├─→ internal/installer (orchestration)
                  │        ├─→ internal/downloader ──→ internal/auth.Transport ──→ internal/credstore
                  │        ├─→ internal/archive (zip/tar, native)
                  │        ├─→ internal/pwsh ──→ prelude.ps1 (7z.exe/msiexec.exe/dark.exe/innounp.exe)
                  │        ├─→ internal/shim + internal/shimbin (embedded shim.exe)
                  │        ├─→ internal/envvars (HKCU\Environment)
                  │        └─→ internal/vercmp (dependency constraint checks)
                  │
                  ├─→ internal/lockfile (EXF-10–13)
                  └─→ internal/minisign (EXF-41, verification only)

cmd/shim ──→ internal/shim (the actual shim logic, shared with what cmd/goop embeds)
```

`internal/installer` is the hub. Almost everything else is a leaf
package with no knowledge of the others — `internal/downloader` doesn't
know what a manifest is, `internal/archive` doesn't know what a bucket
is. This is deliberate: it's what let each of J0–J3 be built and
verified as an independent, real-world-tested slice without the earlier
ones needing to change shape underneath.

## The install pipeline (`internal/installer.installResolved`)

This is the piece worth understanding before changing anything, since
`Install` (bucket-resolved), `Sync` (lockfile-resolved, EXF-11), and
recursive dependency installs (A4) all funnel through it:

1. Idempotency check: if the exact version is already installed
   (`goop-install.json` present in its version dir), relink `current`
   and return — no re-download, no re-run of hooks.
2. Stage into `<app>/<version>.partial` (never the final path yet).
3. Download + hash-verify every URL (`FR-40`, mandatory, no opt-out).
4. `pre_install` (CPT-04, via `pwsh`).
5. `installer` hook (script or a literal file+args to run).
6. `persist` linking.
7. `post_install` — **still against the staging dir, still before the
   commit point.** This one is easy to get backwards: if `post_install`
   ran after the commit (rename + shims + shortcuts), a failing
   `post_install` would leave a fully-working install on disk while
   still reporting failure to the caller — a real bug this project
   shipped once and fixed (TR-04 requires true atomicity: everything
   or nothing visible, not "everything except the last hook").
8. **Commit point**: `os.Rename(staging, versionDir)`. Nothing before
   this line is allowed to have any externally-visible effect; nothing
   after it is allowed to fail silently.
9. Relink `current`, create shims, create shortcuts, apply
   `env_set`/`env_add_path`.

`Sync` and dependency-triggered installs reuse this exact function with
a `manifest.Resolved` built from a lockfile entry or a re-resolved
manifest instead of a fresh bucket lookup — there is no separate,
un-tested "fast path."

## Concurrency (A1)

Installs run concurrently across *different* apps (`internal/installer`
`InstallAll`/`Sync`, bounded by `GOOP_CONCURRENCY`, default 8). Three
things had to be made safe for this, discovered by actually running 200
real packages concurrently, not by inspection:

- **The download cache** (`internal/downloader`): two packages sharing
  an identical asset must not race on the same temp file. Serialized
  per cache-key (`sync.Map` of mutexes), not globally — different
  assets still download fully in parallel.
- **The embedded shim master file** (`internal/installer.ensureShimMaster`):
  written via temp-file-then-rename under a mutex, so a concurrent
  `os.Link` from another goroutine can never observe a partially-written
  file.
- **Delegated scripts** (`internal/pwsh.Run`): serialized process-wide.
  `msiexec.exe` holds a single machine-wide installer lock; two
  concurrent MSI extractions don't queue, one just fails with "Another
  program is being installed." Real Scoop never hit this because it
  never parallelized installs — it's a regression goop's own
  parallelism introduced, and downloads (the bulk of most packages'
  install time) never touch this lock, so the cost is narrow.

A second real bug surfaced the same way: `Invoke-ExternalCommand` (the
`pwsh` prelude's wrapper for shelling out, used by every delegated
script) used to `ReadToEnd()` stdout fully before touching stderr — the
textbook .NET Process deadlock. A child that writes enough to stderr
while stdout stays empty (which `msiexec` routinely does) can hang.
Fixed with concurrent async reads (`ReadToEndAsync` on both streams
before `WaitForExit`); there's a regression test that specifically
reproduces the ~1MB-of-stderr-with-empty-stdout pattern.

## Progress reporting

`internal/downloader` exposes three package-level callback vars
(`OnDownloadStart`/`OnDownloadProgress`/`OnDownloadDone`, mirroring the
existing `installer.Logf` pattern) rather than importing any UI concept
itself — same leaf-package discipline as everything else it doesn't
know about. A shared `tracker` accumulates bytes via `atomic.Int64` and
throttles callback firing to a fixed interval (150ms) under its own
mutex; for a chunked download all `numChunks` Range-request goroutines
report through the *same* tracker, so the throttle is per-download, not
per-writer — 4 concurrent chunks don't quadruple the callback rate.
Firing costs a few bytes of formatting and a terminal write at most 6-7
times/second regardless of transfer speed, so hooking it adds no
measurable latency to the transfer itself.

`cmd/goop` wires these into `internal/ui.Progress`, which renders one
live line per active download via ANSI cursor movement, and shares its
lock with ordinary log output (`Println`) so a log line printed while
bars are showing always erases them, prints above, and redraws them
below rather than tearing the terminal. Falls back to plain
non-redrawing output when `ui.Enabled` is false (redirected
output, `NO_COLOR`, etc.), same gate as all of `internal/ui`'s color
support.

## Proxy resolution

Same transport-layer-only discipline as authentication (invariant #1
above): `internal/downloader`'s `resolveProxy` and `internal/bucket`'s
`gitProxyArgs` are the only two places a proxy is ever consulted, both
built on `internal/paths`' persisted `Proxy`/`NoProxy` config fields
(`goop config set-proxy`/`set-no-proxy`). Precedence mirrors D4's root
resolution exactly: the standard `HTTP_PROXY`/`HTTPS_PROXY` environment
variables, when set, are deferred to entirely (via Go's own
`http.ProxyFromEnvironment` for downloads; git already honors them
itself for bucket clones/pulls) — goop's own persisted proxy is only
consulted when neither is set, so there's exactly one resolution to
reason about per call site, not a merge of two.

## Maven coordinate resolution

A second, manifest-free install path alongside the normal
bucket-resolved one: `installer.installSpec` (`internal/installer/installer.go`)
detects a `"maven:"` prefix on the raw spec string *before*
`manifest.ParseSpec` ever sees it (Maven's `:`-separated coordinate
grammar doesn't overlap with Scoop's `[bucket/]name[@constraint]` one),
and dispatches to `installMaven` (`internal/installer/maven.go`) instead
of `bucket.Resolve`. `internal/maven` is a leaf package (coordinate
parsing + URL building are pure functions, unit-tested — the one
exception in this codebase to the "verify against real data, not
fixtures" default, because this logic genuinely has no real-world corpus
to test against the way manifest decoding does) that also fetches the
repo's `.sha1` sidecar via a new `downloader.FetchText` (the same
authenticated, proxied client `Get` uses, just for small text content
instead of a file). The result is a hand-synthesized `manifest.Resolved`
(`Name`/`Version`/`URLs`/`Hashes` only) fed into the existing,
*unmodified* `installResolved` — proof that `Resolved` was already a
clean enough seam to support a resolution mechanism its own author
didn't originally have in mind.

Maven repos are configured the same way as buckets, not as a single
global setting: `internal/mavenrepo` mirrors `internal/bucket`'s
config-list shape (`Entry`/`Config`/`List`/priority-ordered `Resolve`)
without any of its git-clone/archive-fetch machinery, which doesn't
apply — a Maven repo is just a URL resolved against per-install, no
local directory. `maven.SplitSpec` (a second pure, unit-tested function)
splits an optional `reponame/` qualifier off the front of the spec using
the same `"/"`-separator convention as Scoop's own `[bucket/]name`
grammar (safe because a Maven coordinate's fields never contain `/`);
`mavenrepo.Resolve(repoName, coord)` then either targets that one named
repo or searches every configured repo in priority order, mirroring
`bucket.Find`'s try/continue/aggregate-tried-names pattern exactly. This
went through one real design pivot: it originally started as a single
global `goop config set-maven-repo <url>` setting, replaced once it
became clear that a real setup often needs a private Artifactory repo
*and* Maven Central configured at once.

Deliberately narrow scope, verified against the real Apache Maven
distribution on Maven Central: no shim is auto-created (there's no
manifest to declare a `bin` entry from — and the real extraction result
confirms why guessing would be wrong: Maven's own `bin/mvn.cmd` is
nested inside a `<artifactId>-<version>/` wrapper directory the archive
itself carries, is named `mvn` not `apache-maven`, and neither fact is
derivable from the coordinate alone), and `goop update` on a
Maven-installed record (`Bucket: "maven"`) fails with an explicit,
early error rather than the confusing "bucket not found" it would
otherwise hit by trying to re-resolve `"maven/<name>"` as a real bucket.

## Profiles

`internal/profile` groups installed apps into user-curated named
profiles (`core`, `projectA`, ...) -- a membership label, not an
isolated environment; installation itself stays exactly as it always
was, one shared `apps/<name>/` tree regardless of how many profiles
reference an app. This went through one real design pivot worth
recording: it started as a Maven-inspired transitive dependency graph
(`goop why lib-X` walking a tree of what requires it, packages kept
alive by reference count). Two things killed that direction before any
code was written for it: real Scoop manifests barely use `depends` at
all -- every real entry in the actual main/extras corpus is a bare,
unconstrained name, so there's no graph in the wild to build reverse
lookups over -- and Windows tools are mostly standalone, unlike the
shared-library dependency web apt/dpkg's model targets. What was
actually wanted was project/workflow grouping, which reframed `goop why`
from a tree query into a flat "which profiles reference this" one, and
reframed the "keep it around if still needed" idea from a graph-derived
reference count into apt-autoremove-style membership tracking against
user-declared groups instead.

The reuse story is the interesting part: `internal/lockfile.Load`/`Save`
were already parameterized by path, not hardcoded to the root lockfile
-- `lockfile.Path()` was just the *default* argument every existing
caller happened to pass. A profile file is structurally identical to
`goop.lock.json` (same `lockfile.File`/`Entry`), just stored under a
name (`profile.Path`) instead of the fixed root path -- profiles needed
zero new file format and zero changes to `internal/lockfile` itself.
`profile.Default` maps straight onto `lockfile.Path()`, so an existing
`goop.lock.json` from before profiles existed keeps working with no
migration, and plain `goop lock`/`sync`/`status` (no profile named)
resolve against `profile.Active()` -- whatever `goop profile use` last
set, `Default` if it's never been called.

Two authoring modes share that one format: a snapshot
(`goop lock --as <name>`, reusing `Lock`'s existing "walk every
installed Record" logic verbatim) produces fully pinned entries; a
declarative add (`goop profile add <name> <app>`) produces a bare entry
with only `Name` set. `installer.Sync` branches on that directly --
`Entry.Version == ""` resolves live through the normal bucket path
(`installSpec`, not the public `Install`, to avoid re-registering the
app into whatever profile happens to be active right now -- it's already
a member of the profile being synced, that's why it's there) instead of
EXF-11's usual frozen-fields-only install.

`installer.Install` (the public entry point, not `installSpec`) is the
only place that registers an app into `profile.Active()` on success --
deliberately not inside `installSpec` itself, since that's also the
recursive path `installDependencies` uses, and a profile's membership is
meant to reflect what was actually asked for, not everything pulled in
transitively to satisfy it. `manage.go`'s `Uninstall` gained a `force
bool` parameter and a safety check ahead of any destructive work:
refuses if `profile.ContainingProfiles` returns anything beyond (at
most) the currently active profile, since removing something from the
profile you're actually working in is the normal case, but silently
breaking a *different* profile that still needs it isn't. A successful
removal cleans the app's membership out of every profile that
referenced it, `force` or not.

## Testing philosophy

Nearly everything in this project is verified against **real,
unmodified data** rather than invented fixtures, because the entire
premise of the tool is compatibility with a corpus goop doesn't control:

- Manifest decoding is checked against the *actual*
  `ScoopInstaller/Main` and `/Extras` repositories (100% of ~4000 real
  manifests decode without error), not a curated sample.
- The `pwsh` compat-function prelude (`Expand-7zipArchive`, `shim`,
  `Invoke-ExternalCommand`, ...) is validated by running real manifest
  scripts that call these functions, not by testing the polyfills in
  isolation. Coverage is necessarily incremental, not exhaustive up
  front: `is_admin` (a manifest's own admin-rights check) and
  `startmenu_shortcut` (a manifest calling Scoop's shortcut helper
  directly from its own script, distinct from the manifest-level
  `shortcuts` field `createShortcuts` already handles) both surfaced as
  missing this way, against a real manifest (`virtualbox-np`) on a real,
  fully migrated installation — not synthetic test data.
- `internal/minisign` is tested against signatures produced by the
  actual `minisign.exe` binary, not a hand-rolled test signer — this is
  what caught the global-signature byte-layout being wrong on the first
  attempt (it covers the raw 64-byte signature plus the trusted
  comment, not the algorithm+keyID-prefixed blob).
- Anything that touches real, persistent system state (Windows
  Credential Manager, `HKCU\Environment`) has real-system integration
  tests, gated behind an opt-in env var (`GOOP_TEST_CREDSTORE=1`,
  `GOOP_TEST_ENVVARS=1`) so `go test ./...` never mutates a developer's
  actual environment by accident, but the real path is still exercised
  when explicitly asked for.

The corollary: a failure surfaced by real-world testing is treated as
real until proven otherwise. Two examples worth knowing about, because
the *investigation*, not just the fix, is the reusable lesson:

- A batch install failure that looks like "MSI extraction failed" may
  actually be `MAX_PATH` (260 chars) — Windows Installer's own error
  text (`Error 1304: Error writing to file: <path>`) says so directly,
  but only if you read the log `/lwe` produces; `msiexec` in `/qn` mode
  says nothing on stdout/stderr even on failure. `Expand-MsiArchive`
  now parses that log and surfaces the real reason instead of a bare
  "extraction failed" (TR-08).
- A test failure isn't necessarily the code under test's fault — a
  scratchpad path nested ~90 characters deeper than a real user's
  `~\goop` produced `MAX_PATH` failures that had nothing to do with the
  package being tested (this is why `azure-ps`'s MSI failed even in a
  short-rooted retest: *its own* internal PowerShell-module paths are
  long enough to exceed `MAX_PATH` regardless of goop's root — a real,
  user-facing limitation, just not one goop can fix without Windows
  long-path support, per TR-31).

## Known, deliberate gaps

- **`checkver`/`autoupdate` (CPT-06) — not implemented.** This is a
  bucket-*maintainer* tool (keeping a bucket's own manifests current
  against upstream releases), not something a consumer of a bucket
  needs. Lower priority by design, not an oversight.
- **`env_set`/`env_add_path` write to `HKCU\Environment`** (never
  `HKLM` — NR-01) and are the one piece of the install pipeline that
  touches real, persistent account settings outside goop's own
  directory tree. This was implemented only after explicit user
  sign-off, distinct from everything else goop does inside its own
  root without asking.
- **Signature verification (EXF-41) has no real-world manifest usage
  to hook into.** No manifest in the actual `main`/`extras` corpus
  provides a signature field — `internal/minisign` is real, tested
  infrastructure (and used for goop's own release artifacts via
  `scripts/sign.ps1` + `goop verify`), not wired to a bucket-provided
  field that doesn't exist yet.
- **NSIS installers that expect a `$PLUGINSDIR` structure** (produced
  by actually running an NSIS installer, not by extracting its payload
  archive directly) aren't fully supported — a narrow gap, affects a
  specific installer-script pattern, not the common case.
- **A4's dependency version constraints have no real-world manifest
  usage either** (every real `depends` entry is a bare name, optionally
  bucket-qualified — `"extras/86box"` — never version-constrained). The
  constraint grammar and conflict/cycle detection are real and tested,
  ready for the day a manifest uses them, and directly usable today via
  CLI install specs (`goop install jq@1.8.2`).

## Open decisions still worth revisiting

The spec (§10) leaves several things explicitly open; here's what was
picked pragmatically to keep moving, and what that implies if revisited:

- **D1 (project name)**: "goop" — inherited from the working directory
  name, never a deliberate choice. Cheap to change now; expensive once
  published.
- **D2 (lockfile format)**: canonical JSON, not TOML — avoids adding a
  TOML dependency, and JSON's already used everywhere else
  (`goop-install.json`, bucket config). Name-sorted and stably
  indented so it diffs cleanly (EXF-13's actual requirement), which is
  the property that mattered, not the specific format.
- **D3 (version constraint grammar)**: extended Scoop-style
  component-wise ordering (`internal/vercmp`), not strict SemVer — real
  package versions (`704`, `15859902`, `2026.1.3.7`) routinely aren't
  valid SemVer at all.
- **D4 (install root)**: goop's own directory (`~\goop` by default,
  overridable via `GOOP_HOME` or persisted with `goop config
  set-root`), with two non-destructive bridges from an existing Scoop
  install: `goop import` (junctions straight at Scoop's own version
  directories — zero copying, but keeps depending on Scoop staying
  installed) and `goop migrate` (copies buckets and apps into goop's
  own tree, independent of Scoop afterward). Neither ever writes into
  the Scoop tree — `goop uninstall` on an imported/migrated app only
  ever removes goop's own bookkeeping.
- **D5 (signature mechanism)**: minisign, chosen over Authenticode
  (needs a purchased/CA-issued certificate, which nothing in this
  project can obtain) and over Sigstore (needs supporting
  infrastructure — a transparency log, OIDC — that a single-maintainer
  project doesn't have set up). Authenticode signing of the published
  binary itself (distinct from minisign-signing release artifacts,
  and relevant to TR-32's antivirus-false-positive concern) is still
  entirely open and needs a real certificate.
- **D6 (license, and when to open the source)**: not decided here —
  explicitly the maintainer's call, not something to default silently.

## Governance risk (§9, §13)

The spec calls out the bus factor as the *dominant* risk for a project
like this, more than any technical gap: "a Scoop replacement maintained
by one person is a trap for its users." Nothing about this codebase's
structure changes that risk — it's a process/people problem
(GOV-01–GOV-04: shared ownership from the start, code review on every
change, at least two people able to touch each component), not
something more code fixes.
