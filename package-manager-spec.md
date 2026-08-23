# Requirements Specification — Scoop-Compatible Windows Package Manager

> **Codename: `spm`** (placeholder — to be decided).
> Version 0.2 — revised: the goal is a **Scoop replacement**, not an internal tool.

---

## 1. Goal

Reimplement the Scoop runtime, preserving its manifest format, to fix the limitations that its PowerShell implementation makes structurally unfixable.

**Scoop's product is not its runtime — it is its manifest corpus.** `main` and `extras` amount to thousands of maintained manifests with autoupdate, contributed by third parties. That corpus cannot be replicated or rebuilt. It is therefore consumed as-is, without transformation or fork.

Reference trajectory: `fnm` against `nvm`, `mise` against `pyenv`. The ecosystem is inherited; only the executor is rewritten.

## 2. Defining "better"

Five axes, each measurable. An axis that cannot be measured does not justify the project.

| # | Axis | Scoop today | Target |
|---|---|---|---|
| A1 | **Speed** | PowerShell parsing, sequential downloads | Order-of-magnitude gain on `update` and `status`; native parallel downloads |
| A2 | **Authentication** | None per repository | Native, host-keyed, encrypted credentials |
| A3 | **Reproducibility** | None | Versionable lockfile, deterministic `sync` |
| A4 | **Dependencies** | `depends` with no version constraints | Version constraints, explicit resolution, conflicts reported |
| A5 | **Provenance** | Hash only | Signature verification, traceable origin |

### 2.1 What must not regress

Enforceable in review. A replacement that regresses on any one of these is not a replacement.

| Ref | Scoop capability to preserve |
|---|---|
| NR-01 | Works without administrator rights, entirely in user space |
| NR-02 | Clean uninstall: no registry writes, no residue outside the dedicated directory |
| NR-03 | Portable installs, multiple versions side by side, version switching |
| NR-04 | User data preserved across versions (`persist`) |
| NR-05 | Third-party buckets usable without special configuration |
| NR-06 | Shim behaviour strictly equivalent |
| NR-07 | Reversibility: a user can return to Scoop without reinstalling their tools |

## 3. Manifest compatibility — the structuring requirement

This is where most of the effort goes, and the criterion that decides whether the project is viable at all.

| Ref | Requirement | Priority |
|---|---|---|
| CPT-01 | Decode the Scoop manifest format with no transformation and no added fields | Blocking |
| CPT-02 | Polymorphic fields (`url`, `hash`, `bin`: string \| array \| array of arrays) and nesting under `architecture.*` | Blocking |
| CPT-03 | `extract_dir`, `extract_to`, `env_add_path`, `env_set`, `shortcuts`, `persist` | Blocking |
| CPT-04 | `installer`, `uninstaller`, `pre_install`, `post_install` — PowerShell scripts executed by delegation to `pwsh` | Blocking |
| CPT-05 | Archive and installer formats: zip, 7z, MSI, InnoSetup, NSIS | Blocking |
| CPT-06 | `checkver` and `autoupdate` | Important |
| CPT-07 | Import an existing Scoop installation without reinstalling packages | Important |

> **CPT-04 is not negotiable.** A significant share of `extras` relies on PowerShell scripts. Partial compatibility would shrink the usable corpus and defeat the point of the project. Scripts are delegated, never reinterpreted.

**Viability gate:** an automated harness installs the 200 most-used manifests from `main` and `extras`. Below 95 % pass rate at J3, the project is reassessed.

## 4. Scope

### 4.1 In scope

Everything Scoop covers: CLI tools, portable applications, installer-based applications, public and private buckets.

### 4.2 Out of scope

| Excluded | Rationale |
|---|---|
| Installs requiring administrator rights | Core identity of the tool (NR-01) |
| Drivers and signed kernel components | Outside the user-space model |
| Node/server-licensed vendor IDEs (Keil MDK, STM32CubeIDE) | Licensing constraint, not technical — official installers |
| Linux / macOS | No demand; do not pay the abstraction cost |
| Graphical interface | — |

## 5. Functional requirements

### 5.1 Core

| Ref | Requirement | Priority |
|---|---|---|
| FR-01 | Install: download, verify, extract, expose binaries | Blocking |
| FR-02 | Uninstall with no residue (NR-02) | Blocking |
| FR-03 | Multiple versions and switching | Blocking |
| FR-04 | List installed packages with version, source bucket, state | Blocking |
| FR-05 | Update a single package or all | Blocking |
| FR-06 | Resolve dependencies with version constraints (A4) | Blocking |
| FR-07 | Search across configured buckets | Important |
| FR-08 | Clean up stale versions and cache | Important |

