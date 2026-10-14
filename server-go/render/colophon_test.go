package render

import (
	"strings"
	"testing"
	"time"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/llm"
)

// A chapter a model wrote says so at its very end, quietly, naming the
// model; the calls that wrote it ride along as its provenance, prompts
// and times included, for the reader who asks.
func TestAChapterNamesTheModelThatWroteIt(t *testing.T) {
	at := time.Date(2026, 10, 14, 13, 45, 0, 0, time.UTC)
	calls := []llm.Call{
		{Role: "planner", Name: "directives", Model: "claude-fable-5-1", System: "s", Prompt: "plan", At: at},
		{Role: "author", Name: "rewrite", Model: "claude-fable-5-1", System: "s", Prompt: "write", At: at},
	}
	u := &corpus.Unit{ID: "u1", Title: "x", IntroMD: "Prose.", Front: map[string]any{"sources": []any{
		map[string]any{"title": "Think Bayes 2e", "licence": "CC BY-NC-SA 4.0"}}}}
	ch, err := RenderChapter(u, nil, Directives{NextAction: "read", Generated: calls}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Provenance) != 2 || ch.Provenance[1].Prompt != "write" {
		t.Fatalf("provenance = %+v", ch.Provenance)
	}
	footer := `<footer class="colophon">Written by claude-fable-5-1</footer>`
	if !strings.HasSuffix(ch.HTML, footer) {
		t.Fatalf("html does not end with the colophon:\n%s", ch.HTML)
	}
	if strings.Index(ch.HTML, `<section class="sources">`) > strings.Index(ch.HTML, footer) {
		t.Fatal("the colophon comes before the sources")
	}

	// Two models share the line; a call with no model known adds nothing.
	calls[1].Model = "gpt-oss-20b"
	ch, _ = RenderChapter(u, nil, Directives{Generated: append(calls, llm.Call{Role: "grader"})}, nil, nil)
	if !strings.Contains(ch.HTML, `Written by claude-fable-5-1 and gpt-oss-20b</footer>`) {
		t.Fatalf("two models:\n%s", ch.HTML)
	}

	// Nothing generated (a page read as it is): no colophon, no provenance.
	plain, _ := RenderChapter(&corpus.Unit{ID: "u1", Title: "x", IntroMD: "p"}, nil, Directives{}, nil, nil)
	if plain.Provenance != nil || strings.Contains(plain.HTML, "colophon") {
		t.Fatal("an unwritten chapter has a colophon")
	}
	// Calls without a model name: the provenance is kept, the line is not.
	anon, _ := RenderChapter(u, nil, Directives{Generated: []llm.Call{{Role: "author"}}}, nil, nil)
	if len(anon.Provenance) != 1 || strings.Contains(anon.HTML, "colophon") {
		t.Fatalf("anonymous calls: provenance=%d html=%s", len(anon.Provenance), anon.HTML)
	}
}

// The colophon's line is one for a single model, "a and b" for two, a
// list for more, and the names are escaped on their way into HTML.
func TestColophonLine(t *testing.T) {
	for _, tc := range []struct {
		models []string
		want   string
	}{
		{nil, ""},
		{[]string{"a"}, "Written by a"},
		{[]string{"a", "b"}, "Written by a and b"},
		{[]string{"a", "b", "c"}, "Written by a, b and c"},
	} {
		if got := colophonLine(tc.models); got != tc.want {
			t.Errorf("colophonLine(%v) = %q, want %q", tc.models, got, tc.want)
		}
	}
	if got := Colophon([]llm.Call{{Model: "<m>"}}); got != `<footer class="colophon">Written by &lt;m&gt;</footer>` {
		t.Fatalf("escaped = %q", got)
	}
}
