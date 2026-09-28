# Chiron for iPad: the plan

Written 2026-09-01 after the Paper Pro turned out to be the wrong device for
this book. This document is meant to be executed cold after a context
compaction: every fact needed to start is here or in a file it names.
Research behind the design choices: `ipad-app/RESEARCH-ipados.md`.
Design lessons that carry over from the e-ink client: `DESIGN-ui.md`.

## 0. Where things stand (measured, not assumed)

**There is already an iPad app.** `ipad-app/` is a SwiftUI app (xcodegen
project, `project.yml`, deployment target iOS 15.0, iPad only, bundle id
`dev.mjbraun.chiron`, signed by the developer team in `CHIRON_TEAM_ID`). About 2,300
lines in eleven files:

| file | what it owns |
|---|---|
| `App.swift` | `ChironApp`, `ContentView` (screen switch + top-right chrome), `LibraryView`, `StartView`, connection settings/server editor, `ConnectionBadge`, `GeneratingOverlay` |
| `AppModel.swift` | the state machine (`Screen` enum: menu/start/reading/pretest/check/gate/takingBreak/teach), per-subject persistence in Documents, the exchange flow (`run`), start-over, static offline book |
| `Models.swift` | Codable wire models: `ChapterPayload`, `CheckItem` (+`Reveal`), `ItemResponse`, `BeatResponse`, `SubjectInfo`, `ExchangeRequest/Response`, `GradeResult`, `Gate`, `BookState`, `SpineEntry`, `BreakSuggestion`, `JSONValue` |
| `Sync.swift` | HTTP exchange with bearer auth, 600 s timeout, `CHIRON_SERVER` launch-env override, plus a USB-bridge listener on :8081 (flight-era, see roadmap) |
| `ReaderView.swift` | `ReaderContainer` (bottom bar: Catch me up / Take the check / Skip) + `ReaderView` (`WKWebView` loading `Resources/chapter.html`, JS bridge `bridge`, `initChapter(json)`) |
| `MathText.swift` | prompts/options with `$` math render in a per-view `WKWebView` with KaTeX, height measured by JS; plain text otherwise |
| `CheckViews.swift` | `ItemFlowView` (one item at a time, confidence slider BEFORE reveal, commit, "I don't know", per-item reveal), `GateView`, `BreakView`, `SpineView` (sheet, progress, debt, start-over) |
| `Servers.swift`, `Credentials.swift` | saved servers list, Keychain-held shared key |
| `SelfTest.swift` | launch-argument harness: `selftest`, `selftestweak`, `showreader|showcheck|showpretest|showbreak [unit]`, `showteach [demo]`, `server=URL` |
| `TeachView.swift` | "Teach me" conversation (v2 feature; untouched by this plan) |
| `Resources/` | `chapter.html`, `book.css` (paper + dark themes, 19px serif, 44em column), `book.js` (chapter render, beats, mechanical grading mirror of `checkers`), `katex/` (folder reference - must stay a folder or math dies), `default-book.json` (bundled offline book) |

**It builds today** on Xcode 26.6 for the iOS 26.5 simulator (`chiron-ipad`,
device type iPad (A16)). `scripts/sim-run.sh` builds, installs, launches with
`SIMCTL_CHILD_CHIRON_SERVER`, and screenshots.

**Baseline run on 2026-09-01** (fresh two-subject server on :8084, app
launched with `selftest`):

1. `/subjects` decode fails silently and falls back to one built-in subject.
   Cause: the response gained a top-level `active` string (bookshelf work)
   and the app decodes `[String: [SubjectInfo]]`. Client fix, Phase 0.
2. The screener exchange failed to decode: MCQ reveal options omitted
   `"correct": false`. Server bug from the Go port, fixed in `e8b9403`
   (`corpus.RevealOption` no longer `omitempty`), regression test in
   `render/render_test.go`. Real responses saved as fixtures:
   `ipad-app/fixtures/exchange-start.json`, `exchange-screener.json`.
3. After that fix the app reaches the calibration series, but the screener
   is presented as a free-text item (it is `kind: constructed`, `check:
   screener`, with `options`), and `SelfTest` expects a gate after every
   check - the gateless screener step postdates it.
4. Everything the tablet client learned to do since July is absent: native
   placement screen, calibration-series presentation, typeset results with
   READ AS / WHY, contents page, bookshelf, reopen-on-last-book, break
   flows tied to server pacing, error/loading copy, "Start over" from the
   book, authoring wait.

**Server protocol the iPad uses** (unchanged, synchronous, JSON):
- `GET /subjects` -> `{subjects: [{id,title,units_total,units_cleared,current_unit,debt}], active}`
- `POST /exchange {subject, phase: start|boundary|pretest, unit, check_responses[{item_id, response|selected_index, confidence, idk}], pretest_responses, beat_responses, override, skipped_check, catch_me_up, choice, chunk_minutes, break_minutes}`
  -> `{results[{item_id, verdict, feedback_md, misconceptions, confidence}], gate|null, chapter|null, state, break_suggestion|null, results_pages?}`
- `chapter`: `{unit, title, minutes, html, beats[], pretest[], check[], calibration, next_action}`;
  check items: `{id, unit, concept, kind: constructed|mcq, prompt, check: llm|exact|numeric(t)|choice|screener, difficulty, options[{text}], reveal{answer,rubric | options[{explain,correct}]}}`
- `GET /state?subject=` -> `{spine[{unit,title,status,score,in_fringe}], fringe, debt, active_misconceptions, summary, session_minutes, llm}`
- `POST /reset {subject, confirm: true}`; `GET /health`, `GET /ping`
- `GET /pages/{subject}` (meta) marks the subject active server-side; the
  iPad does not use rendered pages, so it needs another way to mark the
  book open (Phase 1, server change A).
- The ink path (`POST /ink/{subject}`) is async-authoring; the JSON path is
  not. A graded exchange can take a couple of minutes while Opus writes the
  next chapter.

