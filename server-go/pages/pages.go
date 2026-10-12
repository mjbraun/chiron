// Package pages renders chapter HTML into e-ink page images.
//
// The reMarkable client cannot render HTML or LaTeX; it displays page images
// and captures ink. This package turns a chapter (the same HTML fragment the
// iPad renders in a WKWebView) into a stack of PNGs at the Paper Pro's native
// portrait resolution, via headless Chrome print-to-PDF (which runs KaTeX)
// and pdftoppm.
//
// Layout follows rmpp/design/SPEC.md: a fixed page grid (running head, folio,
// 1400x1960 content area), item boxes at two fixed heights with a reserved
// control strip, and pagination done by an in-page script that packs prose
// into explicit page containers - so every page's geometry is known and
// enforced by the server, and the interesting parts (item rects, strips) are
// published to the client through the pages meta.
package pages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mjbraun/chiron/server/corpus"
	"github.com/mjbraun/chiron/server/render"
)

// ErrRenderingOff is every render's answer while Renderer.Off is set.
var ErrRenderingOff = errors.New("page rendering is switched off (CHIRON_RENDER=0)")

// reMarkable Paper Pro portrait, native pixels (11.8", 229 ppi).
const (
	PageW = 1620
	PageH = 2160
)

// The page grid and the item-box geometry contract (SPEC §1, §2.1). Boxes
// come in exactly two heights and pack top-aligned into the content area;
// the server both enforces the geometry (explicit page containers and box
// heights) and publishes it (per-item rect + strip in the pages meta), which
// is what lets the client place controls INSIDE each box and assign ink to
// items without extracting anything from the render.
const (
	MarginX  = 110  // side margins
	MarginY  = 100  // top/bottom margins
	ContentW = 1400 // PageW - 2*MarginX
	ContentH = 1960 // PageH - 2*MarginY

	BoxShort = 640 // MCQ <=4 options, short constructed
	BoxTall  = 970 // constructed with ink work, MCQ >=5 options, long stems
	BoxGap   = 20  // white between stacked boxes

	StripConstructed = 120 // control strip above the box's inner bottom edge
	StripMCQ         = 196 // two control rows (letters, then confidence)

	// PretestNoteH is reserved at the top of the first pretest page for the
	// framing note - pretest items are designed to be failed, and without
	// the framing they read as a test to pass.
	PretestNoteH = 160
)

// Bump when the wrapper HTML/CSS changes so cached renders invalidate.
const styleVersion = "v19"

type Renderer struct {
	// Off refuses every render with ErrRenderingOff. The iPad reads the
	// chapter as HTML and never asks for page images; on a small box that
	// also serves the book, five Chromium processes per placement is what
	// takes it down.
	Off bool
	// ChromePath overrides Chrome discovery; empty means look in the
	// usual places.
	ChromePath string
	// Renders of DISTINCT chapters run concurrently (the calibration
	// prerender walks five level variants while the learner reads the
	// screener), but identical renders - eager plus on-demand for the same
	// chapter - collapse onto one in-flight job so they cannot trample the
	// same cache directory.
	mu       sync.Mutex
	inflight map[string]*renderJob
	// KatexDir holds katex.min.css/js and contrib/auto-render.min.js
	// (the same assets the iPad bundles).
	KatexDir string
	// FontsDir holds the bundled page faces (Source Serif 4). Empty falls
	// back to system Georgia - renders still work, off-spec.
	FontsDir string
	// CacheDir receives one subdirectory per rendered chapter, keyed by
	// content hash.
	CacheDir string
	// PrintLayout renders answer scaffolding ON the page - confidence
	// pills, IDK tick rows, MCQ tick squares - for clients that are real
	// paper (the rmapi flow). The default interactive layout leaves those
	// to the client's own controls and letters the MCQ options instead.
	PrintLayout bool
}

type Result struct {
	Dir   string `json:"-"`
	Hash  string `json:"hash"`
	Count int    `json:"count"`
}

func (r Result) PagePath(n int) string {
	return filepath.Join(r.Dir, fmt.Sprintf("page-%03d.png", n))
}

// ItemPage is the published geometry for one item (SPEC §9): which page its
// box is on, the box rect in page pixels, and the strip height reserved for
// native controls above the box's inner bottom edge. Pretest items open the
// stack, check items close it; every question page is an explicit container,
// so the mapping is arithmetic. Ink drawn inside an item's rect (above the
// strip) belongs to that item.
type ItemPage struct {
	Item    string `json:"item"`
	Kind    string `json:"kind"`
	Check   string `json:"check,omitempty"`
	Options int    `json:"options,omitempty"`
	Page    int    `json:"page"`
	Rect    [4]int `json:"rect"`  // x, y, w, h in page px
	Strip   int    `json:"strip"` // px above the inner bottom edge
}

