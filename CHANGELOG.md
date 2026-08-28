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

Nothing yet.

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

[Unreleased]: https://github.com/TanguyBaudoin/goop/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/TanguyBaudoin/goop/releases/tag/v0.1.0
