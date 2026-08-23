package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

func TestEnvVarName(t *testing.T) {
	tests := map[string]string{
		"github.com":          "GOOP_AUTH_GITHUB_COM",
		"my-artifacts.corp":   "GOOP_AUTH_MY_ARTIFACTS_CORP",
		"Example.COM":         "GOOP_AUTH_EXAMPLE_COM",
		"host.with.dots:8080": "GOOP_AUTH_HOST_WITH_DOTS_8080",
	}
	for host, want := range tests {
		if got := EnvVarName(host); got != want {
			t.Errorf("EnvVarName(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestHeaderFromSpec(t *testing.T) {
	tests := []struct {
		spec    string
		want    string
		wantErr bool
	}{
		{spec: "bearer:abc123", want: "Bearer abc123"},
		{spec: "bearer:token:with:colons", want: "Bearer token:with:colons"},
		{spec: "basic:alice:hunter2", want: "Basic YWxpY2U6aHVudGVyMg=="},
		{spec: "bearer:", wantErr: true},
		{spec: "basic:alice", wantErr: true},
		{spec: "digest:x", wantErr: true},
		{spec: "garbage", wantErr: true},
	}
	for _, tt := range tests {
		got, ok, err := headerFromSpec(tt.spec)
		if tt.wantErr {
			if err == nil {
				t.Errorf("headerFromSpec(%q) expected error", tt.spec)
			}
			continue
		}
		if err != nil {
			t.Errorf("headerFromSpec(%q) unexpected error: %v", tt.spec, err)
			continue
		}
		if !ok || got != tt.want {
			t.Errorf("headerFromSpec(%q) = (%q, %v), want (%q, true)", tt.spec, got, ok, tt.want)
		}
	}
}

func TestResolve_EnvVarTakesPrecedence(t *testing.T) {
	const host = "goop-test-auth-precedence.example"
	envVar := EnvVarName(host)
	os.Setenv(envVar, "bearer:from-env")
	defer os.Unsetenv(envVar)

	got, ok, err := resolve(host)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != "Bearer from-env" {
		t.Fatalf("resolve(%q) = (%q, %v), want (Bearer from-env, true)", host, got, ok)
	}
}

func TestResolve_AnonymousWhenNothingConfigured(t *testing.T) {
	_, ok, err := resolve("goop-test-auth-nothing-configured.example")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected anonymous (ok=false) for a host with no env var or stored credential")
	}
}

func TestTransport_InjectsHeaderOnlyForConfiguredHost(t *testing.T) {
	var gotAuthHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Transport keys strictly by hostname (req.URL.Hostname(), which
	// excludes the port) -- auth doesn't vary by port, matching EXF-30's
	// "keyed by host". Set the env var under that same hostname, not
	// host:port.
	reqURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	envVar := EnvVarName(reqURL.Hostname())
	os.Setenv(envVar, "bearer:injected-token")
	defer os.Unsetenv(envVar)

	tr := &Transport{Base: http.DefaultTransport}
	client := &http.Client{Transport: tr}

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if gotAuthHeader != "Bearer injected-token" {
		t.Fatalf("server saw Authorization = %q, want %q", gotAuthHeader, "Bearer injected-token")
	}
}

func TestTransport_NoHeaderForUnconfiguredHost(t *testing.T) {
	var sawAuthHeader bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthHeader = r.Header.Get("Authorization") != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tr := &Transport{Base: http.DefaultTransport}
	client := &http.Client{Transport: tr}

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if sawAuthHeader {
		t.Fatal("server should not have received an Authorization header for an unconfigured host")
	}
}
