package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthState(t *testing.T) {
	cases := []struct {
		name                                   string
		paired, loggedOut, connected, loggedIn bool
		want                                   string
	}{
		{"healthy", true, false, true, true, "connected"},
		{"never paired", false, false, false, false, "needs_pairing"},
		// whatsmeow deletes the device on logout, so paired reads false then too;
		// logged_out says more than needs_pairing until the process restarts.
		{"logged out this run", false, true, false, false, "logged_out"},
		{"socket down, reconnecting", true, false, false, false, "disconnected"},
		{"socket up, not yet authenticated", true, false, true, false, "connecting"},
	}
	for _, c := range cases {
		if got := healthState(c.paired, c.loggedOut, c.connected, c.loggedIn); got != c.want {
			t.Errorf("%s: healthState = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHealthHandler(t *testing.T) {
	report := healthReport{State: "connected", Paired: true, Connected: true, LoggedIn: true}
	h := healthHandler(func() healthReport { return report })

	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))
		return w
	}

	w := get()
	if w.Code != http.StatusOK {
		t.Fatalf("connected: status %d, want 200", w.Code)
	}
	var got healthReport
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got != report {
		t.Fatalf("connected: body %q, err %v", w.Body.String(), err)
	}

	report = healthReport{State: "needs_pairing"}
	if w := get(); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("needs_pairing: status %d, want 503", w.Code)
	}

	w = httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodPost, "/api/health", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: status %d, want 405", w.Code)
	}
}

func TestServeAPIRefusesTakenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := serveAPI(ln.Addr().String()); err == nil {
		t.Fatal("serveAPI on a taken port returned nil, want an error")
	}
}
