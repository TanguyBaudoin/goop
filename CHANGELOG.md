# Changelog

All notable changes to goop are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

goop is pre-1.0: the command surface and on-disk formats may still
change between minor versions. Anything that would break an existing
install or pinned file is called out explicitly under **Changed**.

`goop version` reports the running build, including the commit it was
built from — quote it in bug reports.

## [0.5.0] — 2026-08-31

Two gaps in the profile commands, both reported from use: there was no
way to delete a profile, and the "active profile" was a hidden mode that
decided where installs landed.

### Added

- `goop profile delete <name>` — there was no way to remove a profile.
  `profile remove` takes apps out of one, `profile reset` destroys every
  named profile at once, and nothing sat between them.

  Nothing is uninstalled: a profile is a grouping, not an installation.
  A member left in no profile at all falls back to `default` — that is
  what `default` is for, and an orphan would otherwise be invisible to
  the uninstall safety net. A member another profile still claims stays
  there. `default` itself cannot be deleted.

- `goop install --profile <name>` files packages under a named profile.

### Changed

- **Breaking: removed `goop profile use` and the active profile.** It
  was a hidden mode that decided where an install landed, from a setting
  possibly made days earlier, and it borrowed the `conda activate` model
  for something that is not an isolated environment. Which profile a
  package joins is now said on the command that installs it.

  Packages land in `default` unless `--profile` says otherwise, and
  filing one under a named profile takes it out of `default`.

- The uninstall safety net now stops when a package belongs to **more
  than one** profile. It used to mean "any profile other than the active
  one", which made the warning depend on that hidden setting — and warned
  about a package with a single owner whenever you happened to be
  somewhere else. The new rule is the one the README always described:
  *"if cmake belongs to another profile too"*.

## [0.4.0] — 2026-08-31

Four changes, all from a day of real use. The command surface says which
subject it acts on, `goop update` shows its plan before running it, and
profile membership is part of conformance rather than something a sync
silently left wrong.

### Changed

- **Breaking: the two planes now say which subject they act on.** Five
  bare top-level verbs — `export`, `import`, `audit`, `check`, `sync` —
  read as unrelated commands whose subject you had to remember. They were
  never unrelated: the same three verbs applied to two subjects that have
  nothing to do with each other.

  | | writes a file | reads and installs | reads and compares |
  |---|---|---|---|
  | this machine | `goop machine export` | `goop machine restore` | `goop machine audit` |
  | a repository | `goop profile export` | `goop profile sync` | `goop profile check` |

  The old names are not kept as aliases — aliases are the clutter this
  removes — but typing one tells you where it went rather than
  "unknown command".

  `goop adopt` (take over a real Scoop install) and `goop digest` are
  unchanged and stay top-level: neither belongs to a plane.

- **`goop update` shows what it would do, and asks.** It used to run
  every install first and report afterwards. On a maintained machine the
  answer is "nothing" — reported from a real one: 41 packages, none with
  a newer version — and finding that out cost a full pass plus 41 lines
  of roll-call.

  It resolves first now (a bucket manifest read per app, no downloads;
  well under a second for those 41), prints a table of what would change,
  and asks. The prompt appears only where there is someone to answer it:
  a pipe, CI or a scheduled task proceeds, because update is routine
  rather than destructive. `-y` skips it, `--dry-run` stops after the
  plan, `-v` lists the packages already current — which are otherwise a
  single count, since that list was the noise.

  Held packages and ones goop could not resolve are always named. A held
  app that quietly did not update looks exactly like one with nothing to
  update, and an unresolvable one looks healthy; the latter now sets the
  exit code whether or not anything else changed.

  Refreshing buckets names each one and how long it took, instead of a
  single line followed by a silent multi-second pause — indistinguishable
  from a hang on a slow link.

- **Profile membership is part of conformance.** Install a package by
  hand and it lands in `default`. Sync a file whose `ide` profile
  declares it, and goop printed "nothing to do" — the version was right,
  so there was no deviation — and left it under `default`. The machine
  did not match the file and goop said it did.

  Being filed somewhere else is now a deviation naming where the package
  actually is, and `goop profile sync` fixes it **by filing it, not by
  reinstalling**: the package is already there at the right version, and
  only a line in a text file is wrong. Correcting bookkeeping must not
  re-download a 4 GB IDE.

  Only *absence* from the declared profile counts. Extra local
  memberships are the machine's own business, the same way a package
  outside every profile is never a deviation.

