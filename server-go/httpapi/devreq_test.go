package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mjbraun/chiron/server/devreq"
)

// "Request a change" in the app: the text, the app's state and a
// screenshot go in; the request comes back queued, and the list shows
// every request newest first with what the agent has said so far.
func TestAChangeRequestIsQueuedAndListed(t *testing.T) {
	s := newServer(t, "sekrit")
	s.requests = devreq.Open(t.TempDir())

	png := base64.StdEncoding.EncodeToString([]byte("PNG"))
	w := do(t, s, "POST", "/dev/requests", `{"text":"the pen is too thin","state":{"screen":"reading","unit":"u3"},"screenshot_png_b64":"`+png+`"}`, "sekrit")
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var r devreq.Request
	json.Unmarshal(w.Body.Bytes(), &r)
	if r.Status != devreq.Queued || r.Text != "the pen is too thin" || !strings.HasPrefix(r.ID, "req-") || r.Screenshot == "" {
		t.Errorf("created = %s", w.Body)
	}
	if w := do(t, s, "POST", "/dev/requests", `{"text":"  "}`, "sekrit"); w.Code != 422 {
		t.Errorf("empty request: %d %s", w.Code, w.Body)
	}
	do(t, s, "POST", "/dev/requests", `{"text":"and bluer"}`, "sekrit")

	w = do(t, s, "GET", "/dev/requests", "", "sekrit")
	var list []devreq.Request
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list) != 2 || list[0].Text != "and bluer" {
		t.Errorf("list: %d %s", w.Code, w.Body)
	}
	if len(list[1].State) == 0 {
		t.Error("the state travels with the request")
	}

	w = do(t, s, "GET", "/dev/requests/"+r.ID, "", "sekrit")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"the pen is too thin"`) {
		t.Errorf("get: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, "GET", "/dev/requests/req-nonesuch", "", "sekrit"); w.Code != 404 {
		t.Errorf("missing: %d", w.Code)
	}
	if w := do(t, s, "GET", "/dev/requests", "", ""); w.Code != 401 {
		t.Errorf("without the key: %d", w.Code)
	}
}

// The app answers a request the agent stopped to ask about.
func TestAnAnswerGoesToAWaitingRequest(t *testing.T) {
	s := newServer(t, "sekrit")
	s.requests = devreq.Open(t.TempDir())
	r, _ := s.requests.Create("make the shelf nicer", nil, nil)

	if w := do(t, s, "POST", "/dev/requests/"+r.ID+"/answer", `{"answer":"denser"}`, "sekrit"); w.Code != 409 {
		t.Errorf("not waiting: %d %s", w.Code, w.Body)
	}
	r.Status, r.Question = devreq.Waiting, "Denser, or covers?"
	s.requests.Save(r)
	if w := do(t, s, "POST", "/dev/requests/"+r.ID+"/answer", `{"answer":" "}`, "sekrit"); w.Code != 422 {
		t.Errorf("empty answer: %d %s", w.Code, w.Body)
	}
	w := do(t, s, "POST", "/dev/requests/"+r.ID+"/answer", `{"answer":"Denser."}`, "sekrit")
	var got devreq.Request
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Status != devreq.Queued || len(got.Thread) != 1 || got.Thread[0].Answer != "Denser." {
		t.Errorf("answer: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, "POST", "/dev/requests/req-nonesuch/answer", `{"answer":"x"}`, "sekrit"); w.Code != 404 {
		t.Errorf("missing: %d", w.Code)
	}
}
