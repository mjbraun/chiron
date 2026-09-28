package pages

import (
	"fmt"
	"strings"
)

// The results screen is server-rendered pages, same pipeline as chapters
// (rmpp/design/SPEC.md §0.1, §3): headline, gate bar, and per-item entries
// are typeset (with KaTeX) and paginated; the native client contributes only
// page turns and the action button. No math ever appears in native text.

// ResultsEntry is one graded item on the results pages. Entries carry the
// full audit trail: what the system read (READ AS is mandatory on every
// constructed answer), what was chosen or marked, the reference answer, and
// the grader's explanation for misses.
type ResultsEntry struct {
	N          int    `json:"n"` // the item's printed number in the chapter
	Verdict    string `json:"verdict"`
	IDK        bool   `json:"idk,omitempty"`
	Kind       string `json:"kind"`
	Prompt     string `json:"prompt"`
	Confidence int    `json:"confidence,omitempty"`  // 1-4, 0 when absent
	ReadAs     string `json:"read_as,omitempty"`     // transcript, constructed
	Chose      string `json:"chose,omitempty"`       // "B — 'option text'", MCQ
	Answer     string `json:"answer,omitempty"`      // reference; math allowed
	Why        string `json:"why,omitempty"`         // grader/option explanation
	Flag       string `json:"flag,omitempty"`        // the reader's concern, if they flagged the item
	FlagRuling string `json:"flag_ruling,omitempty"` // the grader's word on the flag
	FlagUpheld bool   `json:"flag_upheld,omitempty"` // whether the reader was right
}

type ResultsDoc struct {
	Unit              string  `json:"unit"`
	HeadLeft          string  `json:"head_left"`
	Calibration       bool    `json:"calibration,omitempty"`
	Score             float64 `json:"score"` // 0..1
	GatePct           float64 `json:"gate"`  // 0..1
	Passed            bool    `json:"passed"`
	ExtensionUnlocked bool    `json:"extension_unlocked,omitempty"`
	Action            string  `json:"action"` // native button label
	Dek               string  `json:"dek"`
	// Headline and Tally are the typeset page's own words, filled by
	// Summarize so a client rendering the document natively says the same.
	Headline string         `json:"headline"`
	Tally    string         `json:"tally,omitempty"` // calibration only
	Entries  []ResultsEntry `json:"entries"`
}

// Summarize fills the derived text fields from the score and entries.
func (d *ResultsDoc) Summarize() {
	d.Headline = d.headline()
	d.Tally = ""
	if d.Calibration {
		d.Tally = d.tally()
	}
}

func (d *ResultsDoc) headline() string {
	pct := int(d.Score*100 + 0.5)
	switch {
	case d.Calibration:
		return fmt.Sprintf("Calibration complete — %d%%.", pct)
	case d.Passed:
		return fmt.Sprintf("Gate cleared — %d%%.", pct)
	default:
		return fmt.Sprintf("Below the gate — %d%%.", pct)
	}
}

func passedResultVerdict(v string) bool {
	return v == "pass" || v == "valid_alternative_path"
}

// tally is the calibration replacement for the gate bar: marking IDK freely
// is part of the design, so "passed" is its own count, not a miss.
func (d *ResultsDoc) tally() string {
	correct, missed, passedOn := 0, 0, 0
	for _, e := range d.Entries {
		switch {
		case e.IDK:
			passedOn++
		case passedResultVerdict(e.Verdict):
			correct++
		default:
			missed++
		}
	}
	return fmt.Sprintf("%d CORRECT · %d MISSED · %d PASSED", correct, missed, passedOn)
}

var confNames = [5]string{"", "UNSURE", "SHAKY", "CONFIDENT", "SURE"}

func entryMark(e ResultsEntry) (glyph, class string) {
	switch {
	case e.IDK, e.Verdict == "ungraded":
		return "—", "mark m-idk"
	case e.Verdict == "fail":
		return "✗", "mark m-miss"
	case e.Verdict == "partial":
		return "~", "mark"
	default:
		return "✓", "mark"
	}
}

func metaRow(label, value, rowClass string) string {
	return `<div class="erow ` + rowClass + `"><div class="elabel">` + label +
		`</div><div class="evalue">` + value + `</div></div>`
}

// escapeText escapes only what element content requires; quotes and
// apostrophes render as themselves (these are typeset pages, not attributes).
var escapeText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

func quoted(s string) string {
	return "“" + escapeText(s) + "”"
}

