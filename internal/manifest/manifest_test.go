package manifest

import "testing"

// Fixtures below are trimmed from real manifests in ScoopInstaller/Main,
// to pin decoding against actual upstream shapes rather than invented ones.

const jqManifest = `{
    "version": "1.8.2",
    "bin": "jq.exe",
    "architecture": {
        "64bit": {
            "url": "https://github.com/jqlang/jq/releases/download/jq-1.8.2/jq-windows-amd64.exe#/jq.exe",
            "hash": "a6fc67fedaf9128a3309a1e2ebb8b986aeccf70122ee46d2cb4849e423f0c627"
        },
        "32bit": {
            "url": "https://github.com/jqlang/jq/releases/download/jq-1.8.2/jq-windows-i386.exe#/jq.exe",
            "hash": "a99cb668f95bdd788d9ee20529613b115e5d2a0d7f9127ee6976607e878558ba"
        }
    },
    "checkver": {"github": "https://github.com/jqlang/jq", "regex": "jq-([\\d.]+)"}
}`

const fdManifest = `{
    "version": "10.4.2",
    "architecture": {
        "64bit": {
            "url": "https://github.com/sharkdp/fd/releases/download/v10.4.2/fd-v10.4.2-x86_64-pc-windows-msvc.zip",
            "hash": "b2816e506390a89941c63c9187d58a3cc10e9a55f2ef0685f9ea0eccaf7c98c8",
            "extract_dir": "fd-v10.4.2-x86_64-pc-windows-msvc"
        }
    },
    "bin": "fd.exe"
}`

const gsudoManifest = `{
    "version": "2.6.1",
    "url": "https://github.com/gerardog/gsudo/releases/download/v2.6.1/gsudo.portable.zip",
    "hash": "21130bf178d7b9891207f00bff56f05b6b363ce9cec7b2d084e45fb12ee51f44",
    "architecture": {
        "64bit": {"extract_dir": "x64"},
        "32bit": {"extract_dir": "x86"}
    },
    "bin": [["gsudo.exe", "sudo"]],
    "post_install": "try { & \"$dir\\gsudo.exe\" -k 2>&1 | Out-Null } catch { info $_.Exception.Message }"
}`

const lessManifest = `{
    "version": "704",
    "architecture": {
        "64bit": {
            "url": "https://github.com/jftuga/less-Windows/releases/download/less-v704/less-x64.zip",
            "hash": "9f5cfcc2452e5e06916f75077cfdbf331a85a3cbd249f93b2b4449216984d7bc"
        }
    },
    "bin": ["less.exe", "lesskey.exe"]
}`

const autosshManifest = `{
    "version": "1.4g",
    "architecture": {
        "64bit": {
            "url": [
                "https://github.com/jazzl0ver/autossh/releases/download/1.4g/autossh.exe",
                "https://github.com/jazzl0ver/autossh/releases/download/1.4g/msys-2.0.dll"
            ],
            "hash": [
                "b05e9599fa6e9ff9d2a57e141bdca0eb7f5829e8fd67e37e878863a4fd46469f",
                "462fcdec4f2e390d806e2cc21874db208645d69bf9d70a1d0f4981164569db79"
            ]
        }
    },
    "bin": "autossh.exe"
}`

func TestDecode_JQ_PerArchURL(t *testing.T) {
	m, err := Decode([]byte(jqManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.8.2" {
		t.Errorf("version = %q", m.Version)
	}
	if len(m.Bin) != 1 || m.Bin[0].Exe != "jq.exe" || m.Bin[0].Name != "jq" {
		t.Errorf("bin = %+v", m.Bin)
	}
	r, err := m.Resolve("jq", "64bit")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.URLs) != 1 || r.URLs[0] != "https://github.com/jqlang/jq/releases/download/jq-1.8.2/jq-windows-amd64.exe#/jq.exe" {
		t.Errorf("URLs = %v", r.URLs)
	}
	rawURL, fname := SplitURLFragment(r.URLs[0])
	if fname != "jq.exe" {
		t.Errorf("fragment filename = %q", fname)
	}
	if rawURL != "https://github.com/jqlang/jq/releases/download/jq-1.8.2/jq-windows-amd64.exe" {
		t.Errorf("raw url = %q", rawURL)
	}
}

func TestDecode_FD_ExtractDir(t *testing.T) {
	m, err := Decode([]byte(fdManifest))
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Resolve("fd", "64bit")
	if err != nil {
		t.Fatal(err)
	}
	if r.ExtractDirFor(0) != "fd-v10.4.2-x86_64-pc-windows-msvc" {
		t.Errorf("extract_dir = %q", r.ExtractDirFor(0))
	}
}

func TestDecode_Gsudo_BaseURLWithArchExtractDir(t *testing.T) {
	m, err := Decode([]byte(gsudoManifest))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Bin) != 1 || m.Bin[0].Exe != "gsudo.exe" || m.Bin[0].Name != "sudo" {
		t.Fatalf("bin = %+v", m.Bin)
	}
	if m.PostInstall == "" {
		t.Error("expected non-empty post_install")
	}
	r, err := m.Resolve("gsudo", "64bit")
	if err != nil {
		t.Fatal(err)
	}
	// base url/hash preserved since the arch override only sets extract_dir
	if len(r.URLs) != 1 || r.URLs[0] != "https://github.com/gerardog/gsudo/releases/download/v2.6.1/gsudo.portable.zip" {
		t.Errorf("URLs = %v", r.URLs)
	}
	if r.ExtractDirFor(0) != "x64" {
		t.Errorf("extract_dir = %q, want x64", r.ExtractDirFor(0))
	}

	r32, err := m.Resolve("gsudo", "32bit")
	if err != nil {
		t.Fatal(err)
	}
	if r32.ExtractDirFor(0) != "x86" {
		t.Errorf("32bit extract_dir = %q, want x86", r32.ExtractDirFor(0))
	}
}

func TestDecode_Less_MultiBinArray(t *testing.T) {
	m, err := Decode([]byte(lessManifest))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Bin) != 2 || m.Bin[0].Exe != "less.exe" || m.Bin[1].Exe != "lesskey.exe" {
		t.Fatalf("bin = %+v", m.Bin)
	}
}

func TestDecode_Autossh_MultiURLMultiHash(t *testing.T) {
	m, err := Decode([]byte(autosshManifest))
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Resolve("autossh", "64bit")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.URLs) != 2 || len(r.Hashes) != 2 {
		t.Fatalf("URLs=%v Hashes=%v", r.URLs, r.Hashes)
	}
}

func TestResolve_MissingArch(t *testing.T) {
	m, err := Decode([]byte(fdManifest))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Resolve("fd", "arm64"); err == nil {
		t.Fatal("expected error for missing architecture")
	}
}

func TestParseHash(t *testing.T) {
	tests := []struct {
		in      string
		algo    string
		digest  string
		wantErr bool
	}{
		{in: "abcd1234", algo: "sha256", digest: "abcd1234"},
		{in: "sha1:ABCD1234", algo: "sha1", digest: "abcd1234"},
		{in: "md5:0011", algo: "md5", digest: "0011"},
		{in: "sha256:zz", wantErr: true},
		{in: "crc32:aabb", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseHash(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseHash(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseHash(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if got.Algo != tt.algo || got.Digest != tt.digest {
			t.Errorf("ParseHash(%q) = %+v, want {%s %s}", tt.in, got, tt.algo, tt.digest)
		}
	}
}
