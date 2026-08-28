# Requirements Specification — goop

> Version 1.0 — revised 2026-08-23 against the implementation as shipped.
> Goal: a **Scoop replacement**, not an internal tool.

Every requirement below carries a status:

| | Meaning |
|---|---|
| **Met** | Implemented and verified against real Scoop behaviour, real manifests, or real installs |
| **Partial** | Implemented in a narrower form than written; the gap is stated |
| **Diverged** | Deliberately implemented differently; the reason is stated |
| **Open** | Not implemented |

Status claims are made against the code, not against intent. Where this
document and the code disagree, the code is right and this document is
the bug.

---

## 1. Goal

Reimplement the Scoop runtime, preserving its manifest format, to fix the
limitations that its PowerShell implementation makes structurally
unfixable.

**Scoop's product is not its runtime — it is its manifest corpus.**
`main` and `extras` amount to thousands of maintained manifests with
autoupdate, contributed by third parties. That corpus cannot be
replicated or rebuilt. It is therefore consumed as-is, without
transformation or fork.

Reference trajectory: `fnm` against `nvm`, `mise` against `pyenv`. The
ecosystem is inherited; only the executor is rewritten.

## 2. Defining "better"

Five axes, each measurable. An axis that cannot be measured does not
justify the project.

| # | Axis | Scoop today | Target | Status |
|---|---|---|---|---|
| A1 | **Speed** | PowerShell parsing, sequential downloads | Order-of-magnitude gain on `update` and `status`; native parallel downloads | **Met** |
| A2 | **Authentication** | None per repository | Native, host-keyed, encrypted credentials | **Met** |
| A3 | **Reproducibility** | None | Versionable lockfile, deterministic `sync` | **Met** |
| A4 | **Dependencies** | `depends` with no version constraints | Version constraints, explicit resolution, conflicts reported | **Partial** |
| A5 | **Provenance** | Hash only | Signature verification, traceable origin | **Partial** |

**A4 is partial by the corpus, not by the code.** Version constraints are
implemented and work in install specs (`goop install extras/mpv@>=0.40`),
and dependency resolution is recursive with cycle detection. But the
Scoop manifest schema has no place for a constraint inside `depends`, and
no real manifest carries one — so against the actual corpus, dependency
resolution is by name only, exactly as in Scoop. The added value is at
the command line and in lockfiles, not in dependency edges.

**A5 is partial in the same way.** `goop verify` checks minisign
signatures, and every installed package records where it came from, but
no Scoop manifest supplies a signature — so verification is a deliberate
act by the user, never an automatic step in `install`.

### 2.1 What must not regress

Enforceable in review. A replacement that regresses on any one of these
is not a replacement.

| Ref | Scoop capability to preserve | Status |
|---|---|---|
| NR-01 | Works without administrator rights, entirely in user space | **Met** |
| NR-02 | Clean uninstall: no registry writes, no residue outside the dedicated directory | **Met** |
| NR-03 | Portable installs, multiple versions side by side, version switching | **Met** |
| NR-04 | User data preserved across versions (`persist`) | **Met** |
| NR-05 | Third-party buckets usable without special configuration | **Met** |
| NR-06 | Shim behaviour strictly equivalent | **Met** |
| NR-07 | Reversibility: a user can return to Scoop without reinstalling their tools | **Partial** |

**NR-07 is partial, and was overstated until now.** The on-disk layout is
genuinely Scoop's — `apps\<name>\<version>\` with a `current` junction —
so packages installed by goop are not locked into a private format and
remain runnable if goop is abandoned. But goop records its own
`goop-install.json` rather than Scoop's `install.json`, so Scoop taking
over such a tree would know how to run those packages and not which
bucket to update them from. The direction that *is* verified is the other
one: `goop import` adopts a real Scoop installation without reinstalling
anything (CPT-07). Handing a tree back has never been performed or
documented, which is what the status now says.

## 3. Manifest compatibility — the structuring requirement

This is where most of the effort goes, and the criterion that decides
whether the project is viable at all.

