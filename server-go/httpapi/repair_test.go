package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mjbraun/chiron/server/state"
)

// u1Check answers every item of u1's bank, right for the ids in right and
// wrong otherwise. Every u1 item grades mechanically, so no model is
// involved; the ids come back in bank order.
func u1Check(t *testing.T, s *Server, right map[string]bool) (body string, ids []string) {
	t.Helper()
	sub, _ := s.subject("ai")
	var answers []string
	for _, q := range sub.Corpus.Units["u1"].Questions.Check {
		ids = append(ids, q.ID)
		switch {
		case !right[q.ID] && q.Kind == "mcq":
			answers = append(answers, `{"item_id":"`+q.ID+`","selected_index":99,"confidence":4}`)
		case !right[q.ID]:
			answers = append(answers, `{"item_id":"`+q.ID+`","response":"definitely wrong","confidence":4}`)
		case q.Kind == "mcq":
			idx := -1
			for i, o := range q.Options {
				if o.Correct {
					idx = i
				}
			}
			answers = append(answers, `{"item_id":"`+q.ID+`","selected_index":`+itoa(idx)+`,"confidence":4}`)
		default:
			if q.Check != "exact" {
				t.Fatalf("%s is graded by %s; this helper answers exact items only", q.ID, q.Check)
			}
			resp, _ := json.Marshal(q.Answer.String())
			answers = append(answers, `{"item_id":"`+q.ID+`","response":`+string(resp)+`,"confidence":4}`)
		}
	}
	if len(answers) < 5 {
		t.Fatalf("u1 has %d check items; the repair round needs a real bank", len(answers))
	}
	return "[" + strings.Join(answers, ",") + "]", ids
}

func itoa(n int) string { return strconv.Itoa(n) }

type exchangeOut struct {
	Gate    *Gate `json:"gate"`
	Chapter *struct {
		Unit    string           `json:"unit"`
		Title   string           `json:"title"`
		HTML    string           `json:"html"`
		Pretest []map[string]any `json:"pretest"`
		Check   []struct {
			ID string `json:"id"`
		} `json:"check"`
	} `json:"chapter"`
	Authoring  string `json:"authoring"`
	ResultsDoc *struct {
		Unit     string           `json:"unit"`
		HeadLeft string           `json:"head_left"`
		Dek      string           `json:"dek"`
		Passed   bool             `json:"passed"`
		Entries  []map[string]any `json:"entries"`
	} `json:"results_doc"`
}

