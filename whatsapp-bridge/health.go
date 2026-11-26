package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// A running process is not a working bridge. When WhatsApp unlinks the device,
// the old bridge sat in a three-minute QR wait nobody could see, exited, and was
// restarted by its supervisor, all week, while a process check called it healthy.
// /api/health answers the question that matters: can this bridge send right now?

// noQREnv, when set, means no person is watching stdout (a LaunchAgent or a
// systemd unit). An unpaired bridge then waits and reports needs_pairing on
// /api/health instead of printing a QR code into a log and exiting on timeout.
const noQREnv = "WHATSAPP_BRIDGE_NO_QR"

// loggedOutThisRun is set by the events.LoggedOut handler. whatsmeow deletes the
// device on logout, so without it a logout would read as never having paired.
var loggedOutThisRun atomic.Bool

type healthReport struct {
	State     string `json:"state"`
	Paired    bool   `json:"paired"`
	Connected bool   `json:"connected"`
	LoggedIn  bool   `json:"logged_in"`
}

// healthState names the bridge's condition; only "connected" can send.
func healthState(paired, loggedOut, connected, loggedIn bool) string {
	switch {
	case loggedOut:
		return "logged_out"
	case !paired:
		return "needs_pairing"
	case !connected:
		return "disconnected"
	case !loggedIn:
		return "connecting"
	default:
		return "connected"
	}
}

// healthHandler serves GET /api/health: 200 when connected, 503 otherwise, with
// the report as JSON either way so a probe can say why.
func healthHandler(probe func() healthReport) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rep := probe()
		w.Header().Set("Content-Type", "application/json")
		if rep.State != "connected" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(rep)
	}
}

// logState writes one line to stderr, which a supervisor keeps even when stdout
// (every message, in full) goes to /dev/null.
func logState(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s bridge: %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