| Ref | Requirement | Priority | Status |
|---|---|---|---|
| CPT-01 | Decode the Scoop manifest format with no transformation and no added fields | Blocking | **Met** |
| CPT-02 | Polymorphic fields (`url`, `hash`, `bin`: string \| array \| array of arrays) and nesting under `architecture.*` | Blocking | **Met** |
| CPT-03 | `extract_dir`, `extract_to`, `env_add_path`, `env_set`, `shortcuts`, `persist` | Blocking | **Met** |
| CPT-04 | `installer`, `uninstaller`, `pre_install`, `post_install` — PowerShell scripts executed by delegation | Blocking | **Met** |
| CPT-05 | Archive and installer formats: zip, 7z, MSI, InnoSetup, NSIS | Blocking | **Met** |
| CPT-06 | `checkver` and `autoupdate` | Important | **Open** |
| CPT-07 | Import an existing Scoop installation without reinstalling packages | Important | **Met** |
| CPT-08 | PowerShell modules (`psmodule`) | Important | **Met** |

> **CPT-04 is not negotiable.** A significant share of `extras` relies on
> PowerShell scripts. Partial compatibility would shrink the usable
> corpus and defeat the point of the project. Scripts are delegated,
> never reinterpreted.

**CPT-06 is deliberately unimplemented.** `checkver` and `autoupdate` are
*bucket-maintainer* tooling: they generate new manifest versions upstream
and belong to whoever maintains the bucket. goop consumes the result. The
fields are preserved on decode so a manifest round-trips intact, but goop
never acts on them. This is the one place where goop is knowingly not a
drop-in replacement for the full `scoop` command set.

**Viability gate — the harness now exists, and it measures something
different from what was originally specified.**

The gate as written called for installing the 200 most-used manifests.
That was the wrong measurement, and building it made the reason obvious:
popularity says nothing about which code path a package exercises. Two
hundred plain zips would prove less than a handful spanning every
extraction and hook mechanism goop has.

`TestInstallHarness` (`internal/installer/harness_test.go`) therefore
covers *shapes*. Each package was chosen by grepping the real `main`
bucket for the field that puts it on a distinct path: a bare executable,
a plain zip, an MSI with `post_install`/`shortcuts`/`persist`, a
`psmodule`, a `.7z` that forces the implicit 7zip helper to install, a
`.tar.xz` that 7z must unpack in two passes, an InnoSetup installer
(CPT-05), and a declared `depends` that pulls its dependency in first.

Each is installed into an isolated root, checked — `current` resolves,
every declared `bin` produced a shim whose sidecar names a target that
exists and is not still inside `.partial` — then uninstalled and checked
again for residue (NR-02). Current result: **8/8, in about a minute.**

It runs weekly and on demand rather than on every push, because it
downloads from real upstream URLs and can fail for reasons unrelated to
goop. Set `GOOP_HARNESS_APPS` to point it at any other set.

The decode corpus test remains, and still runs on every push:
**1635/1635 manifests from ScoopInstaller/Main decode without error**.

## 4. Scope

### 4.1 In scope

Everything Scoop covers: CLI tools, portable applications,
installer-based applications, public and private buckets.

### 4.2 Out of scope

| Excluded | Rationale |
|---|---|
| Installs requiring administrator rights | Core identity of the tool (NR-01) |
| Drivers and signed kernel components | Outside the user-space model |
| Node/server-licensed vendor IDEs (Keil MDK, STM32CubeIDE) | Licensing constraint, not technical — official installers |
| Linux / macOS | No demand; do not pay the abstraction cost |
| Graphical interface | — |
| Manifest authoring tooling (`checkver`, `autoupdate`) | Bucket-maintainer concern, not package-manager concern (CPT-06) |
| Third-party malware scanning (e.g. VirusTotal) | Requires a user-supplied API key and sends data to a third party; hash verification (FR-40) already covers integrity |

## 5. Functional requirements

### 5.1 Core