func entryHTML(e ResultsEntry) string {
	glyph, markClass := entryMark(e)
	var b strings.Builder
	b.WriteString(`<div class="entry"><div class="` + markClass + `">` + glyph + `</div><div class="ebody">`)

	// Header row: number + prompt, confidence tag right. IDK entries carry
	// no tag; a confident/sure miss turns the tag accent - the
	// miscalibration signal.
	b.WriteString(`<div class="ehead"><div class="eprompt"><span class="enum">` +
		fmt.Sprintf("%d.", e.N) + `</span> ` + escapeText(e.Prompt) + `</div>`)
	if !e.IDK && e.Confidence >= 1 && e.Confidence <= 4 {
		tagClass := "etag"
		if e.Confidence >= 3 && e.Verdict == "fail" {
			tagClass = "etag tag-miscal"
		}
		b.WriteString(`<div class="` + tagClass + `">` + confNames[e.Confidence] + `</div>`)
	}
	b.WriteString(`</div>`)

	miss := !passedResultVerdict(e.Verdict)
	switch {
	case e.IDK:
		b.WriteString(metaRow("MARKED", quoted("I don't know."), ""))
	case e.Kind == "mcq":
		if e.Chose != "" {
			b.WriteString(metaRow("CHOSE", escapeText(e.Chose), ""))
		}
	default:
		// The audit trail: every constructed entry shows what the
		// transcriber read, even when correct.
		b.WriteString(metaRow("READ AS", quoted(e.ReadAs), ""))
	}
	if e.Answer != "" && (miss || strings.TrimSpace(e.ReadAs) != strings.TrimSpace(e.Answer)) {
		b.WriteString(metaRow("ANSWER", escapeText(e.Answer), ""))
	}
	if e.Why != "" && miss && !e.IDK {
		b.WriteString(metaRow("WHY", escapeText(e.Why), "erow-why"))
	}
	// What the reader said was wrong with the item, in their words.
	if e.Flag != "" {
		b.WriteString(metaRow("FLAGGED", quoted(e.Flag), "erow-flag"))
		if e.FlagRuling != "" {
			label := "NOT UPHELD"
			if e.FlagUpheld {
				label = "UPHELD"
			}
			b.WriteString(metaRow(label, escapeText(e.FlagRuling), "erow-flag"))
		}
	}
	b.WriteString(`</div></div>`)
	return b.String()
}

func (r *Renderer) wrapResults(d *ResultsDoc) string {
	var b strings.Builder
	b.WriteString(`<div class="res-headline">` + d.headline() + `</div>`)
	if d.Calibration {
		b.WriteString(`<div class="tally">` + d.tally() + `</div>`)
	} else {
		fill := int(float64(ContentW)*d.Score + 0.5)
		if fill > ContentW {
			fill = ContentW
		}
		tick := int(float64(ContentW)*d.GatePct+0.5) - 1
		b.WriteString(fmt.Sprintf(`<div class="gate-wrap"><div class="gate-bar">`+
			`<div class="gate-fill" style="width: %dpx"></div>`+
			`<div class="gate-tick" style="left: %dpx"></div></div>`+
			`<div class="gate-label" style="width: %dpx">GATE %d</div></div>`,
			fill, tick, tick+2, int(d.GatePct*100+0.5)))
	}
	if d.Dek != "" {
		b.WriteString(`<div class="dek">` + escapeText(d.Dek) + `</div>`)
	}
	b.WriteString(`<div class="res-divider"></div>`)
	for _, e := range d.Entries {
		b.WriteString(entryHTML(e))
	}

	right := "RESULTS"
	if d.Calibration {
		right = "CALIBRATION"
	}
	return r.shell(b.String(), resultsCSS(), d.HeadLeft, right)
}

// RenderResults produces the results page stack, cached and deduplicated
// exactly like chapter renders.
func (r *Renderer) RenderResults(d *ResultsDoc) (Result, error) {
	return r.renderShared(d.Unit+"-results", r.wrapResults(d))
}

func resultsCSS() string {
	return `.res-headline { font-size: 72px; line-height: 80px; font-weight: 600; padding-top: 78px; }
.gate-wrap { margin-top: 40px; }
.gate-bar { position: relative; width: ` + fmt.Sprint(ContentW-2) + `px; height: 8px; border: 1px solid #999; }
.gate-fill { position: absolute; left: 0; top: 0; bottom: 0; background: #000; }
.gate-tick { position: absolute; top: -10px; width: 2px; height: 28px; background: #000; }
.gate-label { margin-top: 20px; text-align: right; font-size: 20px; line-height: 24px; font-weight: 600; letter-spacing: 0.10em; color: #444; }
.tally { margin-top: 40px; font-size: 24px; line-height: 28px; font-weight: 600; letter-spacing: 0.10em; text-transform: uppercase; color: #444; }
.dek { font-style: italic; font-size: 32px; line-height: 44px; color: #444; margin-top: 44px; max-width: 1220px; }
.res-divider { border-top: 1px solid #000; margin-top: 44px; }
.entry { display: flex; padding: 34px 0; }
.entry + .entry { border-top: 1px solid #CCC; }
.mark { flex: 0 0 72px; font-size: 40px; line-height: 40px; font-weight: 700; }
.m-miss { color: #8A3D30; }
.m-idk { color: #999; font-weight: 400; }
.ebody { flex: 1 1 auto; min-width: 0; }
.ehead { display: flex; justify-content: space-between; gap: 24px; }
.eprompt { font-size: 30px; line-height: 40px; color: #222; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.enum { font-weight: 700; }
.etag { flex: 0 0 auto; font-size: 22px; line-height: 40px; font-weight: 600; letter-spacing: 0.10em; color: #777; }
.tag-miscal { color: #8A3D30; }
.erow { display: flex; margin-top: 18px; }
.elabel { flex: 0 0 170px; font-size: 22px; line-height: 42px; font-weight: 600; letter-spacing: 0.10em; text-transform: uppercase; color: #777; }
.evalue { flex: 1 1 auto; min-width: 0; font-size: 30px; line-height: 42px; color: #000; }
.erow-why .evalue { font-size: 28px; line-height: 40px; color: #333; }
.erow-flag .evalue { font-size: 28px; line-height: 40px; font-style: italic; color: #8A3D30; }`
}
