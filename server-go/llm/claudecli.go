package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ClaudeCLI runs the tutor roles through headless `claude -p`, authenticated by
// the user's Claude Code login - subscription billing, no API key on the box.
//
// There is no token-level schema enforcement here: the JSON contract is
// prompt-enforced, extracted, and retried once.
type ClaudeCLI struct {
	// Model, when set, replaces BookModel for every role.
	Model string
	// ConfigDir, when set, is CLAUDE_CONFIG_DIR for every call: a
	// directory of the server's own, so the user's hooks (which on the
	// sprite call its control plane on every prompt) and any stale
	// credentials there never run under a call. Auth is the token in the
	// environment.
	ConfigDir string
}

// The planner writes a whole syllabus in one call (every unit's concepts,
// prereqs, notes and sources, plus the misconception bank), so it gets a
// long budget: at five minutes a reference-depth book timed out at that
// step three times running. The author's depth variants are longer still:
// a deeper-math file for a 35 KB chapter, written at the CLI's ~33 tokens
// a second with three units in flight, ran past fifteen minutes three
// times on the first sourced book.
var roleTimeouts = map[string]time.Duration{
	"grader":  5 * time.Minute,
	"planner": 40 * time.Minute,
	"author":  40 * time.Minute,
}

func (c *ClaudeCLI) available() bool {
	// Presence of the binary, not of credentials: the login lives in a file on
	// Linux but in the Keychain on macOS, so probing storage reports "not
	// logged in" on a machine where it works. A real auth failure surfaces
	// through the CLI's own output, which says more than a guess would.
	_, err := exec.LookPath("claude")
	return err == nil
}

func (c *ClaudeCLI) ModelFor(role string) string {
	if c.Model != "" {
		return c.Model
	}
	return BookModel
}

func (c *ClaudeCLI) Status() Status {
	if !c.available() {
		return Status{Connected: false, Upstream: "claude-cli", Error: "`claude` not on PATH"}
	}
	model := c.Model
	if model == "" {
		model = BookModel
	}
	return Status{Connected: true, Upstream: "claude-cli", Model: model}
}

// Command builds the argv. Exported so the contract below can be tested without
// spending a model call.
func (c *ClaudeCLI) Command(role, prompt, system string) []string {
	argv := []string{
		"claude", "-p", prompt,
		"--append-system-prompt", system,
		"--output-format", "json",
		// These roles are pure text generation. With the built-in tools
		// enabled the model can spend its single allowed turn on a tool call;
		// the CLI then exits non-zero as error_max_turns and the generated work
		// is discarded. It also prepends ~17k tokens of tool schemas to every
		// call, which is most of what made grading slow (~55s/item -> ~7s).
		"--tools", "",
		"--max-turns", "1",
		// Left to itself the CLI thinks at high effort with no bound, and
		// on an authoring prompt of 80 KB that ran past thirty minutes
		// without a word of text, every time; at low effort the same call
		// finished in 82 seconds. Medium unless the environment says.
		"--effort", effort(),
		"--model", c.ModelFor(role),
	}
	if c.ConfigDir != "" {
		// The CLI's own debug log, per call: on a timeout its tail is the
		// record of what the CLI and the API were doing.
		argv = append(argv, "--debug-file", c.debugFile(role))
	}
	return argv
}

func (c *ClaudeCLI) debugFile(role string) string {
	dir := filepath.Join(c.ConfigDir, "debug")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, fmt.Sprintf("%s-%d-%d.log", role, time.Now().UnixNano(), os.Getpid()))
}

// debugTail is the end of a call's debug file, API lines preferred.
func debugTail(argv []string) string {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == "--debug-file" {
			data, err := os.ReadFile(argv[i+1])
			if err != nil {
				return ""
			}
			var api []string
			for _, l := range strings.Split(string(data), "\n") {
				if strings.Contains(l, "[API") || strings.Contains(l, "retry") || strings.Contains(l, "Retry") || strings.Contains(l, "error") {
					api = append(api, l)
				}
			}
			if len(api) > 8 {
				api = api[len(api)-8:]
			}
			return strings.Join(api, "\n")
		}
	}
	return ""
}

// forget removes a finished call's debug file; the ones that matter are
// the timeouts, which keep theirs.
func forget(argv []string) {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == "--debug-file" {
			os.Remove(argv[i+1])
		}
	}
}

