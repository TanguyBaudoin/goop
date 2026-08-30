# Changelog

All notable changes to goop are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

goop is pre-1.0: the command surface and on-disk formats may still
change between minor versions. Anything that would break an existing
install or pinned file is called out explicitly under **Changed**.

`goop version` reports the running build, including the commit it was
built from — quote it in bug reports.

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

[Unreleased]: https://github.com/TanguyBaudoin/goop/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.3.0
[0.2.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.2.0
[0.1.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.1.0
