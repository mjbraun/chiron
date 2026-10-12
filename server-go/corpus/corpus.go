// Package corpus loads and parses an authored subject.
//
// A unit directory contains canon.md (prose + fenced ```beat blocks), depths/
// variants keyed by identical section headings, questions.yaml, and
// misconceptions.yaml. Canon is parsed into an ordered list of segments grouped
// by section heading, so a depth variant can be swapped in per section.
package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	beatFence   = regexp.MustCompile("(?ms)^```beat[ \t]*$(.*?)^```[ \t]*$")
	frontMatter = regexp.MustCompile(`(?s)\A---[ \t]*\n(.*?)\n---[ \t]*\n`)
	sectionHead = regexp.MustCompile(`(?m)^## (.+)$`)
)

// Segment is either prose or a beat, in authored order.
type Segment struct {
	Type string // "prose" | "beat"
	MD   string
	Beat Beat
	// raw keeps the beat's original YAML so re-emitting a section is
	// byte-faithful rather than a lossy round-trip through the struct.
	raw string
}

type Section struct {
	Heading  string
	Segments []Segment
}

// Markdown re-emits one segment exactly as it was authored. A beat round-trips
// through its original YAML rather than through the struct, so re-emitting is
// byte-faithful and a beat a depth swap re-appends is the same beat.
func (s Segment) Markdown() string {
	if s.Type == "prose" {
		return s.MD
	}
	return "```beat\n" + s.raw + "```"
}

// Markdown re-emits the section, beats included, for the author role to rewrite.
func (s Section) Markdown() string {
	parts := []string{"## " + s.Heading}
	for _, seg := range s.Segments {
		parts = append(parts, seg.Markdown())
	}
	return strings.Join(parts, "\n\n")
}

type Unit struct {
	ID       string
	Slug     string
	Title    string
	Minutes  int
	Prereqs  []string
	Concepts []ConceptRef
	Notes    string

	Front          map[string]any // canon front matter (assumes, etc.)
	IntroMD        string         // prose before the first ## section
	Sections       []Section
	Depths         map[string]map[string]string // depth name -> heading -> md
	Questions      QuestionFile
	Misconceptions []Misconception
}

func (u *Unit) ConceptIDs() []string {
	out := make([]string, 0, len(u.Concepts))
	for _, c := range u.Concepts {
		out = append(out, c.ID)
	}
	return out
}

func (u *Unit) Beats() []Beat {
	var out []Beat
	for _, s := range u.Sections {
		for _, seg := range s.Segments {
			if seg.Type == "beat" {
				out = append(out, seg.Beat)
			}
		}
	}
	return out
}

type Corpus struct {
	Dir            string
	Syllabus       Syllabus
	Units          map[string]*Unit
	Misconceptions map[string]Misconception
}

func Load(dir string) (*Corpus, error) {
	c := &Corpus{
		Dir:            dir,
		Units:          map[string]*Unit{},
		Misconceptions: map[string]Misconception{},
	}
	raw, err := os.ReadFile(filepath.Join(dir, "syllabus.yaml"))
	if err != nil {
		return nil, fmt.Errorf("syllabus: %w", err)
	}
	if err := yaml.Unmarshal(raw, &c.Syllabus); err != nil {
		return nil, fmt.Errorf("syllabus: %w", err)
	}
	bankRaw, err := os.ReadFile(filepath.Join(dir, "misconception-bank.yaml"))
	if err != nil {
		return nil, fmt.Errorf("misconception bank: %w", err)
	}
	var bank struct {
		Misconceptions []Misconception `yaml:"misconceptions"`
	}
	if err := yaml.Unmarshal(bankRaw, &bank); err != nil {
		return nil, fmt.Errorf("misconception bank: %w", err)
	}
	for _, m := range bank.Misconceptions {
		c.Misconceptions[m.ID] = m
	}
	for _, spec := range c.Syllabus.Units {
		u, err := c.loadUnit(spec)
		if err != nil {
			return nil, fmt.Errorf("unit %s: %w", spec.ID, err)
		}
		if u == nil {
			continue // not authored yet; a partial corpus is tolerated
		}
		c.Units[u.ID] = u
		for _, m := range u.Misconceptions {
			if _, seen := c.Misconceptions[m.ID]; !seen {
				c.Misconceptions[m.ID] = m
			}
		}
	}
	return c, nil
}

func (c *Corpus) loadUnit(spec UnitSpec) (*Unit, error) {
	udir := filepath.Join(c.Dir, "units", spec.ID+"-"+spec.Slug)
	canon, err := os.ReadFile(filepath.Join(udir, "canon.md"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	front, intro, sections, err := parseCanon(string(canon))
	if err != nil {
		return nil, err
	}
	minutes := spec.Minutes
	if minutes == 0 {
		minutes = 25
	}
	u := &Unit{
		ID: spec.ID, Slug: spec.Slug, Title: spec.Title, Minutes: minutes,
		Prereqs: spec.Prereqs, Concepts: spec.Concepts, Notes: spec.Notes,
		Front: front, IntroMD: intro, Sections: sections,
		Depths: map[string]map[string]string{},
	}

	ddir := filepath.Join(udir, "depths")
	if entries, err := os.ReadDir(ddir); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			body, err := os.ReadFile(filepath.Join(ddir, name))
			if err != nil {
				return nil, err
			}
			u.Depths[strings.TrimSuffix(name, ".md")] = sectionsByHeading(string(body))
		}
	}

	if qraw, err := os.ReadFile(filepath.Join(udir, "questions.yaml")); err == nil {
		if err := yaml.Unmarshal(qraw, &u.Questions); err != nil {
			return nil, fmt.Errorf("questions.yaml: %w", err)
		}
		for i := range u.Questions.Pretest {
			u.Questions.Pretest[i].deriveKind()
			u.Questions.Pretest[i].shuffleOptions()
		}
		for i := range u.Questions.Check {
			u.Questions.Check[i].deriveKind()
			u.Questions.Check[i].shuffleOptions()
		}
	}
	if mraw, err := os.ReadFile(filepath.Join(udir, "misconceptions.yaml")); err == nil {
		var local struct {
			Misconceptions []Misconception `yaml:"misconceptions"`
		}
		if err := yaml.Unmarshal(mraw, &local); err != nil {
			return nil, fmt.Errorf("misconceptions.yaml: %w", err)
		}
		u.Misconceptions = local.Misconceptions
	}
	return u, nil
}