**Devices.** Test iPad: iPad mini 4 (iPad5,1), iPadOS 15.8.8, 7.9" at
768x1024 pt (@2x), A8, 2 GB RAM, **no Apple Pencil support** (so no
Scribble, no pencil-only ink). Full screen it is regular x regular; in Split
View it is compact. Signing/device notes are in the project memory
(`dynamic-book-project.md`): profiles from the Fly team last a year; the
device must be registered in the team; `ios-deploy` and `idevicesyslog` are
installed.

**Toolchain.** Xcode 26.6 (17F113), only the iOS 26.5 runtime installed.
Apple's runtime index still offers iOS 15.5 for Xcode <= 26.99; Xcode 27
will probably drop it. `xcodegen`, `idb` + `idb_companion` are installed.
macOS has no `timeout`; use a background process + `kill`.

## 1. Goals and non-goals

Goals, in priority order:
1. The iPad is the primary reading device for both books, with everything
   the Paper Pro client can do, done the iPad way.
2. Runs on the mini 4 (iOS 15.8) and stays correct on current iPadOS.
3. All development and verification in the Simulator, driven by scripts,
   with an iOS 15.5 simulator as the gate and a device pass at milestones.
4. Multiple books in progress is designed for now, built later (section 8).

Non-goals for this plan: the USB bridge and Wi-Fi soft-AP transports
(flight era; delete when convenient), Teach-me changes, the bundled offline
book (keep it loading, do not extend it), App Store anything.

## 2. Design principles for the iPad client

Carried over from `DESIGN-ui.md` (they were learned the hard way):
- Anything with typography or math is rendered content; anything the
  learner operates is a native control producing exact data. Screener and
  confidence and MCQ choice are controls; prompts are typeset.
- Every answer surface has "I don't know", one action, never punished,
  always in the same place (leftmost).
- Confidence is captured before any reveal; the UI makes peeking
  structurally impossible.
- Buttons that fire an exchange drop duplicate taps while one is in flight.
- Controls live inside the question box they answer.
- Interpreted answers (handwriting) keep their evidence beside the grade.

iPad-specific, from the HIG research:
- Readable column: cap prose at about 680 pt and center it; body text
  around 19-20 pt serif with generous leading; support Dynamic Type up to
  200 percent by feeding the web view a CSS variable from
  `UIFontMetrics`, and reflow rather than zoom.
- Two explicit reading themes, Paper (warm, the current `book.css`) and
  Dark, following the system appearance by default; chrome uses semantic
  system colors so it is right in both.
- Regular width: contents (spine) as a sidebar beside the reader; compact
  width (Split View, Slide Over): contents as a pushed list. Design the
  reader for a 320 pt floor.
