package pages

import (
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

func sampleResults() *ResultsDoc {
	return &ResultsDoc{
		Unit:     "u2",
		HeadLeft: "2 · THE SHAPE OF COMPUTATION",
		Score:    0.82,
		GatePct:  0.80,
		Passed:   true,
		Action:   "Next chapter",
		Dek:      "The next chapter builds on what held.",
		Entries: []ResultsEntry{
			{N: 4, Verdict: "pass", Kind: "constructed", Confidence: 4,
				Prompt: "Compute the dot product of $[2,0,-3]$ and $[1,4,2]$.",
				ReadAs: "-4", Answer: "-4"},
			{N: 5, Verdict: "fail", Kind: "constructed", Confidence: 3,
				Prompt: "What is the shape of $XW$?",
				ReadAs: "512 x 32", Answer: "32 x 512",
				Why: "Order matters: rows come from $X$."},
			{N: 6, Verdict: "fail", IDK: true, Kind: "mcq",
				Prompt: "Which dimension is contracted?",
				Answer: "B — the inner dimension"},
			{N: 7, Verdict: "fail", Kind: "mcq", Confidence: 1,
				Prompt: "Softmax of shifted scores?",
				Chose:  "A — 'they differ'", Answer: "C — identical",
				Why: "Softmax depends only on differences."},
		},
	}
}

// SPEC §3: headline with em-dashed percentage, gate bar with the 80% tick,
// dek, and the entry anatomy: verdict marks, READ AS as the mandatory audit
// row, MARKED for IDK, ANSWER on every miss, WHY on misses with an
// explanation, and the miscalibration accent on confident misses only.
func TestWrapResultsAnatomy(t *testing.T) {
	r := &Renderer{KatexDir: "k", CacheDir: "c"}
	doc := r.wrapResults(sampleResults())

	for _, want := range []string{
		"Gate cleared — 82%.",
		`class="gate-tick"`,
		"GATE 80",
		"The next chapter builds on what held.",
		`<span class="enum">4.</span>`,
		"READ AS",
		"“-4”",
		"“512 x 32”",
		"MARKED",
		"“I don't know.”",
		"CHOSE",
		"ANSWER",
		"WHY",
		"Softmax depends only on differences.",
		"RESULTS",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("results doc missing %q", want)
		}
	}
	// Confidence tags: the confident miss carries the accent, the sure pass
	// stays quiet, the IDK entry has no tag at all.
	if !strings.Contains(doc, `class="etag tag-miscal">CONFIDENT<`) {
		t.Error("confident miss not accented")
	}
	if !strings.Contains(doc, `class="etag">SURE<`) {
		t.Error("sure pass should carry a plain tag")
	}
	if strings.Contains(doc, `>UNSURE<`) == false {
		// entry 7: unsure miss keeps a plain gray tag
		t.Error("unsure tag missing")
	}
	// The pass entry's transcript matches the reference: no ANSWER row for it.
	if strings.Count(doc, "ANSWER") != 3 {
		t.Errorf("want ANSWER on exactly the 3 non-pass entries, doc has %d",
			strings.Count(doc, "ANSWER"))
	}
}

func TestWrapResultsCalibration(t *testing.T) {
	r := &Renderer{KatexDir: "k", CacheDir: "c"}
	d := sampleResults()
	d.Calibration = true
	d.HeadLeft = "HOW AI WORKS"
	d.Score = 0.07
	d.Action = "Begin chapter 1"
	doc := r.wrapResults(d)
	for _, want := range []string{
		"Calibration complete — 7%.",
		"1 CORRECT · 2 MISSED · 1 PASSED",
		"CALIBRATION",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("calibration results missing %q", want)
		}
	}
	for _, banned := range []string{`class="gate-bar"`, "GATE 80", "Gate cleared"} {
		if strings.Contains(doc, banned) {
			t.Errorf("calibration results must not contain %q", banned)
		}
	}
}

