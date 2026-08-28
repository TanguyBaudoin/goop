# goop

Scoop-compatible Windows package manager — replacement executor, same
manifest corpus. See [`REQUIREMENTS.md`](REQUIREMENTS.md) for the full
requirements, each with its implementation status, and the milestone
plan.

## Status

**J0 (shim), J1 (core) and J3 (differentiation) are complete. J2
(compatibility) is functionally complete but its exit criterion is not
met, and J4 (publication) is in progress.** The code is open (MIT, see
LICENSE) and CI runs build, vet, gofmt, the test suite and a decode pass
over the real `main` bucket on every push, and a tag publishes a release
binary that `scripts/install.ps1` downloads and checksum-verifies. What
remains for J4 is Authenticode signing (TR-32), which needs a
certificate rather than a change to the code.

J2's gap is worth stating plainly: the specification called for a harness
*installing* the 200 most-used manifests, and that harness was never
built. CI verifies that the corpus **decodes** (1635/1635); installation
is verified only by hand. See [`REQUIREMENTS.md`](REQUIREMENTS.md) for
every requirement with its status, and the "Known gaps" section there for
what is honestly still missing. [`ARCHITECTURE.md`](ARCHITECTURE.md)
covers how it's built and why.

The CLI itself (`internal/ui`) has colored, symbol-based output (✓/✗/→),
aligned tables for `list`/`info`/`bucket list`/`auth list`/`status`, live
in-place progress bars for downloads (one line per active download, so
concurrent installs each get their own — redrawn via ANSI cursor
movement, throttled to a few updates/second regardless of how many
Range chunks are feeding it so it adds no latency to the transfer
itself), and a properly sectioned `help` screen — dependency-free
(hand-rolled ANSI, with the Windows-specific step of opting a console
into interpreting escape codes at all, since it's off by default even
in modern Windows Terminal). Auto-disables (bars included) for
`NO_COLOR`, `GOOP_NO_COLOR`, or when either stdout or stderr isn't a
real console (redirected to a file/pipe).

**Shell completion**: `goop completion powershell` / `goop completion
bash` print a completer script for their shell:

```powershell
goop completion powershell --install   # appends the loader to your $PROFILE
goop completion bash --install         # appends it to ~/.bashrc
```

`--install` is idempotent (an already-registered loader is left alone,
never duplicated) and only ever *appends* — it never rewrites a startup
file that may hold unrelated setup. It registers the loader line, not
the generated script, so the completer is regenerated from whatever
`goop` is on `PATH` at shell startup and never goes stale as commands
are added. To wire it up by hand instead:

```powershell
# PowerShell profile
goop completion powershell | Out-String | Invoke-Expression
```
```bash
# ~/.bashrc (e.g. Git Bash)
eval "$(goop completion bash)"
```

Completes subcommands, and dynamically completes app names for
`install`/`uninstall`/`update`/`info` (installed apps from `goop list`
for the latter three; every bucket-available app, bare and
bucket-qualified, e.g. both `jq` and `main/jq`, for `install` — drawn
from a plain manifest-filename listing, not a full decode, so it's fast
even against the real ~4000-manifest corpus), bucket names for `bucket
update`, and every `config`/`auth`/`bucket` subcommand. The generated
scripts assume `goop` itself is resolvable by name (on `PATH`), same as
any other CLI's completion script — they shell back out to hidden
`goop completion __apps`/`__available`/`__buckets` helpers (plain
names, one per line, no color/table formatting) for the dynamic parts.

**`goop update [name]...`** (FR-05) — upgrades installed apps to their
bucket's current version, reusing the exact same atomic install pipeline
as `install`/`sync` (an update is just an install whose target happens
to already exist at an older version). This was a genuine gap this
project shipped for a while: FR-05 is a *Blocking* J1 core requirement
("update a package or all"), missed entirely until it was pointed out —
worth calling out plainly rather than glossing over, since the gap
existed the whole time `install`/`sync`/`status` were being built and
verified. Verified with a real package (already-up-to-date path) and a
synthetic version bump (a local test bucket's manifest edited from
1.0.0 to 2.0.0, confirming the actual upgrade path: new version
installed, `current` repointed, old version preserved per NR-03).

- Provenance (FR-42): `goop info <name>` shows exactly where an
  installed app came from — resolved URL(s), hash(es), bucket,
  architecture, install time — plus a manifest's `description`/
  `homepage`/`license` (captured at install time, so still shown even
  if the bucket manifest has since changed or the bucket was removed),
  mirroring real Scoop's own `scoop info`. `license` handles both real
  shapes (a plain SPDX identifier/URL/`|`-separated list, or
  `{identifier, url}`).
