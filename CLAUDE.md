# Chiron

An adaptive textbook: a Go server (`server-go/`), two corpora (`corpus/`
is the subject `ai`, `corpus-v2/` is `data`), and an iPad app
(`ipad-app/`, iOS 26, built only on a Mac with Xcode).

## If this session is on the sprite

You are on the machine that serves the book. Facts that matter:

- The live server is `chiron-server` on `127.0.0.1:8081`, behind
  `chiron-gate` on `0.0.0.0:8080` (the public URL). Its state under
  `/home/sprite/chiron/state` is Matt's real learner record. Never point
  a test or a script at 8080 or 8081.
- A throwaway server for experiments: from `server-go`,
  `CHIRON_DRIVE=1 CHIRON_TRANSCRIBE=stub go run ./cmd/chiron-server -addr :8084 -config <a copy of config.yaml with its own state dirs>`.
  Drive mode grades mechanically, serves cached chapters and stubs
  handwriting, so no model budget is spent.
- Deploy is `make deploy` from this checkout; it keeps a dated copy of the
  previous binary in `/home/sprite/chiron/bin` for rollback. `make
  deploy-gate` replaces the way in; do it from tmux.
- The iPad app is driven with `chiron-app` (`chiron-app state`,
  `chiron-app open subject=ai`, `chiron-app shot page.png`); it works only
  while the reader has the agent switched on in the app's server settings.
- Services are `sprite-env services ...`; logs under `/.sprite/logs/services/`.
- The sprite has 8 GB and serves the book while you work. Run one heavy
  thing at a time (`make test` uses `-p 2` and `CHIRON_RENDER=0`, so no
  Chromium; do not run a build beside it).
  On 2026-09-03 the sprite twice came back from a platform storage fault
  restored to an older checkpoint; a checkpoint you made yourself is what
  limits the loss. Before anything risky: `sprite-env checkpoints create`.
- `claude --update` hangs here: it shells out to `npm -g config get prefix`
  through the sprite's nvm shim and never returns. Update with
  `curl -fsSL https://claude.ai/install.sh | bash` instead.
- Never push to GitHub unless Matt asks in the conversation, even though
  the credential here allows it.
- `chiron-dev-agent` (a service; `make deploy-agent`) takes change
  requests the app sends (`state/requests/`) and runs Claude Code on them
  in a worktree under `~/src/work`, then `make test`, fast-forwards
  `main`, `make deploy` if the server changed, `make app-build` if the
  app did. Matt's tap on "Request a change" is the permission for that
  deploy. A failed request keeps its worktree for a person; `git
  worktree list` shows them. When Claude Code would be guessing it ends
  with `QUESTION: ...`; the request waits (status `waiting`, the
  wrench shows a badge), Matt answers in the requests card (`POST
  /dev/requests/{id}/answer`), and the request is queued again into the
  same worktree with the questions and answers in its brief. While it works a request, the agent holds
  the sprite awake with a runtime task (`sprite-env curl /v1/tasks`
  lists it as `request-<id>`); nothing else keeps a sprite from pausing
  when no one is connected.

## Everywhere

- `LESSONS.md` holds what cost more than ten minutes to learn. Read it
  before debugging; add to it in the same commit as the fix.
- Tests: `make test` (Go and the page script); corpus lint: `make lint`.
- `corpus-taps <unit dir>` (from `server-go`, `go run ./cmd/corpus-taps`)
  rewrites a bank's prose items as tap-answered ones, and with `-beats`
  a chapter's prose beats as choices (canon.md and every depth);
  `corpus-calibrate <book dir>` gives a built book its placement unit.
  `corpus-v2` and generated books have no spec copy, so pass
  `-spec corpus/authoring-spec.md`. A stored chapter of a unit in
  progress is dropped when its bank changes; nothing else to clear.
- App tests and Simulator walks need the Mac: `scripts/sim-run.sh test`,
  `scripts/sim-verify.sh`. The Mac runs the same app as a Catalyst build:
  `scripts/mac-run.sh` (build, run, test, harness).
- Plans and status: `IPAD-PLAN.md`, `SPRITE-DEV-PLAN.md`. Open material
  the book builder may adapt, with licences and fetch recipes, and the
  design for building from it: `OPEN-SOURCES.md`.
- `chiron` (`go install ./cmd/chiron` from `server-go`) drives the live
  server from a shell: shelf, captures at every scale, plan, build, wait,
  read, `page URL` (a page on the web read here), `follow URL` (a blog by
  its feed) and `feeds [-check]`. `skills/chiron/SKILL.md` is the agent skill that uses it; link it
  with `ln -sfn $PWD/skills/chiron ~/.claude/skills/chiron`.
