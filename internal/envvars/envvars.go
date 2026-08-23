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

func openWritable() (registry.Key, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
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