| Ref | Requirement | Priority | Status |
|---|---|---|---|
| FR-01 | Install: download, verify, extract, expose binaries | Blocking | **Met** |
| FR-02 | Uninstall with no residue (NR-02) | Blocking | **Met** |
| FR-03 | Multiple versions and switching | Blocking | **Met** |
| FR-04 | List installed packages with version, source bucket, state | Blocking | **Met** |
| FR-05 | Update a single package or all | Blocking | **Met** |
| FR-06 | Resolve dependencies with version constraints (A4) | Blocking | **Partial** |
| FR-07 | Search across configured buckets | Important | **Met** |
| FR-08 | Clean up stale versions and cache | Important | **Met** |

### 5.2 Reproducibility (A3)

| Ref | Requirement | Priority | Status |
|---|---|---|---|
| FR-10 | Versionable lockfile: name, version, bucket, resolved URL, hash, architecture | Blocking | **Met** |
| FR-11 | `sync` installs exactly the locked state, with no resolution step | Blocking | **Met** |
| FR-12 | Drift detection with a dedicated exit code, usable in CI | Blocking | **Met** |
| FR-13 | Text format, stable ordering, diffable | Blocking | **Met** |
| FR-14 | Lockfile locatable inside a project repository, not only under the goop root | Blocking | **Met** |

**FR-14 was added after the fact.** The original design put lockfiles
under the goop home, which is wrong for the actual use case: a lockfile
describes what a *project* needs, so it belongs in that project's
repository and evolves on its branches. `goop lock --file ./chipA.lock.json`
and `goop sync --file` take a path.

### 5.3 Buckets

| Ref | Requirement | Priority | Status |
|---|---|---|---|
| FR-20 | Bucket from a Git repository — default mode, compatibility with the public ecosystem | Blocking | **Met** |
| FR-21 | Bucket from an archive served by an artifact repository, no Git required | Blocking | **Met** |
| FR-22 | Multiple buckets with explicit priority order and name-collision resolution | Blocking | **Met** |
| FR-23 | Incremental bucket updates | Important | **Met** |

FR-23 is met by refreshing buckets in parallel and by a staleness
threshold that skips a refresh that just happened, mirroring Scoop's own
3-hour rule.

### 5.4 Authentication (A2)

| Ref | Requirement | Priority | Status |
|---|---|---|---|
| FR-30 | Authentication keyed by **host**, never by URL and never written into a manifest | Blocking | **Met** |
| FR-31 | Bearer (access token) and Basic (user + token) | Blocking | **Met** |
| FR-32 | Storage in Windows Credential Manager (DPAPI, per-user isolation) | Blocking | **Met** |
| FR-33 | Resolution order: environment variable → Credential Manager → anonymous | Blocking | **Met** |
| FR-34 | Host management: add, remove, list — never displaying the secret | Blocking | **Met** |
| FR-35 | No credential in logs, error messages or URLs | Blocking | **Met** |

### 5.5 Provenance (A5)

| Ref | Requirement | Priority | Status |
|---|---|---|---|
| FR-40 | Hash verification before every extraction | Blocking | **Met** |
| FR-41 | Signature verification where the manifest or bucket supplies one | Important | **Partial** |
| FR-42 | Traceable origin for every installed package | Important | **Met** |

### 5.6 Grouping and workflow

Requirements with no Scoop equivalent, added during implementation.

| Ref | Requirement | Priority | Status |
|---|---|---|---|
| FR-50 | Named profiles grouping packages, with membership as a removal safety net | Important | **Met** |
| FR-51 | Explain why a package is installed (`why`) | Important | **Met** |
| FR-52 | Pin a package against routine updates (`hold`/`unhold`) | Important | **Met** |
| FR-53 | Uninstall cascades to packages declaring the removed one as a dependency | Important | **Met** |
| FR-54 | Rebuild shims, shortcuts and environment for an existing install (`reset`) | Important | **Met** |
| FR-55 | Download and verify without installing (`download`) | Important | **Met** |
| FR-56 | Maven coordinates installable as first-class packages | Important | **Met** |
| FR-57 | Shell completion for PowerShell and bash, self-registering | Important | **Met** |
| FR-58 | User-configurable behaviour: install root, cache limit, bucket staleness | Important | **Met** |

