package credstore

import (
	"syscall"
	"testing"
	"unsafe"
)

// writeLikeTheCredentialManagerUI stores a credential the way Windows'
// own Credential Manager does when somebody edits one by hand:
//
//   - the secret as UTF-16LE, not UTF-8. The UI hands CredWrite the
//     wide string it has in its edit box.
//   - no Comment. The UI does not show that field for a generic
//     credential and does not carry it across an edit, so goop's record
//     of whether this is a bearer token or a basic password is simply
//     gone.
//
// Both halves of the reported failure: "it has a bug on the type" and
// "after I change the password goop can no longer read it".
func writeLikeTheCredentialManagerUI(t *testing.T, host, username, secret string) {
	t.Helper()
	targetPtr, err := syscall.UTF16PtrFromString(targetName(host))
	if err != nil {
		t.Fatal(err)
	}
	userPtr, err := syscall.UTF16PtrFromString(username)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := syscall.UTF16FromString(secret)
	if err != nil {
		t.Fatal(err)
	}
	// Without the terminating NUL: that is what the UI stores.
	wide = wide[:len(wide)-1]
	blob := make([]byte, 0, len(wide)*2)
	for _, u := range wide {
		blob = append(blob, byte(u), byte(u>>8))
	}

	cred := credentialW{
		Type:               credTypeGeneric,
		TargetName:         targetPtr,
		Comment:            nil,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
		UserName:           userPtr,
	}
	ret, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if ret == 0 {
		t.Fatalf("simulating a Credential Manager edit: %v", callErr)
	}
}

// Editing a credential's password in the Windows Credential Manager must
// not make goop unable to read it. goop stored the secret as UTF-8 and
// read it back as UTF-8, which is self-consistent and wrong the moment
// anything else writes it: the UI writes UTF-16LE, so every character
// came back followed by a NUL.
func TestGet_SurvivesAnExternalPasswordEdit(t *testing.T) {
	skipUnlessOptedIn(t)
	const host = "goop-test-extedit-4b81de.example"
	t.Cleanup(func() { Delete(host) })

	if err := Set(host, "basic", "alice", "original-password"); err != nil {
		t.Fatal(err)
	}
	writeLikeTheCredentialManagerUI(t, host, "alice", "changed-password")

	authType, username, secret, ok, err := Get(host)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the credential still exists; Get must find it")
	}
	if secret != "changed-password" {
		t.Errorf("secret = %q, want %q -- a UTF-16 blob read as UTF-8", secret, "changed-password")
	}
	if username != "alice" {
		t.Errorf("username = %q, want alice", username)
	}
	// The type is gone from the credential, so it has to be inferred.
	// A basic credential is the one with a username.
	if authType != "basic" {
		t.Errorf("authType = %q, want basic -- inferred from the username the UI did keep", authType)
	}
}

// Same edit on a bearer credential: no username, so nothing to infer
// "basic" from.
func TestGet_InfersBearerAfterAnExternalEdit(t *testing.T) {
	skipUnlessOptedIn(t)
	const host = "goop-test-extedit-bearer-4b81de.example"
	t.Cleanup(func() { Delete(host) })

	if err := Set(host, "bearer", "", "original-token"); err != nil {
		t.Fatal(err)
	}
	writeLikeTheCredentialManagerUI(t, host, "", "changed-token")

	authType, _, secret, ok, err := Get(host)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the credential still exists; Get must find it")
	}
	if secret != "changed-token" {
		t.Errorf("secret = %q, want %q", secret, "changed-token")
	}
	if authType != "bearer" {
		t.Errorf("authType = %q, want bearer", authType)
	}
}

// A secret goop wrote itself must keep round-tripping unchanged,
// including non-ASCII -- the UTF-16 detection must not misfire on a
// UTF-8 blob.
func TestGet_StillReadsItsOwnUTF8Secrets(t *testing.T) {
	skipUnlessOptedIn(t)
	const host = "goop-test-utf8-4b81de.example"
	t.Cleanup(func() { Delete(host) })

	for _, secret := range []string{
		"plain-ascii-token",
		"pa55w0rd!#$%&",
		"mot-de-passe-éàü",
		"密码-token",
		"a", // single byte: the shortest thing that could be misread
	} {
		if err := Set(host, "bearer", "", secret); err != nil {
			t.Fatal(err)
		}
		_, _, got, ok, err := Get(host)
		if err != nil {
			t.Fatal(err)
		}
		if !ok || got != secret {
			t.Errorf("round trip of %q gave %q (ok=%v)", secret, got, ok)
		}
	}
}
