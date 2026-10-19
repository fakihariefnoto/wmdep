package sshx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"systemctl", "restart", "api.service"}, "'systemctl' 'restart' 'api.service'"},
		{[]string{"sh", "-c", "echo hi"}, "'sh' '-c' 'echo hi'"},
		{[]string{"echo", "it's"}, `'echo' 'it'\''s'`},
		{[]string{"x", "$(rm -rf /)"}, `'x' '$(rm -rf /)'`},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%v) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestParsePort(t *testing.T) {
	if n, err := parsePort("2222"); err != nil || n != 2222 {
		t.Errorf("parsePort = %d, %v", n, err)
	}
	for _, bad := range []string{"0", "70000", "abc", ""} {
		if _, err := parsePort(bad); err == nil {
			t.Errorf("parsePort(%q) should fail", bad)
		}
	}
}

func TestResolveAddr(t *testing.T) {
	h := app.Host{ID: "web-1", Address: "10.0.0.11", Port: 2222}
	if got := resolveAddr(h, ""); got != "10.0.0.11:2222" {
		t.Errorf("addr = %s", got)
	}
	h.Port = 0
	if got := resolveAddr(h, ""); got != "10.0.0.11:22" {
		t.Errorf("default port = %s", got)
	}
}

func TestClientConfigNeedsKnownHosts(t *testing.T) {
	p := NewPool(Options{
		Timeout:    time.Second,
		KnownHosts: filepath.Join(t.TempDir(), "missing_known_hosts"),
	})
	_, err := p.clientConfig(app.Host{ID: "h", Auth: app.AuthAgent})
	if err == nil {
		t.Fatal("missing known_hosts should be an error")
	}
}

func TestClientConfigRejectsBadKey(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(kh, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p := NewPool(Options{Timeout: time.Second, KnownHosts: kh})
	_, err := p.clientConfig(app.Host{ID: "h", Auth: app.AuthKey, Key: filepath.Join(dir, "nope")})
	if err == nil {
		t.Fatal("missing key should be an error")
	}
}

func TestClassifyUnknownHostKey(t *testing.T) {
	err := classify(&knownhosts.KeyError{}, app.Host{ID: "h", Address: "1.2.3.4"}, "/tmp/kh")
	if !errors.Is(err, ErrUnknownHostKey) {
		t.Fatalf("err = %v, want ErrUnknownHostKey", err)
	}
}
