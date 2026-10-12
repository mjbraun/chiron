package render

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mjbraun/chiron/server/checkers"
	"github.com/mjbraun/chiron/server/corpus"
)

type checkersOption = checkers.Option

// These cases are the systemic math-corruption bug that shipped once and had to
// be found by loading the real renderer in a browser. Every equation in the
// book was at risk from two independent mechanisms: CommonMark eating the
// backslash in `\{`, and the browser's HTML parser swallowing `x_{<t}` as an
// unknown tag.
func TestMathSurvivesMarkdown(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		contains []string
		absent   []string
	}{
		{
			name:     "braces keep their backslashes",
			in:       `The set $\{1, 2\}$ is small.`,
			contains: []string{`$\{1, 2\}$`},
		},
		{
			name:     "less-than in a subscript is escaped, not swallowed",
			in:       `Condition on $x_{<t}$ here.`,
			contains: []string{`x_{&lt;t}`},
			absent:   []string{`x_{<t}`},
		},
		{
			name:     "display math is one span, not two empty ones",
			in:       "$$C = M \\oplus K$$",
			contains: []string{`$$C = M \oplus K$$`},
		},
		{
			name:     "ampersand in matrix alignment is escaped",
			in:       `$$\begin{bmatrix} a & b \end{bmatrix}$$`,
			contains: []string{`a &amp; b`},
		},
		{
			name:     "prices are not math",
			in:       `It costs $5 or maybe $10 tops.`,
			contains: []string{"$5", "$10"},
			absent:   []string{"MATHPLACEHOLDER"},
		},
		{
			name:   "escaped dollar stays prose",
			in:     `A literal \$ sign.`,
			absent: []string{"MATHPLACEHOLDER"},
		},
		{
			name:     "emphasis inside math is not markdown",
			in:       `$a_*b_*c$ stays put.`,
			contains: []string{`$a_*b_*c$`},
			absent:   []string{"<em>"},
		},
		{
			name:     "an unclosed dollar does not swallow the chapter",
			in:       "A stray $ here.\n\nA new paragraph with **bold**.",
			contains: []string{"<strong>bold</strong>"},
			absent:   []string{"MATHPLACEHOLDER"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := RenderMarkdown(c.in)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, want := range c.contains {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\ngot: %s", want, out)
				}
			}
			for _, bad := range c.absent {
				if strings.Contains(out, bad) {
					t.Errorf("output contains %q, which it must not\ngot: %s", bad, out)
				}
			}
		})
	}
}