- Continuous scroll for chapters (Books' Scroll mode); no page-curl. Tap in
  the middle toggles chrome; the reader is the scroller (never a web view
  inside a SwiftUI ScrollView).
- Keyboard first for answers on this device (no Pencil); hardware keyboard
  shortcuts: number keys pick an MCQ letter, Return commits, Cmd-Return
  checks in, `[`/`]` previous/next item. Pointer hover effects on buttons.
- All four orientations; `UIRequiresFullScreen` stays false; save state on
  `.inactive` and `.background`.
- Typography: keep Source Serif 4 / Source Sans 3 (bundled in
  `assets/fonts`, the e-ink book's faces) for content and controls so the
  two clients read as one book; SF for system chrome only.
- Accessibility: every control labeled; MathText web views expose an
  accessibility label with the plain prompt; contrast 4.5:1 minimum; test
  with `simctl ui content_size accessibility-extra-large` and Increase
  Contrast.

## 3. Screens and navigation (what to build)

Mapping the tablet client's modes onto iPad screens:

| tablet mode | iPad screen | notes |
|---|---|---|
| books | **Bookshelf** (`LibraryView` reborn) | one card per subject from `/subjects`: title, progress line, "Open now" marker on the active book; opens straight into the reader when a chapter exists; server-setting sheet stays here |
| contents | **Contents** sidebar/list | from `/state` spine: status glyph, score, tap to browse a cleared chapter read-only, current chapter resumes; "Start over" and "Catch me up" live here |
| reading | **Reader** | `WKWebView`, continuous scroll, chrome on tap; bottom bar: item count, "Take the check" / "Answer the questions"; beats inline as today |
| screener | **Placement** | native: the question, five rows, one tap, advances immediately (gateless) |
| reading (calibration series) | **Calibration series** | the series items one per screen, no per-item reveal (it is measurement); commit or IDK; progress "3 of 11"; ends in results |
| results | **Results** | native: headline (Calibration complete / Gate cleared / Below the gate + score), gate bar, per-item entries with READ AS / ANSWER / WHY typeset via MathText; actions: Continue / Just the misses / Explain it differently / Override |
| screener->series, series->chapter | **Authoring wait** | "The next chapter is being written" static screen; see server change B |
| breakSuggested / breakActive | **Break** | existing `BreakView`, wired to `break_suggestion` and `break_minutes` |
| confirmReset | **Start over** confirmation | existing alert, reachable from Contents |
| loading / submitting / error | **Wait / Error** | same copy as the tablet: "Opening the book.", "Grading your answers.", "The server can't be reached." with Try again |

Navigation on iOS 15: `NavigationView` with `.navigationViewStyle(.stack)`
in compact width; a `UISplitViewController` representable (sidebar =
Contents, secondary = Reader) in regular width. Modal flows (placement,
checks, results, break) present with `fullScreenCover` so the reader cannot
be peeked at mid-check. Deep links `chiron://book/<subject>/<screen>` route
to any screen (used by the harness and by the multi-book future).

## 4. Architecture inside the app

- Keep SwiftUI + `ObservableObject` (`@Observable` is iOS 17). Keep the
  `AppModel` state machine but split it: `BookSession` (per-subject: chapter,
  responses, position, persistence) and `Library` (subjects, active book,
  servers). Multiple `BookSession`s can exist; the UI shows one. That is the
  multi-book foundation and costs nothing now.
- Persistence: per-subject JSON in Application Support (move from
  Documents; mark caches excluded from backup); reading position debounced
  from the web view's scroll offset and top visible section id; save on
  scene phase `.inactive`/`.background`.
- Networking: one `ChironClient` with typed endpoints and `URLSession`
  async; long timeout for exchanges; a `busy` flag that disables all
  exchange buttons.
- Reader: one `WKWebView` per chapter, `loadFileURL` of the bundle template,
  chapter HTML injected via `initChapter`; a `WKUserScript` reports the
  scroll position and the visible section; CSS variables for font scale and
  theme are set from Swift on load and on change.
- MathText: keep, but pool web views per screen (one item on screen at a
  time in the item flow, so the cost is bounded) and give each an
  accessibility label. Consider `SwiftMath` later only if A8 web views prove
  slow (measure first, Phase 5).
- Debug harness (`#if DEBUG`, launch arg `harness`): an `NWListener` HTTP
  server on localhost:8087 with `GET /state` (screen, subject, unit, item
  index, error), `POST /navigate` (deep link), `POST /answer` (fill the
  current item), `GET /shot` (writes a screenshot via the app if useful; the
  shell uses `simctl io` anyway). This replaces the ad-hoc `show*` launch
  args and lets a script drive every screen without taps. Taps that must be
  real taps use idb.

## 5. Server changes (small, all TDD in Go)

- **A. Mark the book open from the JSON path.** Only `GET /pages/{subject}`
  calls `markActive` today (verified: `handleExchange` never does). Add
  `s.markActive(sub.ID)` in `handleExchange` after the subject lookup so
  opening a book on the iPad makes it the active one.
  Test: after an exchange on `data`, `/subjects.active == "data"`.
- **B. Async authoring for the JSON path.** Add `async: true` to
  `Exchange`; when set, `processExchange(..., asyncAuthor=true)` returns the
  grades immediately with `authoring: "<unit>"`. Add `GET
  /chapter/{subject}` returning the persisted current chapter JSON
  (`currentChapter(sub)` already exists for pages) plus `{authoring,
  authoring_error}` from `buildStatus`, so the iPad polls it the way the
  tablet polls pages meta. Tests: exchange with `async` returns no chapter
  and an `authoring` unit; `/chapter` reports authoring then the chapter.
- **C. Results doc in the response.** `buildResultsDoc` already produces
  `{headline pieces, dek, entries[{n, verdict, idk, kind, prompt,
  confidence, read_as, chose, answer, why}]}` for the typeset pages; add
  `out["results_doc"] = doc` so the iPad renders the same content natively.
  Test: a graded exchange carries `results_doc` with one entry per item.
- Nothing else. The screener, calibration sets, bands, reveal, and state
  payload are already what the iPad needs.

## 6. Dev loop (Simulator for everything)

One-time setup, done 2026-09-01. `xcodebuild -downloadPlatform` refuses
iOS 15.5 ("not available for download"): Apple's index lists it only as a
legacy installer package, not a runtime image. What worked:
```
# index: https://devimages-cdn.apple.com/downloads/xcode/simulators/index2.dvtdownloadableindex
curl -L -o ios15.5-sim.dmg https://devimages-cdn.apple.com/downloads/xcode/simulators/com.apple.pkg.iPhoneSimulatorSDK15_5-15.5.1.1653527639.dmg   # 5.4 GB
hdiutil attach -nobrowse -readonly ios15.5-sim.dmg
pkgutil --expand-full /Volumes/Clearwater*/iPhoneSimulatorSDK15_5.pkg sdk15   # 12 GB, minutes
mv sdk15/Payload ~/Library/Developer/CoreSimulator/Profiles/Runtimes/"iOS 15.5.simruntime"   # no sudo
xcrun simctl create chiron-ipad-15 com.apple.CoreSimulator.SimDeviceType.iPad-mini-4 com.apple.CoreSimulator.SimRuntime.iOS-15-5
```
`chiron-ipad-15` (iPad mini 4 device type, 768x1024 pt, iOS 15.5) is the
gate; `chiron-ipad` (iPad A16, iOS 26.5) is the second target. Both must
pass before a phase closes. Quirk: that runtime cannot delta-install over an
existing copy of the app; `sim-run.sh` uninstalls and reinstalls when the
install fails, which wipes the app's Documents on that device.

Scripts (extend, do not fork):
- `scripts/sim-run.sh [device]` - build, install, launch against the
  disposable server; `DEVICE=chiron-ipad-15` selects the runtime; `shot`
  screenshots. Never point it at :8080 (real state) or :8082 (Matt's sim
  server); use `scripts/sim-server.sh` or a throwaway server on :8084 with
  `state_dir`s under the scratchpad, both subjects configured.
- `scripts/sim-verify.sh` (new) - for each device x {light, dark} x {default,
  accessibility-extra-large}: launch with `harness`, walk every screen via
  `/navigate` and the fixtures, screenshot each to `verify/<device>/<theme>/
  <screen>.png`, and print `/state` after each step. Reviewed by eye; the
  harness output is asserted.
- `xcodebuild test` runs the unit tests on both simulators.
- Device pass: `ios-deploy --bundle ... --justlaunch` + `idevicesyslog |
  grep chiron`, at the end of Phases 2, 3 and 5.

Tests to add (a `ChironTests` XCTest target in `project.yml`):
- Decoding: every fixture in `ipad-app/fixtures/` decodes; add a fixture
  for a graded check response, a break suggestion, `/subjects` with
  `active`, `/state`, and the async `authoring` shape as they are captured
  from the real server.
- State machine: `BookSession` transitions with a fake client (start ->
  placement -> series -> results -> reading; gate fail -> just the
  misses / remediate / override; break; error recovery; reopen restores position).
- Mechanical grading mirror: `book.js` must keep matching `checkers`
  (already covered server-side by `checkers` tests; add a JS test runner
  only if `book.js` changes).

## 7. Phases

Each phase ends with: unit tests green on both simulators, `sim-verify.sh`
screenshots reviewed, a commit. TDD for every behavior change (failing
test first), including the Go changes.

**Phase 0 - Toolchain and baseline (half a day)**
- Install the iOS 15.5 runtime, create `chiron-ipad-15`, parametrize
  `sim-run.sh`, add the `ChironTests` target and the fixture decode tests.
- Fix `/subjects` decoding (struct with `subjects` + `active`).
- Replace `SelfTest.run` with a calibration-aware loop: start -> screener
  (selected_index) -> series (answer from reveal) -> gate present -> PASS.
- Server change A. Exit: self-test passes on both simulators against a
  throwaway server for both subjects.

**Phase 1 - Protocol parity with the tablet**
- Placement screen (native, five rows, IDK-less by design, one tap).
- Calibration series presentation (no reveal, progress, IDK leftmost).
- Results screen from `results_doc` (server change C), replacing the
  feedback wall in `GateView`; actions per gate state.
- Authoring wait with polling (server change B); "Grading your answers"
  wait; error screen with Try again; the exact copy the tablet uses.
- Bookshelf from `/subjects` with the active book marked; reopen on it at
  launch; Start over from Contents; break suggestion and break-taken
  reporting wired to the server.
- Exit: a full pass through v0 -> v1 on `data` and u0 -> u1 on `ai`, driven
  by the harness, screenshots reviewed.

**Phase 2 - iPad shape**
- Split view (sidebar Contents / Reader) in regular width; stack in compact;
  chrome toggle on tap; reader position persistence and restore.
- Typography pass: fonts, readable column, Dynamic Type via CSS variable,
  Paper/Dark themes, dark-mode audit of every screen.
- Keyboard shortcuts and pointer hover; all orientations; Split View by
  hand in the simulator.
- Exit: `sim-verify.sh` at both text sizes, both themes, both devices; a
  device pass on the mini 4.

**Phase 3 - Answers**
- Item flow polish: four confidence pills (replacing the slider), IDK
  leftmost, MCQ letters with number-key shortcuts, keyboard-first text
  entry with a proper `UITextView` (Scribble comes free on Pencil iPads),
  reveal only on teaching-unit checks.
- Handwriting box (optional per item): `PKCanvasView` with `.anyInput` in a
  fixed box; strokes exported from `PKDrawing` as normalized polylines and
  sent through the existing `/ink` contract (`strokes`, `aspect`, `idk`,
  `text`), so the server rasterizes, transcribes, and keeps the audit trail
  exactly as for the tablet. Default is typed; handwriting is a toggle.
- Exit: harness-driven check with typed, MCQ, IDK and one inked answer;
  results show READ AS for the inked one.

**Phase 4 - Pacing, resilience, performance**
- Chunk/break accounting parity with the tablet (chunk minutes from reader
  open, break minutes reported).
- Offline resilience: cached chapter readable when the server is away;
  every server failure is a screen with a way out, never a spinner.
- Performance on A8: one chapter web view at a time; MathText pooling; font
  subsetting for KaTeX; measure chapter load and check-page render on the
  device and record numbers in `DESIGN-ui.md`.

**Phase 5 - Device milestone and cleanup**
- Signing check (profile validity), install on the mini 4, full read of one
  chapter and one check on the device, Wi-Fi to the sprite.
- Remove the USB bridge listener and flight-only code paths; update
  `FLIGHT.md` pointers; update project memory.

## 8. Roadmap: multiple books in progress

Already true on the server: every subject has its own learner state,
chapters, results, and the active-book marker. Already true in the app after
Phase 0: per-subject persistence and a `BookSession` per subject. What
remains is UI and policy, in this order when the time comes:
1. Bookshelf cards showing, per book, the current chapter title, last-read
   time (from the event log's last `ts`; expose `last_active` in
   `/subjects`), and progress; the active book first.
2. Switching books preserves each book's reading position and any
   in-progress (uncommitted) answers; a check in progress in one book is
   never lost by opening another.
3. Pacing across books: session minutes and break suggestions are per human,
   not per book - the server's pacing model should read across subjects
   (server change, small).
4. Extension books (x1-x4) unlock per the syllabus rule and appear on the
   shelf as they are authored.
5. Later still: Pencil iPads get Scribble in every text field automatically
   and pencil-only ink in the handwriting box; a Mac Catalyst build is a
   settings flip if ever wanted.

## 9. Risks and open questions

- The iOS 15.5 runtime download may fail or be withdrawn when Xcode 27
  ships; if it does, the mini 4 itself becomes the iOS 15 gate (slower
  loop, still workable over USB).
- A8 + 2 GB: many `WKWebView`s at once will be evicted. The item flow shows
  one item, so the bound is small, but the reader plus MathText on a results
  screen with eleven entries needs measuring (Phase 4).
- Long synchronous exchanges (Opus authoring) versus iOS background
  suspension: an exchange that outlives the app being backgrounded is lost;
  server change B removes the long request entirely.
- Handwriting with a finger on a 7.9" screen is a poor answer medium; it is
  a toggle, not the default, and the typed path must be excellent.
- `TeachView` and the bundled book are carried, not improved; if they rot,
  they are removed rather than repaired.

## 10. Status (2026-09-06)

Phases 0 through 5 are built. The app runs on the iPad Air 13" (M4) and
the iPhone 16 against the sprite (its public `sprites.app` address,
Anthropic-backed), set up from a QR code; every change is tested in the
Simulator (`scripts/sim-run.sh test`, 80 tests, plus the harness walks)
before it goes on a device, and the server deploys with `make deploy`
from the sprite's checkout. What each later section records:

- Section 11: iOS 26 and the Pencil Pro (the double-tap is still
  unverified on the device).
- Section 12: the reader's chrome and the palette.
- Section 13: the library first, shelves, the phone, another device from
  a QR code, an agent sending work (`chiron` and its skill), annotation
  sync with an offline shelf cache and a three-way conflict card, and
  PDFs on the shelf (step 1 of three).
- `SPRITE-DEV-PLAN.md`: development from the sprite with the in-app
  shell; `OPEN-SOURCES.md`: books built from open texts, with the
  exercise import and the finder.

Still open: the Pencil Pro double-tap on a device; the PDF selection
menu on a device; measurements on the A8 iPad mini, which is no longer
the target device.

## 11. iOS 26 and the Pencil Pro (2026-09-02)

A current iPad with a Pencil Pro replaced the mini 4 as the reading device.
The deployment target is now iOS 26.0; the iOS 15.5 simulator and the mini 4
are no longer gates (`sim-verify.sh` walks `chiron-ipad` alone). Built:

- An annotation layer over the page: the reader's ink rides inside the
  page's scroll view (PencilKit, default drawing policy, so a paired Pencil
  draws and fingers scroll), persisted per chapter as PencilKit data.
- Highlights and questions as marks on runs of the chapter's text, by
  character offsets into the article's visible prose (beats and hidden
  MathML excluded so answering a beat cannot shift them), applied by the
  page script and persisted per chapter.