- **Lockfiles live where the project lives.** `goop lock`/`sync`/`status`
  accept `--file <path>` (and `--as`/`--profile` take a path too), so a
  lockfile pinning a project's toolchain sits in that project's repo and
  is versioned by the repo's own history -- the same place
  `package-lock.json` or `Cargo.lock` would be, rather than buried in
  goop's install root where it can't be committed next to the code it
  pins. Going back to an older baseline is then just checking out an
  older commit and running `goop sync --file ./that.lock.json`: a pinned
  entry installs straight from its frozen version/URL/hash and never
  consults the bucket (FR-11), which is what makes installing a
  no-longer-current version possible at all.
- **Bucket priority is yours to set.** Buckets are searched in the order
  `goop bucket list` shows (now numbered), first match wins, and
  `goop bucket priority <name> <n>` reorders them -- so when several
  buckets carry the same app (e.g. `flux` sits in both main and extras
  at different versions) you decide which one answers, instead of it
  being decided by whichever bucket you happened to add first. New
  buckets append at the lowest priority. Real Scoop offers no control
  here at all: its order is alphabetical with "known" buckets hardcoded
  to the front (`lib/buckets.ps1`'s `Get-LocalBucket`).
- `goop list --tree` groups installed apps by profile, nesting each
  app's dependencies underneath it. Only apps you explicitly asked for
  become profile members (a dependency pulled in automatically never
  is), so the nesting shows exactly what you asked for versus what came
  along. Apps in no profile are grouped last so nothing is hidden.
- `goop search <query> [--bin]` finds manifests across every configured
  bucket by name (`query` is a case-insensitive regex, same as Scoop's
  own `scoop search`) — plain name matching only ever decodes actual
  matches, so it's effectively instant even across a ~5000-manifest
  bucket set. `--bin` also matches each manifest's `bin` field (so
  `goop search rg --bin` finds `ripgrep`, whose manifest never mentions
  "rg" in its own name), which does mean decoding every manifest —
  parallelized across `runtime.NumCPU()` workers (CPU/disk-bound, not
  network-bound like installs, so sized differently than
  `GOOP_CONCURRENCY`), cutting a real ~5000-manifest `--bin` search from
  ~52s single-threaded to ~1s.
- `goop depends <spec>` prints an app's full dependency closure in
  installation order, the app itself last — same shape and ordering as
  real Scoop's `scoop depends` (verified identical against it on real
  manifests: `ack`→perl, `ani-cli`→fzf+extras/mpv, `keepass`→innounp).
  Like Scoop's, the listing includes the **implicit extraction helpers**
  an install would pull in, not just the manifest's own `depends` field.
- **Implicit helper dependencies**: a manifest whose URL or scripts need
  `7z`/`innounp`/`dark` gets that helper installed automatically as a
  dependency, mirroring real Scoop's `Get-InstallationHelper`
  (`lib/depends.ps1`). Previously only `innounp` was handled, so e.g.
  installing a `.tar.xz` package aborted telling the user to install
  7-Zip with *some other package manager* — goop now bootstraps itself
  (the helpers are cycle-free: `7zip` extracts from a `.msi` via
  `msiexec /a`, `innounp`/`dark` from plain `.zip` handled natively in
  Go). Two deliberate divergences from Scoop: no `lessmsi` (goop always
  uses `msiexec /a`), and `.zip`/`.tar.gz` don't pull in `7zip` since
  Go extracts those natively.
- **Cascading uninstall** (goop-only — real Scoop's uninstall never
  looks at `depends` at all): removing an app also removes every
  installed app that declares a `depends` on it, recursively, so a
  KeePass plugin doesn't outlive KeePass and `ack` doesn't outlive
  `perl`. Only manifest-declared `depends` cascade, never the implicit
  extraction helpers above — `7zip` unpacked `curl` once, it isn't
  needed to keep `curl` working. Each cascaded removal runs the full
  uninstall pipeline (its own hooks, running-process check, profile
  safety net), so a dependent protected by another profile aborts the
  whole cascade *before anything is deleted* rather than leaving a
  half-removed state; `--force` overrides.
- **Self-updating apps that clobber `current`**: a browser's own updater
  (confirmed for real with Zen Browser) can replace goop's `current`
  junction with a real directory holding the whole app, leaving the
  version directory gutted. `goop list` used to report that as
  "broken: no readable current record" (the record had simply moved
  into `current`), and uninstall used to fail outright with "The
  directory is not empty" because it assumed a junction and ran `rmdir`
  without `/S`, making the app impossible to remove through goop. Both
  paths now detect a real directory and handle it.
- Signature verification (A5/FR-41): `goop verify <file> <sigfile>
  <pubkey>` checks a minisign signature (Ed25519, verified against
  the real `minisign.exe` tool's own output — including the
  BLAKE2b-512 hashed-message mode and the separate trusted-comment
  signature). `scripts/sign.ps1` signs goop's own release artifacts;
  no real manifest in the wild provides a signature to verify yet, so
  this is real, tested infrastructure without a live use case in the
  ecosystem today — same situation A4's version constraints were in
  before `goop install name@constraint` gave them one.

