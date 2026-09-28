package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Alerts are what a machine behind the book has to tell the reader: the
// build Mac gone onto battery, and so about to sleep with its lid shut.
// The machine posts one; the app asks for what is new when it looks and
// shows each as a notice.

type Alert struct {
	Source string    `json:"source"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

// How many are kept; the oldest go first.
const alertsKept = 50

func (s *Server) alertsPath() string {
	return filepath.Join(filepath.Dir(s.requests.Dir()), "alerts.json")
}

func (s *Server) readAlerts() []Alert {
	var all []Alert
	if data, err := os.ReadFile(s.alertsPath()); err == nil {
		json.Unmarshal(data, &all)
	}
	return all
}

// POST /alerts {source, text}
func (s *Server) handleAlertPost(w http.ResponseWriter, r *http.Request) {
	var in Alert
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: %v", err)
		return
	}
	in.Text, in.Source = strings.TrimSpace(in.Text), strings.TrimSpace(in.Source)
	if in.Text == "" {
		writeError(w, http.StatusUnprocessableEntity, "an alert says something")
		return
	}
	in.At = time.Now().UTC()

	s.alertMu.Lock()
	defer s.alertMu.Unlock()
	all := append(s.readAlerts(), in)
	if len(all) > alertsKept {
		all = all[len(all)-alertsKept:]
	}
	data, _ := json.MarshalIndent(all, "", "  ")
	tmp := s.alertsPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "alerts: %v", err)
		return
	}
	if err := os.Rename(tmp, s.alertsPath()); err != nil {
		writeError(w, http.StatusInternalServerError, "alerts: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

// GET /alerts[?since=RFC3339]: oldest first, only those after since.
func (s *Server) handleAlertList(w http.ResponseWriter, r *http.Request) {
	s.alertMu.Lock()
	all := s.readAlerts()
	s.alertMu.Unlock()
	out := []Alert{}
	since, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("since"))
	for _, a := range all {
		if a.At.After(since) {
			out = append(out, a)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