- A palette on the trailing edge (pen, highlighter, ask, eraser) with Liquid
  Glass; a Pencil Pro squeeze cycles the tools and a double-tap flips pen
  and eraser.
- The ask card: a question about a highlighted passage goes to
  `POST /ask/{subject}` with the passage; the tutor answers from the section
  the passage came from, the question is recorded in the learner state, and
  the planner sees recent questions when it plans the next chapter. A dev
  server without a model stubs the answer.
- Harness verbs for all of it (`/tool`, `/mark`, `/ask`, `/close`), and the
  walk screenshots a highlight and an answered question.

Not yet verified on a device: the Pencil Pro gestures and the drawing
policy's finger/pencil split, which the Simulator cannot exercise.


## 12. Capture: primers from anything on the iPad (design, 2026-09-02)

Matt's note from a side channel, folded in verbatim in substance. The
idea: select text, a URL, an image or a PDF anywhere on the iPad, send it
to Chiron with a question, and get a *primer* on the shelf a minute later.

**Two entry points, one code path.**

1. *Share Extension.* An extension target appears in the share sheet for
   text, URLs, images and PDFs. Extensions run sandboxed with a strict
   memory cap and no access to the app's state, so the extension does
   almost nothing: write the payload to an App Group container and open
   the main app via `chiron://capture/<id>`. The main app shows the "What
   do you want to know about this?" card.