- Version-constrained dependencies (A4, FR-06): `depends` entries and
  install specs both use `[bucket/]name[@constraint]` (e.g.
  `extras/mpv@>=0.40`), resolved recursively before the app itself,
  cycle-detected, with version conflicts reported clearly rather than
  silently mis-resolved. Verified against a real chain (`navi`→`fzf`)
  plus synthetic conflict/cycle cases.
- Git-less buckets (FR-21): `goop bucket add <name> <url> [git|archive]`
  fetches a plain `.zip`/`.tar.gz`/`.tar` (auto-detected, including
  GitHub codeload URLs) with no Git involved. Verified against the real
  `ScoopInstaller/Main` bucket via its GitHub archive URL.
- **200-package real-world benchmark**: batch-installed 200 real
  manifests from `main`+`extras` with full parallelism — 184/200
  (92%) installed directly, +5 pulled in as dependencies, in **20
  minutes for 200 real packages** including several very large ones.
  Of the 16 failures: 9 were external (dead/blocked/rate-limited
  third-party hosts), and investigating the rest caught 3 real bugs,
  now fixed: a classic .NET Process stdout/stderr deadlock in
  `Invoke-ExternalCommand` (read stdout to completion before touching
  stderr — any external tool that writes enough to stderr while stdout
  stays empty, which `msiexec` routinely does, could hang or fail),
  a URL with no manifest rename whose query string leaked into the
  derived filename (`?` is illegal in a Windows filename), and opaque
  MSI failures (`msiexec` stays silent on stdout/stderr in `/qn` mode
  even on failure) now surfaced from its log file instead of a bare
  "extraction failed" (TR-08). Two failures remain genuinely
  unresolved: one MSI package's own internal paths exceed `MAX_PATH`
  regardless of goop's install root (documented per TR-31, not
  something goop can fix without Windows long-path support); one NSIS
  installer's script expects a `$PLUGINSDIR` structure goop's
  extraction doesn't currently produce.

- Lockfile + `sync` (A3, FR-10–13): `goop lock` snapshots installed
  apps to `goop.lock.json` (name-sorted, byte-stable across
  regenerations — diffs cleanly in version control). `goop sync`
  installs exactly that state from the lockfile's own frozen
  URLs/hashes — proven to work with **zero buckets configured at all**.
  `goop status` reports drift for CI with a dedicated exit code (3).
- **Profiles + `goop why`**: user-curated named groups of apps — `core`,
  `projectA`, `projectB`, whatever you want — not isolated environments
  (install stays global/shared, one `apps/<name>/` tree regardless of
  how many profiles reference it; a profile is a membership label). This
  replaced an earlier idea (a Maven-style transitive dependency tree,
  packages kept alive by reference count) once two things became clear:
  real Scoop manifests barely use `depends` at all (every real entry in
  the actual corpus is a bare name — nothing to build a graph from), and
  Windows tools are mostly standalone, unlike the shared-library web
  apt/dpkg's model targets. What people actually want to group and query
  isn't "what depends on what" — it's project/workflow groupings.
  ```
  goop profile use projectA          # switch active profile (conda-activate-style)
  goop install cmake                 # registers into whichever profile is active
  goop why cmake                     # -> "referenced by profile(s): projectA"
  goop profile add core gcc          # declarative: list a member without installing it
  goop lock --as core                # snapshot: capture what's currently installed into "core"
  goop sync --profile core           # install exactly core's state
  goop uninstall gcc                 # refuses if another profile still needs it (--force to override)
  ```
  Dual authoring, both landing in the same file format goop's lockfile
  already used (`internal/lockfile.File`/`Entry`, unmodified — `Load`/
  `Save` were already parameterized by path, not hardcoded to the root
  lockfile, so a profile needed no new file format): a snapshot
  (`goop lock --as`) produces fully pinned entries, exactly like the
  classic lockfile always has; a declarative add
  (`goop profile add <profile> <app>`) produces a bare, unpinned entry,
  and `goop sync` resolves those live via the normal bucket path instead
  of the usual frozen-fields-only behavior. The default profile maps
  directly onto the classic `goop.lock.json` at the root — an existing
  lockfile from before profiles existed keeps working with zero
  migration.
- Parallel downloads (A1, TR-03): installs/syncs run concurrently across
  apps (bounded, tunable via `GOOP_CONCURRENCY`) — **measured 4.95x
  speedup** on a real batch of 14 packages (18.4s → 3.7s). Large,
  Range-capable single files (≥8MB) also download over 4 concurrent
  byte-range requests instead of one stream. Downloads sharing a cache
  entry are safely serialized; a batch install/sync is race-detector
  clean.