func stripH(it render.ClientItem) int {
	if it.Kind == "mcq" {
		return StripMCQ
	}
	return StripConstructed
}

// boxH picks between the two fixed heights. The estimate is deliberately
// conservative (boxes clip overflow): prompt lines at ~62 chars each,
// option rows at ~68.
func boxH(it render.ClientItem) int {
	promptPx := (1 + len(it.Prompt)/62) * 50
	if it.Kind == "mcq" {
		if len(it.Options) >= 5 {
			return BoxTall
		}
		rows := 0
		for _, o := range it.Options {
			rows += 1 + len(o.Text)/68
		}
		need := 28 + promptPx + 14 + rows*46 + (len(it.Options)-1)*14 + StripMCQ + 1
		if need > BoxShort {
			return BoxTall
		}
		return BoxShort
	}
	if it.Check == "llm" {
		// Prose answers get real ink room.
		return BoxTall
	}
	// Short constructed: prompt, dotted rule, modest ink room.
	if 28+promptPx+22+240+StripConstructed+1 > BoxShort {
		return BoxTall
	}
	return BoxShort
}

// packItems fills pages in order: a new page starts when the next box
// does not fit under the content height. lead is height already consumed
// at the top of the first page (the pretest framing note).
func packItems(items []render.ClientItem, lead int) [][]render.ClientItem {
	var pages [][]render.ClientItem
	var cur []render.ClientItem
	used := lead
	for _, it := range items {
		h := boxH(it)
		if len(cur) > 0 && used+BoxGap+h > ContentH {
			pages = append(pages, cur)
			cur, used = nil, 0
		}
		if len(cur) > 0 {
			used += BoxGap
		}
		cur = append(cur, it)
		used += h
	}
	if len(cur) > 0 {
		pages = append(pages, cur)
	}
	return pages
}

func ItemPages(ch *render.Chapter, pageCount int) []ItemPage {
	regions := func(groups [][]render.ClientItem, firstPage int, fromEnd bool, lead int) []ItemPage {
		var out []ItemPage
		base := firstPage
		if fromEnd {
			base = pageCount - len(groups)
		}
		for p, group := range groups {
			y := MarginY
			if p == 0 {
				y += lead
			}
			for _, it := range group {
				h := boxH(it)
				out = append(out, ItemPage{
					Item: it.ID, Kind: it.Kind, Check: it.Check,
					Options: len(it.Options), Page: base + p,
					Rect:  [4]int{MarginX, y, ContentW, h},
					Strip: stripH(it),
				})
				y += h + BoxGap
			}
		}
		return out
	}
	var out []ItemPage
	out = append(out, regions(packItems(ch.Pretest, PretestNoteH), 0, false, PretestNoteH)...)
	out = append(out, regions(packItems(ch.Check, 0), 0, true, 0)...)
	return out
}

func (r *Renderer) chrome() string {
	if r.ChromePath != "" {
		return r.ChromePath
	}
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/usr/bin/chromium",
		"/usr/bin/google-chrome",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if p, err := exec.LookPath("chromium"); err == nil {
		return p
	}
	return ""
}

type renderJob struct {
	done chan struct{}
	res  Result
	err  error
}

// Render produces the page stack for a chapter, reusing a previous render
// of identical content and joining an in-flight render of the same content.
func (r *Renderer) Render(ch *render.Chapter) (Result, error) {
	return r.renderShared(ch.Unit, r.wrap(ch))
}

// renderShared is the cached, deduplicated path under every page render:
// chapters and results docs both land here, keyed by content hash.
func (r *Renderer) renderShared(name, doc string) (Result, error) {
	if r.Off {
		return Result{}, ErrRenderingOff
	}
	sum := sha256.Sum256([]byte(doc))
	key := hex.EncodeToString(sum[:8])

	r.mu.Lock()
	if r.inflight == nil {
		r.inflight = map[string]*renderJob{}
	}
	if job, ok := r.inflight[key]; ok {
		r.mu.Unlock()
		<-job.done
		return job.res, job.err
	}
	job := &renderJob{done: make(chan struct{})}
	r.inflight[key] = job
	r.mu.Unlock()

	job.res, job.err = r.renderDoc(name, doc, key)
	close(job.done)
	r.mu.Lock()
	delete(r.inflight, key)
	r.mu.Unlock()
	return job.res, job.err
}

