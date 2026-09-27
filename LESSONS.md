# Lessons learned

One entry per thing that cost more than ten minutes to learn: the
symptom, the cause, what to do. Add to it in the same commit as the fix.

## iPad app

- **Liquid Glass swallows taps (2026-09-03, again 09-04).** Buttons on a
  view with `.glassEffect()` (plain or `.interactive()`) do not get their
  taps, and the same for a card with buttons on it. Use
  `.buttonStyle(.glass)` on each button inside a `GlassEffectContainer`,
  and material (`.regularMaterial`) for a surface that carries text and
  buttons. The palette UI test is the proof either way.
- **An SF Symbol name that does not exist renders as text (2026-09-04).**
  A `Label` in a toolbar showed its title instead of the symbol:
  `doc.text.badge.plus` is not a symbol. Check a name with
  `swift -e 'import AppKit; print(NSImage(systemSymbolName: "x", accessibilityDescription: nil) != nil)'`.
- **Toolbar items hide a Label's title (2026-09-04).** In iOS 26 bars a
  `Label` shows the symbol only; a titled button needs a `Text` label.
  A `Text` item in a bottom bar gets its own pill and wraps.
- **`hitTest` never sees the touch on the device (2026-09-04).** Deciding
  by `event?.allTouches?.first?.type` worked in the Simulator and let
  nothing through on the iPad. To let a finger scroll under a PencilKit
  canvas, make the canvas the scroll view (`isScrollEnabled`, policy
  `.pencilOnly`) and mirror its offset to the page.
- **Page rectangles are offset by the bar inset (2026-09-04).** With
  `contentInsetAdjustmentBehavior = .always` under a navigation bar, a
  web page's `getBoundingClientRect` is measured from the content's top,
  `adjustedContentInset.top` below the view's top. Convert both ways.
- **A PencilKit canvas inside a WKWebView's scroll view shows strokes late
  (2026-09-03).** WebKit holds touches for the page; strokes appeared on
  lift. Put the canvas over the web view and mirror content size, inset
  and offset.
- **The Simulator draws nothing with the default drawing policy
  (2026-09-03).** No Pencil there; use `.anyInput` under
  `targetEnvironment(simulator)`.
- **XCUI typing into a terminal takes minutes (2026-09-04).** Each
  keystroke waits for the app to idle and the caret never stops blinking.
  Type through the harness; use XCUI for the one key under test.
- **`XCUIApplication.typeText` cannot send control characters
  (2026-09-04).** "\u{03}" arrives as nothing. Send them through the
  harness's `shell/type`.
- **Two sheets in one beat: the second never shows (2026-09-04).** Setting
  the next `sheet(item:)` while the first dismisses loses it. Wait about
  350 ms between them.
- **A ternary between two button styles does not type-check
  (2026-09-04).** `.buttonStyle(cond ? .glass : .glassProminent)` fails;
  branch with `if` around the whole button.
- **`removeLast()` on an empty array crashes the test host silently
  (2026-09-03).** A grep for "passed" hid it. Read the result bundle.
- **New test files need `xcodegen generate` (2026-09-04).** The project
  is generated; a new file is not in it until then, and the count of
  passed tests stays where it was.
- **Date fixtures depend on the timezone (2026-09-03).** Use noon UTC.
- **`NWListener` needs its handler before `start` (2026-09-02).** Otherwise
  EINVAL; and `.any` port, `acceptLocalOnly` and the loopback interface
  all fail; pick a random port and check the peer is loopback.
- **SwiftTerm's Metal shaders need the Metal toolchain (2026-09-02).**
  `xcodebuild -downloadComponent MetalToolchain`, and builds need
  `-skipPackagePluginValidation`.
- **Confirmation dialogs on iPad have no cancel button (2026-09-03).**
  They are popovers; a UI test dismisses with `PopoverDismissRegion`.

## Server and sprite

- **The first request to a sleeping sprite fails (2026-09-24).** The
  platform wakes the sprite on the first connection and that connection
  is refused, or the gate answers 502 while the server comes up; the
  reader saw "unable to reach the server" and had to send the primer
  twice. `Sync` now sends a request that never reached the server again,
  a few times, a moment apart (connection refused, DNS, 502/503/504),
  and never one that timed out or was answered: a capture sent twice is
  two drafts.