- Host-based auth (A2, FR-30–35): `goop auth add/remove/list` stores
  Bearer/Basic credentials in the real Windows Credential Manager
  (DPAPI, per-user), keyed strictly by host — never written into a
  manifest or a URL. Resolution is env var (`GOOP_AUTH_<HOST>`, for CI)
  → Credential Manager → anonymous. Auth lives entirely in an
  `http.RoundTripper` the downloader wraps; manifests and buckets are
  unaware it exists. `list` never displays a secret — verified
  end-to-end against the real Credential Manager and a server that
  actually rejects unauthenticated requests (fails → succeeds once
  stored → fails again once removed).

- Manifest decoding: 100% on all 1627 `main` and 2364 `extras` real
  manifests, no errors (CPT-01, CPT-02).
- Batch-installed real manifests well past J1's 50-manifest exit
  criterion, and past J2's 95%-of-200 target once external-host
  flakiness (non-goop failures: 401s from mirrors, one stale upstream
  hash, one timeout) is excluded.
- `installer`/`uninstaller`/`pre_install`/`post_install` script execution
  via a real `pwsh`/`powershell` subprocess (CPT-04), with polyfills for
  the Scoop helper functions manifests call directly (`Invoke-ExternalCommand`,
  `Expand-7zipArchive`, `Expand-MsiArchive`, `Expand-InnoArchive`,
  `Expand-DarkArchive`, `shim`/`unshim`, `info`/`warn`/`error`/`abort`,
  `versiondir`/`appdir`/`currentdir`, ...).
- Archive formats: zip (native, with a 7z fallback for compression
  methods Go's stdlib doesn't implement, e.g. Deflate64), `.7z`, MSI (via
  `msiexec /a`, matching what Scoop itself does), WiX burn bundles (via
  `dark.exe`, including restoring a burn bundle's attached-container
  payloads from dark's anonymized `a0`/`a1`/`a2`/... names back to their
  real filenames per `UX\manifest.xml`'s own burn manifest, same as real
  Scoop's `Expand-DarkArchive` — confirmed necessary against a real WiX 5
  bundle, PowerToys, whose own `installer.script` looks up its MSI by
  its real filename and silently found nothing without this), InnoSetup
  (via `innounp.exe`; a manifest with
  `"innosetup": true` gets `innounp` installed as an implicit dependency
  automatically, same as real Scoop's `lib/depends.ps1` does — no 7z
  fallback: a plain `7z x` against an Inno Setup installer only unpacks
  the wrapper `.exe`'s own PE sections, not the embedded install tree,
  so goop aborts with an actionable message instead of silently
  committing a broken install) (CPT-05).
- `persist`, `shortcuts`, `env_set`, `env_add_path` (CPT-03) — the last
  two write to `HKCU\Environment` (never `HKLM` — NR-01) and are cleanly
  reverted on uninstall. A manifest's own script can also call Scoop's
  internal persist machinery directly (`persist_data`/
  `persist_permission`/`unlink_persist_data`, ported as PowerShell
  polyfills, same hardlink-a-file/junction-a-directory behavior as real
  Scoop) — rare (2 manifests in the real corpus: `extras/hwinfo.json`,
  `main/cygwin.json`), for a case beyond what the top-level `persist`
  field alone expresses. `shortcuts` can also be set per architecture
  override (`architecture.<64bit|32bit|arm64>.shortcuts`), which real
  Scoop's own schema explicitly allows — confirmed genuinely common in
  practice (253 real manifests, including every JetBrains IDE: no
  top-level `shortcuts` at all, only the per-arch one), and a real gap
  before this: an app installed under this pattern silently got no
  Start Menu shortcut whatsoever. Shortcut creation also validates its
  target (and icon, if set) actually exists first and skips with a
  warning otherwise, matching real Scoop's own `startmenu_shortcut` —
  previously a wrong `exe`/`icon` path produced a silently-broken
  `.lnk` with no warning, since Windows shortcuts don't validate their
  own target.
- `psmodule` (31 real manifests, e.g. `extras/psreadline.json`): junctions
  the installed app directory into goop's own PowerShell modules
  directory under the manifest's `psmodule.name`, and adds that
  directory to `PSModulePath` (`HKCU\Environment`) the first time it's
  needed — so `Import-Module <name>` finds it with no extra step,
  mirroring real Scoop's own `install_psmodule`/`uninstall_psmodule`
  (`lib/psmodules.ps1`). Native Go, not a script polyfill — like `bin`/
  `persist`/`shortcuts`, this is a declarative top-level field Scoop's
  own orchestration applies automatically, not something a manifest
  script calls directly.
- `suggest` (687 real manifests): after a batch of `goop install`s
  finishes, prints "'app' suggests installing 'alt1' or 'alt2'." for
  any suggestion not already satisfied by something installed —
  mirrors real Scoop's own `show_suggestions` (`lib/install.ps1`;
  confirmed for real, matching the exact message format seen from real
  Scoop itself during this session's benchmark runs). Previously
  silently dropped entirely.
