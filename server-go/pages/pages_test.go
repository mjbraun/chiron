package pages

import (
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mjbraun/chiron/server/checkers"
	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/render"
)

func testRenderer(t *testing.T) *Renderer {
	t.Helper()
	r := &Renderer{
		KatexDir: "../../ipad-app/Chiron/Resources/katex",
		CacheDir: t.TempDir(),
	}
	if os.Getenv("CHIRON_RENDER") == "0" {
		t.Skip("CHIRON_RENDER=0: no browser renders in this run")
	}
	if _, err := os.Stat(r.KatexDir); err != nil {
		t.Skip("katex assets not present")
	}
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not installed")
	}
	if r.chrome() == "" {
		t.Skip("chrome not installed")
	}
	return r
}

func TestRenderPagesProducesDeviceSizedPNGs(t *testing.T) {
	r := testRenderer(t)
	ch := &render.Chapter{
		Unit:  "u-test",
		Title: "Pages under test",
		HTML: `<h1>Attention</h1>
<p>The scaled dot product $\frac{QK^T}{\sqrt{d_k}}$ is the core.</p>
<div class="beat" data-beat-id="b1"></div>
<p>` + loremParagraphs(40) + `</p>
$$H(P^*, P_\theta) = H(P^*) + D_{KL}(P^* \| P_\theta)$$`,
		Beats: []corpus.Beat{{ID: "b1", Prompt: "Compute $[1,2]\\cdot[3,4]$ by hand."}},
	}

	res, err := r.Render(ch)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if res.Count < 2 {
		t.Fatalf("want >=2 pages for long content, got %d", res.Count)
	}
	for i := 0; i < res.Count; i++ {
		p := res.PagePath(i)
		w, h, err := pngSize(p)
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		if w != PageW || h != PageH {
			t.Fatalf("page %d is %dx%d, want %dx%d", i, w, h, PageW, PageH)
		}
	}

	// Same chapter renders from cache: identical dir, no re-render.
	res2, err := r.Render(ch)
	if err != nil {
		t.Fatalf("cached render: %v", err)
	}
	if res2.Dir != res.Dir || res2.Count != res.Count {
		t.Fatalf("cache miss: %v vs %v", res2, res)
	}
}

func TestWrapOrdersPretestBodyCheck(t *testing.T) {
	r := &Renderer{KatexDir: "k", CacheDir: "c"}
	ch := &render.Chapter{
		Unit:  "u9",
		Title: "Ordering",
		HTML:  `<p>BODY-MARKER</p>`,
		Pretest: []render.ClientItem{{
			ID: "u9-p1", Kind: "constructed",
			Prompt: "Compute $2+2$. <script> must be escaped.",
		}},
		Check: []render.ClientItem{
			{ID: "u9-q1", Kind: "constructed", Prompt: "Free text one."},
			{ID: "u9-q2", Kind: "mcq", Prompt: "Pick one.",
				Options: []render.ClientOption{{Text: "first"}, {Text: "second"}}},
		},
	}
	doc := r.wrap(ch)

	pre := strings.Index(doc, `data-item-id="u9-p1"`)
	body := strings.Index(doc, "BODY-MARKER")
	chk := strings.Index(doc, `data-item-id="u9-q1"`)
	if pre < 0 || body < 0 || chk < 0 || !(pre < body && body < chk) {
		t.Fatalf("order wrong: pretest@%d body@%d check@%d", pre, body, chk)
	}
	if strings.Contains(doc, "Comprehension check") {
		t.Error("interactive layout should be headerless on item pages")
	}
	if !strings.Contains(doc, "control-strip") {
		t.Error("interactive answer boxes must reserve the control strip")
	}
	if !strings.Contains(doc, "&lt;script&gt;") || strings.Contains(doc, "<script> must") {
		t.Fatal("prompt not HTML-escaped")
	}
	for _, want := range []string{`data-item-id="u9-p1"`, `data-item-id="u9-q1"`,
		`data-item-id="u9-q2"`, "first", "second", "mcq-letter"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q", want)
		}
	}
	// Interactive strips stay empty - controls are the client's.
	if strings.Contains(doc, "☐") {
		t.Fatal("interactive layout printed scaffold checkboxes")
	}
	// Print shares the whole page chrome (SPEC §8); its strips carry the
	// checkbox scaffold on the same geometry.
	rp := &Renderer{KatexDir: "k", CacheDir: "c", PrintLayout: true}
	pdoc := rp.wrap(ch)
	for _, want := range []string{"☐ I don't know", "☐ unsure", "☐ A", "☐ B",
		`class="control-strip"`, `class="sc-row"`} {
		if !strings.Contains(pdoc, want) {
			t.Fatalf("print layout missing %q", want)
		}
	}
	if strings.Contains(pdoc, "How confident are you?") {
		t.Error("old prose scaffolding leaked into print")
	}
	// Reference answers and rubrics must never reach paper.
	if strings.Contains(doc, "Reference answer") {
		t.Fatal("reveal leaked into pages")
	}
}