- **A "Teach me" book is invisible until it is written, and a dead
  connection to GitHub makes the writing look stuck (2026-09-27).** The
  shelf lists a generated book only after every unit is on disk; while
  the job runs, only the "Teach me something else" screen shows it
  (`GET /teach/jobs`), so a reader who goes back to the shelf sees nothing.
  A Macaroons primer asked for there sat at `planning` for an hour with
  an empty `corpus-<slug>` dir: the finder was fetching the Rust book's
  eighty chapter files from raw.githubusercontent.com for their titles,
  and the HTTP/2 connection went dead (peer stopped acking; `ss -tnpio`
  showed backoff 13 on the server's socket). Go's client timeout ends each
  request after 60s but keeps reusing the dead connection, so every file
  costs a minute until the kernel drops the socket (`tcp_retries2`, about
  eighteen minutes), a new one works for a few files, and it dies again.
  Symptoms: no log line at all, no `claude` process, `chiron status
  <slug>` stays `planning`, `sources.yaml` never appears. Fix to make:
  the fetch client should not reuse a connection a request timed out on
  (HTTP/1.1 only, or a ping timeout on HTTP/2). Deploying the server
  restarts it and kills the running job, so not while one is running.
- **`sprite-env services create` keeps only the last `--env`
  (2026-09-02).** Two flags left the book server without its key and open.
  One flag, comma-separated.
- **No `--needs` between services (2026-09-03).** It blocks restarts.
- **The sprite user's shadow entry is locked (2026-09-02).** sshd refuses
  key auth until `sudo usermod -p '*' sprite`.
- **`claude --update` hangs on the sprite (2026-09-04).** It shells out to
  `npm -g config get prefix` through the nvm shim. Reinstall with
  `curl -fsSL https://claude.ai/install.sh | bash`.
- **A shell with no locale draws ASCII (2026-09-04).** Claude Code showed
  "__" for its glyphs; the client rendered fine. `export LANG=C.UTF-8` in
  the sprite's `.bashrc`, and `tmux set-environment -g LANG C.UTF-8` for
  the running server.
- **Go installs under `/.sprite` without GOBIN (2026-09-03).** Set it.
- **One heavy job at a time on the sprite (2026-09-03).** 8 GB, serving
  the book; `go test -p 2`, `CHIRON_RENDER=0`, no build beside a test run.
  Checkpoint (`sprite-env checkpoints create`) before anything risky: the
  platform restored an older overlay three times in two days.
- **The Anthropic API now requires `additionalProperties: false` on every
  object in a structured-output schema (2026-09-03).** `llm.strictSchema`
  adds it.
- **An OAuth token gets 429 for Opus and Sonnet on the raw API
  (2026-09-04).** Haiku answers. `CHIRON_PROVIDER=claude-cli` runs the
  models through headless Claude Code instead; PATH must be in the service
  env.
- **`op read` times out when non-interactive (2026-09-02).** Automation
  passes the key as `CHIRON_KEY`.
- **`pkill -f` matches your own ssh command line (2026-09-03).** Anchor
  the pattern.
- **A test that gates every model call deadlocks a new call in front of
  it (2026-09-04).** `gatedChain` now gates the author only.

- **A debounce that resets on every change can wait forever
  (2026-09-06).** The annotation push was rescheduled by each position
  report as the page settled, so the phone never pushed its highlight;
  the iPad, which had not scrolled, did. A push is now due once per pause
  and sends whatever is current.
- **The CLI model writes about 33 tokens a second, and a depth variant
  of a long chapter is a 10k-token reply (2026-09-05).** Three units of
  the first sourced book timed out at fifteen minutes on deeper-math.md,
  three runs in a row; the author's limit is now thirty. And only the
  canon call needs the source material: the later five files are written
  from canon.md, and carrying 30k tokens of sources into them made every
  call slower for nothing.
- **A failed `go test` hidden behind a pipe (2026-09-05).** `go test ./x |
  tail -1 && git push ...` pushed a failing test because the pipe's status
  is tail's; the sprite's `make deploy` then refused, and a book resumed
  on the old binary. Gate on `go test` itself, never on a pipe.
- **The served corpus on the sprite is a copy, not the checkout
  (2026-09-05).** `/home/sprite/chiron/corpus` was copied once in August;
  `make deploy` swapped only the binary, so the new source index was not
  there and the first sourced book planned from the brief alone. Deploy
  now copies the files the server reads at build time (the authoring
  contract, `corpus/sources/index.yaml`); unit files stay as they are.
- **A primer has no chapter until something opens it (2026-09-05).**
  `GET /chapter/{id}` is nil for a ready primer nobody has read; the app
  opens it with `POST /exchange {phase: start}`, which makes the first
  unit current. `chiron read` does the same when it finds nothing.
- **A dev server needs the corpus's `authoring-spec.md` to build a book
  (2026-09-05).** A config with made-up corpus paths captures and writes
  primers, and fails a book draft at planning with that file's name.
- **A book draft fails with "claude-cli: timed out (planner)" (2026-09-05).**
  The planner is one `claude -p` call that emits the whole syllabus and
  the misconception bank as JSON; under the claude-cli provider its
  deadline was 5 minutes and a reference-depth brief blew through it
  three builds in a row, after 2-3 minutes of source resolution. Passage
  size is not the lever: a half-size passage failed at the same step. The
  planner now has the author's 15 minutes (`llm/claudecli.go`,
  `roleTimeouts`). The log line is the only symptom; nothing under the
  corpus dir is written before planning succeeds except `sources.yaml`.