func (r *Renderer) renderDoc(name, doc, hash string) (Result, error) {
	dir := filepath.Join(r.CacheDir, name+"-"+hash)
	res := Result{Dir: dir, Hash: hash}

	if meta, err := os.ReadFile(filepath.Join(dir, "meta.json")); err == nil {
		if json.Unmarshal(meta, &res) == nil && res.Count > 0 {
			res.Dir = dir
			return res, nil
		}
	}

	chrome := r.chrome()
	if chrome == "" {
		return res, fmt.Errorf("no chrome/chromium found for page rendering")
	}
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return res, fmt.Errorf("pdftoppm not installed: %w", err)
	}

	work, err := os.MkdirTemp("", "chiron-pages-*")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(work)

	htmlPath := filepath.Join(work, "chapter.html")
	if err := os.WriteFile(htmlPath, []byte(doc), 0o644); err != nil {
		return res, err
	}
	pdfPath := filepath.Join(work, "chapter.pdf")

	// virtual-time-budget lets KaTeX, font loading, and the paginator
	// finish before printing. Each render gets its own profile dir -
	// concurrent Chromes sharing the default profile deadlock on its
	// singleton lock - and a hard deadline, so a wedged Chrome can never
	// jam the render singleflight forever.
	chromeCtx, chromeCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer chromeCancel()
	cmd := exec.CommandContext(chromeCtx, chrome,
		"--headless=new", "--disable-gpu", "--no-first-run",
		"--disable-component-update",
		// Fresh profiles hang on credential stores without these: the
		// macOS Keychain prompt (invisible under headless) and the Linux
		// keyring equivalent.
		"--use-mock-keychain", "--password-store=basic",
		"--user-data-dir="+filepath.Join(work, "chrome-profile"),
		"--virtual-time-budget=15000",
		"--no-pdf-header-footer",
		"--print-to-pdf="+pdfPath,
		"file://"+htmlPath)
	// Output goes to a file, not pipes: a fresh profile makes Chrome fork
	// updater/crashpad children that inherit pipes and outlive the print,
	// which would block CombinedOutput until the deadline.
	chromeLog := filepath.Join(work, "chrome.log")
	if f, err := os.Create(chromeLog); err == nil {
		cmd.Stdout, cmd.Stderr = f, f
		defer f.Close()
	}
	chromeFail := func(why error) error {
		out, _ := os.ReadFile(chromeLog)
		if len(out) > 400 {
			out = out[len(out)-400:]
		}
		return fmt.Errorf("chrome print: %w: %s", why, out)
	}
	if err := cmd.Start(); err != nil {
		return res, chromeFail(err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	// Wait for the PDF, not the process: branded Chrome with a fresh
	// profile finishes the print and then lingers (updater machinery on
	// macOS), so process exit is not the completion signal. The artifact
	// settling - or a clean exit - is.
	var lastSize int64 = -1
	settled, exited := false, false
	for !settled {
		if st, err := os.Stat(pdfPath); err == nil && st.Size() > 0 && st.Size() == lastSize {
			settled = true
			break
		} else if err == nil {
			lastSize = st.Size()
		}
		if exited {
			if _, err := os.Stat(pdfPath); err != nil {
				return res, chromeFail(fmt.Errorf("no pdf produced"))
			}
			settled = true
			break
		}
		select {
		case <-waitCh:
			exited = true
		case <-chromeCtx.Done():
			return res, chromeFail(fmt.Errorf("deadline exceeded"))
		case <-time.After(200 * time.Millisecond):
		}
	}
	chromeCancel()

	// Scaling to the device dimensions directly avoids the off-by-one that
	// DPI-based sizing hits when the PDF page size rounds to whole points.
	ppmCtx, ppmCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer ppmCancel()
	cmd = exec.CommandContext(ppmCtx, "pdftoppm", "-png",
		"-scale-to-x", fmt.Sprint(PageW), "-scale-to-y", fmt.Sprint(PageH),
		pdfPath, filepath.Join(work, "page"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return res, fmt.Errorf("pdftoppm: %w: %s", err, out)
	}

	rendered, err := filepath.Glob(filepath.Join(work, "page-*.png"))
	if err != nil || len(rendered) == 0 {
		return res, fmt.Errorf("no pages produced (%v)", err)
	}
	sort.Strings(rendered)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	for i, p := range rendered {
		dst := filepath.Join(dir, fmt.Sprintf("page-%03d.png", i))
		if err := os.Rename(p, dst); err != nil {
			// Rename fails across filesystems (tmp vs cache); fall back
			// to a copy.
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return res, rerr
			}
			if werr := os.WriteFile(dst, data, 0o644); werr != nil {
				return res, werr
			}
		}
		_ = i
	}
	res.Count = len(rendered)
	meta, _ := json.Marshal(res)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o644); err != nil {
		return res, err
	}
	return res, nil
}