- **`default` is a fallback and behaves like one.** Adding a package to a
  named profile drops it from `default`. Before this, `goop why` reported
  two owners for a package with one, and a package stayed in `default`
  after being deliberately filed elsewhere. Two *named* profiles sharing
  a package still both list it.

- **"nothing to do" is gone.** An empty file and a fully conformant
  machine printed the same line, and neither said whether anything had
  been checked. `goop profile sync` now reports what it verified.

- **Removed `goop profile clone`.** It created a local profile from a
  file without installing anything, and since profile membership became
  part of conformance, `goop profile sync` does exactly that *and* the
  installs. Its own workflow never worked either: clone, edit,
  re-export — but `profile export` refuses a member that is not
  installed, so a sync was required anyway.

### Added

- `goop digest <name>... | --all [--recheck]` records a manifest digest
  for installs that have none — installed by a goop older than 0.3.0, or
  adopted from a real Scoop, which never had them. Without one,
  `goop profile export` can only write a version-only pin.

  It does **not** recompute the digest, because that is not possible. The
  digest fingerprints the manifest a package was *installed from*, and
  the only manifest available now is whatever the bucket offers today.
  Stamping that onto an old install would assert something nobody
  checked, and `goop check` would then be green about it — the confident
  wrong answer the digest exists to prevent.

  So it is recorded only when it can be corroborated: the bucket must
  still offer that exact version (goop cannot fetch a historical
  manifest), and every field the receipt captured at install time — urls,
  hashes, bin, extract dirs, shortcuts, uninstaller, uninstall scripts,
  psmodule, depends — must match it. Otherwise the package is reported
  with the field that differs, or with the version the bucket has moved
  on to, and nothing is written.

  **What it cannot corroborate**, stated in the command's own output
  rather than buried here: `pre_install`, `post_install` and
  `installer.script`. The receipt never recorded them, so a manifest
  republished with an edited install script and everything else unchanged
  would pass. A digest recorded this way is an adoption of the current
  manifest, corroborated by everything the receipt remembers — not a
  recovery of what actually ran. Reinstalling is still the only way to
  get a digest of the real thing.

  `--recheck` also examines packages that already have a digest, and
  reports where a bucket has republished a version you have installed.
  Nothing is rewritten in that case: the recorded digest is the evidence.

  Verified against real installs: the digest written by a backfill is
  byte-identical to the one a fresh install of the same package records.

## [0.3.1] — 2026-08-30

Four defects, all reported from real use within hours of 0.3.0. One of
them left `goop update` permanently stuck.

### Fixed

- **`goop update` could get permanently stuck on a self-updating app.**
  Reported from a real machine, on Zen Browser:

      zen-browser 1.21.16b: previous install did not finish, redoing it
      ✗ zen-browser: remove existing junction ...\current: exit status 145
        The directory is not empty.

  goop keeps `<app>\current` as a junction to a versioned directory, and
  `relinkCurrent` removed it with `rmdir` — which on a junction drops the
  link and leaves the target alone. Zen Browser's own updater replaces
  that junction with a **real directory** and moves itself into it, and
  `rmdir` refuses a real directory that is not empty. The install had
  already committed its receipt by then, so it stayed `pending`, and
  every later `goop update` redid the same work and hit the same wall.

  `current` is now detached rather than deleted: it holds a real
  installation, possibly with user data, and goop did not put it there.
  It goes back to the version directory its own receipt names when that
  is free — restoring the layout and losing nothing — and to a numbered
  `current.detached-N` otherwise. Both paths say where it went.

  Verified by reproducing the exact failure against the published 0.3.0
  binary, then the repair: update succeeds, `current` is a reparse point
  again, the command runs, and the old payload is still there.

- **`goop sync <file> <profile>` registered packages into the active
  profile instead of the one it was repairing.** On a new machine the
  active profile is `default`, so syncing `chipA` filed chipA's packages
  under `default` — emptying the very profile the sync was fixing.
  `SyncProfiles` knows which profile each deviation belongs to and now
  says so.

- **`goop profile export` silently dropped the manifest digest** for any
  package installed before goop recorded them, or adopted from Scoop. It
  reported `✓ exported` and "should be green here" over a file that had
  lost its only defence against a manifest republished under an unchanged
  version number, and nothing said so. Version-only pins are still
  written — weaker is not useless — but they are now counted and named,
  with what to do about them.

