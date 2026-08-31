// Package selfupdate replaces goop's own binary with the current
// release.
//
// Deliberately never automatic (REQUIREMENTS.md D7). goop exists to
// freeze toolchains; a binary that replaced itself between two `goop
// sync` runs would change the engine interpreting a lockfile without
// being asked, possibly mid-build. That is exactly what `goop hold`
// prevents for packages, and holding goop itself to a weaker rule would
// be incoherent.
package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/downloader"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/vercmp"
)

// ReleaseBase is where release assets are fetched from. The
// /releases/latest/download/ redirect is used rather than the releases
// API, whose unauthenticated limit is 60 requests per hour *per IP* --
// shared across everyone behind a corporate NAT, and enough to make an
// update fail for someone who has made no API calls at all.
// ReleaseBase is a var rather than a const only so tests can point it
// elsewhere; nothing at runtime changes it.
var ReleaseBase = "https://github.com/TanguyBaudoin/goop/releases/latest/download"

// oldSuffix marks the outgoing binary. Windows will not let a running
// image be deleted or overwritten, but it will let it be *renamed*, so
// the swap is: rename the running goop aside, put the new one in its
// place, and delete the old one on a later run when nothing has it open.
const oldSuffix = ".old"

// Result describes what Update did.
type Result struct {
	AlreadyCurrent bool
	OldVersion     string
	NewVersion     string
	Path           string
}

// Logf, if set, receives progress lines.
var Logf = func(string, ...any) {}

// Plan is what an update would do, worked out without downloading the
// 14 MB binary: a checksum file of a few dozen bytes and one redirect.
//
// Splitting this out from the work is what lets goop show the versions
// and ask before spending someone's bandwidth on a slow link.
type Plan struct {
	CurrentVersion string
	// Available is the version the release redirect names. Empty when it
	// could not be determined -- a courtesy, never a gate: the binary is
	// still probed before anything is swapped.
	Available      string
	AlreadyCurrent bool

	exe      string
	base     string
	checksum string
}

// TargetPath is the binary that would be replaced. Worth showing before
// asking: running this from a checkout replaces a locally built binary
// with a published release.
func (p Plan) TargetPath() string { return p.exe }

// Check reports what an update would do, changing nothing.
func Check(currentVersion string) (Plan, error) {
	exe, err := runningBinary()
	if err != nil {
		return Plan{}, err
	}
	return checkAt(exe, ReleaseBase, currentVersion)
}

// Update is Check followed by Apply, for callers with nothing to ask.
func Update(currentVersion string, force bool) (Result, error) {
	p, err := Check(currentVersion)
	if err != nil {
		return Result{}, err
	}
	return p.Apply(force)
}

func runningBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating the running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// checkAt is Check with the target and release source given explicitly.
//
// Split out to make the upgrade path testable at all. Check takes its
// target from os.Executable(), which inside a test is the test binary --
// so a test of it would try to replace the thing running it. And the
// interesting direction, upgrading to something newer, could otherwise
// only ever be exercised by publishing two releases and hoping, since a
// freshly released binary and the latest release are by definition the
// same version.
func checkAt(exe, base, currentVersion string) (Plan, error) {
	p := Plan{CurrentVersion: currentVersion, exe: exe, base: base}

	// A leftover from a previous update, which could not be deleted then
	// because it was still the running image.
	_ = os.Remove(exe + oldSuffix)

	// checksums.txt is a few dozen bytes, so fetching it first turns
	// "am I current?" into a cheap question instead of a 14 MB one.
	Logf("checking for a newer release")
	sums, err := downloader.FetchText(base + "/checksums.txt")
	if err != nil {
		return Plan{}, fmt.Errorf("fetching checksums.txt: %w", err)
	}
	want, err := parseChecksum(sums)
	if err != nil {
		return Plan{}, err
	}
	p.checksum = want

	have, err := fileSHA256(exe)
	if err != nil {
		return Plan{}, fmt.Errorf("hashing the running binary: %w", err)
	}
	if have == want {
		p.AlreadyCurrent = true
		p.Available = currentVersion
		return p, nil
	}

	// Best effort, and deliberately so: knowing the version before
	// downloading is a courtesy to whoever is being asked to confirm, not
	// something the update depends on. The downloaded binary is still run
	// and its own reported version checked before any swap.
	p.Available = availableVersion(base)
	return p, nil
}