func TestBeatInjection(t *testing.T) {
	html := injectBeats(
		`<p>before</p><div class="beat" data-beat-id="x1"></div><p>after</p>`,
		[]corpus.Beat{{ID: "x1", Type: "compute", Prompt: "Work this out."}})
	want := `class="beat-box"`
	if !contains(html, want) || !contains(html, "Work this out.") {
		t.Fatalf("beat not injected: %s", html)
	}
	if contains(html, `data-beat-id="x1"></div><p>after`) {
		t.Fatalf("placeholder left behind: %s", html)
	}
	choice := injectBeats(`<div class="beat" data-beat-id="x2"></div>`,
		[]corpus.Beat{{ID: "x2", Type: "predict", Prompt: "Which?", Options: []checkers.Option{{Text: "this", Correct: true}, {Text: "that"}}}})
	if !contains(choice, `<span class="letter">A</span> this`) || !contains(choice, `<span class="letter">B</span> that`) || !contains(choice, `beat-ink short`) {
		t.Fatalf("options not printed: %s", choice)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// The design contract (rmpp/design/SPEC.md §2.1, §9): fixed box heights 640
// and 970 with a 20px gap, packed top-aligned into the 1960px content area,
// each item published as page + rect [110, y, 1400, h] + strip.
func TestPackingFixedHeights(t *testing.T) {
	llmQ := func(id string) render.ClientItem {
		return render.ClientItem{ID: id, Kind: "constructed", Check: "llm",
			Prompt: "Explain why the residual stream is a bus."}
	}
	numQ := func(id string) render.ClientItem {
		return render.ClientItem{ID: id, Kind: "constructed", Check: "numeric",
			Prompt: "Compute $2+2$."}
	}
	mcqQ := func(id string, n int) render.ClientItem {
		it := render.ClientItem{ID: id, Kind: "mcq", Check: "key", Prompt: "Pick one."}
		for i := 0; i < n; i++ {
			it.Options = append(it.Options, render.ClientOption{Text: "an option"})
		}
		return it
	}

	// Two ink-work items fill a page flush: 970 + 20 + 970 = 1960.
	ch := &render.Chapter{Check: []render.ClientItem{llmQ("a"), llmQ("b")}}
	ip := ItemPages(ch, 1)
	if len(ip) != 2 || ip[0].Page != 0 || ip[1].Page != 0 {
		t.Fatalf("want both items on page 0: %+v", ip)
	}
	if ip[0].Rect != [4]int{110, 100, 1400, 970} {
		t.Fatalf("first rect = %v", ip[0].Rect)
	}
	if ip[1].Rect != [4]int{110, 1090, 1400, 970} {
		t.Fatalf("second rect = %v", ip[1].Rect)
	}
	if ip[0].Strip != StripConstructed || ip[1].Strip != StripConstructed {
		t.Fatalf("constructed strip = %d/%d, want %d", ip[0].Strip, ip[1].Strip, StripConstructed)
	}

	// Three short items pack one page exactly (640×3 + 2×20 = 1960); the
	// fourth starts the next page.
	ch = &render.Chapter{Check: []render.ClientItem{
		mcqQ("a", 3), numQ("b"), mcqQ("c", 4), numQ("d")}}
	ip = ItemPages(ch, 2)
	wantY := []int{100, 760, 1420, 100}
	wantPage := []int{0, 0, 0, 1}
	for i := range ip {
		if ip[i].Page != wantPage[i] || ip[i].Rect[1] != wantY[i] || ip[i].Rect[3] != BoxShort {
			t.Fatalf("item %d = %+v, want page %d y %d h %d",
				i, ip[i], wantPage[i], wantY[i], BoxShort)
		}
	}
	if ip[0].Strip != StripMCQ || ip[1].Strip != StripConstructed {
		t.Fatalf("strips = %d/%d, want %d/%d", ip[0].Strip, ip[1].Strip, StripMCQ, StripConstructed)
	}

	// Tall triggers: five options, or a long stem.
	if h := boxH(mcqQ("e", 5)); h != BoxTall {
		t.Fatalf("5-option MCQ box = %d, want %d", h, BoxTall)
	}
	long := numQ("f")
	long.Prompt = strings.Repeat("A very long stem that keeps going. ", 12)
	if h := boxH(long); h != BoxTall {
		t.Fatalf("long-stem box = %d, want %d", h, BoxTall)
	}

	// The published payload speaks the spec's language: item, rect, strip.
	b, err := json.Marshal(ip[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"item":"a"`, `"rect":[110,100,1400,640]`,
		`"strip":196`, `"page":0`, `"kind":"mcq"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("payload %s missing %s", b, want)
		}
	}
}

func loremParagraphs(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += "Every quantity in this book is a block of numbers, and the only thing you need to track is its shape. Get the shapes right and the equations follow almost mechanically. "
	}
	return s
}

func pngSize(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var hdr [24]byte
	if _, err := f.Read(hdr[:]); err != nil {
		return 0, 0, err
	}
	w := int(hdr[16])<<24 | int(hdr[17])<<16 | int(hdr[18])<<8 | int(hdr[19])
	h := int(hdr[20])<<24 | int(hdr[21])<<16 | int(hdr[22])<<8 | int(hdr[23])
	return w, h, nil
}

// Every interactive page carries the print chrome the design specifies: a
// running head in the top margin and a centered folio in the bottom margin,
// and question pages pin their boxes to the published geometry (SPEC §1, §2.4).
func TestRenderedPagesCarryChrome(t *testing.T) {
	r := testRenderer(t)
	r.FontsDir = "../../assets/fonts"
	if _, err := os.Stat(r.FontsDir); err != nil {
		t.Skip("bundled fonts not present")
	}
	ch := &render.Chapter{
		Unit:  "u2",
		Title: "The shape of computation",
		HTML:  `<h1>Chapter</h1><p>` + loremParagraphs(60) + `</p>`,
		Check: []render.ClientItem{
			{ID: "u2-q1", Kind: "constructed", Check: "llm", Prompt: "Explain."},
			{ID: "u2-q2", Kind: "constructed", Check: "llm", Prompt: "Derive."},
		},
	}
	res, err := r.Render(ch)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if res.Count < 3 {
		t.Fatalf("want >=3 pages (prose + check), got %d", res.Count)
	}
	darkIn := func(img image.Image, x0, y0, x1, y1 int) bool {
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
	for i := 0; i < res.Count; i++ {
		f, err := os.Open(res.PagePath(i))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !darkIn(img, 110, 38, 700, 66) {
			t.Errorf("page %d: no running head in the top margin", i)
		}
		if !darkIn(img, 600, 2098, 1020, 2128) {
			t.Errorf("page %d: no folio in the bottom margin", i)
		}
	}
	// The check page (last): two 970 boxes flush with the content area -
	// top border at y 100, bottom border at y 2060, shelf rule above each
	// strip (970 box, strip 120: shelf top edge at y0+849).
	f, _ := os.Open(res.PagePath(res.Count - 1))
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, band := range [][2]int{{99, 103}, {1088, 1093}, {2055, 2060}} {
		if !darkIn(img, 400, band[0], 420, band[1]) {
			t.Errorf("check page: no box border in rows %d-%d", band[0], band[1])
		}
	}
	for _, shelf := range []int{100 + 970 - 121, 1090 + 970 - 121} {
		if !darkIn(img, 400, shelf-3, 420, shelf+4) {
			t.Errorf("check page: no shelf rule near y %d", shelf)
		}
	}
}

// Pretest pages carry their framing: the note in a reserved block above
// the first box (geometry shifted by PretestNoteH), and a page class the
// paginator turns into the BEFORE YOU READ running head. Check pages stay
// unmarked.
func TestPretestFraming(t *testing.T) {
	r := &Renderer{KatexDir: "k", CacheDir: "c"}
	ch := &render.Chapter{
		Unit: "u3", Title: "T", HTML: "<p>body</p>",
		Pretest: []render.ClientItem{
			{ID: "p1", Kind: "constructed", Check: "llm", Prompt: "One."},
			{ID: "p2", Kind: "constructed", Check: "llm", Prompt: "Two."},
		},
		Check: []render.ClientItem{
			{ID: "q1", Kind: "constructed", Check: "llm", Prompt: "Q."}},
	}
	doc := r.wrap(ch)
	if !strings.Contains(doc, `<div class="qpage qpage-pretest">`) {
		t.Error("pretest pages not marked")
	}
	if !strings.Contains(doc, `class="qpage-note"`) ||
		!strings.Contains(doc, "not supposed to know these yet") {
		t.Error("framing note missing from the first pretest page")
	}
	if strings.Count(doc, `class="qpage-note"`) != 1 {
		t.Error("note must appear only on the first pretest page")
	}
	if !strings.Contains(doc, `<div class="qpage">`) {
		t.Error("check pages must stay unmarked")
	}
	// The note shifts geometry: p1 starts below it; p2 no longer fits the
	// first page (160+970+20+970 > 1960) and opens page 1 at the top.
	ip := ItemPages(ch, 10)
	if ip[0].Rect[1] != MarginY+PretestNoteH || ip[0].Page != 0 {
		t.Fatalf("p1 = %+v, want y %d page 0", ip[0], MarginY+PretestNoteH)
	}
	if ip[1].Rect[1] != MarginY || ip[1].Page != 1 {
		t.Fatalf("p2 = %+v, want y %d page 1", ip[1], MarginY)
	}
}

// The placement step is one question: same page as the intro, no series
// header, and the item map still points at it.
func TestScreenerChapterIsOnePage(t *testing.T) {
	r := &Renderer{KatexDir: "k", CacheDir: "c"}
	ch := &render.Chapter{
		Unit: "u0", Title: "Calibration", Calibration: true,
		HTML: `<p>One placement question.</p>`,
		Check: []render.ClientItem{{
			ID: "u0-s1", Kind: "constructed", Check: "screener",
			Prompt: "Rate yourself 1-5."}},
	}
	doc := r.wrap(ch)
	if !strings.Contains(doc, "items-inline") {
		t.Error("screener section not inlined with the intro")
	}
	if strings.Contains(doc, "The series") {
		t.Error("series framing shown over the screener")
	}
	ip := ItemPages(ch, 1)
	if len(ip) != 1 || ip[0].Page != 0 || ip[0].Check != "screener" {
		t.Fatalf("item map = %+v", ip)
	}
}

func TestScreenerLevelsRenderAsVerticalList(t *testing.T) {
	r := &Renderer{KatexDir: "k", CacheDir: "c"}
	ch := &render.Chapter{
		Unit: "u0", Calibration: true,
		HTML: `<p>intro</p>`,
		Check: []render.ClientItem{{
			ID: "u0-s1", Kind: "constructed", Check: "screener",
			Prompt: "Rate yourself.",
			Options: []render.ClientOption{
				{Text: "Absolute novice"}, {Text: "Expert"}},
		}},
	}
	doc := r.wrap(ch)
	if !strings.Contains(doc, `<ul class="screener-list">`) {
		t.Fatal("no screener list")
	}
	if !strings.Contains(doc, `<div class="item-prompt">Rate yourself.`) {
		t.Error("screener box should carry no item number")
	}
	info := Screener(ch)
	if info == nil || info.ItemID != "u0-s1" || len(info.Options) != 2 ||
		info.Intro != "intro" {
		t.Fatalf("Screener() = %+v", info)
	}
	if Screener(&render.Chapter{HTML: "x"}) != nil {
		t.Error("non-screener chapter must yield nil")
	}
	for _, want := range []string{`<span class="item-num">1.</span>Absolute novice`,
		`<span class="item-num">2.</span>Expert`} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

// A renderer that is off answers at once, without looking for a browser.
func TestRendererOffRefusesWithoutABrowser(t *testing.T) {
	r := &Renderer{Off: true, ChromePath: "/definitely/not/chrome", CacheDir: t.TempDir()}
	_, err := r.renderShared("x", "<html></html>")
	if !errors.Is(err, ErrRenderingOff) {
		t.Fatalf("got %v", err)
	}
}

// The print shell sets only the spans the server takes for math. KaTeX's
// auto-render with `$` as a delimiter paired "about $33K MRR" with "at
// $74.8K MRR" into one equation; the shell now renders backslash
// delimiters only, and the content's spans are rewritten to them.
func TestShellSetsOnlyServerMath(t *testing.T) {
	r := &Renderer{KatexDir: "k", CacheDir: "c"}
	ch := &render.Chapter{
		Unit:  "u1",
		Title: "Dollars",
		HTML:  `<p>about $33K MRR of customers (Nous at $74.8K MRR) and $x^2$.</p>`,
	}
	doc := r.wrap(ch)
	if !strings.Contains(doc, `about $33K MRR of customers (Nous at $74.8K MRR) and \(x^2\).`) {
		t.Errorf("dollar amounts are prose and the span is backslash-delimited; got:\n%s", doc)
	}
	if strings.Contains(doc, `left: "$",`) {
		t.Errorf("the shell still offers `$` as a delimiter, which pairs any two dollars")
	}
}
