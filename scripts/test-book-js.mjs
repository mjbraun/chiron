#!/usr/bin/env node
// The offline grader in ipad-app/Chiron/Resources/book.js must agree with
// the server's checkers package: a beat graded one way while reading and
// the other way at the chapter boundary is a contradiction the learner
// sees. The cases here mirror server-go/checkers/checkers_test.go.
//
//   node scripts/test-book-js.mjs
import { readFileSync } from "node:fs";
import vm from "node:vm";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(here, "../ipad-app/Chiron/Resources/book.js"), "utf8");
const noop = () => {};
const sandbox = {
  document: { addEventListener: noop, getElementById: () => null, querySelector: () => null },
  window: { addEventListener: noop, scrollTo: noop, scrollY: 0, webkit: undefined },
  setTimeout, clearTimeout, console,
};
sandbox.globalThis = sandbox;
vm.createContext(sandbox);
vm.runInContext(src, sandbox);

const cases = [
  ["numeric(1)", "50331648", "50331648", true, "exact hit"],
  ["numeric(1)", "50331648", "100663296", false, "double the answer"],
  ["numeric(1)", "50331648", "50331649", true, "within +/-1"],
  ["numeric(0.001)", "0.50349", "0.503", true, "rounded to three places"],
  ["numeric(0.001)", "0.50349", "0.5", false, "the naive symmetry guess"],
  ["numeric(0.01)", "5.5", "-5.5", false, "sign flip"],
  ["exact", "4, 1; 11, 6", "4,1;11,6", true, "separator spacing is not meaning"],
  ["exact", "4, 1; 11, 6", " 4 , 1 ; 11 , 6 ", true, "padded separators"],
  ["exact", "4, 1; 11, 6", "4, 1; 11, 7", false, "one entry wrong"],
  ["exact", "4 x 7", "4 X 7", true, "case-insensitive"],
  ["exact", "4 x 7", "4x7", true, "spacing inside an expression is not meaning"],
  ["exact", "4 x 7", "4 x7", true, "uneven spacing"],
  ["exact", "w|i|d|est_", "w | i | d | est_", true, "spaces around pipes"],
  ["exact", "4 x 7", "47", false, "the operator is meaning"],
  ["exact", "4 x 7", "7 x 4", false, "order is meaning"],
  ["numeric(0.01)", "6", "the answer is 6", true, "prose around a number still parses"],
];

let failed = 0;
for (const [check, expected, given, want, why] of cases) {
  const got = sandbox.gradeMechanical(check, expected, given);
  if (got !== want) {
    failed++;
    console.error(`FAIL ${check} expected=${JSON.stringify(expected)} given=${JSON.stringify(given)} -> ${got}, wanted ${want} (${why})`);
  }
}
// A choice beat grades from its own options, the way checkers.CheckMCQ does.
const options = [
  { text: "scale by sqrt(d_k)", correct: true, explain: "Right." },
  { text: "overflow", misconception: "M7", explain: "Numerics, not gradients." },
];
const choiceCases = [
  [0, true, "Right.", "the correct option"],
  [1, false, "Numerics, not gradients.", "a distractor's own reveal"],
  [5, false, "no option selected", "out of range"],
];
for (const [index, correct, explain, label] of choiceCases) {
  const g = sandbox.gradeChoice(options, index);
  if (g.correct !== correct || g.explain !== explain || g.answer !== "scale by sqrt(d_k)") {
    failed++;
    console.log(`FAIL choice ${label}: ${JSON.stringify(g)}`);
  }
}
// The reading position is a fraction of the chapter's scroll, so the same
// place holds on a phone's narrower page and reads as a percentage.
sandbox.window.innerHeight = 800;
sandbox.document.documentElement = { scrollHeight: 4800 };
const positionCases = [
  [0, 0, "the top"],
  [2000, 0.5, "halfway down the scroll"],
  [4000, 1, "the bottom"],
  [4800, 1, "overscroll clamps"],
];
for (const [scrollY, want, label] of positionCases) {
  sandbox.window.scrollY = scrollY;
  const got = sandbox.readingPosition();
  if (Math.abs(got - want) > 1e-9) {
    failed++;
    console.log(`FAIL position ${label}: ${got}, wanted ${want}`);
  }
}
sandbox.document.documentElement.scrollHeight = 600; // fits the window
sandbox.window.scrollY = 0;
if (sandbox.readingPosition() !== 0) {
  failed++;
  console.log(`FAIL position with nothing to scroll: ${sandbox.readingPosition()}`);
}
// Restoring turns the fraction back into a scroll offset; anything that
// is not a fraction starts at the top.
sandbox.document.documentElement.scrollHeight = 4800;
const restoreCases = [[0.5, 2000, "halfway"], [1, 4000, "the bottom"], [0, 0, "the top"],
                      [undefined, 0, "nothing remembered"], [670, 0, "not a fraction"], [-0.1, 0, "below the top"]];
