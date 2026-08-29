# Changelog

All notable changes to goop are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

goop is pre-1.0: the command surface and on-disk formats may still
change between minor versions. Anything that would break an existing
install or lockfile is called out explicitly under **Changed**.

`goop version` reports the running build, including the commit it was
built from — quote it in bug reports.

## [Unreleased]

### Added

- **`goop snapshot [--file <path>]`** — freezes everything installed,
  editors included, with versions, URLs and hashes. Replays with
  `goop sync --file`, so nothing new is needed to restore it.

  Where a lockfile answers *what does building this need*, a snapshot
  answers *what did this machine have* — for an audit, for rebuilding a
  workstation, or for freezing before touching something. Defaults to
  `goop-snapshot-<date>.json` in the current directory.

  This is what lets profiles drift freely: the soft groupings can change
  over time because the state they produce can be frozen on demand.

- **`goop bootstrap`** — one idempotent command for day one and for every
  `git pull` after it. A repository declares what it needs in
  `goop.json`:

  ```json
  {"lockfile": "goop.lock.json", "profiles": ["baseline.tool", "ide"]}
  ```

  The repository states its *intent*; the index says what those profiles
  contain. bootstrap refreshes the index, applies the profiles, then
  syncs the lockfile — the toolchain last, because it is what the build
  actually needs. It finds `goop.json` by walking up, so running it from
  a subdirectory works.

  It remembers what it installed, so a package you removed on purpose is
  not quietly put back on the next run — it says it is leaving it out
  instead. Running it twice in a row does nothing the second time.

  `ide` is the only profile treated as a choice rather than a set, and
  the only place goop asks a question. The first entry is the default,
  the answer is remembered — including a refusal — and
  `--non-interactive` takes the default without asking, so CI never sees
  a prompt.

- **A profile index.** What profiles *contain* can now be published by a
  team rather than defined on each machine or committed into every
  repository — so adding a tool to `baseline.tool` does not mean a commit
  per repo.

  ```powershell
  goop config set-index file://fileserver/goop/index.json
  goop index update
  ```

  The document is one JSON object:
  `{"profiles": {"baseline.tool": ["git", "graphviz", "srecord"]}}`.
  It is fetched through goop's own downloader, so per-host auth, the
  configured proxy and `file://` all apply — an internal HTTP server and
  a network share are configured identically, and neither needs git.

  The last good copy is cached, so a machine that cannot reach the index
  keeps resolving profiles, and a publish that produces invalid JSON is
  reported without replacing a working cache.

  A profile defined locally always wins over the index, so a machine can
  diverge deliberately. Editing an index-defined profile copies it here
  and says so — that machine stops receiving the team's changes to it,
  which is worth knowing at the time rather than months later.

- `goop profile show <name>` — what a profile contains, whether it came
  from the index or this machine, and which members are installed.

- An offline bundle, published with each release as
  `goop-<version>-offline.zip`: goop, its checksum and the installer,
  about 15 MB. `install.ps1` detects a `goop.exe` sitting next to it and
  installs from the bundle instead of downloading — no network, no git.
  It adds no bucket, since a machine on an internal network wants its own
  rather than the public one, and says so with the command to run.

### Fixed

- An install could report success while leaving a command that does not
  work, and `goop status` would then call the machine conformant.

  Three things combined. `createShims` never checked that a `bin` entry's
  target existed, so a manifest that did not match its own archive
  produced a shim pointing at nothing. The install record is committed by
  the rename that makes a version visible, but shims are created after
  that — so a failure in between left a record claiming an install with no
  working commands. And `Status` decided conformance from the record's
  version alone, without opening a single file.

  Reproduced end to end before fixing: install green, `goop list` showing
  the package, `goop status` reporting **in sync**, and the command
  failing with "the system cannot find the file specified". A retry then
  reported `already installed` and did nothing, so one failure left a
  machine permanently mislabelled.

  Now: a missing target fails the install before anything is written; the
  record carries `state: "pending"` until shims, shortcuts and
  environment entries exist, so a retry redoes the work instead of
  trusting it; and `Status` checks the record state and every shim target
  on disk, reporting *why* in a new column.

### Changed

- `goop lock` no longer puts editors in a lockfile. Anything in the `ide`
  profile is left out, and named, because a lockfile is what CI installs
  from and nothing in a build may depend on an editor. `goop snapshot`
  captures them when you want the whole machine.

- **Breaking: a profile is no longer a lockfile.** They shared a format,
  and the `default` profile literally *was* `goop.lock.json` — so a soft
  grouping of names was stored as, and indistinguishable from, a pinned
  auditable artifact.

  A profile is now a plain list of package names, with no versions,
  hashes or payload: `{"name": "baseline.tool", "apps": [...]}`, kept in
  the order they were declared — for a profile of alternatives such as
  `ide`, the first entry is the default. It is allowed to drift;
  reproducibility is guaranteed by the lockfile alone.

  `goop lock`, `goop sync` and `goop status` therefore take a lockfile
  **path** only. `--as <profile>` and `--profile <name>` are gone;
  `--file <path>` remains and defaults to the root lockfile.

  Nothing is lost on upgrade. Old profile files are still read, and the
  default profile's membership is recovered from the root lockfile —
  which is left untouched, being a perfectly good lockfile that simply no
  longer doubles as a profile. The new shape is written the next time a
  profile changes.

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

[Unreleased]: https://github.com/TanguyBaudoin/goop/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.2.0
[0.1.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.1.0
