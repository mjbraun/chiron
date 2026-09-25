// Package devagent is the development agent on the sprite
// (SPRITE-DEV-PLAN.md phase G): it takes change requests from the queue
// one at a time, has Claude Code make the change in a worktree off main,
// runs the suite, moves main, deploys the server if it changed, asks the
// MacBook for a build if the app changed, and writes every step back on
// the request for the app to show.
package devagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mjbraun/chiron/server/devreq"
)

// Exec runs commands: the real one shells out, a test scripts answers.
type Exec interface {
	Run(dir, name string, args ...string) (string, error)
	Stream(dir, name string, args []string, onLine func(string)) error
}

type Agent struct {
	// Repo is the checkout main lives in; Work is where worktrees go;
	// Served is the deployed tree, whose builds/latest names the build.
	Repo, Work, Served string
	Store              *devreq.Store
	Model              string
	Exec               Exec
	// Awake holds the sprite awake while a request is worked; nil holds
	// nothing. RenewEvery is how often the hold is renewed (five minutes
	// when zero).
	Awake      Keeper
	RenewEvery time.Duration
}

// Once takes the oldest queued request and sees it through. It reports
// whether there was one; a request's own failure is recorded on it, not
// returned.
func (a *Agent) Once() (bool, error) {
	r, err := a.Store.NextQueued()
	if err != nil || r == nil {
		return false, err
	}
	a.handle(r)
	return true, nil
}

