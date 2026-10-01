package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// apiTokenFile holds the secret every /api/ caller must send in X-Bridge-Token.
// It sits in store/, next to the session keys: anything that can read it can
// already act as this account, so it adds no new place to protect.
const apiTokenFile = "store/api_token"

// loadOrCreateAPIToken returns the API token, writing a random one (0600) on first run.
func loadOrCreateAPIToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= 32 {
			return t, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		return "", err
	}
	return token, nil
}

// requireLocalClient guards an /api/ handler. Binding to loopback keeps the
// network out but not a web page in the owner's browser, so it refuses what a
// page can send: an Origin header, a Host other than this listener (DNS
// rebinding), a body that is not JSON. Then it requires the token.
func requireLocalClient(token string, port int, next http.HandlerFunc) http.HandlerFunc {
	hosts := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", port): true,
		fmt.Sprintf("localhost:%d", port): true,
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" || !hosts[r.Host] {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			http.Error(w, "Unsupported Media Type", http.StatusUnsupportedMediaType)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Bridge-Token")), []byte(token)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// safeMediaName reduces a name to one harmless path element. A document's file
// name is chosen by the sender, so it can be "../../Library/LaunchAgents/x.plist";
// joined onto the chat directory unchanged, it writes wherever this user can.
func safeMediaName(name string) string {
	name = strings.ReplaceAll(name, "\x00", "")
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." || name == ".." || name == "/" {
		return "document_" + time.Now().Format("20060102_150405")
	}
	if strings.HasPrefix(name, ".") {
		return "_" + name
	}
	return name
}

// withinDir reports whether path lies inside dir once both are cleaned.
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// sendRoots lists the directories /api/send may attach files from. Without a
// limit, one confused or manipulated caller can mail out ~/.ssh, the session
// store or synced work documents. The default is deliberately narrow:
// ~/Downloads and the temp directories (voice notes are converted there).
// WHATSAPP_SEND_ALLOWED_DIRS (colon-separated) replaces the defaults.
func sendRoots() []string {
	var dirs []string
	if v := os.Getenv("WHATSAPP_SEND_ALLOWED_DIRS"); v != "" {
		dirs = strings.Split(v, ":")
	} else {
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, "Downloads"))
		}
		dirs = append(dirs, os.TempDir(), "/tmp")
	}
	var roots []string
	for _, d := range dirs {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		if real, err := filepath.EvalSymlinks(d); err == nil {
			roots = append(roots, real)
		}
	}
	return roots
}

// resolveSendPath returns the real path of a file /api/send may attach, after
// resolving symlinks, or an error when it falls outside every allowed root.
func resolveSendPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	for _, root := range sendRoots() {
		if withinDir(root, real) {
			return real, nil
		}
	}
	return "", fmt.Errorf("media path is outside the directories the bridge may send from (set WHATSAPP_SEND_ALLOWED_DIRS to change them)")
}
