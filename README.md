# goop

**A fast, Scoop-compatible package manager for Windows.**

goop installs Windows applications from [Scoop](https://scoop.sh) buckets
— the same thousands of community-maintained manifests — but the engine
underneath is a single Go binary instead of PowerShell. Same packages,
same ecosystem, considerably quicker, plus a few things Scoop was never
built to do.

You don't need Scoop installed. You don't need Git. You don't need
administrator rights, ever.

## Install

```powershell
irm https://raw.githubusercontent.com/TanguyBaudoin/goop/main/scripts/install.ps1 | iex
```

That's it. The script downloads goop, checks its SHA256, sets up
`~\goop`, adds it to your `PATH`, and wires up the main bucket.

Then open a new terminal and try:

```powershell
goop install ripgrep
rg --version
```

Want it somewhere else? Set `GOOP_HOME` first:

```powershell
$env:GOOP_HOME = 'D:\goop'; irm https://raw.githubusercontent.com/TanguyBaudoin/goop/main/scripts/install.ps1 | iex
```

Already using Scoop? `goop import` adopts everything you have installed
without re-downloading a byte, and without touching Scoop's own files —
so you can go back whenever you like.

To remove goop entirely, `scripts/uninstall.ps1` undoes all of it.

## Why you might want it

**It's fast.** Scoop starts a PowerShell interpreter for every
invocation; goop is one native binary. On the same set of packages the
difference is roughly **sixty-fold**. Searching all ~5000 manifests in
`main` and `extras` takes about a second. Installs and updates run in
parallel.

**It keeps your toolchain reproducible.** `goop lock` writes a lockfile
pinning every package's exact version, URL and hash. `goop sync`
reinstalls precisely that, straight from the frozen values, without ever
consulting a bucket. Keep the lockfile in your project's repository and
checking out an old commit gives you the toolchain that went with it.

**It works on locked-down networks.** Per-host authentication with
credentials in the Windows Credential Manager, proxy support, buckets
served from an internal archive, and a content-addressed cache you can
carry to an air-gapped machine on a USB stick.

**It groups things sensibly.** Profiles let you name a set of packages —
`work`, `chipA`, `games` — and goop refuses to remove something another
profile still needs.

**It's honest about what it is.** Every requirement in
[REQUIREMENTS.md](REQUIREMENTS.md) carries a real status, including the
ones that aren't met. There's a "Known gaps" section, and it isn't empty.

## Everyday use

```powershell
goop search jq                 # find it
goop install jq                # install it
goop info jq                   # where it came from, what it provides
goop list                      # what you have
goop update                    # bring everything up to date
goop uninstall jq              # remove it
```

Installs are all-or-nothing. goop stages a package in a temporary
directory, runs the manifest's scripts against it there, and only then
swaps it into place with a single rename. A package that fails halfway
leaves nothing behind.

Old versions stick around so you can roll back, until you ask for the
space:

```powershell
goop cleanup                   # drop superseded versions
goop cache show                # what's in the download cache
goop cache rm firefox          # or clear part of it
```

Pin something you don't want moving:

```powershell
goop hold zulu17-jdk           # `goop update` will skip it now
goop unhold zulu17-jdk
```

### Tab completion

```powershell
goop completion powershell --install    # appends a loader to your $PROFILE
goop completion bash --install          # or to ~/.bashrc
```

It completes subcommands, installed packages, every package available in
your buckets, bucket names and profile names. Safe to re-run — it never
duplicates itself, and never rewrites the rest of your profile.

## Buckets

A bucket is a collection of manifests. `main` is added for you; the
others are one command away:

```powershell
goop bucket add extras https://github.com/ScoopInstaller/Extras
goop bucket list
goop bucket priority extras 1     # extras now wins name collisions
```

Priority is yours to set, which Scoop doesn't offer at all. When a
package exists in several buckets, the first one in the list answers.

Git is used when it's available, because incremental updates are much
cheaper. When it isn't, goop downloads the bucket as an archive instead —
so a clean machine with no Git can still bootstrap. Install Git later and
updates go back to being incremental on their own.

Buckets can also come from a plain archive, including one on a network
share, which is the usual shape of an internal mirror:

```powershell
goop bucket add internal file://fileserver/goop/our-bucket.zip
```

## Profiles

A profile is a named group of packages. Not an isolated environment —
installs stay shared — but a way to say *what belongs to what*.

```powershell
goop profile use chipA
goop install cmake ninja gcc      # these join chipA
goop list --tree                  # grouped by profile, dependencies nested
goop why cmake                    # which profiles reference it
```

The useful part is the safety net. If `cmake` belongs to another profile
too, `goop uninstall cmake` stops and tells you rather than pulling it
out from under someone else. `--force` overrides it.

Only packages you asked for by name become members — a dependency pulled
in automatically never does — so the tree shows what you chose versus
what came along for the ride.

## Reproducible toolchains

This is the part Scoop has no answer for.

```powershell
goop lock --file .\chipA.lock.json     # freeze what's installed
git add chipA.lock.json                # it belongs with your code
```

On another machine, or in six months on the same one:

```powershell
goop sync --file .\chipA.lock.json
```

`sync` installs each entry from its recorded version, URL and hash. It
never asks a bucket anything, which is exactly what lets you install a
version that is no longer current. Going back to an older baseline is
just checking out an older commit.

For CI, `goop status` exits **3** when what's installed has drifted from
the lockfile — a distinct code, so a build can tell drift from failure.

## Private repositories and proxies

Credentials are stored per **host**, never per URL and never inside a
manifest, so a manifest can't leak one:

```powershell
goop auth add artifacts.corp bearer
Token for artifacts.corp: ...
```

The token is asked for, not typed on the command line — a secret in an
argument lands in your shell history and in the process list. Pipe it in
for CI (`echo $TOKEN | goop auth add artifacts.corp bearer`), or set
`GOOP_AUTH_ARTIFACTS_CORP` in the environment.

Secrets live in the Windows Credential Manager, encrypted per user.
`goop auth list` shows hosts and types, never the secret itself.

Behind a proxy:

```powershell
goop config set-proxy http://proxy.corp:8080
goop config set-no-proxy '*.corp,localhost'
```

`HTTP_PROXY`/`HTTPS_PROXY` are honoured too, and take precedence. Both
downloads and Git bucket operations go through the same settings.

## No internet at all

The download cache is keyed by **content hash**, not by URL, and it's
checked before anything is fetched. That makes it portable:

```powershell
# on a connected machine
goop download cmake ninja gcc
goop lock --file .\chipA.lock.json
```

Copy `<GOOP_HOME>\cache` across, then on the isolated machine:

```powershell
goop sync --file .\chipA.lock.json
```

Every package resolves from the cache. Nothing touches the network, and
the lockfile keeps real URLs so it still means something.

Manifests and buckets can also point straight at a share with a `file://`
URL. Use the UNC form (`file://server/share/x.zip`) if the lockfile will
travel — a drive-letter path only exists on the machine that wrote it,
and `goop lock` warns you when it sees one.

## Configuration

```powershell
goop config set-root D:\goop            # where packages live
goop config set-cache-limit 5GB         # evicts oldest-first past this
goop config set-bucket-ttl 3h           # how stale before a refresh
```

Defaults are opinions, so anything goop decides for you is changeable.
`goop config` with no arguments lists everything.

Exit codes: **0** success, **1** error, **2** usage, **3** drift.

## Java, and other Maven artifacts

Maven coordinates install like any other package:

```powershell
goop maven-repo add corp https://nexus.corp/repository/maven-releases
goop install maven:corp/com.example:my-tool:1.4.2::jar
```

Useful when your team's tooling is already published to a Maven
repository and you'd rather not write manifests for it.

## Keeping everything updated together

goop plugs into [topgrade](https://github.com/topgrade-rs/topgrade) via
its `[commands]` table in `%APPDATA%\topgrade.toml`:

```toml
[commands]
"goop" = "goop update"
```

Check with `topgrade --dry-run` first, which prints what would run.

## How it works, briefly

Packages install into `<GOOP_HOME>\apps\<name>\<version>\`, with a
`current` junction pointing at the active one. That's what makes several
versions coexist and switching cheap.

Commands you type resolve through `<GOOP_HOME>\shims\` — hardlinks to one
small dispatcher binary, each paired with a text file naming its real
target. The dispatcher passes arguments through untouched, propagates the
exit code, forwards Ctrl-C, and handles `.exe`, `.bat`, `.cmd`, `.ps1`
and `.jar` targets. It's the piece that runs hundreds of times per build,
so it's deliberately tiny.

Manifests that ship PowerShell (`pre_install`, `post_install`,
`installer.script` — a good chunk of `extras` does) run unmodified: goop
provides the same helper functions Scoop does, and delegates to
PowerShell rather than reinterpreting anything. Zip and tar.gz are
unpacked natively in Go; `.7z`, MSI, InnoSetup and NSIS go to 7zip,
innounp or dark, which goop installs for you rather than assuming they're
already there.

[ARCHITECTURE.md](ARCHITECTURE.md) has the full story, including the
parts that were harder than expected.

## Building from source

```powershell
git clone https://github.com/TanguyBaudoin/goop.git
cd goop
.\scripts\build.ps1
go test ./...
```

`go build ./...` on its own won't work on a fresh clone: `cmd/goop`
embeds the compiled dispatcher, which is a build artifact and therefore
not in the repository. `scripts/build.ps1` builds it first. For the same
reason `go install github.com/TanguyBaudoin/goop/cmd/goop@latest` does
not work — install a release, or build from a checkout.

`.\scripts\install.ps1 -FromSource` installs what you just built.

[CONTRIBUTING.md](CONTRIBUTING.md) covers the rest, including one trap
worth knowing before you run the tests: `GOOP_HOME` alone does **not**
isolate them, because shortcuts always go to the real Start Menu, exactly
as Scoop does it.

## What's not there yet

**One maintainer.** The specification called this the project's critical
risk from day one, and it's still true. Everything below is smaller.

**Installation isn't verified automatically.** CI checks that all 1635
manifests in `main` decode correctly on every push, but actually
installing them is tested by hand. That's the biggest hole in the
testing.

**No code signature.** The binary isn't Authenticode-signed. It doesn't
affect the install above — PowerShell downloads don't carry the Mark of
the Web that triggers SmartScreen — but a manual download from the
Releases page in a browser may warn, some antivirus heuristics are wary
of freshly built Go binaries, and locked-down environments using
AppLocker or WDAC will refuse it.

**No `checkver`/`autoupdate`.** Those generate new manifest versions and
are a bucket *maintainer's* tools; goop consumes buckets rather than
producing them. This is the one place it isn't a drop-in replacement for
the whole `scoop` command set.

Full list with statuses in [REQUIREMENTS.md](REQUIREMENTS.md).

## Thanks

goop reads Scoop's manifests and would be pointless without them. The
real work is in the thousands of manifests the Scoop community maintains,
and in Scoop itself — this project reimplements the engine, not the
ecosystem.

## License

MIT. See [LICENSE](LICENSE).
