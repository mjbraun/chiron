package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// walkToSeries takes a fresh subject through the placement screener and
// returns the calibration series' item ids.
func walkToSeries(t *testing.T, s *Server, subject string) (unit string, items []string) {
	t.Helper()
	do(t, s, "POST", "/exchange", `{"subject":"`+subject+`","phase":"start"}`, "")
	w := do(t, s, "POST", "/exchange",
		`{"subject":"`+subject+`","phase":"boundary","check_responses":[{"item_id":"u0-s1","selected_index":2,"confidence":3}]}`, "")
	var resp struct {
		Chapter struct {
			Unit  string `json:"unit"`
			Check []struct {
				ID string `json:"id"`
			} `json:"check"`
		} `json:"chapter"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Chapter.Check) < 2 {
		t.Fatalf("after the screener, got %d items, want the series", len(resp.Chapter.Check))
	}
	for _, it := range resp.Chapter.Check {
		items = append(items, it.ID)
	}
	return resp.Chapter.Unit, items
}

func idkAnswers(items []string) string {
	var answers []string
	for _, id := range items {
		answers = append(answers, `{"item_id":"`+id+`","idk":true,"confidence":1}`)
	}
	return strings.Join(answers, ",")
}

// The iPad's exchange is a plain HTTP request that dies with the app when
// iOS suspends it; authoring the next chapter can take minutes. With
// async set, the grades come back at once and the chapter is fetched from
// /chapter/{subject} when the server says it is no longer authoring.
func TestAsyncExchangeGradesNowAndAuthorsLater(t *testing.T) {
	s := newServer(t, "")
	unit, items := walkToSeries(t, s, "ai")

	w := do(t, s, "POST", "/exchange",
		`{"subject":"ai","phase":"boundary","unit":"`+unit+`","async":true,"check_responses":[`+idkAnswers(items)+`]}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("async exchange -> %d: %s", w.Code, w.Body.String())
	}
	var graded struct {
		Gate      *Gate           `json:"gate"`
		Chapter   json.RawMessage `json:"chapter"`
		Authoring string          `json:"authoring"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &graded); err != nil {
		t.Fatal(err)
	}
	if graded.Gate == nil {
		t.Fatal("async exchange returned no gate")
	}
	if string(graded.Chapter) != "null" {
		t.Fatalf("async exchange carried a chapter: %.80s", graded.Chapter)
	}
	if graded.Authoring != "u1" {
		t.Fatalf("authoring = %q, want u1", graded.Authoring)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		w := do(t, s, "GET", "/chapter/ai", "", "")
		if w.Code != http.StatusOK {
			t.Fatalf("/chapter/ai -> %d: %s", w.Code, w.Body.String())
		}
		var status struct {
			Authoring      bool   `json:"authoring"`
			AuthoringError string `json:"authoring_error"`
			Chapter        *struct {
				Unit string `json:"unit"`
			} `json:"chapter"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if !status.Authoring {
			if status.AuthoringError != "" {
				t.Fatalf("authoring failed: %s", status.AuthoringError)
			}
			if status.Chapter == nil || status.Chapter.Unit != "u1" {
				t.Fatalf("after authoring, chapter = %+v, want u1", status.Chapter)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("still authoring after 20s")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestChapterEndpointBeforeAnyChapter(t *testing.T) {
	s := newServer(t, "")
	w := do(t, s, "GET", "/chapter/ai", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("/chapter/ai -> %d", w.Code)
	}
	var status struct {
		Authoring bool            `json:"authoring"`
		Chapter   json.RawMessage `json:"chapter"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Authoring || string(status.Chapter) != "null" {
		t.Fatalf("fresh subject: %s", w.Body.String())
	}
	if w := do(t, s, "GET", "/chapter/nope", "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("/chapter/nope -> %d, want 404", w.Code)
	}
}

// The typeset results pages carry the whole audit trail (READ AS, ANSWER,
// WHY); the iPad renders the same document natively, so it travels in the
// exchange response as well.
func TestGradedExchangeCarriesTheResultsDoc(t *testing.T) {
	s := newServer(t, "")
	unit, items := walkToSeries(t, s, "ai")

	w := do(t, s, "POST", "/exchange",
		`{"subject":"ai","phase":"boundary","unit":"`+unit+`","check_responses":[`+idkAnswers(items)+`]}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("exchange -> %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ResultsDoc *struct {
			Headline string `json:"headline"`
			Tally    string `json:"tally"`
			Action   string `json:"action"`
			Dek      string `json:"dek"`
			Entries  []struct {
				N      int    `json:"n"`
				IDK    bool   `json:"idk"`
				Prompt string `json:"prompt"`
			} `json:"entries"`
		} `json:"results_doc"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	doc := resp.ResultsDoc
	if doc == nil {
		t.Fatal("graded exchange carried no results_doc")
	}
	if len(doc.Entries) != len(items) {
		t.Fatalf("results_doc has %d entries, want %d", len(doc.Entries), len(items))
	}
	if !strings.HasPrefix(doc.Headline, "Calibration complete") {
		t.Errorf("headline = %q", doc.Headline)
	}
	if !strings.Contains(doc.Tally, "PASSED") {
		t.Errorf("calibration tally = %q, want the correct/missed/passed counts", doc.Tally)
	}
	if doc.Action != "Begin chapter 1" || doc.Dek == "" {
		t.Errorf("action = %q, dek = %q", doc.Action, doc.Dek)
	}
	for i, e := range doc.Entries {
		if e.N != i+1 || !e.IDK || e.Prompt == "" {
			t.Errorf("entry %d = %+v", i, e)
		}
	}

	// The screener alone is not a graded check: no doc.
	s2 := newServer(t, "")
	do(t, s2, "POST", "/exchange", `{"subject":"ai","phase":"start"}`, "")
	w = do(t, s2, "POST", "/exchange",
		`{"subject":"ai","phase":"boundary","check_responses":[{"item_id":"u0-s1","selected_index":0,"confidence":3}]}`, "")
	if strings.Contains(w.Body.String(), `"results_doc"`) {
		t.Error("the screener answer produced a results_doc")
	}
}

// A stored chapter is a snapshot of the unit with its items baked in.
// When the unit's question bank changes underneath it (a rewrite of the
// corpus), the snapshot is stale: the chapter endpoint reports none and
// the next start builds the chapter from the bank as it is now.
func TestAStoredChapterIsDroppedWhenItsBankChanges(t *testing.T) {
	s := newServer(t, "")
	w := do(t, s, "POST", "/exchange", `{"subject":"ai","phase":"start","unit":"u0"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	chapter := func() json.RawMessage {
		w := do(t, s, "GET", "/chapter/ai", "", "")
		var status struct {
			Chapter json.RawMessage `json:"chapter"`
		}
		json.Unmarshal(w.Body.Bytes(), &status)
		return status.Chapter
	}
	if string(chapter()) == "null" {
		t.Fatal("no stored chapter after start")
	}
	sub, _ := s.subject("ai")
	unit := sub.Corpus.Units["u0"]
	if len(unit.Questions.Check) == 0 {
		t.Fatal("u0 has no check items to change")
	}
	unit.Questions.Check[0].Prompt = "A rewritten prompt."
	if got := chapter(); string(got) != "null" {
		t.Fatalf("a chapter built from the old bank is still served: %.120s", got)
	}
	w = do(t, s, "POST", "/exchange", `{"subject":"ai","phase":"start","unit":"u0"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("restart: %d %s", w.Code, w.Body)
	}
	if got := chapter(); string(got) == "null" {
		t.Fatal("the chapter was not rebuilt from the changed bank")
	}
}

// While a chapter is being written, /chapter says which stage the build is
// at and for how long it has run, so the reader sees movement rather than
// a static line for minutes.
func TestChapterStatusReportsBuildStage(t *testing.T) {
	s := newServer(t, "")
	sub, ok := s.subject("ai")
	if !ok {
		t.Fatal("no ai subject")
	}
	if !sub.beginBuild() {
		t.Fatal("beginBuild refused a first build")
	}
	sub.setStage("writing")
	var status struct {
		Authoring bool   `json:"authoring"`
		Stage     string `json:"authoring_stage"`
		Seconds   int    `json:"authoring_seconds"`
	}
	w := do(t, s, "GET", "/chapter/ai", "", "")
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Authoring || status.Stage != "writing" || status.Seconds < 0 {
		t.Fatalf("mid-build status = %s", w.Body.String())
	}
	sub.endBuild("")
	w = do(t, s, "GET", "/chapter/ai", "", "")
	if strings.Contains(w.Body.String(), "authoring_stage") {
		t.Fatalf("stage reported after the build: %s", w.Body.String())
	}
}

// Whether the chapter's pretest has been taken is the server's to know:
// a second device opening the book must land in the prose where the
// first left off, not on the check-in the first already answered
// (2026-09-29). /chapter/{subject} says so beside the chapter.
func TestChapterEndpointSaysWhenThePretestWasTaken(t *testing.T) {
	s := newServer(t, "")
	unit, items := walkToSeries(t, s, "ai")
	w := do(t, s, "POST", "/exchange",
		`{"subject":"ai","phase":"boundary","unit":"`+unit+`","check_responses":[`+idkAnswers(items)+`]}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("series exchange -> %d: %s", w.Code, w.Body.String())
	}
	status := func() (unit string, done bool) {
		w := do(t, s, "GET", "/chapter/ai", "", "")
		if w.Code != http.StatusOK {
			t.Fatalf("/chapter/ai -> %d: %s", w.Code, w.Body.String())
		}
		var st struct {
			Chapter *struct {
				Unit string `json:"unit"`
			} `json:"chapter"`
			PretestDone bool `json:"pretest_done"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		if st.Chapter == nil {
			t.Fatalf("no chapter: %s", w.Body.String())
		}
		return st.Chapter.Unit, st.PretestDone
	}
	if u, done := status(); u != "u1" || done {
		t.Fatalf("after the series: chapter %s, pretest_done %v; want u1, false", u, done)
	}

	w = do(t, s, "POST", "/exchange",
		`{"subject":"ai","phase":"pretest","unit":"u1","pretest_responses":[{"item_id":"u1-p1","idk":true,"confidence":1}]}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("pretest exchange -> %d: %s", w.Code, w.Body.String())
	}
	if u, done := status(); u != "u1" || !done {
		t.Fatalf("after the pretest: chapter %s, pretest_done %v; want u1, true", u, done)
	}
}