// Rendered results: running head, folio, the headline high on page 1, the
// gate tick at x=1230, and the accent color present (miss marks) - SPEC §3.3.
func TestRenderResultsPages(t *testing.T) {
	r := testRenderer(t)
	r.FontsDir = "../../assets/fonts"
	if _, err := os.Stat(r.FontsDir); err != nil {
		t.Skip("bundled fonts not present")
	}
	res, err := r.RenderResults(sampleResults())
	if err != nil {
		t.Fatalf("render results: %v", err)
	}
	if res.Count < 1 {
		t.Fatal("no results pages")
	}
	f, err := os.Open(res.PagePath(0))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	dark := func(x0, y0, x1, y1 int) bool {
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if r < 0x8000 && g < 0x8000 && b < 0x8000 {
					return true
				}
			}
		}
		return false
	}
	if !dark(110, 38, 700, 66) || !dark(1100, 38, 1510, 66) {
		t.Error("running head missing")
	}
	if !dark(600, 2098, 1020, 2128) {
		t.Error("folio missing")
	}
	if !dark(110, 170, 1200, 260) {
		t.Error("headline not in the expected band")
	}
	// Gate bar: headline block ends at 178+80; bar 40 below; tick spans
	// 28px around it at x 1230.
	if !dark(1228, 285, 1233, 320) {
		t.Error("gate tick not at x=1230")
	}
	// Accent: some pixel close to #8A3D30 (the miss marks / miscal tag).
	accent := false
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y && !accent; y += 2 {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			r, g, bb, _ := img.At(x, y).RGBA()
			r8, g8, b8 := int(r>>8), int(g>>8), int(bb>>8)
			if abs(r8-0x8A) < 40 && abs(g8-0x3D) < 40 && abs(b8-0x30) < 40 && r8 > g8+40 {
				accent = true
				break
			}
		}
	}
	if !accent {
		t.Error("no accent-colored pixels (miss marks) found")
	}
	_ = image.Rect
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Contents rows (SPEC §7): cleared rows carry leader + score, the current
// chapter is the one IN PROGRESS row, unwritten rows have no leader and an
// italic note.
func TestWrapContents(t *testing.T) {
	r := &Renderer{KatexDir: "k", CacheDir: "c"}
	doc := r.wrapContents(&ContentsDoc{
		Subject: "How AI Works",
		Rows: []ContentsRow{
			{Unit: "u1", N: 1, Title: "The core bet", State: "cleared", Score: 91},
			{Unit: "u2", N: 2, Title: "Math floor", State: "in_progress", Current: true},
			{Unit: "u3", N: 3, Title: "Attention", State: "unwritten"},
		},
	})
	for _, want := range []string{
		"Contents.",
		"Chapters are written as you reach them",
		"CLEARED · 91",
		"IN PROGRESS",
		"not yet written",
		`class="crow crow-current"`,
		`class="crow crow-unwritten"`,
		"HOW AI WORKS",
		"CONTENTS",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("contents missing %q", want)
		}
	}
	// Unwritten rows carry no leader; written rows do (2 of 3).
	if strings.Count(doc, `class="cleader"`) != 2 {
		t.Errorf("want 2 leaders, got %d", strings.Count(doc, `class="cleader"`))
	}
}

// An item the reader flagged during the check shows their concern beside
// it, whatever the verdict.
func TestWrapResultsShowsWhatTheReaderFlagged(t *testing.T) {
	r := &Renderer{KatexDir: "k", CacheDir: "c"}
	d := sampleResults()
	d.Entries[0].Flag = "the key would have been rotated at 16:00 & so useless at 17:15"
	doc := r.wrapResults(d)
	if strings.Count(doc, "FLAGGED") != 1 {
		t.Errorf("want FLAGGED on exactly the flagged entry, doc has %d", strings.Count(doc, "FLAGGED"))
	}
	if !strings.Contains(doc, "“the key would have been rotated at 16:00 &amp; so useless at 17:15”") {
		t.Error("the concern is not quoted, escaped, on the page")
	}
}