func exchange(t *testing.T, s *Server, body string) exchangeOut {
	t.Helper()
	w := do(t, s, "POST", "/exchange", body, "")
	if w.Code != http.StatusOK {
		t.Fatalf("exchange -> %d: %s", w.Code, w.Body.String())
	}
	var out exchangeOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// failU1 grades u1's check with two items right, well below the gate, and
// returns the ids missed in bank order.
func failU1(t *testing.T, s *Server) (exchangeOut, []string) {
	t.Helper()
	right := map[string]bool{"u1-q1": true, "u1-q2": true}
	body, ids := u1Check(t, s, right)
	out := exchange(t, s, `{"subject":"ai","phase":"boundary","unit":"u1","async":true,"check_responses":`+body+`}`)
	if out.Gate == nil || out.Gate.Passed {
		t.Fatalf("two right of %d passed the gate: %+v", len(ids), out.Gate)
	}
	var missed []string
	for _, id := range ids {
		if !right[id] {
			missed = append(missed, id)
		}
	}
	return out, missed
}

// A failed gate used to start rewriting the whole chapter at once, and the
// reader who chose to override or to repair the misses had paid for it.
// Now nothing is written until the reader chooses; the misses are kept
// for the choice.
func TestAFailedGateWaitsForTheReadersChoice(t *testing.T) {
	s := newServer(t, "")
	sub, _ := s.subject("ai")
	out, missed := failU1(t, s)
	if out.Authoring != "" {
		t.Errorf("a failed gate started authoring %q before the reader chose", out.Authoring)
	}
	if out.Chapter != nil {
		t.Errorf("a failed gate delivered chapter %s", out.Chapter.Unit)
	}
	if got := unitStarts(t, sub); got != 0 {
		t.Errorf("%d chapters built on a failed gate", got)
	}
	_, kept, items := sub.Learner.LastCheck("u1")
	if strings.Join(kept, ",") != strings.Join(missed, ",") {
		t.Errorf("misses kept %v, want %v", kept, missed)
	}
	if items != len(missed)+2 {
		t.Errorf("items kept %d, want %d", items, len(missed)+2)
	}
	if !strings.Contains(out.ResultsDoc.Dek, "misses") {
		t.Errorf("the results do not offer the misses: %q", out.ResultsDoc.Dek)
	}
}

// The repair is a short chapter on the misses alone, then those items
// again. The round is scored as the whole check with what it closed, so
// two right of twelve and then nine of the ten misses is eleven of twelve:
// the unit passes, no debt, no starting over.
func TestRepairTakesTheMissesAloneThenScoresTheWholeCheck(t *testing.T) {
	s := newServer(t, "")
	sub, _ := s.subject("ai")
	// u0 is behind us, so the book moves on to u2 when u1 passes.
	sub.Learner.Apply(state.Event{Kind: "check_result", Unit: "u0", Passed: ptrBool(true)})
	_, missed := failU1(t, s)

	out := exchange(t, s, `{"subject":"ai","phase":"boundary","unit":"u1","repair":true}`)
	if out.Chapter == nil {
		t.Fatal("the repair delivered no chapter")
	}
	if out.Chapter.Unit != "u1.repair" {
		t.Errorf("repair chapter unit %q, want u1.repair", out.Chapter.Unit)
	}
	var got []string
	for _, it := range out.Chapter.Check {
		got = append(got, it.ID)
	}
	if strings.Join(got, ",") != strings.Join(missed, ",") {
		t.Errorf("repair asks %v, want the misses %v", got, missed)
	}
	if len(out.Chapter.Pretest) != 0 {
		t.Error("a repair chapter has no pretest")
	}
	if !strings.Contains(out.Chapter.HTML, "planner-note") || !strings.Contains(out.Chapter.HTML, "<h2") {
		t.Error("the repair chapter lacks its note or its sections")
	}
	if cur := sub.Learner.Snapshot().CurrentUnit; cur == nil || *cur != "u1.repair" {
		t.Errorf("current unit after the repair build: %v", cur)
	}
	w := do(t, s, "GET", "/chapter/ai", "", "")
	if !strings.Contains(w.Body.String(), `"unit":"u1.repair"`) {
		t.Errorf("/chapter does not serve the repair chapter: %.120s", w.Body.String())
	}

	// Nine of the ten misses closed.
	right := map[string]bool{}
	for _, id := range missed[1:] {
		right[id] = true
	}
	body, _ := u1Check(t, s, right)
	var only []json.RawMessage
	var all []json.RawMessage
	json.Unmarshal([]byte(body), &all)
	for _, a := range all {
		for _, id := range missed {
			if strings.Contains(string(a), `"`+id+`"`) {
				only = append(only, a)
			}
		}
	}
	repaired, _ := json.Marshal(only)
	out = exchange(t, s, `{"subject":"ai","phase":"boundary","unit":"u1.repair","async":true,"check_responses":`+string(repaired)+`}`)
	if out.Gate == nil || !out.Gate.Passed {
		t.Fatalf("the repair round did not pass the gate: %+v", out.Gate)
	}
	total := float64(len(missed) + 2)
	if want := (total - 1) / total; *out.Gate.Score < want-0.001 || *out.Gate.Score > want+0.001 {
		t.Errorf("score %.3f, want %.3f (the whole check with the misses closed)", *out.Gate.Score, want)
	}
	if st := sub.Learner.UnitStatus("u1"); st != "passed" {
		t.Errorf("u1 is %s after the repair round, want passed", st)
	}
	if len(sub.Learner.OpenDebt()) != 0 {
		t.Error("a repair round accrued debt")
	}
	_, still, _ := sub.Learner.LastCheck("u1")
	if len(still) != 1 || still[0] != missed[0] {
		t.Errorf("still missed %v, want %v", still, missed[:1])
	}
	for _, c := range sub.Corpus.Units["u1"].ConceptIDs() {
		if sub.Learner.ConceptLevel(c) != "mastered" {
			t.Errorf("%s is %s after passing, want mastered", c, sub.Learner.ConceptLevel(c))
		}
	}
	if out.Authoring != "u2" {
		t.Errorf("after the repair passed the book authors %q, want u2", out.Authoring)
	}
	doc := out.ResultsDoc
	if doc == nil {
		t.Fatal("no results doc for the repair round")
	}
	if !strings.HasPrefix(doc.HeadLeft, "1 · ") {
		t.Errorf("results head %q does not name the unit repaired", doc.HeadLeft)
	}
	if len(doc.Entries) != len(missed) {
		t.Errorf("results carry %d entries, want the %d repaired", len(doc.Entries), len(missed))
	}
	if !doc.Passed || !strings.Contains(doc.Dek, "closed") {
		t.Errorf("results dek after a passed repair: %q", doc.Dek)
	}
}

// A repair round that still falls short is graded like any failed check:
// nothing is written, the still-missed items are kept, and the next
// repair takes only those.
func TestARepairThatStillMissesOffersTheChoicesAgain(t *testing.T) {
	s := newServer(t, "")
	sub, _ := s.subject("ai")
	_, missed := failU1(t, s)
	exchange(t, s, `{"subject":"ai","phase":"boundary","unit":"u1","repair":true}`)

	right := map[string]bool{missed[0]: true, missed[1]: true}
	body, _ := u1Check(t, s, right)
	var all []json.RawMessage
	json.Unmarshal([]byte(body), &all)
	var only []json.RawMessage
	for _, a := range all {
		for _, id := range missed {
			if strings.Contains(string(a), `"`+id+`"`) {
				only = append(only, a)
			}
		}
	}
	repaired, _ := json.Marshal(only)
	out := exchange(t, s, `{"subject":"ai","phase":"boundary","unit":"u1.repair","async":true,"check_responses":`+string(repaired)+`}`)
	if out.Gate == nil || out.Gate.Passed {
		t.Fatalf("four right of twelve passed: %+v", out.Gate)
	}
	if out.Authoring != "" {
		t.Errorf("a failed repair round started authoring %q", out.Authoring)
	}
	if st := sub.Learner.UnitStatus("u1"); st != "failed" {
		t.Errorf("u1 is %s, want failed", st)
	}
	_, still, _ := sub.Learner.LastCheck("u1")
	if len(still) != len(missed)-2 {
		t.Errorf("still missed %d, want %d", len(still), len(missed)-2)
	}
	out = exchange(t, s, `{"subject":"ai","phase":"boundary","unit":"u1","repair":true}`)
	if out.Chapter == nil || len(out.Chapter.Check) != len(missed)-2 {
		t.Fatalf("the second repair does not take the remaining misses: %+v", out.Chapter)
	}
}

// "Explain it differently" is the whole chapter again from a different
// angle, written when asked for; and a repair asked for with no misses on
// record (a learner from before misses were kept) is that rewrite too.
func TestExplainItDifferentlyRewritesTheWholeChapterWhenAsked(t *testing.T) {
	s := newServer(t, "")
	failU1(t, s)
	out := exchange(t, s, `{"subject":"ai","phase":"boundary","unit":"u1","remediate":true,"async":true}`)
	if out.Authoring != "u1" {
		t.Errorf("remediate authors %q, want u1", out.Authoring)
	}
	if out.Chapter != nil {
		t.Error("an async remediate delivered a chapter at once")
	}
	s.renders.Wait()

	s2 := newServer(t, "")
	out = exchange(t, s2, `{"subject":"ai","phase":"boundary","unit":"u1","repair":true}`)
	if out.Chapter == nil || out.Chapter.Unit != "u1" {
		t.Fatalf("a repair with nothing missed: %+v, want the whole u1", out.Chapter)
	}
	if len(out.Chapter.Check) < 5 {
		t.Errorf("the whole chapter carries %d check items", len(out.Chapter.Check))
	}
}

func ptrBool(b bool) *bool { return &b }

// The app sends the override as its own exchange, with no answers: the
// check was graded in the one before. The server recorded an override only
// while grading a check, so an override alone left the failed unit on the
// fringe, and the chapter built next was that unit again, from its pretest,
// though the reader had said to go on.
func TestOverrideAloneMovesOnAndRecordsTheDebt(t *testing.T) {
	s := newServer(t, "")
	sub, _ := s.subject("ai")
	// u0 is behind us, so going on from u1 means u2.
	sub.Learner.Apply(state.Event{Kind: "check_result", Unit: "u0", Passed: ptrBool(true)})
	_, missed := failU1(t, s)

	out := exchange(t, s, `{"subject":"ai","phase":"boundary","unit":"u1","override":true,"async":true}`)
	s.renders.Wait()
	if out.Chapter != nil {
		t.Errorf("an async override delivered chapter %s at once", out.Chapter.Unit)
	}
	if out.Authoring != "u2" {
		t.Errorf("override authors %q, want u2, the unit after the one overridden", out.Authoring)
	}
	if got := sub.Learner.UnitStatus("u1"); got != "overridden" {
		t.Errorf("u1 after the override is %q, want overridden", got)
	}
	debt := sub.Learner.OpenDebt()
	if len(debt) != 1 || debt[0].Unit != "u1" || debt[0].Reason != "failed_gate" {
		t.Fatalf("debt after the override: %+v, want one entry for u1", debt)
	}
	if strings.Join(debt[0].ItemsMissed, ",") != strings.Join(missed, ",") {
		t.Errorf("debt carries misses %v, want the check's %v", debt[0].ItemsMissed, missed)
	}
	if len(debt[0].Concepts) == 0 {
		t.Error("debt names no concepts")
	}
	w := do(t, s, "GET", "/chapter/ai", "", "")
	if !strings.Contains(w.Body.String(), `"unit":"u2"`) {
		t.Errorf("/chapter after the override does not serve u2: %.120s", w.Body.String())
	}
}
