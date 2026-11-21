// Package envvars implements env_set/env_add_path (CPT-03): setting and
// unsetting per-user environment variables via HKCU\Environment, the
// same registry location Scoop itself uses (never HKLM -- NR-01, no
// admin rights, no system-wide changes). Every write broadcasts
// WM_SETTINGCHANGE so already-running processes that listen for it (e.g.
// Explorer) pick up the change; a shell already open when a variable is
// set still needs restarting to see it, which is an unavoidable
// property of how process environments work on Windows, not something
// goop can work around.
package envvars

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	hwndBroadcast   = 0xffff
	wmSettingChange = 0x001A
	smtoAbortIfHung = 0x0002
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
)

func broadcastChange() {
	param, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	procSendMessageTimeoutW.Call(
		uintptr(hwndBroadcast),
		uintptr(wmSettingChange),
		0,
		uintptr(unsafe.Pointer(param)),
		uintptr(smtoAbortIfHung),
		5000,
		uintptr(unsafe.Pointer(&result)),
	)
}

// subkey is the HKCU subkey this package reads and writes.
//
// Always `Environment` in real use. The override exists because nothing
// else isolates it: a test can point GOOP_HOME, APPDATA and LOCALAPPDATA
// at a temp directory and still have env_add_path write to the user's
// real PATH, since the registry is not under any of them. The install
// harness installs `ack`, which depends on perl, whose manifest uses
// env_add_path -- so every harness run permanently appended two dead
// entries to the real per-user PATH. Found with 80 of 98 entries dead on
// the development machine, about thirty of them from TestInstallHarness
// temp directories that had long since been deleted.
//
// Set GOOP_ENV_REGISTRY_SUBKEY to redirect writes somewhere harmless.
// Only honoured for a name that is not `Environment`, so this can never
// be the reason a real install fails to write where it should.
func subkey() string {
	if v := os.Getenv("GOOP_ENV_REGISTRY_SUBKEY"); v != "" && v != `Environment` {
		return v
	}
	return `Environment`
}

func openWritable() (registry.Key, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, subkey(), registry.QUERY_VALUE|registry.SET_VALUE)
	return k, err
}

// Set sets a user environment variable.
func Set(name, value string) error {
	k, err := openWritable()
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer k.Close()
	if err := k.SetExpandStringValue(name, value); err != nil {
		return fmt.Errorf("set %s: %w", name, err)
	}
	broadcastChange()
	return nil
}

// Unset removes a user environment variable. Not being set is not an error.
func Unset(name string) error {
	k, err := openWritable()
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("unset %s: %w", name, err)
	}
	broadcastChange()
	return nil
}

func currentPath(k registry.Key) ([]string, error) {
	return currentListVar(k, "Path")
}

// PathEntries reads the persisted per-user PATH, split into entries.
//
// The persisted one, not the running process's: a shell inherited its
// copy when it started and may be hours out of date, which is exactly
// the confusion this package's doc comment describes. Anything reasoning
// about what goop has written has to look at the registry.
func PathEntries() ([]string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, subkey(), registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil, nil
		}
		return nil, fmt.Errorf("open HKCU\\%s: %w", subkey(), err)
	}
	defer k.Close()
	return currentPath(k)
}

// currentListVar reads a semicolon-separated HKCU\Environment value
// (Path, PSModulePath, ...), split into its entries.
func currentListVar(k registry.Key, name string) ([]string, error) {
	val, _, err := k.GetStringValue(name)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, part := range strings.Split(val, ";") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out, nil
}

func samePathEntry(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, `\`), strings.TrimRight(b, `\`))
}

// AddToPath prepends dir to the user PATH, unless it's already present.
func AddToPath(dir string) error {
	k, err := openWritable()
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer k.Close()

	parts, err := currentPath(k)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if samePathEntry(p, dir) {
			return nil
		}
	}
	newParts := append([]string{dir}, parts...)
	if err := k.SetExpandStringValue("Path", strings.Join(newParts, ";")); err != nil {
		return fmt.Errorf("update Path: %w", err)
	}
	broadcastChange()
	return nil
}

// RemoveFromPath removes dir from the user PATH if present.
func RemoveFromPath(dir string) error {
	k, err := openWritable()
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer k.Close()

	parts, err := currentPath(k)
	if err != nil {
		return err
	}
	var newParts []string
	changed := false
	for _, p := range parts {
		if samePathEntry(p, dir) {
			changed = true
			continue
		}
		newParts = append(newParts, p)
	}
	if !changed {
		return nil
	}
	if err := k.SetExpandStringValue("Path", strings.Join(newParts, ";")); err != nil {
		return fmt.Errorf("update Path: %w", err)
	}
	broadcastChange()
	return nil
}

// AddToPSModulePath prepends dir to the user PSModulePath, unless it's
// already present -- same list-variable semantics as AddToPath, used
// for a manifest's `psmodule` field (real Scoop's own
// ensure_in_psmodulepath, lib/psmodules.ps1) so an installed module is
// importable by name without the user needing to add goop's modules
// directory themselves.
func AddToPSModulePath(dir string) error {
	k, err := openWritable()
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer k.Close()

	parts, err := currentListVar(k, "PSModulePath")
	if err != nil {
		return err
	}
	for _, p := range parts {
		if samePathEntry(p, dir) {
			return nil
		}
	}
	newParts := append([]string{dir}, parts...)
	if err := k.SetExpandStringValue("PSModulePath", strings.Join(newParts, ";")); err != nil {
		return fmt.Errorf("update PSModulePath: %w", err)
	}
	broadcastChange()
	return nil
}
