package shim

import (
	"errors"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		target string
		want   Kind
		err    error
	}{
		{`C:\apps\git\git.exe`, KindExe, nil},
		{`C:\apps\tool\run.BAT`, KindBatch, nil},
		{`C:\apps\tool\run.cmd`, KindBatch, nil},
		{`C:\apps\tool\install.ps1`, KindPowerShell, nil},
		{`C:\apps\tool\app.jar`, KindJar, nil},
		{`C:\apps\tool\readme.txt`, 0, ErrUnsupportedTarget},
		{`C:\apps\tool\noext`, 0, ErrUnsupportedTarget},
	}
	for _, tt := range tests {
		got, err := Classify(tt.target)
		if tt.err != nil {
			if !errors.Is(err, tt.err) {
				t.Errorf("Classify(%q) err = %v, want %v", tt.target, err, tt.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Classify(%q) unexpected err: %v", tt.target, err)
		}
		if got != tt.want {
			t.Errorf("Classify(%q) = %v, want %v", tt.target, got, tt.want)
		}
	}
}

func TestQuoteArg(t *testing.T) {
	tests := map[string]string{
		"simple":          "simple",
		"":                `""`,
		"has space":       `"has space"`,
		`C:\a\b`:          `C:\a\b`,
		`C:\has space\x`:  `"C:\has space\x"`,
		`a"b`:              `"a\"b"`,
		`ends\`:            `ends\`,
		`"quoted" arg`:    `"\"quoted\" arg"`,
	}
	for in, want := range tests {
		if got := QuoteArg(in); got != want {
			t.Errorf("QuoteArg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildPlan_Exe(t *testing.T) {
	plan, err := BuildPlan(KindExe, `C:\apps\git\git.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Program != `C:\apps\git\git.exe` {
		t.Errorf("Program = %q", plan.Program)
	}
	if plan.Prefix != `C:\apps\git\git.exe` {
		t.Errorf("Prefix = %q", plan.Prefix)
	}
}

func TestBuildPlan_Batch(t *testing.T) {
	old := lookPath
	lookPath = func(name string) (string, error) { return `C:\Windows\System32\cmd.exe`, nil }
	defer func() { lookPath = old }()

	plan, err := BuildPlan(KindBatch, `C:\apps\tool\run.cmd`)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Program != `C:\Windows\System32\cmd.exe` {
		t.Errorf("Program = %q", plan.Program)
	}
	if !strings.Contains(plan.Prefix, "/d /c") || !strings.Contains(plan.Prefix, `run.cmd`) {
		t.Errorf("Prefix = %q", plan.Prefix)
	}
}

func TestBuildPlan_Jar_NoJava(t *testing.T) {
	old := lookPath
	lookPath = func(name string) (string, error) { return "", errors.New("not found") }
	defer func() { lookPath = old }()

	_, err := BuildPlan(KindJar, `C:\apps\tool\app.jar`)
	if err == nil {
		t.Fatal("expected error when java.exe is missing")
	}
}