2. *App Intent* (`CapturePrimerIntent`, text, image or file input). This
   puts Chiron in Shortcuts, Spotlight and Apple Intelligence's action
   list; on iOS 26 it can be registered as a Pencil Pro squeeze action and
   can appear in the text-selection menu with no share sheet at all.

Both normalise to `{text?, imagePNG?, sourceURL?, sourceApp?}` and the
main app POSTs `/primer/capture` with the payload plus the prompt. The
server runs the author role in primer mode and returns a new book on the
shelf; the wait lands on the existing authoring screen.

Constraints: image payloads go through the vision transcription path the
ink check-in uses, so capture is offline-capable for text only. The
extension needs its own bundle id (`dev.mjbraun.chiron.share`), an App
Group entitlement on both targets, and the URL scheme in Info.plist: all
three are `project.yml` additions.

**Primers vs smart books.** A `kind` field on the subject: `primer` or
`book`. A primer is a unit list with no gate, no calibration series, no
checks. Its loop is read, annotate, comment; each ask or margin note
becomes an append request (`POST /primer/{id}/extend`) and the author
role extends the document rather than authoring the next chapter. A smart
book is what exists now.

**Library view.** One grid with a badge rather than two sections, since
the count stays small for a long time. SF Symbols over emoji at small
sizes and in dark mode: `brain` for books, `doc.text` for primers. Primer
cards show source and capture date instead of progress; book cards keep
the progress line. A capture still authoring shows greyed with a spinner,
so the reader can go back to what they were reading and check later.

**Steps.**

1. Server: `kind` on subjects, `POST /primer/capture`, primer-mode author
   role, `POST /primer/{id}/extend` for appends from comments.
2. App: App Group, URL scheme, share extension target,
   `CapturePrimerIntent`, the capture card, primer reading screen (reader
   without check chrome; comments feed the extend call).
3. Library: kind-aware shelf with badges and the authoring-in-progress
   state.
4. Verify: sim-verify steps for capture-from-text and a primer extend; a
   device step for the share sheet from Safari, since extensions cannot be
   driven from the harness.

**Open decision for Matt:** whether a primer can be promoted to a smart
book later by asking the server to generate checks for it. Cheap if the
corpus format is shared, and the kind of thing wanted after a primer turns
out to matter.

**Status (2026-09-03, overnight build).** Steps 1 to 3 built and verified in
the Simulator against a dev server; step 4's Simulator half is in
`scripts/sim-verify.sh` (steps 00a to 00d). Server: `primer/` (a primer is
a one-unit corpus written from the author's markdown under
`state/primers/<id>/`, so chapters, asks, marks and learner state need
nothing new), `roles/primer.go` (AuthorPrimer, ExtendPrimer), routes
`POST /primer/capture` and `POST /primer/{id}/extend`, `kind`, `status`,
`source` and `captured_at` on `/subjects`, an image capture through the
ink transcriber, stub documents on a dev server without a model, primers
reloaded at boot (one caught mid-authoring is marked failed). App: shelf
cards with the `brain` / `doc.text` badge, source line, greyed-with-spinner
while authoring, red when failed, a Capture button; the capture card
(`CaptureCard.swift`) opened by `chiron://capture/<id>` from the shared
inbox (`Chiron/Shared/CaptureInbox.swift`, App Group
`group.dev.mjbraun.chiron`), by the `CapturePrimerIntent` App Intent, or by
the shelf button with pasted text; the share extension target
`ChironShare` (text, URL, image, PDF via PDFKit); the primer reader with no
check bar and a `note` tool whose card says "Add to the primer", the
document reloading in place with the new section and the mark keeping its
"+" badge. Harness verbs: `capture`, `capture/card`, `capture/close`,
`note`. Untested until a device: the share sheet from Safari, the intent
in Shortcuts and the Pencil squeeze menu, and App Group provisioning under
automatic signing. Not built: promotion of a primer to a smart book (the
open decision above).

