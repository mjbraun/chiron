package llm

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The case that matters is `--tools ""`. With the built-in tools enabled the
// model can spend its single allowed turn on a tool call; the CLI then exits
// non-zero with subtype error_max_turns and the generated work is discarded.
// That failure is intermittent and surfaces as a bare "exit 1", so it is
// exactly the kind of thing that comes back silently.
func TestCommandDisablesTools(t *testing.T) {
	c := &ClaudeCLI{}
	argv := c.Command("grader", "PROMPT", "SYSTEM")
	flags := map[string]string{}
	for i := 0; i < len(argv)-1; i++ {
		if strings.HasPrefix(argv[i], "--") {
			flags[argv[i]] = argv[i+1]
		}
	}
	if v, ok := flags["--tools"]; !ok || v != "" {
		t.Error("tools are not disabled - a tool turn will exit error_max_turns and discard the grade")
	}
	if flags["--max-turns"] != "1" {
		t.Error("max-turns should stay 1 once tools are off")
	}
	if flags["--output-format"] != "json" {
		t.Error("Structured parses the json envelope")
	}
	if argv[0] != "claude" || argv[1] != "-p" {
		t.Errorf("must invoke the CLI headless, got %v", argv[:2])
	}
	if flags["--append-system-prompt"] != "SYSTEM" {
		t.Error("system prompt must reach the CLI")
	}
	found := false
	for _, a := range argv {
		if a == "PROMPT" {
			found = true
		}
	}
	if !found {
		t.Error("prompt must reach the CLI")
	}
}

// Every role runs on the one model the book is written with, unless the
// config names another.
func TestEveryRoleRunsOnTheBooksModel(t *testing.T) {
	c := &ClaudeCLI{}
	for _, role := range []string{"grader", "planner", "author", "something new"} {
		argv := c.Command(role, "p", "s")
		if got := argv[len(argv)-1]; got != "claude-fable-5-1" {
			t.Errorf("%s uses %q, want claude-fable-5-1", role, got)
		}
	}
	if st := c.Status(); st.Model != "claude-fable-5-1" {
		t.Errorf("status names %q", st.Model)
	}
	pinned := &ClaudeCLI{Model: "haiku"}
	argv := pinned.Command("author", "p", "s")
	if got := argv[len(argv)-1]; got != "haiku" {
		t.Errorf("an explicit model must override the default, got %q", got)
	}
}

func TestTheDirectAPIDefaultsToTheBooksModel(t *testing.T) {
	if DefaultAnthropicModel != "claude-fable-5-1" || DefaultVisionModel != "claude-fable-5-1" {
		t.Errorf("defaults: text %q, vision %q", DefaultAnthropicModel, DefaultVisionModel)
	}
}

func TestUnmarshalLooseHandlesFencedJSON(t *testing.T) {
	var out struct {
		Verdict string `json:"verdict"`
	}
	for _, in := range []string{
		`{"verdict":"pass"}`,
		"```json\n{\"verdict\":\"pass\"}\n```",
		"Here you go:\n{\"verdict\":\"pass\"}\nHope that helps.",
	} {
		out.Verdict = ""
		if err := unmarshalLoose(in, &out); err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if out.Verdict != "pass" {
			t.Errorf("%q -> %q", in, out.Verdict)
		}
	}
	if err := unmarshalLoose("no json here", &out); err == nil {
		t.Error("prose with no object must error rather than silently succeed")
	}
}

// The planner emits the whole syllabus in one call: every unit with its
// concepts, prereqs, notes and sources, plus the misconception bank. On
// 2026-09-05 three book builds in a row hit the 5-minute deadline at exactly
// that step, and the CLI reports nothing but "timed out (planner)". The
// planner's budget has to match the author's, which also writes a chapter's
// worth of output in one call.
func TestPlannerTimeoutCoversASyllabus(t *testing.T) {
	if got, want := roleTimeouts["planner"], roleTimeouts["author"]; got < want {
		t.Errorf("planner timeout %v is shorter than the author's %v; a syllabus is one call and needs the same room", got, want)
	}
}

// A CLI process killed at the deadline may leave a child holding its
// output pipe; the call must still return promptly rather than wait for
// that child. Four author calls once took 108 minutes to time out at 30.
func TestRunReturnsAtTheDeadlineDespiteAChildOnThePipe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := run(ctx, []string{"sh", "-c", "sleep 30 & exec sleep 30"}, cliEnv(""))
	if err == nil {
		t.Fatal("a killed call returned no error")
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("run returned after %v; the child's pipe held it", took)
	}
}

