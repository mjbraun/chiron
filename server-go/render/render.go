// Package render turns assembled markdown sections into one self-contained
// HTML body plus structured beat/check data.
//
// The iPad app owns the page template and KaTeX assets (bundled); the server
// ships only the chapter body HTML. Beats become <div class="beat">
// placeholders and the app's JS instantiates the interactive UI from the
// `beats` array (reference answers and rubrics included - reveals are a
// learning tool here, not exam security). Mechanical beats grade locally in JS
// so reading works fully detached; llm-checked beats capture the response for
// adjudication at the next exchange.
package render

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"gopkg.in/yaml.v3"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/llm"
)

var beatFence = regexp.MustCompile("(?s)```beat[ \t]*\n(.*?)```")

var md = goldmark.New(
	goldmark.WithExtensions(extension.Table),
	// The corpus contains authored HTML (the <!-- refutes: --> markers and the
	// occasional inline span), and it is ours, not user input.
	goldmark.WithRendererOptions(ghtml.WithUnsafe()),
)

// protectMath pulls math spans out before markdown conversion.
//
// CommonMark treats a backslash before ASCII punctuation as an escape, so the
// markdown renderer silently rewrites `\{` into `{` inside what we intend as
// LaTeX. KaTeX then sees an unmatched brace and renders an error box. Hiding
// math behind placeholders keeps the TeX byte-exact.
func protectMath(text string) (string, []string) {
	spans := findMathSpans(text)
	if len(spans) == 0 {
		return text, nil
	}
	var b strings.Builder
	out := make([]string, 0, len(spans))
	prev := 0
	for _, sp := range spans {
		b.WriteString(text[prev:sp.start])
		// The placeholder must survive markdown untouched: letters and digits
		// only, nothing the renderer could treat as emphasis or a link.
		fmt.Fprintf(&b, "MATHPLACEHOLDER%dENDMATH", len(out))
		out = append(out, text[sp.start:sp.end])
		prev = sp.end
	}
	b.WriteString(text[prev:])
	return b.String(), out
}

// restoreMath puts math back, HTML-escaped.
//
// LaTeX routinely contains `<`, `>` and `&` (subscripts like x_{<t}, alignment
// in matrices). Emitted raw, the browser's HTML parser swallows `<t}` as an
// unknown tag and the equation silently loses characters. Escaped, the parser
// leaves it alone and KaTeX - which reads textContent - still sees the original
// TeX.
func restoreMath(html string, spans []string) string {
	for i, s := range spans {
		safe := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
		html = strings.ReplaceAll(html, fmt.Sprintf("MATHPLACEHOLDER%dENDMATH", i), safe)
	}
	return html
}

// RenderMarkdown converts markdown to HTML with LaTeX preserved byte-exact.
func RenderMarkdown(text string) (string, error) {
	protected, spans := protectMath(text)
	var buf bytes.Buffer
	if err := md.Convert([]byte(protected), &buf); err != nil {
		return "", err
	}
	return restoreMath(buf.String(), spans), nil
}

// extractBeats replaces beat fences with placeholder divs, collecting beats in
// authored order.
func extractBeats(markdown string, out *[]corpus.Beat) (string, error) {
	var err error
	replaced := beatFence.ReplaceAllStringFunc(markdown, func(match string) string {
		m := beatFence.FindStringSubmatch(match)
		var b corpus.Beat
		if e := yaml.Unmarshal([]byte(m[1]), &b); e != nil {
			err = fmt.Errorf("beat block: %w", e)
			return ""
		}
		// The same order the grader will see: both parses shuffle by id.
		b.Options = corpus.Shuffled(b.ID, b.Options)
		*out = append(*out, b)
		return fmt.Sprintf("\n<div class=\"beat\" data-beat-id=%q></div>\n", b.ID)
	})
	return replaced, err
}

// AssembledSection is one section of the chapter after the author role has had
// its say - possibly canon, possibly a depth variant, possibly rewritten.
type AssembledSection struct {
	Heading  string `json:"heading"`
	Markdown string `json:"markdown"`
}

// Directives are the planner's instructions that affect the payload.
type Directives struct {
	OpeningNoteMD string `json:"opening_note_md"`
	NextAction    string `json:"next_action"`
	// Generated is every model call that wrote what the chapter shows:
	// the unit's own authoring, if a model wrote the unit, then the
	// planning and the rewrite for this reading. It becomes the
	// chapter's provenance and its colophon.
	Generated []llm.Call `json:"-"`
}

// ClientItem is a question as shipped to the app. Reference answers and rubrics
// ship too (needed for offline self-check display after answering); the app
// must not show them before the learner commits an answer and a confidence.
type ClientItem struct {
	ID         string         `json:"id"`
	Unit       string         `json:"unit,omitempty"`
	Concept    string         `json:"concept,omitempty"`
	Kind       string         `json:"kind"`
	Prompt     string         `json:"prompt"`
	Check      string         `json:"check"`
	Difficulty string         `json:"difficulty"`
	Options    []ClientOption `json:"options,omitempty"`
	Reveal     *corpus.Reveal `json:"reveal,omitempty"`
}

type ClientOption struct {
	Text string `json:"text"`
}