- **A finished book fails with "generated corpus did not load" and the
  corpus lints with a duplicate `check` key (2026-09-06).** The pass that
  adds `check: choice` to bare MCQs looked for an existing check line only
  up to the first blank line, and a prompt written as a block scalar has
  one between paragraphs, so an author-supplied check below the prompt was
  missed and a second one inserted above it. The scan now ends where the
  indent drops back (`generate.go`, `withChoiceChecks`). To find the file:
  `go run ./cmd/corpus-lint <corpus dir>` on the sprite. A rebuild after
  this failure does not reuse the corpus (the failed job holds the slug, so
  the next build takes `-2` and re-plans); repair the file, lint, and
  `sprite-env services restart chiron-server` so discovery registers it.

- **`simctl openurl` for a custom scheme waits behind an "Open in
  Chiron?" alert (2026-09-05).** The app's `onOpenURL` never fires until
  it is tapped, which looks like a dead handler. `idb ui describe-all`
  shows the alert; `idb ui tap` on its Open button delivers the URL.
  Several opens stack several alerts.
- **A `.confirmationDialog` or `.alert` on a button inside a `Menu` or
  `.contextMenu` never shows (2026-09-05).** The menu's content is torn
  down as it closes, and the state that would present the dialog goes
  with it: "Delete shelf" did nothing on the iPad. Keep the presenting
  state and the modifier on the view that stays on screen; the menu item
  only flips the binding. The harness verb had bypassed the menu, so a UI
  test now goes through it.
- **XCUITest drags a `.draggable` card only if the press is short
  (2026-09-05).** A card with both `.contextMenu` and `.draggable` opens
  its menu at about a second; `press(forDuration: 0.6, thenDragTo:,
  withVelocity: .slow, thenHoldForDuration: 0.8)` lifts the drag first.
  And a failed assertion with `continueAfterFailure = false` skips Swift
  `defer`: clean up in `tearDown`, and at the start of the test.
- **A VStack with `maxHeight: .infinity` centres its overflow
  (2026-09-05).** The shelf lost its header at the top once the cards
  outgrew the screen; a ScrollView is what a growing list needs.
- **Nothing on the sprite keeps it awake for the dev agent
  (2026-09-24).** A request sat in `make test` for three hours: the
  sprite hibernates when no one is connected, and its processes freeze
  with it. Tried and failed: a service on the sprite, a loop curling the
  sprite's own public URL, and Truman curling `/ping` every 20 seconds
  from outside. The only thing that kept it running was a live `sprite
  exec` session printing a line every ten seconds. The frozen time still
  counts on the wall clock, so `go test`'s ten-minute limit fired in
  `httpapi` (621 s) and failed a request that had not touched Go. The
  runtime has a documented hold for exactly this: a task, made on
  `/.sprite/api.sock` (`sprite-env curl -X POST /v1/tasks -d
  '{"name":"x","expire":900}'`, at most an hour, renewed with PUT,
  dropped with DELETE, expiring on its own if the holder dies). The
  agent holds one per request (`devagent/awake.go`). Short HTTP pings
  do not count as activity between them; a task does.