// ScreenerInfo describes a screener-only calibration chapter so an
// interactive client can render the placement step natively - the question
// box IS the UI there, with the levels as buttons.
type ScreenerInfo struct {
	ItemID  string   `json:"item_id"`
	Intro   string   `json:"intro"`
	Prompt  string   `json:"prompt"`
	Options []string `json:"options"`
}

var tagStrip = regexp.MustCompile(`<[^>]+>`)

func Screener(ch *render.Chapter) *ScreenerInfo {
	if !ch.Calibration || len(ch.Pretest) != 0 || len(ch.Check) != 1 ||
		ch.Check[0].Check != "screener" {
		return nil
	}
	it := ch.Check[0]
	intro := strings.TrimSpace(tagStrip.ReplaceAllString(ch.HTML, ""))
	// The HTML came through markdown escaping; the native client renders
	// plain text, so entities must come back out.
	for ent, lit := range map[string]string{
		"&quot;": "\"", "&#39;": "'", "&lt;": "<", "&gt;": ">", "&amp;": "&",
	} {
		intro = strings.ReplaceAll(intro, ent, lit)
	}
	info := &ScreenerInfo{
		ItemID: it.ID,
		Intro:  intro,
		Prompt: it.Prompt,
	}
	for _, o := range it.Options {
		info.Options = append(info.Options, o.Text)
	}
	return info
}

var beatDiv = regexp.MustCompile(`<div class="beat" data-beat-id="([^"]+)"></div>`)

// injectBeats replaces the client-side beat placeholders with printable
// prompt boxes: the beat's prompt plus writing room, so a page is something
// the learner can work directly.
func injectBeats(doc string, beats []corpus.Beat) string {
	byID := map[string]corpus.Beat{}
	for _, b := range beats {
		byID[b.ID] = b
	}
	return beatDiv.ReplaceAllStringFunc(doc, func(m string) string {
		id := beatDiv.FindStringSubmatch(m)[1]
		b, ok := byID[id]
		if !ok {
			return ""
		}
		label := "Work this before reading on"
		if b.Type == "self-explain" {
			label = "Explain in your own words"
		}
		// A choice beat prints its options as lettered rows to mark, with a
		// short ink strip; a prose or compute beat prints writing room.
		if len(b.Options) > 0 {
			var rows strings.Builder
			for i, o := range b.Options {
				fmt.Fprintf(&rows, `<div class="beat-option"><span class="letter">%c</span> %s</div>`, 'A'+i, o.Text)
			}
			return `<div class="beat-box"><div class="beat-label">` +
				html.EscapeString(label) + `</div><div class="beat-prompt">` +
				b.Prompt + `</div><div class="beat-options">` + rows.String() + `</div><div class="beat-ink short"></div></div>`
		}
		return `<div class="beat-box"><div class="beat-label">` +
			html.EscapeString(label) + `</div><div class="beat-prompt">` +
			b.Prompt + `</div><div class="beat-ink"></div></div>`
	})
}

// itemsSection renders pretest or check items as workable paper pages:
// prompt, ink room, and a confidence scale to circle. Reveals (reference
// answers, rubrics) deliberately never reach the page - the check is
// closed-book, and answers come back with the next exchange.
// itemsSection numbers its boxes from first so the chapter carries one
// continuous sequence (pretest, then check) - the same numbers the results
// entries cite. pretest sections carry their framing on the page: the note
// in a reserved block above the first box, and a page class the paginator
// turns into the BEFORE YOU READ running head.
// scaffold is the printed stand-in for the native controls (SPEC §8):
// checkboxes on the same strip geometry - IDK left and confidence right for
// constructed answers; MCQ letters left with IDK right, confidence below.
func scaffold(it render.ClientItem) string {
	conf := `<div>☐ unsure&ensp;&ensp;☐ shaky&ensp;&ensp;☐ confident&ensp;&ensp;☐ sure</div>`
	idk := `<div>☐ I don't know</div>`
	if it.Kind != "mcq" {
		return `<div class="sc-row">` + idk + conf + `</div>`
	}
	var letters []string
	for i := range it.Options {
		letters = append(letters, fmt.Sprintf("☐ %c", 'A'+i))
	}
	return `<div class="sc-row"><div>` + strings.Join(letters, "&ensp;&ensp;") + `</div>` + idk + `</div>` +
		`<div class="sc-row"><div></div>` + conf + `</div>`
}