type Chapter struct {
	Unit        string        `json:"unit"`
	Title       string        `json:"title"`
	Minutes     int           `json:"minutes"`
	HTML        string        `json:"html"`
	Beats       []corpus.Beat `json:"beats"`
	Pretest     []ClientItem  `json:"pretest"`
	Check       []ClientItem  `json:"check"`
	Calibration bool          `json:"calibration,omitempty"`
	NextAction  string        `json:"next_action"`
	// Sources are the attribution lines for the open texts the unit adapts.
	Sources []string `json:"sources,omitempty"`
	// BankHash fingerprints the question bank the items came from, so a
	// stored chapter is known stale when the corpus is rewritten under it.
	BankHash string `json:"bank_hash,omitempty"`
	// Provenance is every model call that wrote the chapter, oldest
	// first, prompts and times included: the reader can ask what was
	// asked of which model, and when.
	Provenance []llm.Call `json:"provenance,omitempty"`
}

func RenderChapter(u *corpus.Unit, sections []AssembledSection, d Directives,
	pretest, check []corpus.Question) (*Chapter, error) {
	beats := []corpus.Beat{}
	var parts []string

	if d.OpeningNoteMD != "" {
		var buf bytes.Buffer
		if err := md.Convert([]byte(d.OpeningNoteMD), &buf); err != nil {
			return nil, err
		}
		parts = append(parts, `<div class="planner-note">`+buf.String()+"</div>")
	}
	render := func(src string) error {
		stripped, err := extractBeats(src, &beats)
		if err != nil {
			return err
		}
		html, err := RenderMarkdown(stripped)
		if err != nil {
			return err
		}
		parts = append(parts, html)
		return nil
	}
	if u.IntroMD != "" {
		if err := render(u.IntroMD); err != nil {
			return nil, err
		}
	}
	for _, sec := range sections {
		if err := render(sec.Markdown); err != nil {
			return nil, err
		}
	}

	attributions := Attributions(u.Front)
	if len(attributions) > 0 {
		var b strings.Builder
		b.WriteString(`<section class="sources"><h2>Sources</h2><ul>`)
		for _, a := range attributions {
			b.WriteString("<li>" + htmlEscape(a) + "</li>")
		}
		b.WriteString("</ul></section>")
		parts = append(parts, b.String())
	}
	if colophon := Colophon(d.Generated); colophon != "" {
		parts = append(parts, colophon)
	}

	ch := &Chapter{
		Unit: u.ID, Title: u.Title, Minutes: u.Minutes,
		HTML: strings.Join(parts, "\n"), Beats: beats,
		Pretest: clientItems(pretest), Check: clientItems(check),
		Calibration: u.IsCalibration(),
		NextAction:  d.NextAction,
		Sources:     attributions,
		Provenance:  d.Generated,
	}
	return ch, nil
}

// Colophon is the chapter's last line: the model that wrote it, named
// quietly at the very end. Nothing when no call wrote it, or none of
// the calls can name its model.
func Colophon(calls []llm.Call) string {
	line := colophonLine(llm.Models(calls))
	if line == "" {
		return ""
	}
	return `<footer class="colophon">` + htmlEscape(line) + `</footer>`
}

func colophonLine(models []string) string {
	switch n := len(models); {
	case n == 0:
		return ""
	case n == 1:
		return "Written by " + models[0]
	default:
		return "Written by " + strings.Join(models[:n-1], ", ") + " and " + models[n-1]
	}
}

// Attributions reads the `sources:` list a unit's front matter carries
// and gives one line per source, in order of first use: "Adapted from
// <title> by <authors> (<licence>)".
func Attributions(front map[string]any) []string {
	raw, _ := front["sources"].([]any)
	var out []string
	seen := map[string]bool{}
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		title, _ := m["title"].(string)
		if title == "" {
			continue
		}
		authors, _ := m["authors"].(string)
		licence, _ := m["licence"].(string)
		line := "Adapted from " + title
		if authors != "" {
			line += " by " + authors
		}
		if licence != "" {
			line += " (" + licence + ")"
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func clientItems(qs []corpus.Question) []ClientItem {
	out := make([]ClientItem, 0, len(qs))
	for _, q := range qs {
		out = append(out, clientItem(q))
	}
	return out
}

func clientItem(q corpus.Question) ClientItem {
	kind := q.Kind
	if kind == "" {
		kind = "constructed"
	}
	check := q.Check
	if check == "" {
		check = "llm"
	}
	difficulty := q.Difficulty
	if difficulty == "" {
		difficulty = "core"
	}
	item := ClientItem{
		ID: q.ID, Unit: q.Unit, Concept: q.Concept, Kind: kind,
		Prompt: q.Prompt, Check: check, Difficulty: difficulty,
	}
	if kind == "mcq" || check == "screener" {
		for _, o := range q.Options {
			item.Options = append(item.Options, ClientOption{Text: o.Text})
		}
	}
	if kind == "mcq" {
		// Per-option explanations are revealed only after the answer, together
		// with which one was correct.
		rev := &corpus.Reveal{}
		for _, o := range q.Options {
			rev.Options = append(rev.Options, corpus.RevealOption{
				Explain: o.Explain, Correct: o.Correct,
			})
		}
		item.Reveal = rev
	} else {
		item.Reveal = &corpus.Reveal{Answer: q.Answer, Rubric: q.Rubric}
	}
	return item
}