**FR-53 deliberately follows declared dependencies only**, never the
implicit extraction helpers of TR-06. Removing 7zip must not remove every
package that was once unpacked with it; `perl` disappearing must take
`ack` with it. The distinction is between a build-time tool and a runtime
dependency.

**FR-58 exists because defaults are opinions.** Anything the tool decides
for the user — where packages live, how much cache to keep, how long a
bucket stays fresh — is configurable rather than hard-coded.

## 6. Technical requirements

| Ref | Requirement | Rationale | Status |
|---|---|---|---|
| TR-01 | Single binary, no runtime or prerequisite to install | Bootstrap on a clean machine | **Met** |
| TR-02 | Natively compiled shim | Hundreds of invocations per build; interpreter startup is disqualifying | **Met** |
| TR-03 | Parallel downloads, native `Range` requests (A1) | Removes the aria2 dependency | **Met** |
| TR-04 | Atomic install: full success or no visible change | No half-installed state | **Met** |
| TR-05 | Never reattach `Authorization` across a cross-domain redirect | Presigned URLs carry their own auth; forcing the header leaks the token and yields a 400 | **Met** |
| TR-06 | Extraction delegated to external helpers | Do not reimplement; consistent with Scoop | **Diverged** |
| TR-07 | Distinct, documented exit codes | CI integration | **Met** |
| TR-08 | Actionable error messages stating cause and remedy | A differentiating quality axis | **Met** |

**TR-06 diverged in both directions, and both were forced by reality.**

Zip and tar.gz are extracted **natively in Go**, not delegated. They are
the overwhelming majority of the corpus, the Go standard library already
implements them, and going out to a subprocess for every install would
have surrendered the A1 speed gain for nothing. Only formats Go does not
cover — `.7z`, MSI, InnoSetup, NSIS, compressed tars — reach an external
helper.

In the other direction, the original text assumed those helpers were
simply present. They are not, on a clean machine, and telling a user to
install 7-Zip with *another package manager* to bootstrap this one is
absurd. goop resolves them as **implicit dependencies** and installs them
itself, mirroring Scoop's `Get-InstallationHelper`. One deliberate
narrowing: Scoop can use `lessmsi` for MSI files, goop always uses
`msiexec /a` and never installs lessmsi.

**TR-04 is stronger than it reads, and the strength is where the bugs
were.** Hooks run against a `<version>.partial` staging directory, and a
single rename commits the install. Anything a hook *persists* past that
rename — a shim sidecar naming its target, a Start Menu shortcut — would
otherwise point into a directory that no longer exists. Paths are
rewritten at commit time, with a post-commit pass as a backstop.

### 6.1 Shim

The most exposed component: a defect here discredits the whole tool.

| Ref | Requirement | Status |
|---|---|---|
| TR-20 | Exact propagation of the target's exit code | **Met** |
| TR-21 | Faithful argument passing: spaces, quotes, escaping | **Met** |
| TR-22 | Transparent `stdin`, `stdout`, `stderr` redirection with no added buffering | **Met** |
| TR-23 | `Ctrl-C` propagated to the target process | **Met** |
| TR-24 | Single shim shared via NTFS hard links, target described in a sidecar file | **Met** |
| TR-25 | Per-invocation overhead negligible against the invoked process | **Met** |
| TR-26 | Support for `.exe`, `.bat`, `.cmd`, `.ps1`, `.jar` targets | **Met** |

TR-23 is implemented by *suppressing* Ctrl-C in the shim rather than
forwarding it: the child shares the console group, so Windows already
delivers `CTRL_C_EVENT` to it. A shim that also acted on the signal would
exit first and orphan its child mid-interrupt.

TR-24's sidecar is written **without a BOM**. `Set-Content -Encoding utf8`
in Windows PowerShell 5.1 emits one, and a BOM in the sidecar makes the
target path unparseable.

### 6.2 Windows constraints

