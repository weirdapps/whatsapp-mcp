package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeMediaNameKeepsOneElement(t *testing.T) {
	cases := map[string]string{
		"report.pdf":                         "report.pdf",
		"../../Library/LaunchAgents/x.plist": "x.plist",
		"/etc/passwd":                        "passwd",
		`..\..\evil.bat`:                     "evil.bat",
		".zshrc":                             "_.zshrc",
		"a\x00b.txt":                         "ab.txt",
		"123456789@s.whatsapp.net":           "123456789@s.whatsapp.net",
	}
	for in, want := range cases {
		if got := safeMediaName(in); got != want {
			t.Errorf("safeMediaName(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"", ".", "..", "/", "../"} {
		if got := safeMediaName(in); !strings.HasPrefix(got, "document_") {
			t.Errorf("safeMediaName(%q) = %q, want a document_ fallback", in, got)
		}
	}
}

func TestWithinDir(t *testing.T) {
	if !withinDir("store", filepath.Join("store", "chat", "file")) {
		t.Error("a file under store/ must count as inside")
	}
	for _, p := range []string{filepath.Join("store", "..", "x"), "other/file", "/abs/file"} {
		if withinDir("store", p) {
			t.Errorf("withinDir(store, %q) = true, want false", p)
		}
	}
}

func TestLoadOrCreateAPITokenPersistsWithPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store", "api_token")
	first, err := loadOrCreateAPIToken(path)
	if err != nil || len(first) < 32 {
		t.Fatalf("first call: token %q, err %v", first, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("token file mode = %o, want 600", mode)
	}
	second, err := loadOrCreateAPIToken(path)
	if err != nil || second != first {
		t.Errorf("second call returned %q (err %v), want the stored token", second, err)
	}
}

func TestRequireLocalClientRejectsWhatABrowserCanSend(t *testing.T) {
	token := strings.Repeat("t", 32) // a stand-in, not a credential
	h := requireLocalClient(token, 8080, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := func(mutate func(*http.Request)) int {
		r := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/send", strings.NewReader(`{}`))
		r.Host = "localhost:8080"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Bridge-Token", token)
		mutate(r)
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	checks := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{"valid local client", func(r *http.Request) {}, http.StatusOK},
		{"loopback IP host", func(r *http.Request) { r.Host = "127.0.0.1:8080" }, http.StatusOK},
		{"missing token", func(r *http.Request) { r.Header.Del("X-Bridge-Token") }, http.StatusUnauthorized},
		{"wrong token", func(r *http.Request) { r.Header.Set("X-Bridge-Token", "nope") }, http.StatusUnauthorized},
		{"browser origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
		{"rebound host", func(r *http.Request) { r.Host = "evil.example:8080" }, http.StatusForbidden},
		{"form post", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, http.StatusUnsupportedMediaType},
	}
	for _, c := range checks {
		if got := req(c.mutate); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
}

func TestResolveSendPathHonoursTheAllowlist(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	t.Setenv("WHATSAPP_SEND_ALLOWED_DIRS", allowed)

	inside := filepath.Join(allowed, "photo.jpg")
	secret := filepath.Join(outside, "id_ed25519")
	for _, p := range []string{inside, secret} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := resolveSendPath(inside); err != nil {
		t.Errorf("file inside the allowed dir was refused: %v", err)
	}
	if _, err := resolveSendPath(secret); err == nil {
		t.Error("file outside the allowed dirs was accepted")
	}
	link := filepath.Join(allowed, "innocent.jpg")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSendPath(link); err == nil {
		t.Error("a symlink inside the allowed dir that points outside was accepted")
	}
	if _, err := resolveSendPath(filepath.Join(allowed, "..", filepath.Base(outside), "id_ed25519")); err == nil {
		t.Error("a ../ path out of the allowed dir was accepted")
	}
}
