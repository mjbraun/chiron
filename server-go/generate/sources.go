package generate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mjbraun/chiron/server/llm"
	"github.com/mjbraun/chiron/server/roles"
	"github.com/mjbraun/chiron/server/sources"
)

// UnitSource names one section of one source a unit is built from.
type UnitSource struct {
	Source  string `json:"source" yaml:"source"`
	Locator string `json:"locator" yaml:"locator"`
	// Role is spine or interleave, as the planner assigned it.
	Role string `json:"role" yaml:"role"`
}

// chosen is a source the book is built from, with its table of contents.
type chosen struct {
	ID         string            `yaml:"id"`
	Title      string            `yaml:"title"`
	Authors    []string          `yaml:"authors"`
	Licence    string            `yaml:"licence"`
	LicenceURL string            `yaml:"licence_url"`
	Verdict    sources.Verdict   `yaml:"verdict"`
	Role       string            `yaml:"role"`
	Voice      string            `yaml:"voice,omitempty"`
	Sections   []sources.Section `yaml:"sections,omitempty"`
	Note       string            `yaml:"note,omitempty"`
	// Fetch, Exercises and Origin let a source the finder chose be fetched
	// on a rerun, when the index never listed it.
	Fetch     sources.Recipe `yaml:"fetch,omitempty"`
	Exercises string         `yaml:"exercises,omitempty"`
	Origin    string         `yaml:"origin,omitempty"`
	src       *sources.Source
}

// Phrases a brief uses to name its sources: "starting from X", "based on
// X", "interleaving Y", "interleave content from Y". A name runs from the
// phrase to the next such phrase or the next comma, semicolon or full
// stop; a trailing "and" or "with", a "by <author>", and quotes are
// stripped.
var namedPhrase = regexp.MustCompile(`(?i)\b(?:starting from|start from|based on|adapted from|built from|interleaving(?: content from| chapters from)?|interleave(?: content from)?|weaving in|mixed with)\s+`)