| Ref | Constraint | Handling | Status |
|---|---|---|---|
| TR-30 | A running binary cannot be replaced | Versioned directory + `current` junction; handle the junction itself being held open | **Met** |
| TR-31 | Path length limit | Shallow layout; document enabling long paths | **Met** |
| TR-32 | Antivirus false positives on freshly built binaries | Sign the published binary; allowlisting procedure arranged in advance | **Partial** |

TR-30 required more than the junction. A self-updating application —
Zen Browser is the case that surfaced it — replaces the `current`
junction with a **real directory**, after which every operation assuming
a junction fails. Uninstall and list both handle that shape.

TR-32 is partial: `scripts/sign.ps1` exists, but no release binary has
been published or signed yet.

## 7. Architecture

```
                    ┌──────────────┐
                    │     CLI      │
                    └──────┬───────┘
      ┌────────────────────┼────────────────────┐
      │                    │                    │
┌─────▼─────┐      ┌───────▼───────┐    ┌───────▼───────┐
│  Buckets  │      │   Resolver    │    │   Lockfile    │
│ git | zip │      │ manifests+dep │    │  read/write   │
└─────┬─────┘      └───────┬───────┘    └───────────────┘
      └──────┬─────────────┘
             │
     ┌───────▼────────┐      ┌──────────────────┐
     │   Downloader   │◄─────┤ Auth transport   │
     │   (parallel)   │      │   (per host)     │
     └───────┬────────┘      └────────┬─────────┘
             │                        │
     ┌───────▼────────┐      ┌────────▼─────────┐
     │   Installer    │      │ Credential store │
     │ extract + shim │      │  env | WinCred   │
     └───────┬────────┘      └──────────────────┘
             │
     ┌───────▼────────┐
     │  pwsh bridge   │  (installer.script, pre/post_install)
     └────────────────┘
```

**Architectural invariants:**

- Authentication is an **HTTP transport layer** keyed by host, never code
  inside the downloader. Manifests stay unchanged and cannot leak a
  credential.
- The lockfile is produced by the resolver, never hand-written.
- The shim knows nothing of buckets or the network: it reads a target
  path and executes.
- The `pwsh` bridge is isolated; no business logic passes through it.
- Profiles and Maven coordinates sit **above** the installer and add no
  case to it: a Maven artifact resolves to the same download-verify-
  extract pipeline, and a profile is metadata over installed packages.

## 8. Technology choices

### 8.1 Language — Go

| Criterion | Assessment |
|---|---|
| Single binary, zero runtime | Native (TR-01) |
| Download parallelism | Goroutines — A1 at low cost |
| Win32 APIs: junctions, Credential Manager, hard links | Mature coverage |
| Iteration speed | Decisive outside full-time work |
| Readability for an embedded-C engineer | Decisive for outside contribution |

**Alternatives rejected:**

- **Rust** — better on one point only, polymorphic manifest decoding
  (declarative rather than manual), but contributes none of its
  structural advantages to a project dominated by network and filesystem
  I/O. Entry barrier unjustified.
- **Python** — bootstrap is impossible (the tool that installs the
  toolchain would itself require a preinstalled runtime); the shim cannot
  absorb interpreter startup across hundreds of invocations per build.
- **Forking Scoop** — permanent rebasing against an active project while
  inheriting a language not chosen. More expensive than a rewrite over
  the medium term.

**Retrospective:** the choice held. The dominant cost was not the
language but Windows and PowerShell 5.1 behaviour — BOM emission,
console encoding, case-insensitive paths against byte-exact comparison,
applications that rewrite their own install directory. None of these
would have been cheaper in Rust.

### 8.2 Known technical cost

Polymorphic manifest decoding requires custom decoders across roughly a
dozen types in Go, isolated in a dedicated package and covered by tests
against the real corpus. The estimate of a one-time, non-recurring cost
proved correct: the decoder has needed no structural change, and decodes
the entire `main` bucket without error.

## 9. Governance

The dominant risk is the **bus factor**: a Scoop replacement maintained
by one person is a trap for its users.