func itemsSection(note string, items []render.ClientItem, printLayout, inline bool, first int, pretest bool) string {
	if len(items) == 0 {
		return ""
	}
	var b []string
	class := "items-section"
	if inline {
		class += " items-inline"
	}
	b = append(b, `<section class="`+class+`">`)
	num := first - 1
	renderItem := func(it render.ClientItem, style string) []string {
		num++
		i := num - 1
		var b []string
		b = append(b, fmt.Sprintf(`<div class="item-box" data-item-id=%q%s>`, it.ID, style))
		numSpan := fmt.Sprintf(`<span class="item-num">%d.</span>`, i+1)
		if it.Check == "screener" {
			numSpan = ""
		}
		b = append(b, `<div class="item-prompt">`+numSpan+
			html.EscapeString(it.Prompt)+`</div>`)
		switch {
		case it.Kind == "mcq":
			// Lettered options; the client's letter buttons and the printed
			// ☐ A ☐ B row both key off the same letters.
			b = append(b, `<ul class="mcq">`)
			for oi, o := range it.Options {
				b = append(b, fmt.Sprintf(`<li><span class="mcq-letter">%c.</span><span>`, 'A'+oi)+
					html.EscapeString(o.Text)+`</span></li>`)
			}
			b = append(b, `</ul>`)
		case it.Check == "screener":
			b = append(b, `<ul class="screener-list">`)
			for oi, o := range it.Options {
				b = append(b, fmt.Sprintf(`<li><span class="item-num">%d.</span>`, oi+1)+
					html.EscapeString(o.Text)+`</li>`)
			}
			b = append(b, `</ul>`)
		default:
			b = append(b, `<div class="item-ink"></div>`)
		}
		if it.Check != "screener" {
			inner := ""
			if printLayout {
				inner = scaffold(it)
			}
			b = append(b, fmt.Sprintf(`<div class="control-strip" style="height: %dpx">%s</div>`,
				stripH(it), inner))
		}
		b = append(b, `</div>`)
		return b
	}

	if inline {
		// Natural flow for the inline screener box.
		for _, it := range items {
			b = append(b, renderItem(it, "")...)
		}
	} else {
		// Explicit page containers with fixed-height boxes, exactly
		// mirroring the rects published in the meta.
		lead := 0
		class := `qpage`
		if pretest {
			lead = PretestNoteH
			class = `qpage qpage-pretest`
		}
		for gi, group := range packItems(items, lead) {
			b = append(b, `<div class="`+class+`">`)
			if pretest && gi == 0 && note != "" {
				b = append(b, `<div class="qpage-note">`+html.EscapeString(note)+`</div>`)
			}
			for _, it := range group {
				b = append(b, renderItem(it,
					fmt.Sprintf(` style="height: %dpx"`, boxH(it)))...)
			}
			b = append(b, `</div>`)
		}
	}
	b = append(b, `</section>`)
	return strings.Join(b, "\n")
}

// HeadLeft is the left slot of a page's running head: chapter number and
// title for numbered units, the bare title otherwise.
func HeadLeft(unit, title string) string {
	title = strings.ToUpper(title)
	if m := regexp.MustCompile(`^u(\d+)$`).FindStringSubmatch(unit); m != nil && m[1] != "0" {
		return m[1] + " · " + title
	}
	return title
}

func runningHead(ch *render.Chapter) string {
	return HeadLeft(ch.Unit, ch.Title)
}

// fontFaces binds the bundled Source Serif 4 files. Page-side native-control
// faces (Source Sans 3) live in the same directory but are the client's
// concern, not the page's.
func (r *Renderer) fontFaces() string {
	if r.FontsDir == "" {
		return ""
	}
	base := "file://" + mustAbs(r.FontsDir)
	face := func(file, weight, style string) string {
		return fmt.Sprintf(`@font-face { font-family: 'Source Serif 4'; src: url('%s/%s'); font-weight: %s; font-style: %s; }`,
			base, file, weight, style)
	}
	return strings.Join([]string{
		face("SourceSerif4-Regular.ttf", "400", "normal"),
		face("SourceSerif4-Semibold.ttf", "600", "normal"),
		face("SourceSerif4-Bold.ttf", "700", "normal"),
		face("SourceSerif4-It.ttf", "400", "italic"),
	}, "\n")
}