func effort() string {
	if e := os.Getenv("CHIRON_CLI_EFFORT"); e != "" {
		return e
	}
	return "medium"
}

// thinkingBudget caps extended thinking per call (MAX_THINKING_TOKENS),
// the other half of keeping an authoring call inside its deadline.
func thinkingBudget() string {
	if n := os.Getenv("CHIRON_THINKING_TOKENS"); n != "" {
		return n
	}
	return "4000"
}

func (c *ClaudeCLI) Structured(role, system, user string, schema map[string]any,
	schemaName string, out any) error {
	if !c.available() {
		return Errorf("claude-cli: `claude` not on PATH")
	}
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	prompt := user + "\n\nRespond with ONLY a single JSON object matching this JSON Schema - " +
		"no prose, no code fences, no tool use:\n" + string(schemaJSON)

	timeout, ok := roleTimeouts[role]
	if !ok {
		timeout = 4 * time.Minute
	}
	lastErr := "unknown"
	for attempt := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		// The prompt carries learner and corpus text, and on the sprite some of
		// it arrives from a client. It is passed as a single argv element to
		// exec, never through a shell, so there is nothing to inject into: no
		// word splitting, no globbing, no metacharacters. Building a command
		// string and handing it to `sh -c` is what would make this dangerous.
		argv := c.Command(role, prompt, system)
		started := time.Now()
		stdout, err := run(ctx, argv, cliEnv(c.ConfigDir))
		cancel()
		took := time.Since(started).Round(time.Second)

		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("claude-cli %s: %d KB prompt, timed out after %v: %v\n%s", role, len(prompt)/1024, took, err, debugTail(argv))
			return Errorf("claude-cli: timed out (%s) after %v", role, took)
		}
		log.Printf("claude-cli %s: %d KB prompt, %d KB reply in %v", role, len(prompt)/1024, len(stdout)/1024, took)
		forget(argv)
		if err != nil {
			// With --output-format json the CLI reports the cause on stdout,
			// so stderr alone leaves a bare "exit 1".
			detail := strings.TrimSpace(string(stdout))
			if ee, isExit := err.(*exec.ExitError); isExit && len(ee.Stderr) > 0 {
				detail = strings.TrimSpace(string(ee.Stderr))
			}
			lastErr = tail(detail, 400)
			if lastErr == "" {
				lastErr = err.Error()
			}
			continue
		}
		var envelope struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(stdout, &envelope); err != nil {
			lastErr = "unreadable envelope: " + err.Error()
			continue
		}
		if err := unmarshalLoose(envelope.Result, out); err != nil {
			lastErr = "unparseable output: " + err.Error()
			if attempt == 0 {
				prompt += "\n\nYour previous reply was not a valid bare JSON object. " +
					"Return ONLY the JSON object."
			}
			continue
		}
		return nil
	}
	return Errorf("claude-cli: %s", lastErr)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// cliEnv is the environment a CLI call runs with: the server's own, and
// the CLI's update check and other non-essential traffic switched off.
// The check shells out through an npm shim that never returns on the
// sprite (see LESSONS), and four author calls once hung at startup on it.
func cliEnv(configDir string) []string {
	env := append(os.Environ(), "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"MAX_THINKING_TOKENS="+thinkingBudget(),
		// The CLI gives the API two minutes to send a first byte, then
		// aborts and retries; a large authoring request can sit in the
		// API's queue longer than that, and every attempt is then thrown
		// away (the log showed calls ending at exact even minutes, one
		// abort per two). Eight minutes for the first byte, twenty-five
		// for the whole request.
		"CLAUDE_STREAM_FIRST_BYTE_TIMEOUT_MS=480000", "API_TIMEOUT_MS=1500000")
	if configDir != "" {
		_ = os.MkdirAll(configDir, 0o755)
		env = append(env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	return env
}

// run executes one CLI call under ctx, in its own process group so the
// deadline kills the CLI's children with it (an orphan otherwise keeps
// the API call, and the output pipe, alive). WaitDelay then bounds how
// long Wait sits on that pipe. Stderr is kept: on a timeout it is the
// only record of what the CLI was doing (a 529 it kept retrying, say).
func run(ctx context.Context, argv []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.WaitDelay = 15 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) == 0 {
			ee.Stderr = stderr.Bytes()
		} else if ctx.Err() != nil {
			err = fmt.Errorf("%w; stderr: %s", err, tail(strings.TrimSpace(stderr.String()), 300))
		}
	}
	return out, err
}