### 5.2 Reproducibility (A3)

| Ref | Requirement | Priority |
|---|---|---|
| FR-10 | Versionable lockfile: name, version, bucket, resolved URL, hash, architecture | Blocking |
| FR-11 | `sync` installs exactly the locked state, with no resolution step | Blocking |
| FR-12 | Drift detection with a dedicated exit code, usable in CI | Blocking |
| FR-13 | Text format, stable ordering, diffable | Blocking |

### 5.3 Buckets

| Ref | Requirement | Priority |
|---|---|---|
| FR-20 | Bucket from a Git repository — default mode, compatibility with the public ecosystem | Blocking |
| FR-21 | Bucket from an archive served by an artifact repository, no Git required | Blocking |
| FR-22 | Multiple buckets with explicit priority order and name-collision resolution | Blocking |
| FR-23 | Incremental bucket updates | Important |

### 5.4 Authentication (A2)

| Ref | Requirement | Priority |
|---|---|---|
| FR-30 | Authentication keyed by **host**, never by URL and never written into a manifest | Blocking |
| FR-31 | Bearer (access token) and Basic (user + token) | Blocking |
| FR-32 | Storage in Windows Credential Manager (DPAPI, per-user isolation) | Blocking |
| FR-33 | Resolution order: environment variable → Credential Manager → anonymous | Blocking |
| FR-34 | Host management: add, remove, list — never displaying the secret | Blocking |
| FR-35 | No credential in logs, error messages or URLs | Blocking |

### 5.5 Provenance (A5)

| Ref | Requirement | Priority |
|---|---|---|
| FR-40 | Hash verification before every extraction | Blocking |
| FR-41 | Signature verification where the manifest or bucket supplies one | Important |
| FR-42 | Traceable origin for every installed package | Important |

## 6. Technical requirements

| Ref | Requirement | Rationale |
|---|---|---|
| TR-01 | Single binary, no runtime or prerequisite to install | Bootstrap on a clean machine |
| TR-02 | Natively compiled shim | Hundreds of invocations per build; interpreter startup is disqualifying |
| TR-03 | Parallel downloads, native `Range` requests (A1) | Removes the aria2 dependency |
| TR-04 | Atomic install: full success or no visible change | No half-installed state |
| TR-05 | Never reattach `Authorization` across a cross-domain redirect | Presigned URLs carry their own auth; forcing the header leaks the token and yields a 400 |
| TR-06 | Extraction delegated to `7z.exe` and `dark.exe` | Do not reimplement; consistent with Scoop |
| TR-07 | Distinct, documented exit codes | CI integration |
| TR-08 | Actionable error messages stating cause and remedy | A differentiating quality axis |

### 6.1 Shim

The most exposed component: a defect here discredits the whole tool.

| Ref | Requirement |
|---|---|
| TR-20 | Exact propagation of the target's exit code |
| TR-21 | Faithful argument passing: spaces, quotes, escaping |
| TR-22 | Transparent `stdin`, `stdout`, `stderr` redirection with no added buffering |
| TR-23 | `Ctrl-C` propagated to the target process |
| TR-24 | Single shim shared via NTFS hard links, target described in a sidecar file |
| TR-25 | Per-invocation overhead negligible against the invoked process |
| TR-26 | Support for `.exe`, `.bat`, `.cmd`, `.ps1`, `.jar` targets |

### 6.2 Windows constraints

| Ref | Constraint | Handling |
|---|---|---|
| TR-30 | A running binary cannot be replaced | Versioned directory + `current` junction; handle the junction itself being held open |
| TR-31 | Path length limit | Shallow layout; document enabling long paths |
| TR-32 | Antivirus false positives on freshly built binaries | Sign the published binary; allowlisting procedure arranged in advance |

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

- Authentication is an **HTTP transport layer** keyed by host, never code inside the downloader. Manifests stay unchanged and cannot leak a credential.
- The lockfile is produced by the resolver, never hand-written.
- The shim knows nothing of buckets or the network: it reads a target path and executes.
- The `pwsh` bridge is isolated; no business logic passes through it.

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