- **Maven coordinate resolution**: `goop install
  maven:[reponame/]groupId:artifactId:version:classifier:packaging`
  installs a distribution archive straight from a Maven repository — no
  Scoop manifest needed. Maven repos are configured the same way as
  buckets — named, multiple at once, priority-ordered:
  ```
  goop maven-repo add central https://repo1.maven.org/maven2
  goop maven-repo add internal https://artifactory.example.com/artifactory/maven-local
  goop maven-repo list
  goop maven-repo remove <name>
  ```
  An unqualified spec (`maven:org.foo:tool:1.0:bin:zip`) searches every
  configured repo in priority order, same as an unqualified `[bucket/]name`
  install spec does across buckets; a qualified one
  (`maven:internal/org.foo:tool:1.0:bin:zip`) resolves against exactly
  that repo. No baked-in default — at least one repo must be added
  first. Classifier may be empty (`maven:org.foo:tool:1.0::zip`).
  Hash-verified against the repo's own published `.sha1` sidecar
  (handles both bare-hex and `sha1sum`-style-prefixed sidecar formats),
  then downloaded/extracted/committed through the exact same atomic
  pipeline as any other install — `goop list`/`info`/`uninstall` all
  work on it unchanged (shown with `bucket: maven`). Scope is
  deliberately narrow: distribution archives only, no shim is
  auto-created (there's no manifest to declare a `bin` entry from, and
  guessing one from an arbitrary archive's internal layout is too
  fragile — verified against the real Apache Maven distribution itself,
  where a naive guess would have gotten both the extraction nesting
  *and* the executable name, `mvn` vs. `apache-maven`, wrong), and `goop
  update` on a Maven-installed app fails with a clear message pointing
  at reinstalling a new coordinate directly, rather than a confusing
  "bucket not found."
- Scoop import (CPT-07): `goop import [name]...` brings an existing real
  Scoop installation under goop's management with **zero re-downloading,
  re-extracting, or file copying** — `current` is junctioned straight at
  Scoop's real version directory. goop only ever reads from the Scoop
  tree; `goop uninstall` on an imported app removes only goop's own
  shims/bookkeeping, never anything under Scoop's directories (NR-07,
  GOV-07 reversibility — verified against a real 27-app Scoop
  installation). This means an imported app still depends on the Scoop
  installation staying in place.
- `goop migrate [--dry-run]`: the full-independence counterpart to
  import. Always reports every bucket and app it detects (name,
  version, source bucket, on-disk size) before touching anything;
  `--dry-run` stops there. Otherwise it copies each bucket (`.git`
  included, so `goop bucket update` still works via a plain `git pull`,
  no re-clone) and each app's active version into goop's own tree —
  re-deriving env vars/shortcuts/shims against the copy rather than
  reusing Scoop's already-applied ones — and copies persisted data
  (Scoop's `persist\<app>`) into goop's own persist store rather than
  leaving a junction back into Scoop's. No network access: everything
  Scoop already downloaded and verified is reused as-is. The result has
  no remaining dependency on Scoop, so uninstalling Scoop afterward is
  safe. A final report lists every bucket/app migrated or failed.
  Verified against a real, substantial Scoop installation (28 apps, 5
  buckets, 13GB) — which caught two real bugs: Scoop's own persist
  junctions (e.g. 7zip's `Codecs`, `nodejs-lts`'s `bin`,
  `python`'s `Lib\site-packages`) turned out to be reported by Go as
  `fs.ModeIrregular`, not `fs.ModeSymlink`, so the copy had to check the
  Windows `FILE_ATTRIBUTE_REPARSE_POINT` bit directly instead of
  trusting the portable file-mode bit; and the CLI's own success/failure
  report initially mislabeled every successfully migrated app as failed
  (`runConcurrent`'s result map holds a `nil`-valued entry for every
  attempted name, which a bare "key present" check can't tell apart
  from a real failure).

See the "Not yet built" section at the end for what remains, and
[`REQUIREMENTS.md`](REQUIREMENTS.md) §11 for the milestone table.

## Layout

```
cmd/shim        native shim binary (J0)
cmd/goop        CLI: install / uninstall / update / list / bucket / maven-repo / import / migrate / lock / sync / status / profile / why / auth / config / completion
internal/manifest    Scoop manifest decoding + architecture resolution (CPT-01, CPT-02)
internal/bucket      Git-backed bucket management + manifest lookup (FR-20, FR-22)
internal/downloader  fetch + hash verification (FR-40), bounded timeouts, chunked Range downloads (TR-03)
internal/archive     zip/tar extraction, extract_dir/extract_to, zip-slip guarded
internal/pwsh        delegates installer/uninstaller/pre_install/post_install to real pwsh (CPT-04)
internal/envvars     env_set/env_add_path via HKCU\Environment (CPT-03)
internal/lockfile    reproducibility: goop.lock.json read/write (FR-10, FR-13)
internal/profile     user-curated named app groups + active-profile state, reuses internal/lockfile's format
internal/credstore   Windows Credential Manager bindings (FR-32)
internal/auth        per-host credential resolution + HTTP RoundTripper (FR-30–35)
internal/vercmp      version comparison + constraint satisfaction (A4, D3)
internal/minisign    minisign signature verification (A5, FR-41)
internal/maven       Maven coordinate -> artifact URL + .sha1 hash, no manifest involved
internal/mavenrepo   named, priority-ordered Maven repo config (mirrors internal/bucket's list shape)
internal/installer   install/uninstall/update/list/info/import/migrate/lock/sync orchestration, junctions, shims, shortcuts, concurrency (A1)
internal/shimbin     embeds the built cmd/shim binary into cmd/goop
internal/paths       on-disk layout (root defaults to <user home>\goop, see "Where things live" below)
```

## Where things live

The install root holds everything: `apps\`, `buckets\`, `shims\`,
`cache\`, `persist\`. It resolves in order:

1. `$env:GOOP_HOME`, if set — for scripting/CI, or a one-off override
   without touching persistent state
2. a root persisted via `goop config set-root <path>` — survives across
   sessions, no environment variable needed
3. `<user home>\goop`, the default

```
goop config get-root         # current root + where it came from
goop config set-root D:\pkgs # persist a new root (validated, absolute)
goop config unset-root       # revert to GOOP_HOME or the default
```

The persisted setting lives at `%LOCALAPPDATA%\goop\config.json` — a
fixed location independent of the root itself, since it's what tells
goop where the root is. `set-root` only changes where goop *looks*; it
never moves, copies, or deletes anything, so switching back is always
safe. It warns (but doesn't block) if apps are already installed under
the root being switched away from.

The same file also holds a persistent proxy setting, resolved with the
same precedence pattern as the root (env vars, when present, always
win):

```
goop config get-proxy               # current proxy + where it came from
goop config set-proxy http://host:port
goop config unset-proxy
goop config set-no-proxy localhost,.corp.internal   # bypass list; "*" bypasses everything
goop config unset-no-proxy
```

One setting for both `http://` and `https://` targets (like git's
`http.proxy`, rather than separate keys) — `HTTP_PROXY`/`HTTPS_PROXY`
environment variables still take priority when set, deferring entirely
to Go's own standard resolution (so their own `NO_PROXY` handling,
CIDR ranges, embedded credentials, etc. all still work as expected).
Applies to package downloads and to `git clone`/`git pull` for buckets
alike (injected as `-c http.proxy=...` when goop's own proxy is set and
no `HTTP(S)_PROXY` env var already is — git already honors those
itself).