// availableVersion reads the tag out of the release redirect. GitHub
// answers /releases/latest/download/<asset> with a redirect naming the
// tag, so this costs a request with no body -- and unlike the releases
// API it is not rate limited.
func availableVersion(base string) string {
	target, err := downloader.RedirectTarget(base + "/checksums.txt")
	if err != nil || target == "" {
		return ""
	}
	const marker = "/releases/download/"
	i := strings.Index(target, marker)
	if i < 0 {
		return ""
	}
	rest := target[i+len(marker):]
	tag, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return strings.TrimPrefix(tag, "v")
}

// Apply downloads the release this plan describes and swaps it in.
func (p Plan) Apply(force bool) (Result, error) {
	if p.AlreadyCurrent {
		return Result{AlreadyCurrent: true, OldVersion: p.CurrentVersion, NewVersion: p.CurrentVersion, Path: p.exe}, nil
	}

	// Downloaded through goop's own client, so proxy, per-host auth and
	// retries all apply, and Get verifies the hash before returning.
	Logf("downloading the new binary")
	staged, err := downloader.Get(paths.Cache(), p.base+"/goop.exe", "goop.exe", "sha256:"+p.checksum)
	if err != nil {
		return Result{}, fmt.Errorf("downloading goop.exe: %w", err)
	}

	// Run it before trusting it. A binary that cannot report its own
	// version is not one to swap a working install for, and this costs
	// milliseconds against the cost of leaving someone with a goop that
	// does not start.
	newVersion, err := probeVersion(staged)
	if err != nil {
		return Result{}, fmt.Errorf("the downloaded binary does not run: %w -- nothing was replaced", err)
	}

	// Refuse to go backwards unless asked. The bytes differing is not
	// enough to mean "newer": a locally built binary differs from the
	// published one of the same version, and a developer running this
	// from a checkout would otherwise have their build silently replaced
	// by an older release.
	if !force && vercmp.Compare(newVersion, p.CurrentVersion) < 0 {
		return Result{}, fmt.Errorf(
			"the current release is %s, older than the %s you are running -- pass --force to install it anyway",
			newVersion, p.CurrentVersion)
	}

	if err := swap(p.exe, staged); err != nil {
		return Result{}, err
	}
	return Result{OldVersion: p.CurrentVersion, NewVersion: newVersion, Path: p.exe}, nil
}

// swap puts staged at exe, moving the running image aside first and
// putting it back if anything goes wrong.
func swap(exe, staged string) error {
	old := exe + oldSuffix
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("moving the running binary aside: %w -- nothing was replaced", err)
	}
	if err := copyFile(staged, exe); err != nil {
		// Put it back rather than leaving the user with no goop at all.
		if restoreErr := os.Rename(old, exe); restoreErr != nil {
			return fmt.Errorf("installing the new binary failed (%w), and restoring the old one also failed (%v) -- it is at %s", err, restoreErr, old)
		}
		return fmt.Errorf("installing the new binary: %w -- the previous one was restored", err)
	}
	// Expected to fail while this process is still running from it; the
	// next update removes it.
	_ = os.Remove(old)
	return nil
}

// probeVersion runs the candidate binary and returns the version it
// reports, rejecting anything that does not identify itself as goop.
func probeVersion(path string) (string, error) {
	out, err := exec.Command(path, "version").Output()
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	first = strings.TrimSpace(first)
	rest, ok := strings.CutPrefix(first, "goop ")
	if !ok {
		return "", fmt.Errorf("it reported %q rather than a goop version", first)
	}
	return rest, nil
}

// parseChecksum pulls the hash out of a `<sha256>  goop.exe` line.
func parseChecksum(body string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[1], "goop.exe") {
			continue
		}
		h := strings.ToLower(fields[0])
		if len(h) != 64 {
			return "", fmt.Errorf("checksums.txt has a malformed hash for goop.exe: %q", fields[0])
		}
		return h, nil
	}
	return "", fmt.Errorf("checksums.txt names no goop.exe")
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