func TestCLICallsRunWithoutTheUpdaterInTheirOwnConfigDir(t *testing.T) {
	dir := t.TempDir() + "/claude"
	env := strings.Join(cliEnv(dir), "\n")
	for _, want := range []string{"DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CONFIG_DIR=" + dir} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("config dir not made: %v", err)
	}
	if strings.Contains(strings.Join(cliEnv(""), "\n"), "CLAUDE_CONFIG_DIR") {
		t.Error("no dir asked for, yet one set")
	}
}

// The deadline kills the CLI's children too: a background process the
// CLI left behind does not outlive the call.
func TestRunKillsTheWholeProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, _ = run(ctx, []string{"sh", "-c", "sleep 31.7 & exec sleep 31.7"}, cliEnv(""))
	time.Sleep(200 * time.Millisecond)
	if out, _ := exec.Command("pgrep", "-f", "sleep 31.7").Output(); len(strings.TrimSpace(string(out))) > 0 {
		exec.Command("pkill", "-f", "sleep 31.7").Run()
		t.Fatalf("a child survived the deadline: pids %s", out)
	}
}

// Left to itself the CLI thinks at high effort with no bound, and on an
// authoring prompt of 80 KB that ran past thirty minutes without a word
// of text, every time. Each call names its effort and the environment
// caps the thinking budget; both can be turned from the environment.
func TestCallsNameTheirEffortAndCapThinking(t *testing.T) {
	c := &ClaudeCLI{}
	argv := c.Command("author", "PROMPT", "SYSTEM")
	got := ""
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == "--effort" {
			got = argv[i+1]
		}
	}
	if got != "medium" {
		t.Errorf("author effort = %q, want medium by default", got)
	}
	t.Setenv("CHIRON_CLI_EFFORT", "low")
	argv = c.Command("author", "PROMPT", "SYSTEM")
	if !strings.Contains(strings.Join(argv, " "), "--effort low") {
		t.Errorf("CHIRON_CLI_EFFORT not honoured: %v", argv)
	}
	env := strings.Join(cliEnv(""), "\n")
	if !strings.Contains(env, "MAX_THINKING_TOKENS=4000") {
		t.Errorf("thinking not capped: %s", env)
	}
	t.Setenv("CHIRON_THINKING_TOKENS", "1024")
	if !strings.Contains(strings.Join(cliEnv(""), "\n"), "MAX_THINKING_TOKENS=1024") {
		t.Error("CHIRON_THINKING_TOKENS not honoured")
	}
}

// Each call writes the CLI's own debug log to a file of its own under the
// config dir, the one record of what the CLI and the API did when a call
// is killed at its deadline; without a config dir there is nowhere for
// it and the flag is left out.
func TestCallsKeepADebugFileUnderTheConfigDir(t *testing.T) {
	c := &ClaudeCLI{ConfigDir: t.TempDir()}
	argv := c.Command("author", "PROMPT", "SYSTEM")
	file := ""
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == "--debug-file" {
			file = argv[i+1]
		}
	}
	if !strings.HasPrefix(file, c.ConfigDir+"/debug/author-") || !strings.HasSuffix(file, ".log") {
		t.Errorf("debug file = %q", file)
	}
	if strings.Contains(strings.Join((&ClaudeCLI{}).Command("author", "P", "S"), " "), "--debug-file") {
		t.Error("a debug file with no config dir to hold it")
	}
}

// The CLI gives the API two minutes to send a first byte, then aborts
// and retries; when the API queues a large request for longer than
// that, every attempt is thrown away and the call only ends at our
// deadline. The server's calls wait longer for the first byte and for
// the whole request.
func TestCallsWaitLongerForTheFirstByte(t *testing.T) {
	env := strings.Join(cliEnv(""), "\n")
	for _, want := range []string{"CLAUDE_STREAM_FIRST_BYTE_TIMEOUT_MS=480000", "API_TIMEOUT_MS=1500000"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
	if roleTimeouts["author"] < 40*time.Minute {
		t.Errorf("author deadline %v leaves no room for one long first wait and the reply", roleTimeouts["author"])
	}
}