- **The agent's Swift is never compiled on the sprite (2026-09-24).**
  The sprite has no Xcode, so `make test` there proves the Go side only.
  Request 1 came back with a main-actor `static var` used as a default
  argument (does not build) and new keys written into `Info.plist`,
  which XcodeGen regenerates from `project.yml` - they vanished on the
  next `xcodegen generate`. An app change from the agent is untested
  until the Mac builds it; plist keys go in `project.yml`.

## Mac side

- **xcodebuild will not register a new device (2026-09-05).** With a
  phone plugged in for the first time it fails with "No Accounts: Add a
  new account in Accounts settings" and "provisioning profile doesn't
  include the currently selected device", even with both Apple IDs signed
  into Xcode and outside the sandbox. Open the project in Xcode, pick the
  device as the run destination, and Run once: that registers it and
  refreshes the profiles; `xcodebuild` works from then on.
- **The Mac build is the same target (2026-09-17).** Mac Catalyst on the
  app, the share extension and the test bundles; `scripts/mac-run.sh`
  builds, launches and runs the unit suite on the Mac. The whole app
  compiled first time, one thing excepted: VisionKit's QR scanner is not
  built for the Mac, so a setup link also travels as pasted text. Three
  things only showed up at run time. `xcodebuild` signs a Mac build only
  with an account signed into Xcode plus `-allowProvisioningUpdates
  -allowProvisioningDeviceRegistration` (the Mac counts as a device); the
  iOS profiles on disk had hidden that the account was signed out. An
  Xcode update drops the Metal toolchain that SwiftTerm's shader needs,
  so every build, Simulator included, fails with "cannot execute tool
  'metal'" until `xcodebuild -downloadComponent MetalToolchain` (839 MB).
  And the keychain: macOS has two, and `kSecAttrAccessible` is refused by
  the file keychain, so `kSecUseDataProtectionKeychain` says which (a
  no-op on iOS, where there is only the one).
- **Xcode 27 has no Simulator.app where it was (2026-09-19).** `open -a
  Simulator` fails, and under `set -e` that took `sim-run.sh` down after
  a successful build. Simulators boot and run headless without it; the
  open is now best effort. The sim server also kept readings and
  primers beside /tmp rather than under its own state dir, since the
  server's defaults are relative to the config's parent: the throwaway
  config now names all three directories.
- **The app builds Debug only (2026-09-19).** `xcodebuild archive`
  defaults to Release, and the Release build has never compiled: the
  harness under `#if DEBUG` names members that only exist in Debug.
  Every build on a device has been Debug, which is also where the
  harness and the agent link live, so `mac-build.sh` archives Debug.
  Making Release build is a separate job, not a prerequisite.
- **macOS's `/bin/bash` is 3.2 and an apostrophe inside `${1:?...}`
  breaks it (2026-09-19)**, and bash 5 too: "unexpected EOF while
  looking for matching `'`" pointing at a line far below. A double
  quote inside single quotes inside `$( )` trips 3.2 as well. Scripts a
  person runs on a Mac get checked with `/bin/bash -n`, not just `bash`.
- **The development agent on the sprite handled its first request end
  to end (2026-09-20).** A tap on "Request a change" in the app queues a
  file under `state/requests/`; `chiron-dev-agent` makes a worktree off
  main under `~/src/work`, runs Claude Code on the request there, then
  `make test`, fast-forwards main, `make deploy` if the server changed
  and `make app-build` if the app did. A docs-only request needs neither
  deploy nor build: the suite passes and main moves, and that is the
  whole loop.