**Status (2026-09-04, scales and plan mode).** The capture card asks how
much the reader wants back: summary, description, primer, smart book. A
summary or a description is one model call answered in the card, with "Go
on: make it a primer" under it. A primer or a book becomes a draft on the
shelf, marked with a hammer, and opens a planning conversation
(`PlanCard.swift`): two questions at most for a primer, the Teach-me
elicitation seeded with the capture for a book. "Later" keeps the draft to
come back to; "Write the primer" / "Build the book" is there from the first
turn and prominent once the tutor has a brief; a bin discards. A build
sends the reader to the shelf and the primer, or the book, opens when the
server is done. Server: `scale` on `POST /primer/capture`,
`GET|POST /primer/{id}/plan`, `POST /primer/{id}/build`,
`POST /primer/{id}/discard`; drafts live in the primers directory with
their transcript (`primer.Meta.Plan`); a book draft is a Teach-me job and
leaves the shelf when its book registers. Harness verbs: `capture` takes
`scale`; `draft/open`, `plan`, `build`, `discard`; state carries
`capture_answer` and `plan_card`. The open decision above is settled by
this: a primer is not promoted; the reader picks the scale at capture.

## 13. Phone, other devices, PDFs, and the shelf first (plan, 2026-09-05)

Five asks, in the order to do them. Each is done when its Simulator walk
passes and the device check named for it is done.

### 13.1 The bookshelf is the first screen (done 2026-09-05)

The app opens on the shelf; the book last open, on whichever client, is
the card saying "Open now". `Library.launch()` refreshes the shelf and
stops. The self-test opens its own book; `sim-verify.sh` expects the
shelf at launch. Harness state unchanged.

### 13.2 Lessons learned live in the repo (now, then always)

`LESSONS.md` at the root: one entry per thing that cost more than ten
minutes to learn, dated, with the symptom, the cause and what to do. The
rule in `CLAUDE.md`: add to it in the same commit as the fix. Seeded from
this week (glass swallows taps, invalid SF Symbol names render as text,
XCUI typing waits on a blinking caret, `hitTest` never sees the touch on
the device, page rects are offset by the bar inset, `sprite-env` keeps
only the last `--env`, `claude --update` hangs on the sprite, a shell
without a locale draws ASCII, ssh config order decides which key goes
first, and the rest).

### 13.3 Set up another device from a QR code (done 2026-09-05)

Server settings has "Set up another device": a QR code of
`chiron://server?name=...&url=...&key=...` for the selected server
(`ServerLink`, `QRCode`, `DeviceSetupView`), with the line that the code
carries the shared key and is for your own devices. Two ways in on the
other device:

- The Camera app reads the code and offers "Open in Chiron"; `onOpenURL`
  hands the link to `Library.adopt`, which saves and selects the server
  (updating one with the same address rather than duplicating it), probes,
  refreshes the shelf, and enrols the device key when the link has one.
- "Add a server" has "Scan a code": VisionKit's `DataScannerViewController`
  reads the same URL into the form; the Simulator, with no camera, says so.

Tests: `ServerLinkTests` (round trip, rejects, adopt, render); harness
verbs `server/url` (the link), `server/setup` (the code on screen), state
`server_url`, `servers`, `device_setup`, `last_url`; the code decoded from
an iPad screenshot with CIDetector gave the link back, and the phone
Simulator adopted it through `simctl openurl`. Device check done the same
day: the phone took the iPad's code with the Camera.

### 13.4 Chiron on the phone, with the shelf and progress shared (two days)

Two halves. The **app on the phone** (done 2026-09-05, Simulator; device
check pending): iPhone is in the target, portrait only. At compact width
the palette lies along the bottom edge and a phone's palette has no pen
or eraser (ink needs a Pencil); the ask card is a bottom sheet with the
page above it; contents, capture and planning were sheets already; the
shelf scrolls (it was a stack that centred and lost its header once the
cards overflowed, on the iPad too). `chiron-iphone` is the Simulator
(iPhone 17, iOS 26.5); `DEVICE=chiron-iphone scripts/sim-run.sh ...`.
The share extension is the phone's main way in: "Create me a primer from
this text" from Safari, put the phone down, pick up the iPad.

**Offline, and what happens when both sides changed** (Matt, 2026-09-05).
Each device keeps a cache of the library: the shelf, every chapter it has
opened, and its annotations, so a book reads on a plane. Changes made
offline are journaled and pushed when the server is back. The sync
compares versions per document (a unit's annotations, a shelf, a primer's
notes); when the server's copy and the device's copy have both changed
since they last agreed, the app does not guess: it shows the two and asks
the reader to keep this device's copy, keep the server's, or hand both to
the agent on the sprite to reconcile into one, which the reader then sees
before it is written. This goes with the annotation sync below.

Status 2026-09-06: the annotation sync is built. The server keeps one
versioned document per unit (`GET/PUT /annotations/{subject}/{unit}`, a
409 with the server's copy when both changed, `POST .../reconcile` that
merges two copies: every mark, the richer copy of a shared one, threads
joined by the tutor, the further position, both inks). The app pulls on
opening a unit, pushes changes after a two-second pause (throttled, not
reset by each scroll), and on a conflict shows the two copies with the
three choices; the agent's merge lays one ink over the other with
PencilKit. Harness: `sync`, `conflict/resolve {choice}`, state
`conflict`. Walked on two Simulators against one dev server. The offline
library cache is the next piece.