// NamedInBrief pulls the source names a brief states in prose.
func NamedInBrief(brief string) []string {
	matches := namedPhrase.FindAllStringIndex(brief, -1)
	var out []string
	seen := map[string]bool{}
	for i, m := range matches {
		start := m[1]
		end := len(brief)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		if stop := sentenceStop(brief[start:end]); stop >= 0 {
			end = start + stop
		}
		name := strings.TrimSpace(brief[start:end])
		lower := strings.ToLower(name)
		for _, suffix := range []string{" and", " with", " then"} {
			if strings.HasSuffix(lower, suffix) {
				name = strings.TrimSpace(name[:len(name)-len(suffix)])
				lower = strings.ToLower(name)
			}
		}
		if strings.HasPrefix(lower, "the ") {
			name = name[4:]
		}
		// "X by Y" names the book; the author is not the title.
		if at := strings.Index(strings.ToLower(name), " by "); at > 0 {
			name = name[:at]
		}
		name = strings.Trim(strings.TrimSpace(name), "\"'“”‘’")
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	return out
}

// sentenceStop is the first comma or semicolon, or a full stop that ends
// a sentence (followed by space or the end) rather than one inside "18.05".
func sentenceStop(s string) int {
	for i, r := range s {
		switch r {
		case ',', ';':
			return i
		case '.':
			if i+1 >= len(s) || s[i+1] == ' ' || s[i+1] == '\n' {
				return i
			}
		}
	}
	return -1
}

// resolveSources turns names (explicit ones first, then any the brief
// states) into sources from the index, with their tables of contents.
// The first is the spine, the rest interleave. Names the index does not
// know are recorded, not fatal: the book is then written without them.
func (g *Generator) resolveSources(ctx context.Context, named []string, brief string) ([]chosen, []string) {
	if g.Index == nil {
		return nil, nil
	}
	names := append([]string{}, named...)
	if len(names) == 0 {
		names = NamedInBrief(brief)
	}
	var out []chosen
	var unknown []string
	seen := map[string]bool{}
	for _, name := range names {
		s := g.Index.ByTitle(name)
		if s == nil {
			unknown = append(unknown, name)
			continue
		}
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		role := "interleave"
		if len(out) == 0 {
			role = "spine"
		}
		c := chosen{ID: s.ID, Title: s.Title, Authors: s.Authors, Licence: s.Licence.Name, LicenceURL: s.Licence.URL,
			Verdict: s.Verdict, Role: role, Voice: s.Voice, Fetch: s.Fetch, Exercises: s.Exercises, Origin: "index", src: s}
		switch {
		case s.Verdict == sources.Restricted:
			c.Note = "restricted: cited by title only, never fetched"
		case s.Verdict == sources.QuoteOnly:
			c.Note = "quotation only: a reference for the planner, not material for the author"
		case s.Fetch.Kind == "":
			c.Note = "no fetch recipe yet: the planner may follow its shape, the author writes from scratch"
		case g.Fetch == nil:
			c.Note = "no fetcher configured"
		default:
			secs, err := g.Fetch.Contents(ctx, s)
			if err != nil {
				c.Note = "contents could not be listed: " + err.Error()
			} else {
				if len(secs) > 240 {
					secs = secs[:240]
				}
				c.Sections = secs
			}
		}
		out = append(out, c)
	}
	return out, unknown
}

// sourcesBlock is what the planner reads about the chosen sources.
func sourcesBlock(chosen []chosen) string {
	if len(chosen) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("SOURCES the book is built from (the first is the spine; the rest interleave):\n")
	for _, c := range chosen {
		fmt.Fprintf(&b, "\n[%s] %s: %q by %s (%s; verdict %s)", c.Role, c.ID, c.Title, strings.Join(c.Authors, ", "), c.Licence, c.Verdict)
		if c.Voice != "" {
			fmt.Fprintf(&b, "\n  voice: %s", c.Voice)
		}
		if c.Note != "" {
			fmt.Fprintf(&b, "\n  note: %s", c.Note)
		}
		if len(c.Sections) > 0 {
			b.WriteString("\n  sections (locator | title):")
			for _, s := range c.Sections {
				fmt.Fprintf(&b, "\n    %s | %s", s.Locator, s.Title)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

const sourcesPlanRule = `

With sources given: follow the spine's order of chapters unless the brief or the authoring contract's ordering rules say otherwise, and for every unit list under ` + "`sources`" + ` the spine sections it covers and the interleave sections to weave in, each by its locator exactly as listed and with its role. A unit the sources do not cover gets an empty list. Never list a section of a quotation-only or restricted source. A unit's title and notes must describe the sections it lists, because the author writes from those sections: when the brief names an idea that lives in another section (a worked problem, an example), list that section too rather than titling the unit after material it will not have.`

func (g *Generator) writeSources(chosen []chosen, unknown, references []string) error {
	if len(chosen) == 0 && len(unknown) == 0 && len(references) == 0 {
		return nil
	}
	doc := map[string]any{"sources": chosen}
	if len(unknown) > 0 {
		doc["unresolved"] = unknown
	}
	if len(references) > 0 {
		doc["references"] = references
	}
	return writeYAML(filepath.Join(g.OutDir, "sources.yaml"), doc)
}

// material fetches a unit's sections and writes each beside the unit with
// its provenance, returning the block the author reads and the provenance
// list for the unit's front matter. Quotation-only chunks are not
// material; they are named for the author to restate.
func (g *Generator) material(ctx context.Context, dir string, u unitPlan) (string, []sources.Provenance, error) {
	if len(u.Sources) == 0 || g.Fetch == nil {
		return "", nil, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "sources"), 0o755); err != nil {
		return "", nil, err
	}
	var chunks []*sources.Chunk
	var refs []string
	for _, us := range u.Sources {
		s := g.source(us.Source)
		if s == nil {
			continue
		}
		if s.Verdict != sources.Adaptable || s.Fetch.Kind == "" {
			refs = append(refs, fmt.Sprintf("%s (%s), %s", s.Title, strings.Join(s.Authors, ", "), us.Locator))
			continue
		}
		ch, err := g.Fetch.Fetch(ctx, s, us.Locator)
		if err != nil {
			refs = append(refs, fmt.Sprintf("%s, %s (could not be fetched: %v)", s.Title, us.Locator, err))
			continue
		}
		if ch.Provenance.Verdict != sources.Adaptable {
			refs = append(refs, fmt.Sprintf("%s, %s (%s)", s.Title, us.Locator, ch.Provenance.Note))
			continue
		}
		base := fmt.Sprintf("%02d-%s-%s", len(chunks)+1, s.ID, slugOf(us.Locator))
		if err := os.WriteFile(filepath.Join(dir, "sources", base+".md"), []byte(ch.Markdown), 0o644); err != nil {
			return "", nil, err
		}
		if err := writeYAML(filepath.Join(dir, "sources", base+".provenance.yaml"), ch.Provenance); err != nil {
			return "", nil, err
		}
		chunks = append(chunks, ch)
	}
	if len(chunks) == 0 && len(refs) == 0 {
		return "", nil, nil
	}
	// The author's context is finite: share the budget across the chunks.
	const budget = 48000
	total := 0
	for _, ch := range chunks {
		total += len(ch.Markdown)
	}
	var b strings.Builder
	b.WriteString("MATERIAL TO ADAPT (open text the reader is entitled to have rewritten for them; keep its voice, its worked examples and its order of ideas where the contract's ordering rules allow; weave the interleave sections in where they belong; do not reproduce figures or credited quotations; every adapted chunk is named in the unit's front matter for attribution):\n")
	var provs []sources.Provenance
	for _, ch := range chunks {
		text := ch.Markdown
		if total > budget {
			keep := len(text) * budget / total
			if keep < len(text) {
				text = text[:keep] + "\n\n[... the rest of this section was left out for length ...]\n"
			}
		}
		role := "spine"
		for _, us := range u.Sources {
			if us.Source == ch.Source && us.Locator == ch.Locator && us.Role != "" {
				role = us.Role
			}
		}
		fmt.Fprintf(&b, "\n=== %s: %s, %q (%s; %s) ===\n%s\n", role, ch.Provenance.Title, ch.Title, ch.Provenance.Licence, ch.Provenance.URL, text)
		provs = append(provs, ch.Provenance)
	}
	if len(refs) > 0 {
		b.WriteString("\nREFERENCES to restate in your own words, never to copy:\n")
		for _, r := range refs {
			b.WriteString("- " + r + "\n")
		}
	}
	return b.String(), provs, nil
}

var slugBad = regexp.MustCompile(`[^a-z0-9]+`)

func slugOf(s string) string {
	out := strings.Trim(slugBad.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(out) > 60 {
		out = out[:60]
	}
	if out == "" {
		out = "section"
	}
	return out
}

// withSources adds a `sources:` list to canon.md's front matter, or makes
// front matter when the author wrote none, so attribution is recorded by
// the pipeline rather than left to the model.
func withSources(canon string, provs []sources.Provenance) string {
	if len(provs) == 0 {
		return canon
	}
	entries := make([]map[string]any, 0, len(provs))
	for _, p := range provs {
		entries = append(entries, map[string]any{
			"source": p.Source, "title": p.Title, "authors": p.Authors,
			"licence": p.Licence, "licence_url": p.LicenceURL, "url": p.URL, "fetched": p.Fetched,
		})
	}
	block, err := yaml.Marshal(map[string]any{"sources": entries})
	if err != nil {
		return canon
	}
	text := strings.TrimRight(string(block), "\n") + "\n"
	if strings.HasPrefix(canon, "---\n") {
		if end := strings.Index(canon[4:], "\n---\n"); end >= 0 {
			front := canon[4 : 4+end+1]
			// The author sometimes writes a sources list of its own; one key
			// is the pipeline's, so the author's goes.
			front = dropTopLevelKey(front, "sources")
			return "---\n" + front + text + canon[4+end+1:]
		}
	}
	return "---\n" + text + "---\n" + canon
}

// dropTopLevelKey removes a top-level YAML key and its indented block.
func dropTopLevelKey(front, key string) string {
	lines := strings.Split(front, "\n")
	var out []string
	skipping := false
	for _, l := range lines {
		if strings.HasPrefix(l, key+":") {
			skipping = true
			continue
		}
		if skipping && (l == "" || strings.HasPrefix(l, " ") || strings.HasPrefix(l, "-")) {
			continue
		}
		skipping = false
		out = append(out, l)
	}
	joined := strings.Join(out, "\n")
	// The key's block may have been the last thing in the front matter,
	// taking the closing newline with it; the next key must start a line.
	if strings.HasSuffix(front, "\n") && !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	return joined
}

// importItems turns the exercises of the unit's adaptable sources into
// bank items, kept in imported.yaml beside the unit so a rerun reuses them.
func (g *Generator) importItems(ctx context.Context, chain llm.Chain, dir string, u unitPlan, unitYAML []byte, spec, bank string) (string, error) {
	if len(u.Sources) == 0 || g.Fetch == nil {
		return "", nil
	}
	path := filepath.Join(dir, "imported.yaml")
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		return string(existing), nil
	}
	var imported []roles.Imported
	for _, us := range u.Sources {
		s := g.source(us.Source)
		if s == nil || s.Verdict != sources.Adaptable || s.Fetch.Kind == "" {
			continue
		}
		ex, err := g.Fetch.Exercises(ctx, s, us.Locator)
		if err != nil || len(ex) == 0 {
			continue
		}
		imported = append(imported, roles.Imported{Source: s.ID, Locator: us.Locator, Title: s.Title, Exercises: ex})
	}
	if len(imported) == 0 {
		return "", nil
	}
	items, err := roles.ImportItems(chain, u.ID, string(unitYAML), spec, bank, imported)
	if err != nil || items == "" {
		return "", err
	}
	return items, os.WriteFile(path, []byte(items), 0o644)
}