| Ref | Requirement | Status |
|---|---|---|
| GOV-01 | Project run collectively from the start, never presented as finished | **Open** |
| GOV-02 | At least two people able to modify every component | **Open** |
| GOV-03 | Systematic code review | **Open** |
| GOV-04 | Conventional Commits | **Diverged** |
| GOV-05 | Architecture documentation in the repository, current at every milestone | **Met** |
| GOV-06 | Code opened; outside contribution possible from J3 | **Met** |
| GOV-07 | Documented reversibility (NR-07): return to Scoop without reinstalling | **Partial** |

> GOV-07 is also the adoption argument: "it reads the same manifests, you
> can go back to Scoop whenever you like" is defensible. "I wrote my own
> package manager" is not.

**This section is the honest weak point of the project.** GOV-01 through
GOV-03 are unmet: goop has one maintainer, no second reviewer, and no
review process. The risk the specification named as *critical* is the one
still outstanding. Opening the code (GOV-06, done 2026-08-23) is a
precondition for fixing it, not the fix.

GOV-04 diverged: commit messages are plain imperative subjects with a
body explaining the reasoning, not `feat:`/`fix:` prefixes. Conventional
Commits buys automated changelog generation, which a hand-written
CHANGELOG does not need, and the prefix carries less information than the
sentence it replaces. Revisit if release automation is ever added.

## 10. Decisions — resolved

The specification originally left six decisions open; a seventh was
settled during implementation. All are recorded here.

| # | Question | Resolution |
|---|---|---|
| D1 | Project name | **goop** — the `spm` placeholder is retired |
| D2 | Lockfile format | **JSON**, stably ordered, diffable (FR-13) |
| D3 | Version constraint grammar | Comparison operators (`@>=0.40`, `@1.8.2`) over Scoop's version ordering, extended to handle bare numeric builds |
| D4 | Install directory | **Own root** (`~/goop`, overridable) plus an import path from an existing Scoop install (CPT-07) |
| D5 | Signature mechanism | **minisign** — no PKI to operate, no third-party service, verifiable offline |
| D6 | License and timing of opening | **MIT**, opened 2026-08-23 |
| D7 | Should goop update itself automatically? | **No.** Explicit `goop self-update` only (implemented), with at most a passive notice that a newer version exists |

**D7's reasoning, since it will look like an omission otherwise.** goop
exists to freeze toolchains. A binary that updates itself between two
`goop sync` runs changes the engine interpreting your lockfiles without
being asked, possibly mid-build — which is precisely what `goop hold`
prevents for packages. Applying a weaker rule to goop itself would be
incoherent. Scoop behaves the same way and never updates unasked.
Verifying the new binary's minisign signature with the key embedded in
the already-trusted one is the point at which `scripts/sign.ps1` finally
earns its place; that check is not circular the way verifying an
installer with itself would be.

## 11. Milestones

| Milestone | Content | Exit criterion | Status |
|---|---|---|---|
| **J0 — Shim** | Native shim alone | TR-20 to TR-26 validated | **Met** |
| **J1 — Core** | Manifest decoding, download, hash, extraction, install/uninstall/list, Git buckets | 50 `main` manifests install | **Met** |
| **J2 — Compatibility** | `pwsh` bridge, MSI/Inno/NSIS, `persist`, `env_*`, `shortcuts`, Scoop import | Harness ≥ 95 % on a representative manifest set | **Met** |
| **J3 — Differentiation** | Lockfile, `sync`, parallelism, versioned dependencies, auth, Git-less buckets | A1–A4 measured and documented | **Met** |
| **J4 — Publication** | Signature, provenance, documentation, opening the code | Usable by a third party without assistance | **Partial** |

**Imposed order: the shim before the resolver.** The most exposed
component, and the one that decides trust. This order was respected and
was correct.

J2 is complete. Its functional content -- the `pwsh` bridge, every
installer format, `persist`, `env_*`, `shortcuts` and Scoop import --
works against real manifests, and its exit criterion is now measured
rather than asserted: the install harness exercises each of those paths
against real packages and passes 8/8 (§3).

J4 is partial: the code is open, documented and CI-verified, but no
signed release binary has been published (TR-32), so installation still
means building from a clone.

## 12. Acceptance criteria

