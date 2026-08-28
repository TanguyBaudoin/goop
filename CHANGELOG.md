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

- `goop uninstall --all` removes every installed package in one command.
- `goop uninstall --all` now requires confirmation. Interactively it asks
  for the word `uninstall-all` to be typed, after listing what will go;
  non-interactively — piped or scripted stdin — it **refuses outright**
  rather than assuming consent. There is no override flag: a `--yes` that
  scripts may pass is a `--yes` an automated caller may pass. An automated
  caller reaching for `uninstall --all --force` now fails closed instead
  of quietly wiping the machine.
- `goop profile reset` merges every profile into `default`, deletes the
  named profiles, and makes `default` active again.
- Buckets can be added and updated **without git**: when git is absent
  and the URL is a GitHub one, goop falls back to downloading a codeload
  archive. This removes a bootstrap circularity — goop can install git,
  but previously only from a bucket it could not add without git.
- `file://` sources, so a bucket or manifest can point at a local path or
  a network share. Hash verification is unchanged, so a local source is
  trusted no further than a remote one.
- `scripts/uninstall.ps1` reverses `install.ps1`: removes the PATH
  entries and `GOOP_HOME`, optionally uninstalls managed packages first,
  and deletes the tree. Confirms before acting; `-DryRun`,
  `-PreserveApps` and `-SkipSelfdestruct` are available.
- Downloads retry transient failures with exponential backoff and jitter,
  and the per-download timeout is now an hour rather than 15 minutes.

### Changed

- Git buckets update with `fetch` + `reset --hard` + `clean -fd` instead
  of `pull --ff-only`. A bucket is a disposable mirror, and `pull`
  refused outright whenever upstream added a file whose path already
  existed untracked in the clone.
- Extraction helpers are no longer recorded as functional dependencies.
  A manifest that declares 7zip under `depends` *and* needs it to unpack
  its own archive was making `goop uninstall 7zip` cascade into every
  such package.
- `goop lock` warns when an entry pins a machine-local `file://` source
  (a drive-letter path), which cannot resolve on another machine. UNC
  paths do resolve elsewhere and are not flagged.

### Fixed

- `NO_PROXY` entries written as `*.example.com` — the form curl and pip
  document — matched nothing, so the proxy was used for hosts that
  should have bypassed it.
- `file://` URLs in the standard UNC form (`file://server/share/x.zip`)
  resolved to a *relative* path, and percent-encoding was not decoded, so
  a share named `Program Files` failed. Both were the forms that mattered
  most: UNC is the only spelling that travels between machines.
- Downloads retried every non-200 status, so a 404 — a dead URL in a
  manifest, the most common failure there is — took three attempts and
  seconds of backoff before reporting. Only 5xx, 429, 408 and
  transport-level errors are retried now.
- A retried range chunk counted its already-reported bytes twice, so a
  chunked download that retried could show more than 100% progress.

## [0.1.0] — unreleased

First release. goop installs and manages Windows applications from
Scoop buckets, reading the same manifests without requiring Scoop
itself.

### Added

**Packages** — `install`, `uninstall`, `update`, `list`, `info`,
`search`, `download`, `depends`, `hold`/`unhold`, `cleanup`, `reset`,
`cache`.

Installs are atomic: content is staged in a `<version>.partial`
directory and manifest hooks run against it before a single rename
commits the result, so a failed install leaves no trace. `hold` pins a
package against `update`; `cleanup` removes superseded versions;
`reset` rebuilds shims, shortcuts and environment entries for an
existing install.

**Scoop manifest compatibility** — a PowerShell prelude reimplements
the Scoop helper surface manifests call into (the `Expand-*` archive
helpers, `persist_data`, `Add-Path`, shim and shortcut creation), so
`pre_install`/`post_install` scripts written for Scoop run unmodified.
Zip and tar.gz are extracted natively in Go; other formats shell out to
7zip, innounp or dark, which are resolved as implicit dependencies and
installed automatically rather than assumed present on PATH.

**Buckets** — `bucket add/list/remove`, with per-bucket priority so a
name found in a preferred bucket wins over the same name in `main`.

**Shims** — commands are dispatched through hardlinks to a shim binary
paired with a text sidecar naming the real target.

**Profiles** — `profile`, `why`. Profiles group applications, and
membership acts as a safety net: removing something another profile
still needs requires `--force`.

**Reproducibility** — `lock`, `sync`, `status`, `import`. Lockfiles pin
version, URL and hash; `goop lock --file <path>` keeps one inside a
project repository rather than under the goop home. `sync` installs a
pinned entry straight from the frozen fields without consulting the
bucket, and `status` exits 3 on drift, for use in CI.

**Dependencies** — declared `depends` entries are resolved recursively
at install time, and uninstalling a package cascades to the packages
that declare it. The cascade deliberately follows only declared
dependencies, not the implicit extraction helpers: removing 7zip should
not remove everything that was once unpacked with it.

**Maven** — `maven-repo`, plus Maven coordinates installable as
first-class packages.

**Integrity and auth** — downloads are verified against the manifest
hash; `verify` checks minisign signatures; `auth` stores credentials
for private buckets in the Windows credential store.

**Other** — `config`, `migrate`, `completion` (PowerShell and bash,
with `--install` to register itself), and `version`.

[Unreleased]: https://github.com/TanguyBaudoin/goop/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.1.0