// wrap builds the chapter document. Print and interactive share the whole
// page chrome (SPEC §8) - the only divergence is what fills the control
// strips: nothing for the native client, printed checkboxes for paper.
func (r *Renderer) wrap(ch *render.Chapter) string {
	// A screener-only chapter is one question: it belongs on the intro
	// page, under the intro, with no series framing above it.
	screenerOnly := ch.Calibration && len(ch.Pretest) == 0 &&
		len(ch.Check) == 1 && ch.Check[0].Check == "screener"
	body := itemsSection(
		"You are not supposed to know these yet - answering wrong here is part of how the chapter calibrates.",
		ch.Pretest, r.PrintLayout, false, 1, true) +
		injectBeats(ch.HTML, ch.Beats) +
		itemsSection("", ch.Check, r.PrintLayout, screenerOnly,
			1+len(ch.Pretest), false)
	return r.shell(body, chapterCSS(), runningHead(ch), "")
}

// shell wraps page content in the interactive render skeleton: fonts, KaTeX,
// the base typography, the page-container chrome, and the paginator script.
// rhRight fixes the running head's right slot ("RESULTS", "CALIBRATION");
// empty means the dynamic chapter behavior (CHECK on question pages, the
// time whisper on prose).
func (r *Renderer) shell(content, extraCSS, rhLeft, rhRight string) string {
	katex := "file://" + mustAbs(r.KatexDir)
	left, _ := json.Marshal(rhLeft)
	right, _ := json.Marshal(rhRight)
	return `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">
<link rel="stylesheet" href="` + katex + `/katex.min.css">
<script src="` + katex + `/katex.min.js"></script>
<script src="` + katex + `/contrib/auto-render.min.js"></script>
<style>
/* ` + styleVersion + `-interactive */
` + r.fontFaces() + `
@page { size: ` + fmt.Sprint(PageW) + `px ` + fmt.Sprint(PageH) + `px; margin: 0; }
` + baseCSS() + `
` + chromeCSS() + `
` + extraCSS + `
</style></head><body><div id="src">` + render.TeXDelimiters(content) + `</div><div id="pages"></div>
<script>
document.addEventListener("DOMContentLoaded", function() {
  // Backslash delimiters only: the server's math spans were rewritten to
  // them (render.TeXDelimiters), and a "$" delimiter would pair any two
  // dollar amounts in a sentence into one equation.
  renderMathInElement(document.body, {
    delimiters: [{left: "\\[", right: "\\]", display: true},
                 {left: "\\(", right: "\\)", display: false}],
    throwOnError: false
  });
` + paginatorJS(string(left), string(right)) + `
});
</script></body></html>`
}

// baseCSS is the shared typography (SPEC §0.5) for every rendered page.
func baseCSS() string {
	return `html, body { margin: 0; padding: 0; background: white; color: black; }
body { font-family: 'Source Serif 4', Georgia, serif; font-size: 34px; line-height: 53px; }
h1 { font-size: 56px; line-height: 64px; font-weight: 600; margin: 0 0 28px 0; }
h2 { font-size: 44px; line-height: 54px; font-weight: 600; margin: 46px 0 24px 0; page-break-after: avoid; }
p { margin: 0 0 22px 0; max-width: 1220px; text-align: justify; hyphens: auto; -webkit-hyphens: auto; }
table { border-collapse: collapse; margin: 24px 0; page-break-inside: avoid; }
th, td { border: 2px solid #000; padding: 10px 16px; text-align: left; }
code { font-family: Menlo, monospace; font-size: 0.82em; }
pre { border: 2px solid #000; padding: 16px; overflow: hidden; page-break-inside: avoid; font-size: 28px; line-height: 40px; }
blockquote, .planner-note { border-left: 2px solid #000; margin: 24px 0; padding: 8px 0 8px 28px; font-style: italic; font-size: 32px; line-height: 48px; color: #333; }
blockquote p, .planner-note p { font-size: 32px; line-height: 48px; text-align: left; hyphens: none; }
.katex-display { margin: 26px 0; page-break-inside: avoid; }
.beat-box { border: 2px solid #000; margin: 30px 0; padding: 24px 28px 0 28px; page-break-inside: avoid; }
.beat-label { font-size: 22px; line-height: 28px; font-weight: 600; letter-spacing: 0.10em; text-transform: uppercase; color: #444; margin-bottom: 22px; }
.beat-prompt { font-size: 32px; line-height: 48px; }
.beat-ink { border-top: 2px dotted #999; margin-top: 20px; height: 280px; }
.beat-ink.short { height: 120px; }
.beat-options { margin-top: 16px; }
.beat-option { font-size: 30px; line-height: 44px; margin: 10px 0; padding-left: 64px; text-indent: -64px; }
.beat-option .letter { display: inline-block; width: 48px; height: 48px; line-height: 44px; text-align: center; border: 2px solid #000; border-radius: 50%; margin-right: 12px; text-indent: 0; font-weight: 600; }
.item-num { font-weight: 700; margin-right: 14px; }
.item-prompt { font-size: 34px; line-height: 50px; text-align: left; hyphens: none; max-width: none; }
.mcq { list-style: none; padding: 0; margin: 14px 0 0 0; }
.mcq li { display: flex; margin: 0 0 14px 0; font-size: 32px; line-height: 46px; }
.mcq-letter { font-weight: 700; flex: 0 0 44px; }
.screener-list { list-style: none; padding: 0; margin: 18px 0 0 0; }
.screener-list li { margin: 14px 0; }`
}

