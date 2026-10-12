package devreq

import (
	"encoding/json"
	"testing"
)

// A request typed in the app lands here queued, with the app's state and
// screenshot beside it; the agent takes the oldest queued one, and every
// change it makes to the request is what the app then shows.
func TestARequestIsQueuedListedAndTakenInOrder(t *testing.T) {
	s := Open(t.TempDir())
	first, err := s.Create("make the pen thicker", json.RawMessage(`{"screen":"reading"}`), []byte("PNG"))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := s.Create("and bluer", nil, nil)
	if first.Status != Queued || first.Text != "make the pen thicker" || first.ID == "" || first.ID == second.ID {
		t.Errorf("first = %+v", first)
	}
	if first.Screenshot == "" || second.Screenshot != "" {
		t.Errorf("screenshots: %q %q", first.Screenshot, second.Screenshot)
	}
	if string(first.State) != `{"screen":"reading"}` {
		t.Errorf("state = %s", first.State)
	}

	list, _ := s.List()
	if len(list) != 2 || list[0].ID != second.ID {
		t.Errorf("list newest first: %v", ids(list))
	}
	next, _ := s.NextQueued()
	if next == nil || next.ID != first.ID {
		t.Errorf("next queued should be the oldest: %+v", next)
	}

	next.Status = Working
	next.Say("worktree made")
	if err := s.Save(next); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(first.ID)
	if got.Status != Working || len(got.Log) != 1 || got.Last != "worktree made" {
		t.Errorf("saved = %+v", got)
	}
	next2, _ := s.NextQueued()
	if next2 == nil || next2.ID != second.ID {
		t.Errorf("after taking the first, next is the second: %+v", next2)
	}
	if _, err := s.Get("req-nonesuch"); err == nil {
		t.Error("a missing request is an error")
	}
	if _, err := s.Create("   ", nil, nil); err == nil {
		t.Error("an empty request is refused")
	}
}

func ids(rs []*Request) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// The agent can stop and ask. The request waits with its question; an
// answer goes on the thread and queues it again, and only a waiting
// request takes one.
// The app keeps a request on the device until the sprite takes it, and
// sends it again when a reply was lost; the id it gave the request makes
// the second sending the same request, not a second one.
func TestARequestSentAgainUnderItsOwnIDIsTheSameRequest(t *testing.T) {
	s := Open(t.TempDir())
	first, err := s.CreateOnce("kept-1a2b3c4d", "make the pen thicker", json.RawMessage(`{"screen":"reading"}`), []byte("PNG"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ClientID != "kept-1a2b3c4d" {
		t.Errorf("client id not kept: %+v", first)
	}
	again, err := s.CreateOnce("kept-1a2b3c4d", "make the pen thicker", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Errorf("sent again = %s, want %s", again.ID, first.ID)
	}
	other, _ := s.CreateOnce("kept-ffffffff", "and bluer", nil, nil)
	none, _ := s.CreateOnce("", "no id at all", nil, nil)
	if other.ID == first.ID || none.ID == first.ID || none.ClientID != "" {
		t.Errorf("other = %s, none = %+v", other.ID, none)
	}
	if list, _ := s.List(); len(list) != 3 {
		t.Errorf("list: %v", ids(list))
	}
}

func TestAWaitingRequestTakesAnAnswerAndQueuesAgain(t *testing.T) {
	s := Open(t.TempDir())
	r, _ := s.Create("make the shelf nicer", nil, nil)
	if _, err := s.Answer(r.ID, "denser"); err == nil {
		t.Error("a queued request asked nothing; there is nothing to answer")
	}

	r.Status = Waiting
	r.Question = "Nicer how: denser, or with covers?"
	s.Save(r)
	if _, err := s.Answer(r.ID, "   "); err == nil {
		t.Error("an empty answer is refused")
	}
	got, err := s.Answer(r.ID, "  Denser, one line a row. ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Queued || got.Question != "" {
		t.Errorf("after the answer: status %s question %q", got.Status, got.Question)
	}
	if len(got.Thread) != 1 || got.Thread[0].Question != "Nicer how: denser, or with covers?" || got.Thread[0].Answer != "Denser, one line a row." {
		t.Errorf("thread = %+v", got.Thread)
	}
	if again, _ := s.Get(r.ID); again.Status != Queued || len(again.Thread) != 1 {
		t.Errorf("not saved: %+v", again)
	}
	if next, _ := s.NextQueued(); next == nil || next.ID != r.ID {
		t.Errorf("the answered request is next: %+v", next)
	}
	if _, err := s.Answer("req-nonesuch", "x"); err == nil {
		t.Error("no such request")
	}
}