- `goop audit` and `goop import` no longer accept a profile file. One is
  valid JSON with no `apps`, so it decoded into an empty capture and
  `audit` reported every installed package as "not in the capture" —
  a confident, wrong answer to a question nobody asked. `check` and
  `sync` already refused a capture for the mirror reason; this is the
  other half of that guard, and it names which file you handed over.

  Found by exercising the published 0.3.0 binary rather than the local
  build, which is the only reason it was found at all.

### Changed

- **`default` is a fallback and now behaves like one.** Adding a package
  to a named profile drops it from `default`: `default` is where things
  land when nobody has said otherwise, so once someone does say
  otherwise it has no claim left. Before this, `goop why` reported two
  owners for a package with one, and a package stayed in `default` after
  being deliberately filed elsewhere.

  Two *named* profiles sharing a package still both list it — that is
  what makes the uninstall safety net mean anything.

## [0.3.0] — 2026-08-30

The release that separates what a *project* needs from what is on a
*machine*. If you use `goop lock`, `goop status` or `goop sync --file`,
read the Changed section before upgrading — all three are gone.

### Changed

- **Breaking: `lock`, `status` and the lockfile are gone**, replaced by
  two planes that never touch. They had been one thing pretending to be
  two: the `default` profile literally *was* `goop.lock.json`, so a soft
  grouping of names was stored as, and indistinguishable from, a pinned
  auditable artifact.

  **What a project needs.** One JSON file, committed with the code,
  holding any number of profiles:

  ```json
  {"profiles": {"chipA": {"packages": {
    "cmake": {"version": "3.31.2", "hash": "sha256:9f2a…"}
  }}}}
  ```

  - `goop check <file> [profile...]` — exit **3** on deviation. Reads
    install receipts and nothing else: no bucket, no network, same answer
    offline.
  - `goop sync <file> [profile...]` — install what is missing or wrong.
    Idempotent and needs no prior state; an empty machine and a drifted
    one take the same path.
  - `goop profile export --out <file> --profile <name>...` — the
    maintainer's side, pinning from receipts rather than from the bucket:
    the file describes what someone actually ran.
  - `goop profile clone <file> <name>` — take a published profile as a
    local, editable one.

  Three rules decide the semantics. A package outside the named profiles
  is **never** a deviation — the question is "does this machine have what
  the project needs", not "is this machine clean". Naming a profile the
  file does not declare is an **error**, because silence would read as
  conformance. And syncing one profile never touches another.

  A pin may also be a bare version string when the digest is not wanted.

  **What is on this machine.** Nothing to do with any repository:

  - `goop export [--out <file>]` — buckets *and* every installed package.
  - `goop import <file>` — buckets first, then the packages; a list with
    no catalogue to resolve it against is not a setup.
  - `goop audit <file>` — exit **3** on any difference, reported in
    **both** directions: what the capture has and this machine doesn't,
    and what this machine has that the capture never mentioned.

  Shaped after `scoop export`/`scoop import`, deliberately.

- **Breaking: `goop import` changed meaning.** It replays a machine
  capture. Adopting the packages of an existing Scoop install — what
  `import` used to do — is now **`goop adopt`**.

- **Breaking: `goop sync` changed meaning.** It takes a profile file and
  makes this machine satisfy it. There is no `--file` flag any more: the
  file is the argument.

- Replaying a pin now resolves the manifest through the bucket instead of
  installing from a frozen URL and hash. Stated plainly because it is a
  real reduction: a version withdrawn from every bucket can no longer be
  reinstalled from the file alone, and offline replay needs the bucket
  directory present where before it did not. In exchange there is no
  fourth field to keep true, and a manifest that changed under an
  unchanged version number is *reported* rather than installed over. This
  is what Scoop's own export/import do. Tracked as FR-11 **Partial** in
  REQUIREMENTS.md rather than quietly marked met.

- A profile is no longer a lockfile. It is a plain list of package names
  with no versions, hashes or payload, kept in the order declared. Old
  profile files are still read, and the default profile's membership is
  recovered from the root lockfile, which is left untouched.

### Added

- Installs record a **manifest digest** in the receipt: a fingerprint of
  what the manifest will actually do — URLs, artifact hashes, bin
  entries, every install and uninstall script, shortcuts, environment
  changes, persisted paths, and the per-architecture overrides of all of
  them.

  A manifest is executable content. An artifact hash says the payload is
  unchanged and nothing about a `post_install` edited since; the manifest
  digest covers both, because the artifact hash is itself part of the
  manifest. It is what lets `check` catch a version republished under the
  same number.

  Computed by decoding and re-encoding canonically rather than hashing
  the file, so formatting cannot affect it — which matters concretely: a
  bucket cloned with `core.autocrlf=true` has CRLF on every line while
  the same bucket fetched as a zip has LF, and goop uses both.
  `checkver` and `autoupdate` are excluded: they drive how a maintainer
  produces new versions and change nothing about installing a pinned one.