## The shim

One compiled binary (`cmd/shim`) is meant to be NTFS-hard-linked under
every exposed command name (e.g. `git.exe`, `node.exe`). Each link reads a
sidecar `<name>.shim` file next to itself — the same format Scoop uses:

```
path = "C:\full\path\to\target.exe"
args = "optional default args"
```

...resolves how to launch the target based on its extension
(`.exe`/`.bat`/`.cmd` direct or via `cmd /d /c`, `.ps1` via `pwsh`/
`powershell -File`, `.jar` via `java -jar`), and execs it with:

- exact exit-code propagation
- byte-for-byte argument passthrough (the raw post-program-name command
  line is read directly from Windows rather than reconstructed from
  `os.Args`, so unusual quoting/escaping survives untouched)
- untouched stdin/stdout/stderr (no pipes, no added buffering — the child
  inherits our handles directly)
- Ctrl-C propagated to the target (the shim swallows it in its own
  console-control handler just long enough to relay the child's exit code;
  the target receives it independently since both share the same console
  and no new process group is created)

`cmd/goop install` hard-links this same binary per `bin` entry
(TR-24) and writes matching sidecars pointed through the app's
`current` junction, so an upgrade never has to touch existing shims.

## The core (J1) and compatibility (J2)

`goop install <name>` resolves `<name>` against configured buckets in
priority order, decodes its manifest, merges in the host architecture's
`architecture.{64bit,32bit,arm64}` overrides, downloads and verifies
every URL by hash, runs `pre_install`, extracts/runs the archive or
installer, links `persist` paths, runs `post_install`, and applies
`env_set`/`env_add_path` — staging everything in `<app>/<version>.partial`
first and only committing (rename, junction repoint, shims, shortcuts,
env) after every step succeeds (TR-04 atomicity: a failing `post_install`
rolls back the whole install, not just itself). `uninstall` reverses all
of it — shims, shortcuts, environment changes — with no residue (NR-02),
running the manifest's `uninstaller` hook best-effort first. Manifests
needing an archive format or install mechanism goop still can't handle
are rejected with a clear "not yet supported" error rather than
mis-installing.

`goop import` brings apps already installed by a real Scoop under goop's
management without touching Scoop's own files at all (see Status above).

## Reproducibility and speed (J3)