The **shared state**. The server already holds the learner record (units
cleared, current unit, debt, the active book), so progress is shared
today. What is not: highlights, questions and their answers, margin
notes, ink and the reading position, which live in each device's
Application Support. Move them to the server as annotations:
`GET /annotations/{subject}/{unit}` and `PUT` of the same, one document
per unit with `marks`, `ink` (PencilKit data, base64) and `position`,
each carrying `updated_at`; the app pulls on opening a unit, pushes on
`persist()` and after every mark or ink change (debounced as ink already
is), last writer wins per field, and keeps working offline from its
cache. `BookSession` gains a sync step; `Sync` gains two calls; the
FakeService records them. Device check: highlight on the iPad, see it on
the phone; ink on the phone, see it on the iPad.

### 13.5 PDFs on the shelf (two to three days, in three steps)

Status 2026-09-06: step 1 is built and walked in the Simulator. The
server keeps a PDF under `state/documents/<id>/` with its `document.json`
(`POST /documents?title=&pages=` with the file as the body, `GET
/documents/{id}`, `/file`, `PUT /documents/{id}/position`, `DELETE`) and
lists it on the shelf as kind `pdf` with `pages` and `page`; a document
goes on a shelf like anything else. In the app: "Import a PDF" in the
library bar (`.fileImporter`), or a PDF shared in (the extension now
writes the file into the inbox instead of its text) goes up with its
page count and title; the card wears a `doc.richtext` badge and says
"page x of n"; opening fetches the file once into Application Support
(`documents/<id>.pdf`) and shows it in PDFKit with the bookshelf button
and "x of n"; a page turn goes to the server two seconds later, and on
close. Harness: `import {path}`, `pdf/page {page}`; state `document`,
`shelf_error`. Tests: `documents_test.go`, `DocumentTests`.