- **A service without `NSRequiredContext` is registered and never shown
  (2026-09-17).** `pbs -dump_pboard` listed "Send to Chiron" with every
  key it needs, `NSPerformService` ran it, and no app's Services menu
  offered it, Chrome or TextEdit. The one difference from an entry that
  shows (Bear's) was an empty `NSRequiredContext = {}`; with it the item
  appears everywhere. Chrome has no share sheet for a selection at all,
  so the Services menu is the only way selected text reaches Chiron
  from it.
- **`sim-server.sh` died silently on a fresh /tmp (2026-09-17).** The
  donor search piped an `ls` of nothing under `set -o pipefail`, so
  after a reboot the first server never started and printed nothing. The
  pipeline now tolerates having no donor.
- **ssh tries IdentityFiles in config order, `Host *` included
  (2026-09-04).** A catch-all with the YubiKey identities above the sprite
  stanza prompted for the YubiKey first. Put specific hosts above `Host *`.
- **The 1Password prompt on `ssh chiron` is the tunnel reading the key
  (2026-09-04).** Expected; the YubiKey prompt was not.

- **A rewritten unit file does not reach the sprite by deploying
  (2026-09-06).** `make deploy` carries the binary, the spec and the source
  index; unit files stay because the served corpus is the live book. After
  editing a bank in the checkout (`corpus-taps` on the calibration units),
  check the served copy still matches the version you started from, back it
  up under `~/backups/<date>/`, copy the file over, and restart
  `chiron-server` - it reads units at start. Generated books live only on
  the sprite: copy the unit down, run the tool with
  `-spec corpus/authoring-spec.md`, copy it back.

- **A new Swift file needs `xcodegen generate` before it builds
  (2026-09-06).** `Chiron.xcodeproj` is generated from `project.yml`; a
  file added on disk is not in the project until then, and the error
  reads as "cannot find type in scope" from the first file that uses it.
  Test fixtures live under `fixtures/` as a folder reference: load them
  with `url(forResource: "fixtures", withExtension: nil)` and append the
  name, not `forResource: "name"`. A crash in a test class's stored
  property initialiser takes the whole runner down ("early unexpected
  exit ... at DocumentTests.init").
- **The palette UI tests need the dev server's placed learner
  (2026-09-06).** `PaletteUITests` open `ai` on `:8084` and expect a
  chapter with the palette; a server started on a fresh state dir puts
  the book on the screener and three tests fail on the pen button. Run
  the dev server on `ipadbase/config.yaml`, whose state is past
  placement, and use a copy of the config only for throwaway walks.

- **PDFKit asks for a page's overlay only if the provider is set before
  the document (2026-09-06).** With `pageOverlayViewProvider` assigned
  after `document`, `overlayViewFor` is never called and nothing shows,
  with no error. Set the provider first. And a test that returns a
  `Library` from a helper and drops it (`let (_, doc) = ...`) silently
  loses every `[weak self]` push: keep the library bound until the end.
- **A stored chapter is a snapshot with its items baked in
  (2026-09-06).** The server keeps `state/<subject>/chapters/<unit>.json`
  from the moment a unit is first opened and serves it on every open
  until the unit is cleared, so rewriting `questions.yaml` changed
  nothing on the iPad for a unit already begun (the Agent Auth intake
  kept its prose items). Chapters now carry `bank_hash`, a fingerprint of
  the bank they were built from, and a mismatch drops the snapshot so the
  next open rebuilds it; chapters stored before the hash existed are kept
  as they are, so after rewriting a bank by hand, move the unit's stored
  chapter aside (`~/backups/...`) for any unit in progress.
- **The correct option was always A (2026-09-06).** Authors, human and
  model, write the right option first. Options are now shuffled at load
  in an order fixed by the item's id (`corpus.Shuffled`), in both places
  a beat is parsed (the corpus loader and the renderer), so delivery and
  grading agree without the files changing; the screener keeps its
  authored novice-to-expert order. Never write an option that refers to
  another's position.
- **Models put LaTeX in double-quoted YAML (2026-09-06).** `"\sqrt{d}"`
  is an unknown escape and the whole block fails to parse. The rewrite
  prompts now say single quotes or a block scalar; the tools skip the one
  bad item and report it, so a rerun picks it up.
- **zsh does not word-split `$var` (2026-09-06).** `for d in "a b c"; do
  set -- $d` leaves `$1` as the whole string; the device install loop
  built nothing and `devicectl` complained about a missing path. Use
  `${=d}` or write the commands out.
- **A dropped chapter snapshot did not reach the iPad (2026-09-06).**
  The app keeps its own copy of the chapter for reading detached and, with
  the server reachable but answering "no chapter, not writing", showed the
  copy. It now treats that answer as "the server dropped it" and asks for
  the chapter afresh. The fingerprint also covers the beats, since a beat
  rewrite changes what a chapter bakes in as much as a bank rewrite does.
- **PDFKit overlays are hit-tested only in markup mode (2026-09-07).**
  A `PDFPageOverlayViewProvider` canvas came up and showed ink the
  harness injected, but on the iPad a Pencil stroke scrolled the page and
  a long press selected nothing: with `isInMarkupMode` false PDFView keeps
  every gesture for itself, and the overlays never see a touch. Markup
  mode is on for the pen and eraser, off for the select tool, since in
  markup mode PDFView's own text selection is off. The Simulator could not
  show this because the harness's stroke verb writes into the canvas
  directly; `DocumentUITests` now drags and long-presses through XCUITest.
- **A timed-out CLI call took 108 minutes to return (2026-09-07).** Four
  author calls hung at startup (no session file ever appeared; the CLI's
  update check goes through the npm shim that never returns here), the
  thirty-minute deadline killed them, and `cmd.Output()` then sat on the
  output pipe until the children they had spawned let go. `run` now sets
  `WaitDelay`, so Wait gives up on the pipe fifteen seconds after the
  kill, and every call carries `DISABLE_AUTOUPDATER=1` and
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`. A unit that failed this
  way is re-authored by running the same `chiron teach` again: files on
  disk are kept, only the missing ones are written.
- **Every author call stalled for a day, and it was extended thinking
  (2026-09-08).** After the first few calls of a run, each CLI call opened
  its stream within a second and then delivered only thinking events for
  thirty minutes, never a word of text; the 20 KB cut of the same prompt
  finished in 23 s. Not the network (the sockets were live), not auth, not
  input size (20, 40 and 81 KB inputs all streamed at 150 chars/s), not
  the sprite's hooks or stale credentials (ruled out by a config dir of
  the server's own, which stays). The CLI left to itself thinks at high
  effort with no bound, and an 80 KB "write a whole unit" prompt is a task
  it will think about indefinitely. Every call now names `--effort`
  (medium; `CHIRON_CLI_EFFORT`) and caps `MAX_THINKING_TOKENS`
  (`CHIRON_THINKING_TOKENS`); at low effort the stalled call finished in
  82 s. How to see it next time: replay a hung process's argv and
  environment from /proc by hand with `--output-format stream-json
  --include-partial-messages` and count text deltas per 30 s.
- **The first PDF-built book would not load: prose in plain YAML scalars
  (2026-09-08).** Thirteen of eighteen misconception files failed to
  parse: a value opening with a quotation mark, a colon and space inside
  a sentence, a continuation line starting with a dash. The prose was
  fine. `generate.RepairYAMLProse` rewrites such values as block scalars
  when a written file does not parse, and `corpus-lint -repair` does the
  same for files on disk; unit-local misconception ids are lowercased
  everywhere, since the author wrote U11-M1 in one file and u11-m1 in
  the next. A build that says "generated corpus did not load" is
  diagnosed by copying the corpus down and running `corpus-lint` on it.
- **The Simulator borrows the Mac's keyboard (2026-09-08).** A test that
  taps a text field and types passes whether or not the software keyboard
  ever appeared: XCUITest types through the hardware keys. Any test about
  the keyboard needs `defaults write com.apple.iphonesimulator
  ConnectHardwareKeyboard -bool false` first, which `sim-run.sh test` and
  `sim-run.sh fast` now do. `GCKeyboard.coalesced` is no help there: the
  Simulator reports the Mac's keyboard either way. On a device it is the
  signal, since iPadOS shows no software keyboard while one is attached.
- **Test runs, not builds, are what cost (2026-09-08).** An incremental
  simulator build is 3-9 seconds; the full suite is 183. Of that, the 85
  unit tests are 9 seconds and the 8 UI tests are the rest. `sim-run.sh
  fast [test id ...]` builds once with `build-for-testing` and then runs
  `test-without-building` against the built products: 13 seconds for the
  whole unit target, 39 for one UI test instead of 70. Use it for the
  red-green loop and keep `sim-run.sh test` for the run before a commit.
  `test-without-building` writes no bundle into the scheme's log
  directory, so it must be given `-resultBundlePath`; reading the newest
  bundle there reports the previous full run's verdict.
- **UI tests: the app is shared, the waits are not fixed (2026-09-08).**
  Every UI test now builds on `HarnessTestCase`: one app per class rather
  than one per test, and `waitUntil`/`waitFor` polling the harness instead
  of `Thread.sleep`. That took the UI target from about 174 seconds to 143
  and turned bare timeouts into failures that carry the app's state. Two
  things it uncovered: tests in a class share the page, so a beat's field
  keeps what an earlier test typed (`openBook` clears it), and an app
  container with nothing in it cannot reach a server from the launch
  override alone, since the shelf and the exchange both go to the *saved*
  server (the base class saves it, as the settings screen would).
- **A stale app answers for a new one (2026-09-08).** Simulators share the
  Mac's loopback, so an app left running in any booted simulator holds the
  harness port and answers a test that just launched its own. Every launch
  now carries `harness_nonce=` and the state carries it back; the runner
  terminates the app in every booted simulator first. Without the nonce
  this shows up as a test that cannot find the palette in an app that
  looks connected and healthy.
- **Parallel UI testing does not pay at this size (2026-09-08).**
  `sim-run.sh parallel` exists and works: a dev server per worker
  (`sim-server.sh start <port>`, seeded from an existing sim state), a
  harness port per clone. But with 8 UI tests the clone setup costs about
  what the split saves (144 seconds either way), and two simulators
  contending for the Mac drop touches: `canvas_touches` stays 0 and drags
  never land. Keep it for when the suite is long enough to win.
- **A blog post is not marked up as an article (2026-09-20).** Reading a
  page in Chiron meant pulling the writing out of the page, and the
  obvious route, `<article>` or `<main>`, is not there on most sites:
  simonwillison.net has neither (its post is a `div.entry`), so the first
  cut returned the masthead, the sponsor line and the box of recent
  posts along with the piece. What works is the old readability trick,
  in `sources/page.go`: score every container by the prose in it (a
  paragraph is worth 1, plus its length and its commas, and half of that
  to the grandparent), discount by link density so a list of other posts
  loses, then lift or sink it by what the site calls it (`entry`, `post`,
  `content` up; `sponsor`, `recent`, `footer` down), and prune the named
  furniture out of whatever wins. Two smaller things fell out of it: a
  page's title comes from its own `h1` before `og:title`, since `og:title`
  often carries the site's name too, and the heading the article repeats
  has to be dropped at whatever level it is written or the chapter shows
  its title twice. Pages that render their body in JavaScript (Mintlify
  docs, say) still come back nearly empty; that needs a headless browser,
  not a better heuristic.
- **A picture kept from the web is often a webp (2026-09-20).** The
  imported-book path re-encodes pictures with Go's image package, which
  cannot decode webp, so it keeps them as they came - and the asset route
  answered `image/jpeg` for everything it did not recognise, which the
  reader will not draw. `contentTypeOf` now names webp and avif.
- **A feed reader's unread count is about the chapter, not the post
  (2026-09-20).** The obvious rule - a post is unread until you open it,
  compare the read time to the post's date - is wrong the moment several
  short posts share a chapter: the week of notes gains a note that was
  published before you last read the week, and the count stays at zero.
  Each chapter carries `updated`, set whenever anything is written into
  it, and unread is "read before it last changed". The marks and the
  stamps are RFC3339Nano and compared as times, not as strings: nano
  timestamps drop trailing zeros, so ".5Z" sorts after ".50001Z".
- **Atom and RSS are one struct (2026-09-20).** They name the same
  things differently (`entry`/`item`, `updated`/`pubDate`,
  `content`/`content:encoded`/`description`, `id`/`guid`), so one struct
  with both spellings side by side reads either, taking whichever field
  is there. The catch: leave `XMLName` off the root struct or the
  decoder refuses `<rss>` while expecting `<feed>`, and set
  `Strict = false` with a pass-through `CharsetReader`, since feeds in
  the wild declare encodings Go does not carry.
- **A sheet is its own environment, and on the Mac its own window
  (2026-09-21).** `ConnectionSettings` read `@EnvironmentObject var
  library`, and the sheet that presents it never handed it one. On the
  iPad that works - the sheet inherits the screen's environment - so it
  went unnoticed for weeks; on Mac Catalyst the sheet is hosted
  separately, the lookup fails, and SwiftUI traps: `EXC_BREAKPOINT` in
  `EnvironmentObject.error()`, with nothing in the backtrace but the
  view's own `body`. The gear icon killed the app on the Mac, and so
  would the setup sheet inside it and the reader's contents. Every
  presentation now hands over what its view asks for. The guard is
  `ChironUITests/SheetsUITests`, which opens each sheet and asserts the
  app is still running - and it only catches this **on Catalyst**:
  `xcodebuild test -destination 'platform=macOS,variant=Mac Catalyst'
  -only-testing:ChironUITests/SheetsUITests`. In the Simulator it passes
  either way.