`goop lock` snapshots installed apps' exact resolved URLs/hashes/bin
into `goop.lock.json`. `goop sync` installs precisely that state without
consulting any bucket at all. `goop status` reports drift for CI (exit
3). Installs and syncs run concurrently across apps (`GOOP_CONCURRENCY`
to tune, default 8) — safe under the race detector, ~5x faster on a
real batch. Large single files download over concurrent HTTP Range
requests when the server supports it.

## Offline and restricted networks

The download cache is keyed by **content hash**, not by URL, and it is
consulted before any fetch. That makes a populated cache portable: run
`goop download` where the network reaches, copy `<GoopDir>\cache` to the
isolated machine, and `goop sync` there resolves every entry from the
cache without a single request — the lockfile keeps the canonical remote
URLs, which stay meaningful and portable.

```powershell
goop download <apps>          # connected machine: fills the cache by hash
goop lock --file .\chipA.lock.json
# copy the cache directory across, then on the isolated machine:
goop sync --file .\chipA.lock.json
```

Manifests may also point at a local path or a network share with a
`file://` URL. Hash verification is unchanged, so a local source is
trusted no further than a remote one. Two spellings matter:

- `file://server/share/tool.zip` — a UNC share. Resolves identically from
  any machine that can reach it, so it is safe in a shared lockfile.
- `file:///D:/artifacts/tool.zip` — a drive-letter path. Exists only on
  the machine that wrote it; `goop lock` warns when one is pinned.

A bucket can be added without git at all — if git is missing and the URL
is a GitHub one, goop downloads a codeload archive instead. That is what
makes a clean machine bootstrappable: goop can install git, but until
this it could only do so from a bucket it could not add.

Behind a proxy, `goop config set-proxy` applies to both downloads and git
bucket operations, and `NO_PROXY` is honoured including `*.example.com`
wildcards.

## Install

```powershell
irm https://raw.githubusercontent.com/TanguyBaudoin/goop/main/scripts/install.ps1 | iex
```

Nothing has to be installed first — **no Go toolchain, and no git**. The
script downloads the release binary, verifies its SHA256 against the
checksum published alongside it, lays out
`<GoopDir>\{apps,buckets,shims,cache}`, copies `goop.exe` into
`<GoopDir>\bin` (kept separate from `shims`, which only ever holds
*managed app* shims goop itself creates and removes), adds both to `PATH`
(persisted to the User environment block, plus the current session), and
adds the main bucket. That last step works without git because goop falls
back to a codeload archive when git is absent.

Piped into `iex` there is nothing to bind parameters to, so the options
are also read from the environment:

```powershell
$env:GOOP_HOME = 'D:\goop'; irm https://raw.githubusercontent.com/TanguyBaudoin/goop/main/scripts/install.ps1 | iex
```

`GOOP_HOME` picks the install directory, `GOOP_VERSION` pins a release
instead of taking the latest, `GOOP_NO_BUCKET=1` skips the bucket.
Downloading the script instead lets you pass the same things as
parameters (`-GoopDir`, `-Version`, `-NoBucket`, `-DryRun`).

To build from a checkout instead — what contributors want, and the only
option if you are on a commit that has no release:

```powershell
git clone https://github.com/TanguyBaudoin/goop.git
cd goop
.\scripts\install.ps1 -FromSource     # needs a Go toolchain
```

Refuses to run elevated (`-RunAsAdmin` to override) — goop installs
per-user only (NR-01), same reasoning as Scoop's own installer. Safe to
re-run: skips whatever's already in place instead of duplicating PATH
entries or re-adding an existing bucket. `scripts/uninstall.ps1` reverses
it.

**On trust.** The checksum ships in the same GitHub release as the
binary, so it catches a corrupted or truncated download, not a
compromised release — HTTPS and GitHub are the trust anchor. Verifying a
release signature *with the binary being installed* would be circular;
`goop verify` and `scripts/sign.ps1` are for checking a later upgrade
from an already-trusted goop. The published binary is not Authenticode-
signed yet (TR-32), so SmartScreen may warn on first run.

## Build & test

```powershell
.\scripts\build.ps1   # builds cmd/shim, embeds it, then builds cmd/goop -> build\goop.exe
go test ./...
```

Note that `go install github.com/TanguyBaudoin/goop/cmd/goop@latest` does **not** work, for the same reason: the module proxy serves the repository, which has no `shim.exe` in it. Install a release, or build from a checkout.