Step 2 is built the same day: a PencilKit canvas lies over every page
(`PDFPageOverlayViewProvider`, pencil only, so a finger scrolls), the
strokes are kept in the page's own points so they fit at any zoom and on
either device, and the reader's toolbar has a pen and an eraser. Each
page's ink is versioned on the server (`GET /documents/{id}/ink`, `PUT
/documents/{id}/ink/{page}` with `base_version`, 409 with the server's
copy); the app pushes a drawn page two seconds after the last stroke and
on close, and a page both devices drew on while apart ends with both
drawings laid together, put back on the server's version, so no stroke
is lost and no card is needed. Harness: `pdf/tool`, `pdf/stroke {page,
points}`; state `document.ink_pages`, `ink_strokes`, `overlaid_pages`.
Tests: `TestInkOnAPDFIsKeptPerPage`, `DocumentInkTests`.

Step 3 is built the same evening: a selection on a page has "Send to
Chiron" in its menu (`DocumentPDFView.buildMenu`), which opens the
capture card over the page with the passage and "from <title>, page N"
as its source, so a summary, a primer or a book comes from a paper the
way it does from a book's passage. Harness: `pdf/capture {text}` (the
selection when there is one). Test: the capture path in
`DocumentTests`. The menu item itself has only been exercised through
the harness; select a passage on the iPad to see it. 13.5 is done; a
PDF is not yet a smart book in one tap, as planned.

1. **Import and read.** A PDF arrives by the share sheet (the extension
   already takes PDFs, today as text for a primer) or from Files
   (`.fileImporter` behind an "Import a PDF" item in the shelf's bar).
   The app uploads it: `POST /documents` (multipart: the file, its name),
   the server keeps it under `state/documents/<id>/` with a `document.json`
   (title, pages, size, imported_at) and lists it on the shelf as
   kind `pdf` with a page count and the last page read. The reader shows
   it in `PDFView` (PDFKit) with the same chrome and the reading position
   (page and scroll) as an annotation per 13.4. Harness: `import` with a
   file path, `pdf/page`.
2. **Ink.** Pencil strokes as PDFKit ink annotations, saved into the
   document and pushed as annotations per page rather than re-uploading
   the file, so the phone and the iPad see the same marks.
3. **Ask and capture.** A selection in `PDFView` feeds the ask card and
   the capture tool the way a page selection does (text, page number),
   so a passage of a paper becomes a primer or a book; the primer's
   source names the document and page.

Not planned: rendering PDFs through the book's web page, or turning a PDF
into a smart book in one tap. The capture tool at the book scale already
covers the second from any passage.

### 13.7 The library and its shelves (done 2026-09-05)

The first screen is the Library: the shelves the reader has made, then
everything on no shelf. A shelf is a named folder the server keeps
(`state/shelves.json` beside `active-subject`; `GET/POST /shelves`,
`PUT/DELETE /shelves/{id}`, `PUT /subjects/{id}/shelf`; the subjects reply
carries `shelves` and each row its `shelf`), so both devices see the
same ones. In the app: "New shelf" in the bar; a folder card opens the
shelf; a card dragged onto a folder is filed there and, on the shelf's
screen, dragged onto the library row comes back; "Move to" in a card's
long-press menu does the same without dragging; the shelf's menu renames
or deletes it, and deleting returns its contents to the library. Harness:
`shelf/create`, `shelf/rename`, `shelf/delete`, `shelf/open`,
`shelf/close`, `move`; state `shelves`, `open_shelf`, `shelf_rows[].shelf`.
Tests: server (`shelves_test.go`), app (`ShelvesTests`), and a UI test
that drags a card onto a shelf and back (`ShelvesUITests`).

### 13.6 An agent sends work to Chiron (done 2026-09-05)

"Create a primer from first principles about FOO and send it to Chiron"
is a shell away: `chiron` (`server-go/cmd/chiron`, the same config as
`chiron-dev`) lists the shelf, captures at any scale, runs or skips the
planning turns (`build -brief-file`, which the server now accepts), waits
for the write, and prints the text. `skills/chiron/SKILL.md` is the
skill: the agent writes the notes and the brief, captures, builds, waits,
reads, and reports the title. A title given at capture now stays through
the plan and the writing (`Meta.Named`). Deploy needed for the brief on
build and the kept title; the rest works against the live server today.

## 14. Blogs on the shelf (design, 2026-09-20)

Matt reads blogs to keep up with his work, and the reason to have them in
Chiron is not the reading: it is that a highlight in a post should reach
the tutor with the whole post behind it, and that what he read and asked
stays in the learner record. A passage shared in from another reader
arrives alone; the tutor answers a fragment, the device model cannot
answer at all, and nothing is kept. So a followed blog belongs on the
shelf, next to the books.

This is not an RSS reader. No folders, no OPML, no discovery, no starring,
no read-state sync with another client, no two hundred feeds. A handful of
blogs read closely; everything else stays in NetNewsWire, which can still
share a page to Chiron.

**A feed is a reading.** An imported EPUB already becomes a subject with
one chapter per spine document, read as it is, highlighted, inked, synced
(13.5, and the reading routes in `httpapi/reading.go`). A feed is the same
thing with entries for spine documents: each post is a chapter, newest
first, its HTML through `sources.HTMLToMarkdown`, its images kept as a
reading's assets are. Short entries - Simon Willison's links and
quotations run a paragraph each - are folded into one chapter a week
("Links, week of 14 September") so the list is essays and digests rather
than forty one-liners.

**The client pokes; the sprite fetches.** The sprite hibernates, so
nothing can run on a timer there. It wakes on an inbound request, and the
app already makes one on foreground and on pull-to-refresh. That request
carries the refresh: the server fetches the feeds it has not checked in
the last hour, adds the new entries as chapters, and answers with the
shelf and the unread counts. The client never parses a feed. Feed
parsing, HTML cleanup, asset rewriting and entry dedupe stay in Go, where
the EPUB path already has them, instead of being written again in Swift
for three platforms; and dedupe by entry id makes two devices refreshing
at once harmless. The cost is that a new post is noticed when Chiron is
opened, not before. A background refresh task that pokes the server can
move the badge later; iOS schedules those when it likes, so it is not
where to start.

**The UX.** Add on the shelf takes a feed URL as well as an EPUB. The
feed becomes a shelf card with the blog's name and a badge for unopened
posts. The card opens a list of posts, newest first, unread marked, each
with title, date and first line. A post opens in the reader with nothing
Chiron-authored on the page: the post, its headings, its code, its
images, pages turning, Pencil ink. Highlight gives the capture card as
everywhere else - Ask, Detail, Primer - and the tutor has the post, so
the on-device model can answer it too when the server is out of reach. A
primer lands on the shelf and remembers the post it came from. Opening a
post clears its mark for every device.

**Server.** A feed source beside the EPUB one: subscribe
(`POST /feeds` with the URL, which fetches once and names the shelf entry
from the feed's title), refresh as part of the sync the app already does,
`entry_id` kept per chapter for dedupe, `last_checked` per feed, unread
per entry in the learner record. Atom and RSS in `encoding/xml`; both
carry full content for the feeds worth following, and one that carries
only a summary gets its page fetched through the client that already
fetches sources.

**App.** The shelf card and its badge, the post list, and the feed URL in
Add. The reader, capture card, ink, sync and learner record are
untouched. Harness verbs for the list and the unread counts.

**Order.** "Read in Chiron" for a single URL first: share a page, the
server fetches it, it becomes a one-chapter reading and opens. That is
the piece a feed poller calls once per entry, and it pays off on its own
for any page. Then the feed on top of it.

### 14.1 Read in Chiron (done 2026-09-20)

`POST /readings/page` takes `{"url"}`, fetches the page, and lays the
article out as a reading: `sources.Page` finds the writing on the page by
scoring its containers for prose and against links and furniture (few
sites mark up an `<article>`), turns it into Markdown, and the server
keeps its pictures beside it the way an imported book's are kept, so the
page reads offline. Refused with 422 if there is no article on it. In the
app: "Read a link" on the shelf (the pasteboard's URL is offered), "Read
it in Chiron" on the card a shared page opens, `Library.readPage`, and
the harness verb `read {url}`. Tests: `sources/page_test.go`,
`httpapi/readingpage_test.go`, `ChironTests/ReadPageTests`.

### 14.2 Following a blog (done 2026-09-20)

`POST /feeds` takes `{"url"}` of an Atom or RSS feed and puts the blog on
the shelf as a reading of kind `feed`: `sources.Feed` reads both kinds as
the same handful of facts per entry, and each entry becomes a chapter,
newest first. A post shorter than 120 words is a note - a link, a
quotation, a sighting - and joins the week that holds it ("Notes, week of
14 September"), which keeps a shelf of essays rather than a stream; a
week is as new as its last note. Unit ids are given once and kept, so a
post already read never changes chapter. An entry that carries no content
has its page fetched as "Read in Chiron" does; pictures are kept beside
the blog, named by a stamp of their URL so the portrait on every post is
fetched once.

`POST /feeds/refresh` is the check, and nothing on the server polls: the
app asks for it when the shelf refreshes, which is the request that woke
the sprite. A blog is not asked again within the hour unless `?force=1`.
`POST /readings/{id}/read` marks a chapter seen, `read.json` holds when,
and the card counts what has not been seen as it now is - so a week that
gains a note is unread again. `DELETE /readings/{id}` unfollows.

In the app: "Read a link or follow a blog" on the shelf offers both, the
card shows "3 unread of 17" and wears the feed mark, opening a post marks
it read on every device, and the card's menu unfollows a blog or takes a
reading off the shelf. Harness: `follow {url}`, `feeds`, `forget
{subject}`. Tests: `sources/feed_test.go`, `httpapi/feeds_test.go`,
`ChironTests/FeedTests`.

Not built, and only if the reading asks for it: a feed that carries
truncated content on purpose (the page is fetched only when the entry
carries nothing at all), OPML, and a background refresh that moves the
count while the app is closed.