// chromeCSS is the page-container chrome (SPEC §1): explicit page divs
// filled by the paginator, the content area at (110,100) sized 1400x1960,
// running head in the top margin, centered folio in the bottom margin.
func chromeCSS() string {
	return fmt.Sprintf(`#src { display: none; }
.pg { width: %dpx; height: %dpx; position: relative; overflow: hidden; }
.pg + .pg { page-break-before: always; break-before: page; }
.pg-content { position: absolute; left: %dpx; top: %dpx; width: %dpx; height: %dpx; overflow: hidden; }
.pg-content > :first-child { margin-top: 0; }
.rh { position: absolute; top: 40px; left: %dpx; width: %dpx; height: 24px; line-height: 24px; font-size: 24px; font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase; color: #444; display: flex; justify-content: space-between; }
.rh .whisper { color: #777; }
.folio { position: absolute; top: 2100px; left: %dpx; width: %dpx; height: 26px; line-height: 26px; font-size: 26px; color: #444; text-align: center; }
.split-head { text-align-last: justify; }`,
		PageW, PageH, MarginX, MarginY, ContentW, ContentH,
		MarginX, ContentW, MarginX, ContentW)
}

// chapterCSS pins interactive item boxes to the geometry contract: fixed
// heights, clipped overflow, and the control strip with the shelf rule as
// its top border.
func chapterCSS() string {
	return fmt.Sprintf(`.qpage { height: %dpx; }
.item-box { border: 2px solid #000; box-sizing: border-box; position: relative; padding: 24px 28px 0 28px; overflow: hidden; margin: 0 0 %dpx 0; }
.items-inline .item-box { height: auto; margin: 34px 0; }
.item-ink { border-top: 2px dotted #999; margin-top: 20px; }
.control-strip { position: absolute; left: 0; right: 0; bottom: 0; border-top: 1px solid #000; display: flex; flex-direction: column; justify-content: center; box-sizing: border-box; padding: 0 30px; gap: 16px; }
.sc-row { display: flex; justify-content: space-between; font-size: 26px; line-height: 34px; color: #333; }
.qpage-note { height: %dpx; box-sizing: border-box; padding-bottom: 20px; font-style: italic; font-size: 28px; line-height: 40px; color: #444; overflow: hidden; }`,
		ContentH, BoxGap, PretestNoteH)
}