- **Rust** — better on one point only, polymorphic manifest decoding (declarative rather than manual), but contributes none of its structural advantages to a project dominated by network and filesystem I/O. Entry barrier unjustified.
- **Python** — bootstrap is impossible (the tool that installs the toolchain would itself require a preinstalled runtime); the shim cannot absorb interpreter startup across hundreds of invocations per build.
- **Forking Scoop** — permanent rebasing against an active project while inheriting a language not chosen. More expensive than a rewrite over the medium term.

### 8.2 Known technical cost

Polymorphic manifest decoding requires custom decoders across roughly a dozen types in Go. Around 200 lines, isolated in a dedicated package, covered by tests against the real corpus. A one-time cost, not recurring.

## 9. Governance

The dominant risk is the **bus factor**: a Scoop replacement maintained by one person is a trap for its users.

| Ref | Requirement |
|---|---|
| GOV-01 | Run as a shared project from the start, never presented as finished work |
| GOV-02 | At least two people able to modify each component |
| GOV-03 | Code review on every change |
| GOV-04 | Conventional Commits |
| GOV-05 | Architecture documentation in the repository, current at each milestone |
| GOV-06 | Source opened; outside contribution possible from J3 |
| GOV-07 | Documented reversibility (NR-07): return to Scoop without reinstalling |

> GOV-07 is also the adoption argument: "it reads the same manifests, you can switch back to Scoop whenever you want" is defensible. "I wrote my own package manager" is not.

## 10. Open decisions

| # | Question | Options | Due |
|---|---|---|---|
| D1 | Project name | — | Before J1 |
| D2 | Lockfile format | TOML \| canonical JSON | Before J3 |
| D3 | Version-constraint grammar (A4) | SemVer \| extended Scoop lexicographic ordering | Before J3 |
| D4 | Install root | Reuse `SCOOP` \| own directory + import | Before J1 |
| D5 | Signature mechanism (A5) | minisign \| Sigstore \| Authenticode | Before J4 |
| D6 | Licence and timing of open-sourcing | — | Before J3 |

## 11. Milestones

| Milestone | Content | Exit criterion |
|---|---|---|
| **J0 — Shim** | Native shim alone, no package manager | Full CMake/Ninja build identical to a manual install; TR-20 to TR-26 validated |
| **J1 — Core** | Manifest decoding, download, hash, extraction, install/uninstall/list, Git buckets | 50 `main` manifests install |
| **J2 — Compatibility** | `pwsh` bridge, MSI/Inno/NSIS, `persist`, `env_*`, `shortcuts`, Scoop import | Harness: ≥ 95 % across 200 `main` + `extras` manifests |
| **J3 — Differentiation** | Lockfile, `sync`, parallelism, versioned dependencies, auth, Git-less buckets | A1 to A4 measured and documented |
| **J4 — Release** | Signing, provenance, documentation, open-sourcing | Usable by an outsider with no assistance |

**Mandatory ordering: shim before resolver.** It is the most exposed component and the one that decides user trust.

## 12. Acceptance criteria

1. Harness passes ≥ 95 % across the 200 most-used `main` and `extras` manifests.
2. `update` and `status` gain at least an order of magnitude over Scoop, measured on an identical set.
3. A `sync` on two separate machines produces trees with matching hashes.
4. No secret in cleartext on disk, in a log, or in a URL.
5. No regression on NR-01 through NR-07, verified point by point.
6. A full CMake/Ninja build shows no measurable slowdown.
7. An existing Scoop installation imports without reinstalling packages.
8. CI fails with a dedicated exit code when installed state drifts from the lockfile.

## 13. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Manifest compatibility underestimated (§3) | **Critical** | J2 dedicated entirely to it; harness as the exit gate |
| Bus factor on a single maintainer | Critical | GOV-01 to GOV-03 and GOV-06; abandon if not met |
| Shim defect going undetected | High | J0 isolated, validated against a real build before anything else is written |
| Gradual drift of the upstream format | Medium | Format is stable; harness runs in CI |
| Antivirus blocking | Medium | Sign the binary; IT procedure started at J0 |
| Ongoing maintenance cost | High | Open-source (GOV-06); formal reassessment at end of J2 |

> **Decision point — end of J2.** If the harness stays below 95 %, full replacement is not viable: fall back to a companion tool alongside Scoop covering axes A2 and A3 only, at a fraction of the maintenance cost.