func parseCanon(text string) (map[string]any, string, []Section, error) {
	front := map[string]any{}
	if m := frontMatter.FindStringSubmatchIndex(text); m != nil {
		if err := yaml.Unmarshal([]byte(text[m[2]:m[3]]), &front); err != nil {
			return nil, "", nil, fmt.Errorf("front matter: %w", err)
		}
		text = text[m[1]:]
	}
	idx := sectionHead.FindAllStringSubmatchIndex(text, -1)
	intro := text
	if len(idx) > 0 {
		intro = text[:idx[0][0]]
	}
	var sections []Section
	for i, m := range idx {
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		body := text[m[0]:end]
		if nl := strings.Index(body, "\n"); nl >= 0 {
			body = body[nl+1:]
		} else {
			body = ""
		}
		segs, err := parseSegments(body)
		if err != nil {
			return nil, "", nil, err
		}
		sections = append(sections, Section{
			Heading:  strings.TrimSpace(text[m[2]:m[3]]),
			Segments: segs,
		})
	}
	return front, strings.TrimSpace(intro), sections, nil
}

func parseSegments(body string) ([]Segment, error) {
	var segs []Segment
	cursor := 0
	for _, m := range beatFence.FindAllStringSubmatchIndex(body, -1) {
		if prose := strings.TrimSpace(body[cursor:m[0]]); prose != "" {
			segs = append(segs, Segment{Type: "prose", MD: prose})
		}
		raw := body[m[2]:m[3]]
		var b Beat
		if err := yaml.Unmarshal([]byte(raw), &b); err != nil {
			return nil, fmt.Errorf("beat block: %w", err)
		}
		b.Options = Shuffled(b.ID, b.Options)
		segs = append(segs, Segment{Type: "beat", Beat: b, raw: strings.TrimLeft(raw, "\n")})
		cursor = m[1]
	}
	if tail := strings.TrimSpace(body[cursor:]); tail != "" {
		segs = append(segs, Segment{Type: "prose", MD: tail})
	}
	return segs, nil
}

func sectionsByHeading(text string) map[string]string {
	if m := frontMatter.FindStringIndex(text); m != nil {
		text = text[m[1]:]
	}
	idx := sectionHead.FindAllStringSubmatchIndex(text, -1)
	out := map[string]string{}
	for i, m := range idx {
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		body := text[m[0]:end]
		if nl := strings.Index(body, "\n"); nl >= 0 {
			body = strings.TrimSpace(body[nl+1:])
		} else {
			body = ""
		}
		out[strings.TrimSpace(text[m[2]:m[3]])] = body
	}
	return out
}

// ---------- queries ----------

// IsCalibration marks a unit that only measures: its whole check bank is
// delivered in authored order, and its gate always passes - the score is
// calibration data, not a barrier.
func (u *Unit) IsCalibration() bool {
	b, _ := u.Front["calibration"].(bool)
	return b
}

func (c *Corpus) UnitOrder() []string {
	out := make([]string, 0, len(c.Syllabus.Units))
	for _, u := range c.Syllabus.Units {
		out = append(out, u.ID)
	}
	return out
}

func (c *Corpus) PrereqGraph() map[string][]string {
	out := map[string][]string{}
	for _, u := range c.Syllabus.Units {
		out[u.ID] = u.Prereqs
	}
	return out
}

func (c *Corpus) ConceptUnit(conceptID string) string {
	for _, id := range c.UnitOrder() {
		u, ok := c.Units[id]
		if !ok {
			continue
		}
		for _, cid := range u.ConceptIDs() {
			if cid == conceptID {
				return u.ID
			}
		}
	}
	return ""
}

// FindQuestion walks units in syllabus order so the result does not depend on
// map iteration order.
func (c *Corpus) FindQuestion(qid string) (*Question, string) {
	for _, id := range c.UnitOrder() {
		u, ok := c.Units[id]
		if !ok {
			continue
		}
		for _, pool := range [][]Question{u.Questions.Pretest, u.Questions.Check} {
			for i := range pool {
				if pool[i].ID == qid {
					return &pool[i], u.ID
				}
			}
		}
		if s := u.Questions.Screener; s != nil && s.ID == qid {
			return s, u.ID
		}
	}
	return nil, ""
}

func (c *Corpus) FindBeat(beatID string) (*Beat, string) {
	for _, id := range c.UnitOrder() {
		u, ok := c.Units[id]
		if !ok {
			continue
		}
		beats := u.Beats()
		for i := range beats {
			if beats[i].ID == beatID {
				return &beats[i], u.ID
			}
		}
	}
	return nil, ""
}
