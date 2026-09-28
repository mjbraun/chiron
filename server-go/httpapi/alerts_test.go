package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mjbraun/chiron/server/devreq"
)

// A machine with something to say (Truman gone onto battery) posts it
// here; the app asks for what is new since it last looked and shows each
// once.
func TestAnAlertIsKeptAndListedSinceWhenAsked(t *testing.T) {
	s := newServer(t, "sekrit")
	s.requests = devreq.Open(t.TempDir())

	if w := do(t, s, "POST", "/alerts", `{"source":"truman","text":" "}`, "sekrit"); w.Code != 422 {
		t.Errorf("an empty alert: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, "POST", "/alerts", `{"source":"truman","text":"on battery"}`, ""); w.Code != 401 {
		t.Errorf("without the key: %d", w.Code)
	}
	w := do(t, s, "POST", "/alerts", `{"source":"truman","text":"Truman is on battery"}`, "sekrit")
	var first Alert
	json.Unmarshal(w.Body.Bytes(), &first)
	if w.Code != 200 || first.Text != "Truman is on battery" || first.Source != "truman" || first.At.IsZero() {
		t.Fatalf("post: %d %s", w.Code, w.Body)
	}
	time.Sleep(2 * time.Millisecond)
	do(t, s, "POST", "/alerts", `{"source":"truman","text":"again"}`, "sekrit")

	var all []Alert
	w = do(t, s, "GET", "/alerts", "", "sekrit")
	json.Unmarshal(w.Body.Bytes(), &all)
	if w.Code != 200 || len(all) != 2 || all[0].Text != "Truman is on battery" {
		t.Fatalf("list, oldest first: %d %s", w.Code, w.Body)
	}
	var since []Alert
	w = do(t, s, "GET", "/alerts?since="+first.At.Format(time.RFC3339Nano), "", "sekrit")
	json.Unmarshal(w.Body.Bytes(), &since)
	if len(since) != 1 || since[0].Text != "again" {
		t.Errorf("since the first: %s", w.Body)
	}
}

// Only the newest are kept: a machine that flaps must not grow the file
// without end.
func TestAlertsKeepTheNewestFifty(t *testing.T) {
	s := newServer(t, "sekrit")
	s.requests = devreq.Open(t.TempDir())
	for i := 0; i < 55; i++ {
		do(t, s, "POST", "/alerts", fmt.Sprintf(`{"source":"truman","text":"alert %d"}`, i), "sekrit")
	}
	var all []Alert
	json.Unmarshal(do(t, s, "GET", "/alerts", "", "sekrit").Body.Bytes(), &all)
	if len(all) != 50 || all[0].Text != "alert 5" || all[49].Text != "alert 54" {
		t.Errorf("kept %d, first %q", len(all), all[0].Text)
	}
}
