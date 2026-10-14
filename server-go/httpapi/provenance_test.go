package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mjbraun/chiron/server/llm"
)

type provenanceChapter struct {
	Unit       string     `json:"unit"`
	HTML       string     `json:"html"`
	Provenance []llm.Call `json:"provenance"`
}

func chapterOf(t *testing.T, body []byte) provenanceChapter {
	t.Helper()
	var resp struct {
		Chapter provenanceChapter `json:"chapter"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Chapter
}

// A chapter the tutor wrote says at its end which model wrote it, and
// carries every call that did (the planner's and the author's, prompts
// and times included), both in the exchange that delivers it and in the
// stored copy that serves it again. The first chapter of a book is a
// cold start that asks the model nothing, so it is the second that is
// written.
func TestAChapterCarriesItsProvenance(t *testing.T) {
	s := newServer(t, "")
	s.chain = &cannedChain{payload: map[string]any{
		"depth": "default", "refutation_targets": []string{},
		"sections_to_rewrite": []map[string]any{{"heading": "Why prediction forces world modeling", "instruction": "shorter"}},
		"opening_note_md":     "Start here.", "next_action": "read", "summary": "fresh",
		"sections": []map[string]any{{"heading": "Why prediction forces world modeling", "markdown": "Rewritten, shorter."}},
	}}
	first := chapterOf(t, do(t, s, "POST", "/exchange", `{"subject":"ai","phase":"start"}`, "").Body.Bytes())
	if first.Provenance != nil || strings.Contains(first.HTML, "colophon") {
		t.Fatalf("the cold-start chapter was not written by a model: %+v", first.Provenance)
	}
	unit, items := walkToSeries(t, s, "ai")
	w := do(t, s, "POST", "/exchange",
		`{"subject":"ai","phase":"boundary","unit":"`+unit+`","check_responses":[`+idkAnswers(items)+`]}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("boundary -> %d: %s", w.Code, w.Body.String())
	}
	ch := chapterOf(t, w.Body.Bytes())
	if ch.Unit != "u1" || len(ch.Provenance) != 2 || ch.Provenance[0].Role != "planner" || ch.Provenance[1].Role != "author" {
		t.Fatalf("unit %s provenance = %+v", ch.Unit, ch.Provenance)
	}
	for _, c := range ch.Provenance {
		if c.Model != "canned-model" || c.Prompt == "" || c.System == "" || c.At.IsZero() {
			t.Fatalf("call = %+v", c)
		}
	}
	if ch.Provenance[0].Name != "directives" || ch.Provenance[1].Name != "rewrite" {
		t.Fatalf("names = %s, %s", ch.Provenance[0].Name, ch.Provenance[1].Name)
	}
	if !strings.HasSuffix(ch.HTML, `<footer class="colophon">Written by canned-model</footer>`) {
		t.Fatalf("no colophon:\n%s", ch.HTML[len(ch.HTML)-200:])
	}
	again := chapterOf(t, do(t, s, "GET", "/chapter/ai", "", "").Body.Bytes())
	if again.Unit != "u1" || len(again.Provenance) != 2 || again.Provenance[1].Prompt != ch.Provenance[1].Prompt {
		t.Fatalf("the stored chapter lost its provenance: %s %+v", again.Unit, again.Provenance)
	}
}

// A primer names the model that wrote it; a margin note's section adds
// its own call to the record.
func TestAPrimerCarriesItsProvenance(t *testing.T) {
	s := newServer(t, "")
	s.chain = &cannedChain{payload: primerPayload()}
	rep := capture(t, s, `{"text":"captured","prompt":"what is this?"}`)
	s.renders.Wait()
	w := do(t, s, "POST", "/exchange", `{"subject":"`+rep.Subject+`","phase":"start"}`, "")
	ch := chapterOf(t, w.Body.Bytes())
	if len(ch.Provenance) != 1 || ch.Provenance[0].Name != "primer" || ch.Provenance[0].Model != "canned-model" || !strings.Contains(ch.Provenance[0].Prompt, "what is this?") {
		t.Fatalf("primer provenance = %+v", ch.Provenance)
	}
	if !strings.Contains(ch.HTML, `<footer class="colophon">Written by canned-model</footer>`) {
		t.Fatal("the primer has no colophon")
	}
	w = do(t, s, "POST", "/primer/"+rep.Subject+"/extend", `{"quote":"captured","note":"why?"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("extend -> %d: %s", w.Code, w.Body.String())
	}
	ext := chapterOf(t, w.Body.Bytes())
	if len(ext.Provenance) != 2 || ext.Provenance[1].Name != "primer_section" || !strings.Contains(ext.Provenance[1].Prompt, "why?") {
		t.Fatalf("after extend: %+v", ext.Provenance)
	}
	// The record survives a restart: it is in the primer's own file.
	raw, err := os.ReadFile(filepath.Join(s.primersRoot(), rep.Subject, "primer.json"))
	if err != nil || !strings.Contains(string(raw), `"provenance"`) {
		t.Fatalf("primer.json: %v %s", err, raw)
	}
}

// An answer to a question says who wrote it and when; the prompt, too
// big to ride with a mark on every device, is kept on the server and
// fetched by its reference.
func TestAnAnswerKeepsItsRecordForTheReader(t *testing.T) {
	s := newTwoSubjectServer(t, t.TempDir())
	s.chain = &cannedChain{payload: map[string]any{"answer_md": "Because."}}
	do(t, s, "POST", "/exchange", `{"subject":"ai","phase":"start"}`, "")
	w := do(t, s, "POST", "/ask/ai", `{"unit":"u1","quote":"the loss","question":"why nats?"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("/ask/ai -> %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Generated llm.Call `json:"generated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	g := resp.Generated
	if g.Model != "canned-model" || g.Role != "planner" || g.At.IsZero() || g.Prompt != "" || g.System != "" || g.Ref == "" {
		t.Fatalf("generated = %+v", g)
	}
	w = do(t, s, "GET", "/generated/ai/"+g.Ref, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("fetch -> %d: %s", w.Code, w.Body.String())
	}
	var full llm.Call
	json.Unmarshal(w.Body.Bytes(), &full)
	if full.Model != "canned-model" || !strings.Contains(full.Prompt, "why nats?") || full.System == "" || full.At != g.At {
		t.Fatalf("full = %+v", full)
	}
	if w := do(t, s, "GET", "/generated/ai/ask-99", "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown ref -> %d", w.Code)
	}
	if w := do(t, s, "GET", "/generated/ai/..%2F..%2Fx", "", ""); w.Code == http.StatusOK {
		t.Fatalf("a path for a ref -> %d", w.Code)
	}
}

// A capture answered in the card carries its whole record: the card is
// the only place it is read, and nothing keeps it after.
func TestACaptureAnswerCarriesItsRecord(t *testing.T) {
	s := newServer(t, "")
	s.chain = &cannedChain{payload: map[string]any{"markdown": "In short."}}
	w := do(t, s, "POST", "/primer/capture", `{"text":"words","prompt":"what?","scale":"summary"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("capture -> %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Generated llm.Call `json:"generated"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Generated.Model != "canned-model" || !strings.Contains(resp.Generated.Prompt, "what?") {
		t.Fatalf("generated = %+v", resp.Generated)
	}
}