for (const [position, want, label] of restoreCases) {
  const got = sandbox.scrollTop(position);
  if (got !== want) {
    failed++;
    console.log(`FAIL restore ${label}: ${got}, wanted ${want}`);
  }
}
// A position that arrives after the page loaded (the server's copy of
// where another device left off) moves the page there.
let scrolledTo = null;
sandbox.window.scrollTo = (_x, y) => { scrolledTo = y; };
sandbox.setPosition(0.25);
if (scrolledTo !== 1000) {
  failed++;
  console.log(`FAIL setPosition: scrolled to ${scrolledTo}, wanted 1000`);
}
// A drag that strays off the text keeps the run it had. The page answers
// nothing for a point under a bar or past its bottom edge (the point is
// outside what the bars leave uncovered), and a lift there used to drop
// the selection the reader was looking at.
sandbox.window.innerWidth = 1024;
sandbox.window.innerHeight = 1200;
let selectionFailed = 0;
const selectionFail = (msg) => { selectionFailed++; console.log(`FAIL selection: ${msg}`); };
// A caret lookup that answers only inside the viewport, as WebKit's does.
const caretInView = (x, y) => (y >= 0 && y < 1200 && x >= 0 && x < 1024) ? { x, y } : null;
const above = sandbox.caretNear(300, -40, caretInView);
if (!above || above.y !== 0) selectionFail(`a point under the top bar is read at the edge, got ${JSON.stringify(above)}`);
const below = sandbox.caretNear(300, 1260, caretInView);
if (!below || below.y !== 1199) selectionFail(`a point past the bottom is read at the edge, got ${JSON.stringify(below)}`);
const inside = sandbox.caretNear(300, 500, caretInView);
if (!inside || inside.y !== 500 || inside.x !== 300) selectionFail(`a point on the page is read where it is, got ${JSON.stringify(inside)}`);
if (sandbox.caretNear(300, 500, () => null) !== null) selectionFail("a point with no text anywhere near it is still a miss");
// The caret lookup answers nothing for a point in a band 24px below the
// edge too (the page's own idea of its edge differs from the view's): the
// lookup steps inward until it finds text.
const caretBelowBand = (x, y) => (y >= 24 && y < 1200) ? { x, y } : null;
const banded = sandbox.caretNear(300, -10, caretBelowBand);
if (!banded || banded.y < 24 || banded.y > 60) selectionFail(`a point in a dead band at the edge steps inward, got ${JSON.stringify(banded)}`);

// The provisional highlight stays while a drag strays, and the lift takes
// what was shown when its own point misses.
const run = { start: 10, end: 20, text: "ten letters" };
let answer = run;
sandbox.offsetsFromPoints = () => answer;
let marked = null, unmarked = 0;
sandbox.markRange = (start, end, kind, id) => { marked = { start, end, kind, id }; };
sandbox.unmark = () => { unmarked++; };
if (sandbox.previewRange(0, 0, 1, 1) !== run) selectionFail("a drag over text previews the run");
if (!marked || marked.id !== "preview" || marked.start !== 10) selectionFail("the preview is marked on the page");
answer = null;
marked = null;
if (sandbox.previewRange(0, 0, 1, -50) !== run) selectionFail("a drag that strays keeps the run it had");
if (marked !== null) selectionFail("a stray point marks nothing new");
const ended = sandbox.endSelection(0, 0, 1, -50);
if (ended !== run) selectionFail(`a lift off the text takes the run shown, got ${JSON.stringify(ended)}`);
if (unmarked === 0) selectionFail("the preview comes off at the lift");
if (sandbox.endSelection(0, 0, 1, -50) !== null) selectionFail("the run shown is taken once; a second lift with nothing shown is nothing");
const other = { start: 30, end: 40, text: "other text" };
answer = other;
if (sandbox.endSelection(0, 0, 1, 1) !== other) selectionFail("a lift on text takes the run under it");
const selectionCases = 13;

const total = cases.length + choiceCases.length + positionCases.length + 1 + restoreCases.length + 1 + selectionCases;
failed += selectionFailed;
console.log(`${total - failed}/${total} book.js cases pass`);
process.exit(failed ? 1 : 0);