func (a *Agent) handle(r *devreq.Request) {
	say := func(format string, args ...any) {
		r.Say(fmt.Sprintf(format, args...))
		a.Store.Save(r)
	}
	fail := func(reason string) {
		r.Status = devreq.Failed
		r.Reason = reason
		say("failed: %s", firstLine(reason))
	}
	defer a.keepAwake(r.ID)()
	branch := "req/" + r.ID
	wt := filepath.Join(a.Work, r.ID)

	r.Status = devreq.Working
	a.Store.Save(r)
	// A request that stopped to ask comes back to the worktree it left.
	if _, err := os.Stat(wt); err == nil {
		say("back on %s with the answer", branch)
	} else {
		if out, err := a.Exec.Run(a.Repo, "git", "-C", a.Repo, "worktree", "add", "-B", branch, wt, "main"); err != nil {
			fail("worktree: " + out)
			return
		}
		say("working on %s", branch)
	}

	// Claude Code, its words as they come; the result is its summary.
	err := a.Exec.Stream(wt, "claude", []string{
		"-p", a.prompt(r), "--model", a.Model, "--dangerously-skip-permissions",
		"--output-format", "stream-json", "--verbose",
	}, func(line string) {
		var ev struct {
			Type    string `json:"type"`
			Result  string `json:"result"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			return
		}
		switch ev.Type {
		case "assistant":
			for _, c := range ev.Message.Content {
				if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
					say("%s", clip(strings.TrimSpace(c.Text), 400))
				}
			}
		case "result":
			r.Summary = strings.TrimSpace(ev.Result)
			a.Store.Save(r)
		}
	})
	if err != nil {
		fail("claude: " + err.Error())
		return
	}
	// It stopped to ask: the request waits on Matt, worktree and all.
	if q := question(r.Summary); q != "" {
		r.Status = devreq.Waiting
		r.Question = q
		say("asking: %s", clip(q, 400))
		return
	}

	// Whatever it left uncommitted is committed on its behalf.
	if out, _ := a.Exec.Run(wt, "git", "-C", wt, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		a.Exec.Run(wt, "git", "-C", wt, "add", "-A")
		a.Exec.Run(wt, "git", "-C", wt, "commit", "-q", "-m", "Request "+r.ID+": work the agent left uncommitted")
	}
	commits, _ := a.Exec.Run(wt, "git", "-C", wt, "log", "--oneline", "main.."+branch)
	if strings.TrimSpace(commits) == "" {
		fail("the agent made no change")
		return
	}
	say("%d commit(s)", len(strings.Split(strings.TrimSpace(commits), "\n")))
	changed, _ := a.Exec.Run(wt, "git", "-C", wt, "diff", "--name-only", "main.."+branch)

	r.Status = devreq.Testing
	say("running the suite")
	if out, err := a.Exec.Run(wt, "make", "test"); err != nil {
		fail("the suite failed:\n" + tail(out, 30))
		return
	}

	if out, err := a.Exec.Run(a.Repo, "git", "-C", a.Repo, "merge", "--ff-only", branch); err != nil {
		fail("main would not fast-forward: " + out)
		return
	}
	head, _ := a.Exec.Run(a.Repo, "git", "-C", a.Repo, "rev-parse", "--short", "HEAD")
	r.Commit = strings.TrimSpace(head)
	say("main is now %s", r.Commit)

	r.Status = devreq.Building
	if touches(changed, "server-go/", "corpus/", "corpus-v2/") {
		say("deploying the server")
		if out, err := a.Exec.Run(a.Repo, "make", "deploy"); err != nil {
			fail("deploy failed:\n" + tail(out, 30))
			return
		}
	}
	if touches(changed, "ipad-app/", "scripts/mac-build.sh") {
		say("building the app on the MacBook")
		if out, err := a.Exec.Run(a.Repo, "make", "app-build"); err != nil {
			fail("the build failed:\n" + tail(out, 30))
			return
		}
		if b, err := a.latestBuild(); err == nil {
			r.Build = b
			say("built %s (%d)", b.Version, b.Number)
		}
	}

	a.Exec.Run(a.Repo, "git", "-C", a.Repo, "worktree", "remove", "--force", wt)
	a.Exec.Run(a.Repo, "git", "-C", a.Repo, "branch", "-D", branch)
	r.Status = devreq.Ready
	say("ready")
}

// prompt is the brief Claude Code works from: the request, where the
// reader was, and the rules of this repo in short.
func (a *Agent) prompt(r *devreq.Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Matt, reading on his iPad, asked for this change to Chiron:\n\n%s\n\n", r.Text)
	if len(r.State) > 0 {
		fmt.Fprintf(&b, "The app's state when he asked (the debug harness's view): %s\n\n", string(r.State))
	}
	if p := a.Store.ScreenshotPath(r); p != "" {
		fmt.Fprintf(&b, "A screenshot of what he was looking at is at %s; read it.\n\n", p)
	}
	if len(r.Thread) > 0 {
		b.WriteString("You stopped on this request before to ask Matt something; your work so far is already in this worktree. What you asked and what he answered:\n\n")
		for _, e := range r.Thread {
			fmt.Fprintf(&b, "You asked: %s\nMatt answered: %s\n\n", e.Question, e.Answer)
		}
	}
	b.WriteString(`You are in a git worktree on a branch off main, in the Chiron repo. Read CLAUDE.md and LESSONS.md first. Rules:
- TDD: a failing test first, then the smallest change that passes it. Never delete a failing test.
- Run the tests of the package you touched (go test ./<package>, or node scripts/test-book-js.mjs for the page script). Do not run the whole suite (make test): the runner does that after you, and on this machine it takes a long time. The app's own unit suite runs on the build Mac afterwards, so keep app changes compilable and covered.
- Before your final message, commit your work with git commit and a message saying what changed and why, in the voice of the log. A request whose work is left uncommitted is committed on your behalf under a poorer message. Never push; never deploy: the runner does both once the suite is green.
- Never use em or en dashes, only hyphens. No attribution lines in commits.
- If the request is unsafe, make no change and say why in your final message.
- If it is unclear enough that you would be guessing at what Matt wants, ask him instead: stop, and make your final message only the question, beginning "QUESTION: ". He answers on the iPad and the request comes back to you here. Ask only what the repo cannot tell you, and put everything he needs to answer in the one question.
When done, your final message is a short summary of what you changed and why, for Matt to read on the iPad.`)
	return b.String()
}

func (a *Agent) latestBuild() (*devreq.Build, error) {
	data, err := os.ReadFile(filepath.Join(a.Served, "builds", "latest", "build.json"))
	if err != nil {
		return nil, err
	}
	var b struct {
		Version string `json:"version"`
		Build   int    `json:"build"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &devreq.Build{Version: b.Version, Number: b.Build, Token: b.Token}, nil
}

// question is what the agent asked, when its final message is a
// question for Matt, and "" otherwise.
func question(summary string) string {
	s := strings.TrimSpace(summary)
	if !strings.HasPrefix(s, "QUESTION:") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(s, "QUESTION:"))
}

func touches(changed string, prefixes ...string) bool {
	for _, f := range strings.Split(changed, "\n") {
		for _, p := range prefixes {
			if strings.HasPrefix(strings.TrimSpace(f), p) {
				return true
			}
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Small file helpers the tests share.
func mkdirAll(dir string) error      { return os.MkdirAll(dir, 0o755) }
func writeFile(path, s string) error { return os.WriteFile(path, []byte(s), 0o644) }
