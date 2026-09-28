package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The reader thinks a question or its reference answer is wrong. The flag
// is recorded against the item with what they answered and the reference
// as it stands, and the results of that check show it beside the item.
func TestFlagRecordsTheConcernAgainstTheItem(t *testing.T) {
	s := newServer(t, "")
	unit, items := walkToSeries(t, s, "ai")

	concern := "the key would have been rotated at 16:00 and so useless at 17:15"
	body := `{"unit":"` + unit + `","item":"` + items[0] + `","text":"  ` + concern + `  ","answer":"0"}`
	w := do(t, s, "POST", "/flag/ai", body, "")
	if w.Code != http.StatusOK {
		t.Fatalf("/flag/ai -> %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Unit string `json:"unit"`
		Item string `json:"item"`
		N    int    `json:"n"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Unit != unit || resp.Item != items[0] || resp.N == 0 {
		t.Fatalf("flag = %+v", resp)
	}
	sub, _ := s.subject("ai")
	flags := sub.Learner.Flags(unit)
	if len(flags) != 1 || flags[0].Item != items[0] || flags[0].Concern != concern {
		t.Fatalf("recorded flags = %+v", flags)
	}
	// The answer the reader gave and the reference, as the results page
	// would print them.
	if flags[0].Answer != "0" || flags[0].Reference == "" {
		t.Fatalf("answer = %q, reference = %q", flags[0].Answer, flags[0].Reference)
	}
	if got := activeSubjectOf(t, s); got != "ai" {
		t.Fatalf("active = %q, want ai", got)
	}

	// GET lists it, for a person at a shell.
	w = do(t, s, "GET", "/flags/ai", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), concern) {
		t.Fatalf("/flags/ai -> %d: %s", w.Code, w.Body.String())
	}

	// The results of the check carry the flag on its item and no other.
	w = do(t, s, "POST", "/exchange",
		`{"subject":"ai","phase":"boundary","unit":"`+unit+`","check_responses":[`+idkAnswers(items)+`]}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("exchange -> %d: %s", w.Code, w.Body.String())
	}
	var graded struct {
		ResultsDoc struct {
			Entries []struct {
				Prompt string `json:"prompt"`
				Flag   string `json:"flag"`
			} `json:"entries"`
		} `json:"results_doc"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &graded); err != nil {
		t.Fatal(err)
	}
	var flagged []string
	for _, e := range graded.ResultsDoc.Entries {
		if e.Flag != "" {
			flagged = append(flagged, e.Flag)
		}
	}
	if len(flagged) != 1 || flagged[0] != concern {
		t.Fatalf("flagged entries = %q, want the one concern", flagged)
	}
	// The typeset page is a picture; the flag's row on it is tested where
	// the page is laid out (pages/results_test.go). Here: it renders.
	page := do(t, s, "GET", "/pages/ai/results/0", "", "")
	if page.Code == http.StatusOK && page.Header().Get("Content-Type") != "image/png" {
		t.Errorf("the typeset results page is %q", page.Header().Get("Content-Type"))
	}
}

// A flag without a concern is nothing to record, and one on an item the
// book does not have is a mistake, not a record.
func TestFlagRejectsWhatItCannotRecord(t *testing.T) {
	s := newServer(t, "")
	do(t, s, "POST", "/exchange", `{"subject":"ai","phase":"start"}`, "")
	if w := do(t, s, "POST", "/flag/ai", `{"unit":"u1","item":"u1-q1","text":"   "}`, ""); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty concern -> %d, want 422", w.Code)
	}
	if w := do(t, s, "POST", "/flag/ai", `{"unit":"u1","item":"u1-q99","text":"wrong"}`, ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown item -> %d, want 404", w.Code)
	}
	if w := do(t, s, "POST", "/flag/nope", `{"unit":"u1","item":"u1-q1","text":"wrong"}`, ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown subject -> %d, want 404", w.Code)
	}
	sub, _ := s.subject("ai")
	if len(sub.Learner.Flags("")) != 0 {
		t.Errorf("recorded %+v", sub.Learner.Flags(""))
	}
	// The unit is optional, the item names it; and a choice is kept as
	// its letter and text, the reference as the right option.
	w := do(t, s, "POST", "/flag/ai", `{"item":"u1-q1","text":"two options are both right","selected_index":1}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("flag without unit -> %d: %s", w.Code, w.Body.String())
	}
	flags := sub.Learner.Flags("u1")
	if len(flags) != 1 || !strings.HasPrefix(flags[0].Answer, "B — '") || !strings.Contains(flags[0].Reference, " — ") {
		t.Fatalf("flags = %+v", flags)
	}
	// Passing on the item is an answer too.
	do(t, s, "POST", "/flag/ai", `{"item":"u1-q3","text":"the prompt is ambiguous","idk":true}`, "")
	if flags = sub.Learner.Flags("u1"); len(flags) != 2 || flags[1].Answer != "I don't know." {
		t.Fatalf("flags = %+v", flags)
	}
}
