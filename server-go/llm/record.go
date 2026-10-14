package llm

import (
	"sync"
	"time"
)

// Call is the record of one model call that produced something the reader
// sees: which role asked, what it asked for, of which model, with what
// prompts, and when. It rides with the thing it produced (a chapter's
// provenance, a primer's) so the reader can see where the words came from.
type Call struct {
	Role string `json:"role"`
	// Name is what the call produced, by the schema it asked for:
	// "rewrite", "directives", "primer", "answer".
	Name    string    `json:"name,omitempty"`
	Model   string    `json:"model,omitempty"`
	System  string    `json:"system,omitempty"`
	Prompt  string    `json:"prompt,omitempty"`
	At      time.Time `json:"at"`
	Seconds float64   `json:"seconds,omitempty"`
	// Ref names where the whole record is kept when the call travels
	// without its prompts (GET /generated/{subject}/{ref}).
	Ref string `json:"ref,omitempty"`
}

// Brief is the call without its prompts: enough to say who wrote a thing
// and when, small enough to keep beside a mark or a shelf row.
func (c Call) Brief() Call {
	c.System, c.Prompt = "", ""
	return c
}

// Models names each model the calls ran on, once, in order of first use.
func Models(calls []Call) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range calls {
		if c.Model == "" || seen[c.Model] {
			continue
		}
		seen[c.Model] = true
		out = append(out, c.Model)
	}
	return out
}

// Modeler is a chain that can say which model a role would run on
// without a call being made.
type Modeler interface {
	ModelFor(role string) string
}

// Recorder is a chain that keeps a record of the calls made through it.
// One is made for each thing built (a chapter, a primer, an answer), so
// its calls are that thing's provenance and nothing else's.
type Recorder struct {
	Chain
	mu    sync.Mutex
	calls []Call
}

func Record(chain Chain) *Recorder { return &Recorder{Chain: chain} }

func (r *Recorder) modelFor(role string) string {
	if m, ok := r.Chain.(Modeler); ok {
		return m.ModelFor(role)
	}
	return r.Chain.Status().Model
}

// Structured runs the call and, when it produced an answer, keeps the
// record. A failed call produced nothing the reader sees.
func (r *Recorder) Structured(role, system, user string, schema map[string]any, name string, out any) error {
	start := time.Now()
	if err := r.Chain.Structured(role, system, user, schema, name, out); err != nil {
		return err
	}
	call := Call{
		Role: role, Name: name, Model: r.modelFor(role),
		System: system, Prompt: user,
		At:      start.UTC().Truncate(time.Second),
		Seconds: float64(time.Since(start).Milliseconds()) / 1000,
	}
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	return nil
}

// Calls is the record so far, oldest first.
func (r *Recorder) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.calls...)
}