- `goop profile show <name>` — what a profile contains, and which of its
  members are installed. A name that does not exist is an error, not an
  empty list.

- An offline bundle, published with each release as
  `goop-<version>-offline.zip`: goop, its checksum and the installer,
  about 15 MB. `install.ps1` detects a `goop.exe` sitting next to it and
  installs from the bundle instead of downloading — no network, no git.
  It adds no bucket, since a machine on an internal network wants its own
  rather than the public one, and says so with the command to run.

### Fixed

- An install could report success while leaving a command that does not
  work, and goop would then call the machine conformant.

  Three things combined. `createShims` never checked that a `bin` entry's
  target existed, so a manifest that did not match its own archive
  produced a shim pointing at nothing. The install record is committed by
  the rename that makes a version visible, but shims are created after
  that — so a failure in between left a record claiming an install with no
  working commands. And conformance was decided from the record's version
  alone, without opening a single file.

  Reproduced end to end before fixing: install green, `goop list` showing
  the package, the status command reporting **in sync**, and the command
  failing with "the system cannot find the file specified". A retry then
  reported `already installed` and did nothing, so one failure left a
  machine permanently mislabelled.

  Now: a missing target fails the install before anything is written; the
  record carries `state: "pending"` until shims, shortcuts and
  environment entries exist, so a retry redoes the work instead of
  trusting it; and `check`/`audit` inspect the record state and every
  shim target on disk, reporting *why*.

- Exporting a pin whose source is a drive-letter `file://` URL warns
  again. The check existed for `goop lock` and lost its only caller when
  `lock` was removed — on files that are now *more* likely to travel, not
  less. A UNC share resolves from any host that can reach it and stays
  quiet.

- `goop self-update`'s upgrade path is tested for the first time. It
  matters for this release in particular: everyone on 0.2.0 reaches 0.3.0
  through it, and until now only the already-current case could be
  exercised — a freshly released binary and the latest release are by
  definition the same version, so the forward path was always deferred to
  the release after next. `updateAt` now takes its target and release
  base explicitly, so an upgrade, a no-op and a refused downgrade all run
  against a release served over `file://` in about four seconds.

### Removed

- `internal/lockfile`, which no longer had a single caller in the
  product. Reading the legacy on-disk shape lives in `internal/profile`,
  where the compatibility actually matters.

## [0.2.0] — 2026-08-29

### Added

- `goop self-update` replaces goop itself with the current release. It
  compares the published checksum against the running binary first, so
  being already current costs a few dozen bytes rather than a download;
  otherwise it verifies the hash, **runs the new binary once to confirm
  it works**, and only then swaps it in, restoring the old one if the
  swap fails. Windows will not let a running image be overwritten but
  will let it be renamed, so the outgoing binary is moved aside and
  deleted on a later run.

  Never automatic, per D7 — and it refuses to go backwards unless
  `--force` is given, since a locally built binary differs from the
  published build of the same version and would otherwise be silently
  replaced by an older release.
- An install harness. `TestInstallHarness` installs real packages into an
  isolated root, checks that `current` resolves and that every declared
  `bin` produced a shim whose sidecar names a target that exists, then
  uninstalls and checks for residue. Opt-in (`GOOP_HARNESS=1`), and run
  weekly in CI rather than on every push since it downloads from real
  upstream URLs.

  This closes what REQUIREMENTS.md called the largest verification gap:
  until now the corpus was verified to *decode*, and installing was
  checked only by hand. The set is chosen for **shape** coverage — one
  package per extraction and hook mechanism — rather than the
  specification's "200 most-used manifests", because popularity says
  nothing about which code path a package exercises. Current result: 8/8.

### Fixed

- `scripts/install.ps1` no longer asks the GitHub API which release is
  latest. That endpoint allows 60 unauthenticated requests per hour *per
  IP*, which everyone behind a corporate NAT shares, so the install could
  fail with an opaque 403 for someone who had made no API calls at all.
  It now uses the `/releases/latest/download/` redirect, which is not rate
  limited. This one reached users without a release, since the installer
  is fetched from `main`.

## [0.1.0] — 2026-08-28