`go build ./...` alone only works once `internal/shimbin/shim.exe`
exists (it's `go:embed`-ed into `cmd/goop`); `scripts/build.ps1` handles
the ordering, and `go generate ./...` produces the same file. Pass
`-Version 0.1.0` to stamp a release build — `goop version` reports that
stamp plus the commit it was built from, which is what to quote in a bug
report.

To run the corpus decode check that CI runs, point `GOOP_MAIN_BUCKET` at
a checkout of ScoopInstaller/Main; it skips itself when unset:

```powershell
$env:GOOP_MAIN_BUCKET = "$env:USERPROFILE\goop\buckets\main\bucket"
go test ./internal/manifest/ -run Corpus -v
``` `internal/envvars`'s tests touch the real
`HKCU\Environment` (opt-in: `GOOP_TEST_ENVVARS=1`); `internal/credstore`
and `internal/downloader`'s auth integration test touch the real Windows
Credential Manager (opt-in: `GOOP_TEST_CREDSTORE=1`) — both use
clearly-namespaced sentinel values and always clean up after themselves.
`go test -race` needs a C compiler (`CGO_ENABLED=1`); the shipped binary
itself doesn't need cgo.

## Try it

```powershell
.\scripts\build.ps1

$env:GOOP_HOME = "$HOME\goop-test"   # optional: keep test state separate from a real ~\goop
.\build\goop.exe bucket add main https://github.com/ScoopInstaller/Main.git
.\build\goop.exe install ripgrep
.\build\goop.exe list
& "$env:GOOP_HOME\shims\rg.exe" --version
.\build\goop.exe update            # upgrade everything installed to its bucket's current version
.\build\goop.exe uninstall ripgrep

# or, if you already have a real Scoop install:
.\build\goop.exe import        # everything found
.\build\goop.exe import go     # just one app

# reproducibility
.\build\goop.exe install jq fd ripgrep
.\build\goop.exe lock
.\build\goop.exe status        # "in sync"
.\build\goop.exe sync          # installs exactly goop.lock.json's state, no bucket needed

# auth
.\build\goop.exe auth add example.com bearer sk-my-token-here
.\build\goop.exe auth list      # shows the host and type, never the secret
.\build\goop.exe auth remove example.com

# dependencies and Git-less buckets
.\build\goop.exe install navi           # pulls in fzf automatically (its depends)
.\build\goop.exe install "jq@1.8.2"     # version-constrained install spec
.\build\goop.exe bucket add main-archive https://codeload.github.com/ScoopInstaller/Main/zip/refs/heads/master

# pinning, cache and repair
.\build\goop.exe hold jq              # skipped by `update` until unheld
.\build\goop.exe cache show           # cached installers, biggest first
.\build\goop.exe cache rm firefox     # matches anywhere in the filename
.\build\goop.exe download jq          # fetch + verify into the cache, install nothing
.\build\goop.exe reset jq             # rebuild shims/shortcuts/env for an existing install
.\build\goop.exe cleanup              # drop superseded versions
.\build\goop.exe profile reset        # merge every profile into default
.\build\goop.exe uninstall --all      # remove everything (refuses apps another profile needs)

# provenance and signatures
.\build\goop.exe info ripgrep
minisign -G -p release.pub -s release.key    # one-time, keep release.key secret
.\scripts\sign.ps1 -SecretKey release.key
.\build\goop.exe verify build\goop.exe build\goop.exe.minisig (Get-Content release.pub | Select-Object -Last 1)
```

## Automating updates ([topgrade](https://github.com/topgrade-rs/topgrade))

topgrade updates everything on a machine in one run, but its steps
(Scoop, brew, cargo, npm, ...) are a fixed list compiled into its own
Rust binary — there's no plugin system, so a new tool like goop can't be
auto-detected without a change to topgrade itself. Its supported,
user-side extension point is the `[commands]` table in `topgrade.toml`
(`topgrade --config-reference` prints the full annotated default; the
file itself lives at `%APPDATA%\topgrade.toml` on Windows). Add:

```toml
[commands]
"goop" = "C:\\path\\to\\goop.exe update"
```

(Use a bare `"goop update"` instead once `goop.exe` is on `PATH`.) It'll
then run as one of topgrade's steps, same as any other tool — verify
with `topgrade --dry-run` first, which prints what would run without
executing anything.

## Not yet built

Ordered by what matters most, and stated as gaps rather than plans.
[`REQUIREMENTS.md`](REQUIREMENTS.md) carries the full list with the
requirement each one belongs to.

**One maintainer** (GOV-01–GOV-03). The specification named the bus
factor a critical risk at the outset and it is still open: no second
reviewer, no review process. Opening the code was a precondition for
fixing this, not the fix. Everything below is smaller.

**No automated install harness** (J2's exit criterion). The corpus is
verified to decode; installing is verified by hand. This is the gap most
likely to let a regression through, and the most useful thing an outside
contributor could close.

**No signed release** (TR-32). Releases are published by CI on a tag and
the installer verifies their checksum, but the binary carries no
Authenticode signature, so SmartScreen may warn on first run. That needs
a code-signing certificate, which is a purchase rather than a patch.

**Two acceptance criteria never measured.** Cross-machine `sync`
reproducibility, and build-duration parity against Scoop on a real
CMake/Ninja build — the latter being one of the reasons the project
exists.

**`checkver`/`autoupdate`** (CPT-06), deliberately: they generate new
manifest versions upstream and are a bucket-*maintainer* tool, not
something needed to consume a bucket.
