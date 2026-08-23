// Package credstore stores per-host credentials in the Windows
// Credential Manager (EXF-32: DPAPI-backed, per-user isolated -- the
// OS handles the encryption, goop never does). Entries are namespaced
// under a "goop:auth:" TargetName prefix so goop never touches another
// application's stored credentials.
package credstore

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	errorNotFound           = 1168 // ERROR_NOT_FOUND

	targetPrefix = "goop:auth:"
)

var (
	advapi32           = syscall.NewLazyDLL("advapi32.dll")
	procCredWriteW     = advapi32.NewProc("CredWriteW")
	procCredReadW      = advapi32.NewProc("CredReadW")
	procCredDeleteW    = advapi32.NewProc("CredDeleteW")
	procCredEnumerateW = advapi32.NewProc("CredEnumerateW")
	procCredFree       = advapi32.NewProc("CredFree")
)

// filetime mirrors the Win32 FILETIME struct (two DWORDs).
type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

// credentialW mirrors the Win32 CREDENTIALW struct exactly (field
// order, types) -- verified against
// https://learn.microsoft.com/en-us/windows/win32/api/wincred/ns-wincred-credentialw.
// Go's natural struct alignment on amd64 produces the same layout the
// Win32 API expects, the same technique golang.org/x/sys/windows itself
// relies on for other Win32 structs.
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// Entry is one stored host credential's non-secret metadata (EXF-34:
// list must never expose the secret, so Entry structurally can't carry
// one).
type Entry struct {
	Host string
	Type string // "bearer" or "basic"
}

func targetName(host string) string {
	return targetPrefix + strings.ToLower(host)
}

// Set stores (or replaces) the credential for host. username is
// ignored for "bearer" (pass "").
func Set(host, authType, username, secret string) error {
	if secret == "" {
		return fmt.Errorf("credential secret must not be empty")
	}
	switch authType {
	case "bearer", "basic":
	default:
		return fmt.Errorf("auth type must be \"bearer\" or \"basic\", got %q", authType)
	}

	targetPtr, err := syscall.UTF16PtrFromString(targetName(host))
	if err != nil {
		return err
	}
	commentPtr, err := syscall.UTF16PtrFromString(authType)
	if err != nil {
		return err
	}
	userPtr, err := syscall.UTF16PtrFromString(username)
	if err != nil {
		return err
	}
	blob := []byte(secret)

	cred := credentialW{
		Type:               credTypeGeneric,
		TargetName:         targetPtr,
		Comment:            commentPtr,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
		UserName:           userPtr,
	}

	ret, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	// blob's backing array is only referenced via the raw pointer inside
	// cred.CredentialBlob, which the GC can't see -- keep it alive until
	// the syscall (which reads through that pointer) has returned.
	runtime.KeepAlive(blob)
	if ret == 0 {
		return fmt.Errorf("store credential for %s: %w", host, callErr)
	}
	return nil
}

// Get retrieves the credential for host. ok is false (with a nil error)
// if none is stored.
func Get(host string) (authType, username, secret string, ok bool, err error) {
	targetPtr, err := syscall.UTF16PtrFromString(targetName(host))
	if err != nil {
		return "", "", "", false, err
	}

	var pCred *credentialW
	ret, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&pCred)),
	)
	if ret == 0 {
		if errno, isErrno := callErr.(syscall.Errno); isErrno && errno == errorNotFound {
			return "", "", "", false, nil
		}
		return "", "", "", false, fmt.Errorf("read credential for %s: %w", host, callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(pCred)))

	authType = utf16PtrToString(pCred.Comment)
	username = utf16PtrToString(pCred.UserName)
	if pCred.CredentialBlobSize > 0 {
		secretBytes := unsafe.Slice(pCred.CredentialBlob, pCred.CredentialBlobSize)
		secret = string(secretBytes)
	}
	return authType, username, secret, true, nil
}

// Delete removes the credential for host. Deleting one that doesn't
// exist is not an error (idempotent, like `rm -f`).
func Delete(host string) error {
	targetPtr, err := syscall.UTF16PtrFromString(targetName(host))
	if err != nil {
		return err
	}
	ret, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(targetPtr)), uintptr(credTypeGeneric), 0)
	if ret == 0 {
		if errno, isErrno := callErr.(syscall.Errno); isErrno && errno == errorNotFound {
			return nil
		}
		return fmt.Errorf("delete credential for %s: %w", host, callErr)
	}
	return nil
}

// List returns every host goop has a stored credential for, with its
// auth type but never its secret (EXF-34).
func List() ([]Entry, error) {
	filterPtr, err := syscall.UTF16PtrFromString(targetPrefix + "*")
	if err != nil {
		return nil, err
	}

	// CredEnumerateW's out-param is PCREDENTIALW** -- the address of a
	// variable that receives a PCREDENTIALW* (**credentialW here),
	// itself pointing at the first element of an array of
	// *credentialW. arrayPtr is that properly-typed **credentialW
	// throughout, never a bare uintptr reinterpreted later.
	var count uint32
	var arrayPtr **credentialW
	ret, _, callErr := procCredEnumerateW.Call(
		uintptr(unsafe.Pointer(filterPtr)),
		0,
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&arrayPtr)),
	)
	if ret == 0 {
		if errno, isErrno := callErr.(syscall.Errno); isErrno && errno == errorNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("enumerate credentials: %w", callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(arrayPtr)))

	ptrs := unsafe.Slice(arrayPtr, count)
	entries := make([]Entry, 0, count)
	for _, p := range ptrs {
		target := utf16PtrToString(p.TargetName)
		host := strings.TrimPrefix(target, targetPrefix)
		entries = append(entries, Entry{Host: host, Type: utf16PtrToString(p.Comment)})
	}
	return entries, nil
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for ptr := unsafe.Pointer(p); *(*uint16)(ptr) != 0; n++ {
		ptr = unsafe.Add(ptr, 2)
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n))
}