// paginatorJS packs the rendered flow into explicit page containers after
// KaTeX and the bundled fonts settle (both change metrics; measuring before
// they load would paginate a different document). Question pages (.qpage)
// pass through as-is; prose fills 1960px content areas, paragraphs and lists
// splitting at word/item boundaries. Every page then gets its running head
// (left slot fixed; right slot either the fixed rhRight or the dynamic
// CHECK / remaining-time whisper) and folio.
func paginatorJS(rhLeftJSON, rhRightJSON string) string {
	return `
  var RH_LEFT = ` + rhLeftJSON + `;
  var RH_RIGHT = ` + rhRightJSON + `;
  document.fonts.ready.then(function() {
    var CH = ` + fmt.Sprint(ContentH) + `;
    var src = document.getElementById("src");
    var out = document.getElementById("pages");
    var content = null, kinds = [], contents = [];

    function newPage(kind) {
      var pg = document.createElement("div"); pg.className = "pg";
      content = document.createElement("div"); content.className = "pg-content";
      pg.appendChild(content); out.appendChild(pg);
      kinds.push(kind); contents.push(content);
    }
    function fits() { return content.scrollHeight <= CH; }

    // Split units: paragraphs at word boundaries (inline elements atomic),
    // lists at item boundaries; everything else is unsplittable.
    function units(el) {
      if (el.tagName === "UL" || el.tagName === "OL")
        return Array.prototype.slice.call(el.children);
      if (el.tagName !== "P") return null;
      var us = [];
      Array.prototype.slice.call(el.childNodes).forEach(function(n) {
        if (n.nodeType === 3) {
          n.textContent.split(/(\s+)/).forEach(function(w) {
            if (w !== "") us.push(document.createTextNode(w));
          });
        } else us.push(n);
      });
      return us;
    }

    // Largest unit prefix of el that fits in the current page.
    function maxFit(el, us) {
      var probe = el.cloneNode(false);
      content.replaceChild(probe, el);
      function fitK(k) {
        while (probe.firstChild) probe.removeChild(probe.firstChild);
        for (var i = 0; i < k; i++) probe.appendChild(us[i].cloneNode(true));
        return fits();
      }
      var lo = 0, hi = us.length - 1;
      while (lo < hi) {
        var mid = (lo + hi + 1) >> 1;
        if (fitK(mid)) lo = mid; else hi = mid - 1;
      }
      content.replaceChild(el, probe);
      return lo;
    }

    var queue = [];
    Array.prototype.slice.call(src.children).forEach(function(el) {
      if (el.classList.contains("items-section") && !el.classList.contains("items-inline"))
        Array.prototype.slice.call(el.children).forEach(function(q) { queue.push(q); });
      else queue.push(el);
    });

    while (queue.length) {
      var el = queue.shift();
      if (el.classList && el.classList.contains("qpage")) {
        newPage(el.classList.contains("qpage-pretest") ? "pretest" : "check");
        content.appendChild(el); content = null;
        continue;
      }
      if (!content) newPage("prose");
      content.appendChild(el);
      if (fits()) continue;

      var us = units(el);
      var k = us && us.length > 1 ? maxFit(el, us) : 0;
      if (k > 0) {
        // Head stays (justified through its last line - it is not the
        // paragraph's end), tail reflows onto the next page.
        var head = el.cloneNode(false), tail = el.cloneNode(false);
        for (var i = 0; i < us.length; i++) (i < k ? head : tail).appendChild(us[i]);
        if (head.tagName === "P") head.classList.add("split-head");
        content.replaceChild(head, el);
        queue.unshift(tail);
        content = null;
        continue;
      }
      // Unsplittable: move whole to a fresh page, pulling a stranded
      // heading along. A block too tall even alone stays and clips.
      content.removeChild(el);
      if (content.children.length === 0 || el.dataset.retried) {
        content.appendChild(el);
        content = null;
        continue;
      }
      var last = content.lastElementChild;
      if (last && /^H[12]$/.test(last.tagName)) {
        content.removeChild(last);
        queue.unshift(el); queue.unshift(last);
      } else {
        queue.unshift(el);
      }
      el.dataset.retried = "1";
      content = null;
    }

    // Chrome: running head and folio on every page. The whisper is the
    // remaining reading estimate from this page on (~200 wpm).
    var N = out.children.length;
    var wordsLeft = [], acc = 0;
    for (var i = N - 1; i >= 0; i--) {
      if (kinds[i] === "prose")
        acc += (contents[i].textContent.match(/\S+/g) || []).length;
      wordsLeft[i] = acc;
    }
    for (var i = 0; i < N; i++) {
      var pg = out.children[i];
      var rh = document.createElement("div"); rh.className = "rh";
      var l = document.createElement("span"); l.textContent = RH_LEFT;
      var r = document.createElement("span");
      if (RH_RIGHT) {
        r.textContent = RH_RIGHT;
      } else if (kinds[i] === "pretest") {
        r.textContent = "BEFORE YOU READ";
      } else if (kinds[i] === "check") {
        r.textContent = "CHECK";
      } else {
        r.className = "whisper";
        r.textContent = "≈ " + Math.max(1, Math.round(wordsLeft[i] / 200)) + " MIN LEFT";
      }
      rh.appendChild(l); rh.appendChild(r);
      var folio = document.createElement("div"); folio.className = "folio";
      folio.textContent = (i + 1) + " / " + N;
      pg.appendChild(rh); pg.appendChild(folio);
    }
    src.parentNode.removeChild(src);
  });`
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}
