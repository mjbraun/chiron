// Package devreq is the queue of change requests between the app and the
// development agent on the sprite (SPRITE-DEV-PLAN.md phase G): one JSON
// file per request under a directory, the screenshot beside it. The
// server creates and lists; the agent takes the oldest queued request
// and records what it does; the app shows every step.
package devreq

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The states a request moves through, in order; failed can follow any.
// Waiting is the agent stopped to ask Matt something; his answer queues
// the request again.
const (
	Queued   = "queued"
	Waiting  = "waiting"
	Working  = "working"
	Testing  = "testing"
	Building = "building"
	Ready    = "ready"
	Failed   = "failed"
)

type Request struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Status    string    `json:"status"`
	// State is what the app reported about itself when the request was
	// made (the harness state); Screenshot names the PNG beside the file.
	State      json.RawMessage `json:"state,omitempty"`
	Screenshot string          `json:"screenshot,omitempty"`
	// Log is every step the agent took; Last is the newest line, for a
	// list; Summary is the agent's own account when it is done; Reason
	// says why a failed request failed.
	Log     []string `json:"log"`
	Last    string   `json:"last,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Reason  string   `json:"reason,omitempty"`
	Commit  string   `json:"commit,omitempty"`
	// Build is the app build the request produced, when the app changed.
	Build *Build `json:"build,omitempty"`
	// Question is what the agent is waiting on Matt for; Thread is every
	// question it asked before and the answer it got.
	Question string     `json:"question,omitempty"`
	Thread   []Exchange `json:"thread,omitempty"`
}

// Exchange is one question the agent asked and Matt's answer.
type Exchange struct {
	Question string    `json:"question"`
	Answer   string    `json:"answer"`
	At       time.Time `json:"at"`
}

var (
	ErrNotFound   = errors.New("no such request")
	ErrNotWaiting = errors.New("the request is not waiting on an answer")
	ErrNoAnswer   = errors.New("an answer says something")
)

type Build struct {
	Version string `json:"version"`
	Number  int    `json:"build"`
	Token   string `json:"token"`
}

// Say appends a step to the log and makes it the last line.
func (r *Request) Say(line string) {
	r.Log = append(r.Log, line)
	r.Last = line
}

type Store struct{ dir string }

func Open(dir string) *Store {
	os.MkdirAll(dir, 0o755)
	return &Store{dir: dir}
}

func (s *Store) Dir() string { return s.dir }

// Create queues a request. The screenshot, if any, is written beside it.
func (s *Store) Create(text string, state json.RawMessage, screenshotPNG []byte) (*Request, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("a request says what to change")
	}
	var b [3]byte
	rand.Read(b[:])
	now := time.Now().UTC()
	r := &Request{
		ID:        fmt.Sprintf("req-%s-%s", now.Format("20060102-150405"), hex.EncodeToString(b[:])),
		Text:      text,
		CreatedAt: now, UpdatedAt: now,
		Status: Queued,
		State:  state,
		Log:    []string{},
	}
	if len(screenshotPNG) > 0 {
		r.Screenshot = r.ID + ".png"
		if err := os.WriteFile(filepath.Join(s.dir, r.Screenshot), screenshotPNG, 0o644); err != nil {
			return nil, err
		}
	}
	return r, s.Save(r)
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

// ScreenshotPath is where a request's screenshot lies, or "" when it has none.
func (s *Store) ScreenshotPath(r *Request) string {
	if r.Screenshot == "" {
		return ""
	}
	return filepath.Join(s.dir, r.Screenshot)
}

func (s *Store) Save(r *Request) error {
	r.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(r.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(r.ID))
}

func (s *Store) Get(id string) (*Request, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	var r Request
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Log == nil {
		r.Log = []string{}
	}
	return &r, nil
}

// List is every request, newest first.
func (s *Store) List() ([]*Request, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []*Request
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, err := s.Get(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if out == nil {
		out = []*Request{}
	}
	return out, nil
}

// NextQueued is the oldest request still queued, or nil.
func (s *Store) NextQueued() (*Request, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Status == Queued {
			return all[i], nil
		}
	}
	return nil, nil
}

// Answer puts Matt's answer to the question a request is waiting on
// onto its thread and queues it again, for the agent to take up where it
// stopped.
func (s *Store) Answer(id, answer string) (*Request, error) {
	answer = strings.TrimSpace(answer)
	r, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if r.Status != Waiting {
		return nil, ErrNotWaiting
	}
	if answer == "" {
		return nil, ErrNoAnswer
	}
	r.Thread = append(r.Thread, Exchange{Question: r.Question, Answer: answer, At: time.Now().UTC()})
	r.Question = ""
	r.Status = Queued
	r.Say("answered: " + answer)
	return r, s.Save(r)
}