| # | Criterion | Status | Evidence |
|---|---|---|---|
| 1 | ≥ 95 % pass rate on a representative manifest set | **Met** | Decode 1635/1635 on every push; install harness 8/8 by shape, weekly (§3) |
| 2 | `update` and `status` gain at least an order of magnitude over Scoop | **Met** | Measured head-to-head, ~60× |
| 3 | A `sync` on two separate machines yields matching trees | **Unverified** | Single-machine only; never tested across two |
| 4 | No plaintext secret on disk, in a log, or in a URL | **Met** | FR-32/FR-35 |
| 5 | No regression on NR-01 to NR-07, verified point by point | **Partial** | NR-07 partial, see §2.1 |
| 6 | A full CMake/Ninja build shows no duration regression | **Unverified** | Never measured |
| 7 | An existing Scoop install imports without reinstalling packages | **Met** | Real migration performed |
| 8 | CI fails with a dedicated exit code when state diverges from the lockfile | **Met** | Exit code 3 |

Criterion 2 is met with margin: the measured head-to-head gain is
roughly **60×**, not the single order of magnitude specified. The
difference comes from process startup — Scoop pays a PowerShell
interpreter per invocation, goop pays a process exec.

**Criteria 3 and 6 are unverified, not met.** Both were written as
measurements and neither measurement was ever taken. Criterion 6 in
particular was a founding justification for the project — the build times
that motivated a native shim — and it has never been quantified against
Scoop. Marking these "met" on the strength of the code looking right is
exactly the error this column exists to prevent.

## 13. Risks

| Risk | Impact | Mitigation | Status |
|---|---|---|---|
| Manifest compatibility underestimated (§3) | **Critical** | J2 dedicated entirely; harness as the exit gate | **Closed** — corpus decodes 1635/1635 |
| Bus factor on a single maintainer | Critical | GOV-01 to GOV-03 and GOV-06; abandon if not held | **Open** — the dominant remaining risk |
| Undetected shim defect | High | J0 isolated, validated on a real build before anything else | **Closed** |
| Progressive divergence of the upstream format | Medium | Stable format; harness in continuous integration | **Mitigated** — corpus decode runs on every CI build |
| Antivirus blocking | Medium | Sign the binary; IT procedure started at J0 | **Open** — no signed release yet |
| Permanent maintenance cost | High | Open the code (GOV-06); formal reassessment at the end of J2 | **Open** |

> **The decision point named at the end of J2 has passed, and passed
> well** — the fallback to "a complement to Scoop covering only A2 and
> A3" is not needed. The risk that remains is the one the specification
> called critical from the start and which no amount of code closes: a
> single maintainer.

## 14. Known gaps

Collected from the statuses above, in the order they should be addressed.

1. **Single maintainer** (GOV-01–GOV-03). Named critical at the outset,
   still open. Everything else on this list is smaller.
2. **The install harness covers shapes, not breadth** (§3). It exercises
   every extraction and hook mechanism against real packages, which is
   what catches regressions, but it installs eight packages rather than
   hundreds -- a manifest using some combination nothing in the set
   touches could still break unnoticed.
3. **No signed release** (TR-32, J4). Installation requires a clone and a
   Go toolchain; `scripts/install.ps1` documents this and bootstraps from
   source.
4. **Two acceptance criteria never measured** (3 and 6). Cross-machine
   `sync` reproducibility, and build-duration parity on a real CMake/Ninja
   build — the latter being one of the project's founding justifications.
5. **`checkver`/`autoupdate` unimplemented** (CPT-06). Deliberate — see
   §3 — but it does mean goop is not a complete replacement for the
   `scoop` command set for bucket maintainers.
6. **Dependency version constraints unexercised** (A4, FR-06). The
   machinery works; the corpus never uses it.
7. **Signature verification is manual** (A5, FR-41). No manifest supplies
   a signature, so nothing verifies automatically.
8. **Reversibility is one-way in practice** (NR-07). `goop import` adopts
   a Scoop tree; handing one back to Scoop has never been performed or
   documented, because goop writes its own install metadata.
