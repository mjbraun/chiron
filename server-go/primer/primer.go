// Package primer keeps the documents a reader captures from elsewhere on
// the iPad and asks about. A primer is a one-unit corpus: the same files a
// book has, written by code from the author's markdown, so chapters, asks,
// marks and learner state need nothing new. Each primer lives under
// <root>/<id>/ with primer.json (what it is and how it is doing), doc.md
// (the document), corpus/ (the one-unit corpus) and state/ (the reader's).
package primer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mjbraun/chiron/server/llm"
)

const (
	// A draft being planned in conversation, a book draft whose book is
	// being generated, a primer being written, and the outcomes.
	StatusPlanning  = "planning"
	StatusBuilding  = "building"
	StatusAuthoring = "authoring"
	StatusReady     = "ready"
	StatusFailed    = "failed"

	// UnitID is the one unit every primer has.
	UnitID   = "p1"
	unitSlug = "primer"
)

// Source is where the captured material came from.
type Source struct {
	Text     string `json:"text,omitempty"`
	URL      string `json:"url,omitempty"`
	App      string `json:"app,omitempty"`
	HasImage bool   `json:"has_image,omitempty"`
}

// Entry is one margin note that extended the document.
type Entry struct {
	Quote   string    `json:"quote"`
	Note    string    `json:"note"`
	Heading string    `json:"heading"`
	At      time.Time `json:"at"`
}

// Turn is one exchange of the planning conversation.
type Turn struct {
	Role string `json:"role"` // learner | tutor
	Text string `json:"text"`
}

type Meta struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Prompt     string    `json:"prompt"`
	Source     Source    `json:"source"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	CapturedAt time.Time `json:"captured_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Entries    []Entry   `json:"entries,omitempty"`
	// Scale is "primer" or "book": what the capture is to become. The
	// plan is the conversation so far, the brief what it settled on, and
	// Done whether it did. Book names the subject a book draft grows into.
	Scale string `json:"scale,omitempty"`
	Plan  []Turn `json:"plan,omitempty"`
	Brief string `json:"brief,omitempty"`
	Done  bool   `json:"done,omitempty"`
	// Named: the title was given at capture, and stays through the plan
	// and the writing; otherwise the writer titles it.
	Named bool   `json:"named,omitempty"`
	Book  string `json:"book,omitempty"`
	// Provenance is every model call that wrote the document: the
	// primer itself, then each section a margin note added.
	Provenance []llm.Call `json:"provenance,omitempty"`
}

// Draft is true while the capture is still a plan, or failed before it
// became anything.
func (m *Meta) Draft() bool {
	return m.Status == StatusPlanning || m.Status == StatusBuilding || (m.Status == StatusFailed && m.Scale != "")
}

// Delete removes a primer and everything under it.
func Delete(root, id string) error {
	return os.RemoveAll(Dir(root, id))
}

func Dir(root, id string) string       { return filepath.Join(root, id) }
func CorpusDir(root, id string) string { return filepath.Join(root, id, "corpus") }
func StateDir(root, id string) string  { return filepath.Join(root, id, "state") }
func docPath(root, id string) string   { return filepath.Join(root, id, "doc.md") }
func metaPath(root, id string) string  { return filepath.Join(root, id, "primer.json") }

// Save writes primer.json atomically.
func Save(root string, m *Meta) error {
	m.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(metaPath(root, m.ID), data)
}

// LoadAll reads every primer under root, oldest capture first.
func LoadAll(root string) ([]*Meta, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Meta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(metaPath(root, e.Name()))
		if err != nil {
			continue
		}
		var m Meta
		if err := json.Unmarshal(data, &m); err != nil || m.ID != e.Name() {
			continue
		}
		out = append(out, &m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CapturedAt.Before(out[j].CapturedAt) })
	return out, nil
}

func ReadDoc(root, id string) (string, error) {
	data, err := os.ReadFile(docPath(root, id))
	return string(data), err
}

// WriteDoc stores the document and lays out the one-unit corpus the server
// loads it through. A document without a "## " heading gets one, since a
// chapter is its sections.
func WriteDoc(root, id, title, doc string) error {
	doc = strings.TrimSpace(doc) + "\n"
	if !strings.Contains("\n"+doc, "\n## ") {
		doc = "## " + title + "\n\n" + doc
	}
	if err := writeAtomic(docPath(root, id), []byte(doc)); err != nil {
		return err
	}
	cdir := CorpusDir(root, id)
	syllabus := fmt.Sprintf("title: %s\nunits:\n  - id: %s\n    slug: %s\n    title: %s\n    minutes: 10\n",
		yamlString(title), UnitID, unitSlug, yamlString(title))
	if err := writeAtomic(filepath.Join(cdir, "syllabus.yaml"), []byte(syllabus)); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(cdir, "misconception-bank.yaml"), []byte("misconceptions: []\n")); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(cdir, "units", UnitID+"-"+unitSlug, "canon.md"), []byte(doc))
}

// AppendSection adds a section to the end of the document and rewrites the
// corpus; the new document comes back.
func AppendSection(root, id, title, heading, markdown string) (string, error) {
	doc, err := ReadDoc(root, id)
	if err != nil {
		return "", err
	}
	doc = strings.TrimRight(doc, "\n") + "\n\n## " + strings.TrimSpace(heading) + "\n\n" + strings.TrimSpace(markdown) + "\n"
	return doc, WriteDoc(root, id, title, doc)
}

func yamlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