// The whole-corpus sweep. When this bug was found by hand, the audit was "2045
// equations render, 0 errors" - so this asserts the same property mechanically:
// no placeholder leaks into the output, and no raw `<` survives inside math
// where the HTML parser would eat it.
func TestWholeCorpusRendersWithoutCorruption(t *testing.T) {
	c, err := corpus.Load(filepath.Join("..", "..", "corpus"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	totalEquations, totalBeats := 0, 0
	for _, id := range c.UnitOrder() {
		u := c.Units[id]
		var sections []AssembledSection
		for _, s := range u.Sections {
			sections = append(sections, AssembledSection{Heading: s.Heading, Markdown: s.Markdown()})
		}
		ch, err := RenderChapter(u, sections, Directives{}, u.Questions.Pretest, u.Questions.Check)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if strings.Contains(ch.HTML, "MATHPLACEHOLDER") || strings.Contains(ch.HTML, "ENDMATH") {
			t.Errorf("%s: placeholder leaked into the rendered chapter", id)
		}
		for _, sp := range findMathSpans(ch.HTML) {
			eq := ch.HTML[sp.start:sp.end]
			totalEquations++
			if strings.Contains(eq, "<") {
				t.Errorf("%s: raw '<' survived inside math, the parser will eat it: %.60s", id, eq)
			}
		}
		totalBeats += len(ch.Beats)
		if len(ch.Beats) == 0 && !u.IsCalibration() {
			t.Errorf("%s: no beats extracted - step-based interaction is the point", id)
		}
		for _, b := range ch.Beats {
			if !strings.Contains(ch.HTML, `data-beat-id="`+b.ID+`"`) {
				t.Errorf("%s: beat %s has no placeholder div in the html", id, b.ID)
			}
		}
	}
	if totalEquations < 2000 {
		t.Errorf("only %d equations found across the corpus, expected ~2045", totalEquations)
	}
	t.Logf("%d equations, %d beats, 0 corrupted", totalEquations, totalBeats)
}

// MCQ options must reach the app without their answers attached, and free-text
// items must carry their reveal.
func TestClientItemsHideAndRevealTheRightThings(t *testing.T) {
	mcq := corpus.Question{
		ID: "q1", Kind: "mcq", Prompt: "which", Check: "choice",
		Options: []checkersOption{{Text: "a", Correct: true, Explain: "yes"},
			{Text: "b", Explain: "no", Misconception: "M1"}},
	}
	item := clientItem(mcq)
	if len(item.Options) != 2 {
		t.Fatalf("got %d options", len(item.Options))
	}
	for _, o := range item.Options {
		if o.Text == "" {
			t.Error("option text missing")
		}
	}
	if item.Reveal == nil || len(item.Reveal.Options) != 2 {
		t.Fatal("mcq reveal must carry per-option explanations")
	}
	if !item.Reveal.Options[0].Correct || item.Reveal.Options[1].Correct {
		t.Error("reveal must mark exactly the correct option")
	}

	free := corpus.Question{ID: "q2", Kind: "constructed", Prompt: "why", Check: "llm",
		Answer: "because", Rubric: "must say because"}
	fi := clientItem(free)
	if fi.Reveal == nil || fi.Reveal.Answer != "because" || fi.Reveal.Rubric == "" {
		t.Errorf("constructed item reveal wrong: %+v", fi.Reveal)
	}
	if fi.Difficulty != "core" {
		t.Errorf("difficulty should default to core, got %q", fi.Difficulty)
	}
}

// A wrong option's "correct": false was dropped by omitempty. The iPad's
// strict decoder then failed the whole exchange on the first MCQ reveal,
// which is every calibration series. False is a value here, not an absence.
func TestRevealOptionsAlwaysCarryCorrect(t *testing.T) {
	q := corpus.Question{ID: "q", Kind: "mcq", Prompt: "?", Check: "choice",
		Options: []checkers.Option{{Text: "a", Correct: true, Explain: "yes"},
			{Text: "b", Explain: "no", Misconception: "M1"}}}
	raw, err := json.Marshal(clientItem(q))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"correct":`) != 2 {
		t.Fatalf("every reveal option must carry correct, got %s", raw)
	}
}

// The print shell (pages) hands chapter HTML to KaTeX's auto-render, whose
// own rule pairs any two dollars in a run of text: "about $33K MRR ... at
// $74.8K MRR" was set as one equation on paper as it was on the iPad. The
// shell renders only backslash delimiters, and the server's spans are
// rewritten to them, outside tags and code, before the page is built.
func TestTeXDelimiters(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"inline span", `<p>The set $\{1, 2\}$ is small.</p>`, `<p>The set \(\{1, 2\}\) is small.</p>`},
		{"display span", `<p>$$C = M \oplus K$$</p>`, `<p>\[C = M \oplus K\]</p>`},
		{"two sums are prose", `<p>about $33K MRR; (Nous at $74.8K MRR)</p>`, `<p>about $33K MRR; (Nous at $74.8K MRR)</p>`},
		{"code is left alone", `<p>run <code>echo $x$y</code> and $x$</p>`, `<p>run <code>echo $x$y</code> and \(x\)</p>`},
		{"a fence is left alone", "<pre><code>$a$\n$b$</code></pre><p>$a$</p>", "<pre><code>$a$\n$b$</code></pre><p>\\(a\\)</p>"},
		{"a span does not cross a tag", `<p>$a <em>b</em> c$</p>`, `<p>$a <em>b</em> c$</p>`},
		{"escaped math keeps its entities", `<p>$x_{&lt;t}$</p>`, `<p>\(x_{&lt;t}\)</p>`},
		{"a closer after a space closes before code", `<p>an edit has $\rho = $ <code>sheets</code></p>`, `<p>an edit has \(\rho = \) <code>sheets</code></p>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TeXDelimiters(c.in); got != c.want {
				t.Errorf("TeXDelimiters(%q)\n got: %s\nwant: %s", c.in, got, c.want)
			}
		})
	}
}

// The spans the server finds, as the page script finds them: these cases
// mirror scripts/test-book-js.mjs and must stay the same there. A closer
// may follow a space when a word does not follow it (a chapter wrote
// "$\rho = $ sheets" and it showed raw); a dollar before a word or a digit
// is a price or an opener, so "$5 or $10" and "$5 for $x^2$" keep their
// readings.
func TestFindMathSpans(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"an inline span", `The set $\{1, 2\}$ is small.`, []string{`$\{1, 2\}$`}},
		{"display math is one span", `$$C = M \oplus K$$`, []string{`$$C = M \oplus K$$`}},
		{"prices are not math", `It costs $5 or maybe $10 tops.`, nil},
		{"two sums in one sentence are not math", `for about $33K MRR of customers; a written SLA (Nous at $74.8K MRR, Feldera)`, nil},
		{"an escaped dollar is prose", `A literal \$ sign and $x$.`, []string{`$x$`}},
		{"a closer after a space closes when nothing opens after it", `an edit has $\rho = $ sheets, the test fails`, []string{`$\rho = $`}},
		{"a trailing space inside a span is still a span", `$x $ is a span`, []string{`$x $`}},
		{"a bare dollar with no opener is prose", `capitalization in $ trillion`, nil},
		{"a closer after a space does not close on a word", `Pay $5 and $ten more.`, nil},
		{"an unclosed dollar does not cross a blank line", "A stray $ here.\n\nA new paragraph $y$.", []string{`$y$`}},
		{"emphasis inside math is math", `$a_*b_*c$ stays put.`, []string{`$a_*b_*c$`}},
		{"a price before a span does not take its opener", `Pay $5 for $x^2$ twice.`, []string{`$x^2$`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, sp := range findMathSpans(c.in) {
				got = append(got, c.in[sp.start:sp.end])
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("findMathSpans(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
		})
	}
}