First release. goop installs and manages Windows applications from Scoop
buckets, reading the same manifests without requiring Scoop itself.

### Install

```powershell
irm https://raw.githubusercontent.com/TanguyBaudoin/goop/main/scripts/install.ps1 | iex
```

No Go toolchain and no git needed. The script downloads the release
binary, verifies its SHA256, lays out the tree, and puts goop on `PATH`.
`scripts/uninstall.ps1` reverses it, and `-FromSource` builds from a
checkout instead for contributors.

### Added

**Packages** — `install`, `uninstall`, `update`, `list`, `info`,
`search`, `download`, `depends`, `hold`/`unhold`, `cleanup`, `reset`,
`cache`, `version`.

Installs are atomic: content is staged in a `<version>.partial`
directory and manifest hooks run against it before a single rename
commits the result, so a failed install leaves no trace. `hold` pins a
package against `update`; `cleanup` removes superseded versions;
`reset` rebuilds shims, shortcuts and environment entries for an
existing install.

`uninstall --all` removes everything in one command, and asks for the
word `uninstall-all` to be typed first. Non-interactively — piped or
scripted stdin — it refuses outright rather than assuming consent, with
no override flag: a `--yes` that scripts may pass is a `--yes` an
automated caller may pass.

**Scoop manifest compatibility** — a PowerShell prelude reimplements
the Scoop helper surface manifests call into (the `Expand-*` archive
helpers, `persist_data`, `Add-Path`, shim and shortcut creation), so
`pre_install`/`post_install` scripts written for Scoop run unmodified.
Zip and tar.gz are extracted natively in Go; other formats shell out to
7zip, innounp or dark, which are resolved as implicit dependencies and
installed automatically rather than assumed present on PATH.

**Buckets** — `bucket add/list/remove/priority`, with per-bucket
priority so a name found in a preferred bucket wins over the same name
in `main`. Git is used when present and is not required: a GitHub bucket
is downloaded as a codeload archive when git is absent, which removes a
bootstrap circularity — goop can install git, but only from a bucket. The
fallback is a property of the machine, not of the bucket: the canonical
URL is kept, so installing git later restores incremental updates by
itself.

**Shims** — commands are dispatched through hardlinks to a shim binary
paired with a text sidecar naming the real target.

**Profiles** — `profile`, `why`. Profiles group applications, and
membership acts as a safety net: removing something another profile
still needs requires `--force`. `profile reset` merges everything back
into `default`.

**Reproducibility** — `lock`, `sync`, `status`, `import`. Lockfiles pin
version, URL and hash; `goop lock --file <path>` keeps one inside a
project repository rather than under the goop home. `sync` installs a
pinned entry straight from the frozen fields without consulting the
bucket, and `status` exits 3 on drift, for use in CI. `lock` warns when
an entry pins a machine-local `file://` source, which cannot resolve
elsewhere.

**Offline use** — the download cache is keyed by content hash and
consulted before any fetch, so a cache directory copied from a connected
machine lets `sync` resolve everything with no network. Buckets and
manifests may also point at a local path or network share with a
`file://` URL.

**Dependencies** — declared `depends` entries are resolved recursively
at install time, and uninstalling a package cascades to the packages
that declare it. The cascade deliberately follows only declared
dependencies, not the implicit extraction helpers: removing 7zip should
not remove everything that was once unpacked with it.

**Maven** — `maven-repo`, plus Maven coordinates installable as
first-class packages.

**Integrity and auth** — downloads are verified against the manifest
hash and retry transient failures with exponential backoff, leaving a
definitive answer such as a 404 to be reported at once. `verify` checks
minisign signatures; `auth` stores credentials for private buckets in
the Windows credential store, keyed by host and never written into a
manifest. Tokens and passwords are prompted for without echo rather than
passed as arguments, which would land them in shell history and in the
process list; piping still works for CI.

**Other** — `config` (install root, proxy, `NO_PROXY`, cache limit,
bucket staleness), `migrate`, and `completion` for PowerShell and bash
with `--install` to register itself.

### Known gaps

See [REQUIREMENTS.md](REQUIREMENTS.md) for every requirement with its
status. The largest: installation is verified by hand rather than by an
automated harness, the released binary is not Authenticode-signed so
SmartScreen may warn on first run, and goop has a single maintainer.

[Unreleased]: https://github.com/TanguyBaudoin/goop/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.5.0
[0.4.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.4.0
[0.3.1]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.3.1
[0.3.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.3.0
[0.2.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.2.0
[0.1.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.1.0
