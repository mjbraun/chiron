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
const res = join(here, "../ipad-app/Chiron/Resources");
const src = readFileSync(join(res, "math.js"), "utf8") + "\n" + readFileSync(join(res, "book.js"), "utf8");
const noop = () => {};
// The page's own listeners, kept so a test can send it an event.
const listeners = {};
const sandbox = {
  document: {
    addEventListener: (type, fn) => { (listeners[type] ||= []).push(fn); },
    getElementById: () => null, querySelector: () => null,
  },
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

// What the page sets as math must be what the server protected as math
// (server-go/render/math.go): KaTeX's own auto-render pairs any two
// dollars in a run of text, so "about $33K MRR ... (Nous at $74.8K MRR" was
// set as one italic equation. The spans here mirror the server's tests.
const mathCases = [
  ["The set $\\{1, 2\\}$ is small.", ["$\\{1, 2\\}$"], "an inline span"],
  ["$$C = M \\oplus K$$", ["$$C = M \\oplus K$$"], "display math is one span, not two empty ones"],
  ["It costs $5 or maybe $10 tops.", [], "prices are not math"],
  ["for about $33K MRR of customers; a written SLA (Nous at $74.8K MRR, Feldera)", [], "two sums in one sentence are not math"],
  ["A literal \\$ sign and $x$.", ["$x$"], "an escaped dollar is prose"],
  ["an edit has $\\rho = $ sheets, the test fails", ["$\\rho = $"], "a closer after a space closes when nothing opens after it"],
  ["$x $ is a span", ["$x $"], "a trailing space inside a span is still a span"],
  ["capitalization in $ trillion", [], "a bare dollar with no opener is prose"],
  ["Pay $5 and $ten more.", [], "a closer after a space does not close on a word"],
  ["A stray $ here.\n\nA new paragraph $y$.", ["$y$"], "an unclosed dollar does not cross a blank line"],
  ["$a_*b_*c$ stays put.", ["$a_*b_*c$"], "emphasis inside math is math"],
  ["Pay $5 for $x^2$ twice.", ["$x^2$"], "a price before a span does not take its opener"],
];
let mathFailed = 0;
for (const [text, want, label] of mathCases) {
  const got = sandbox.mathSpans(text).map((sp) => text.slice(sp.start, sp.end));
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    mathFailed++;
    console.log(`FAIL math ${label}: ${JSON.stringify(got)}, wanted ${JSON.stringify(want)}`);
  }
}

// Setting a page: the walker splits each run of text at its spans and
// renders those with KaTeX, leaving prose, code and already-set math as
// they are. A small document stands in for the page here.
function fakeNode(type, tag) {
  const n = { nodeType: type, tagName: tag, childNodes: [], classList: { contains: (c) => (n.className || "").split(" ").includes(c) } };
  n.appendChild = (c) => { n.childNodes.push(c); c.parentNode = n; return c; };
  n.replaceChild = (nu, old) => {
    const i = n.childNodes.indexOf(old);
    const kids = nu.nodeType === 11 ? nu.childNodes : [nu];
    n.childNodes.splice(i, 1, ...kids);
    kids.forEach((k) => (k.parentNode = n));
  };
  return n;
}
const fakeDoc = {
  createTextNode: (t) => Object.assign(fakeNode(3), { nodeValue: t }),
  createElement: (tag) => fakeNode(1, tag.toUpperCase()),
  createDocumentFragment: () => fakeNode(11),
};
const textOf = (n) => n.nodeType === 3 ? n.nodeValue : n.childNodes.map(textOf).join("");
sandbox.katex = { render: (tex, el, opts) => { el.className = "katex"; el.childNodes = [Object.assign(fakeNode(3), { nodeValue: `[${opts.displayMode ? "D" : "I"}:${tex}]` })]; } };
const page = fakeNode(1, "DIV");
const para = page.appendChild(fakeDoc.createElement("p"));
const t1 = para.appendChild(fakeDoc.createTextNode("about $33K MRR (Nous at $74.8K MRR) and $x^2$ then "));
t1.ownerDocument = fakeDoc;
const code = para.appendChild(fakeDoc.createElement("code"));
const t2 = code.appendChild(fakeDoc.createTextNode("$a$"));
t2.ownerDocument = fakeDoc;
const t3 = page.appendChild(fakeDoc.createTextNode("$$E=mc^2$$"));
t3.ownerDocument = fakeDoc;
sandbox.renderMathIn(page);
const once = textOf(page);
sandbox.renderMathIn(page);
let renderFailed = 0;
const renderFail = (msg) => { renderFailed++; console.log(`FAIL render ${msg}`); };
if (once !== "about $33K MRR (Nous at $74.8K MRR) and [I:x^2] then $a$[D:E=mc^2]") renderFail(`a page sets only its math: ${JSON.stringify(once)}`);
if (textOf(page) !== once) renderFail(`setting a page twice changes it: ${JSON.stringify(textOf(page))}`);
if (para.childNodes.length !== 4 || para.childNodes[1].className !== "katex") renderFail("the span is set in place of its text");

// The column reads at a book's measure held upright. On its side the
// iPad keeps the same margins it had upright, so the column takes the
// rest; the web view fills the screen, so landscape's height is
// upright's width. Checked on the 13-inch iPad at the default size.
const css = readFileSync(join(res, "book.css"), "utf8");
const chapterRule = (block) => {
  const m = /#chapter\s*{([^}]*)/.exec(block);
  return m ? m[1] : "";
};
// A width in CSS, with em, vw and vh, evaluated for one screen.
const widthOf = (rule, em, vw, vh) => {
  const m = /max-width:\s*([^;]+);/.exec(rule);
  if (!m) return null;
  const expr = m[1].replace(/calc/g, "").replace(/([\d.]+)em/g, "($1*em)")
    .replace(/([\d.]+)vw/g, "($1*vw/100)").replace(/([\d.]+)vh/g, "($1*vh/100)");
  return new Function("em", "vw", "vh", `return ${expr};`)(em, vw, vh);
};
const sideOf = (rule, em) => {
  const m = /padding:\s*[\d.]+em\s+([\d.]+)em/.exec(rule);
  return m ? parseFloat(m[1]) * em : 0;
};
const landscape = /@media[^{]*orientation:\s*landscape[^{]*{([\s\S]*?)}\s*}/.exec(css);
const uprightRule = chapterRule(css.replace(/@media[\s\S]*?}\s*}/g, ""));
const sidewaysRule = landscape ? chapterRule(landscape[1]) : "";
const em = 19, short = 1024, long = 1366;
const upright = widthOf(uprightRule, em, short, long);
const sideways = widthOf(sidewaysRule, em, long, short);
let layoutFailed = 0;
const layoutFail = (msg) => { layoutFailed++; console.log(`FAIL layout ${msg}`); };
if (upright === null) layoutFail("the chapter has no measure");
if (sideways === null) layoutFail("landscape has no measure of its own");
else if (upright !== null) {
  const margin = (w, measure) => (w - Math.min(w, measure + 2 * sideOf(uprightRule, em))) / 2;
  const up = margin(short, upright), side = margin(long, sideways);
  if (Math.abs(up - side) > 0.5) layoutFail(`landscape's margin is ${side.toFixed(1)}pt, upright's ${up.toFixed(1)}pt`);
  if (sideways <= upright) layoutFail(`landscape's measure ${sideways}pt is no wider than upright's ${upright}pt`);
}
// A URL or a long word in the text breaks rather than running off the
// right edge; pre scrolls instead (its white-space does not wrap).
if (!/#chapter\s*{[^}]*overflow-wrap:\s*anywhere/.test(uprightRule ? css : "")) layoutFail("the chapter does not break long words");

// The page's own selection (a finger's press, WebKit's handles) is
// reported to the native side as it changes, so "Send to Chiron" can be
// offered in its menu: once per change, never for a field's selection.
const posted = [];
sandbox.window.webkit = { messageHandlers: { bridge: { postMessage: (m) => posted.push(m) } } };
let selected = "";
sandbox.document.getSelection = () => ({ toString: () => selected });
sandbox.document.activeElement = null;
let reportFailed = 0;
const reportFail = (msg) => { reportFailed++; console.log(`FAIL selection report: ${msg}`); };
selected = " a passage ";
sandbox.reportSelection();
if (JSON.stringify(posted) !== JSON.stringify([{ type: "selection", text: "a passage" }])) reportFail(`a selection is reported trimmed, got ${JSON.stringify(posted)}`);
sandbox.reportSelection();
if (posted.length !== 1) reportFail("the same selection is not reported twice");
selected = "";
sandbox.reportSelection();
if (posted.length !== 2 || posted[1].text !== "") reportFail(`a cleared selection is reported empty, got ${JSON.stringify(posted)}`);
selected = "typed";
sandbox.document.activeElement = { tagName: "TEXTAREA" };
sandbox.reportSelection();
if (posted.length !== 2) reportFail("a selection inside a field is the field's, not the book's");
sandbox.document.activeElement = null;
sandbox.window.webkit = undefined;

// A tap on the colophon (the line naming the model that wrote the
// chapter) opens the record of its writing, and is not a tap on the page:
// the chrome stays as it was.
sandbox.window.webkit = { messageHandlers: { bridge: { postMessage: (m) => posted.push(m) } } };
posted.length = 0;
const click = (hit) => {
  const target = { closest: (sel) => sel.split(",").some((s) => s.trim() === hit) ? {} : null };
  for (const fn of listeners.click || []) fn({ target, stopPropagation: noop });
};
click(".colophon");
if (JSON.stringify(posted) !== JSON.stringify([{ type: "about" }])) reportFail(`a tap on the colophon opens the record, got ${JSON.stringify(posted)}`);
posted.length = 0;
click("p");
if (JSON.stringify(posted) !== JSON.stringify([{ type: "tap" }])) reportFail(`a tap on the prose is a tap, got ${JSON.stringify(posted)}`);
sandbox.window.webkit = undefined;

// A selecting tool picked while the page's own selection is up takes
// that selection as its run: the reader chose the words already, and a
// second drag over them is not what anyone means. The run is measured
// the way a drag's is, in the article's offsets, whatever nodes the
// selection's ends sit in, and the selection goes with it.
const runNodes = [];
let at = 0;
for (const text of ["The quick brown ", "fox jumps ", "over the dog."]) {
  const node = { nodeType: 3, nodeValue: text };
  runNodes.push({ node, start: at, end: at + text.length });
  at += text.length;
}
sandbox.markableTextNodes = () => runNodes;
const block = { nodeType: 1, childNodes: runNodes.map((t) => t.node) };
const rangeOver = (sc, so, ec, eo, inside) => ({
  startContainer: sc, startOffset: so, endContainer: ec, endOffset: eo,
  intersectsNode: (n) => inside.includes(n),
});
let takeFailed = 0;
const takeFail = (msg) => { takeFailed++; console.log(`FAIL take selection: ${msg}`); };
const within = sandbox.offsetsOfRange(rangeOver(runNodes[0].node, 5, runNodes[0].node, 11, [runNodes[0].node]));
if (!within || within.text !== "quick brown") takeFail(`a selection inside one node is its run, got ${JSON.stringify(within)}`);
const across = sandbox.offsetsOfRange(rangeOver(runNodes[0].node, 6, runNodes[2].node, 4, [runNodes[0].node, runNodes[1].node, runNodes[2].node]));
if (!across || across.start !== 4 || across.text !== "quick brown fox jumps over") takeFail(`a selection across nodes snaps outward to words, got ${JSON.stringify(across)}`);
const toElement = sandbox.offsetsOfRange(rangeOver(runNodes[1].node, 0, block, 2, [runNodes[1].node]));
if (!toElement || toElement.text !== "fox jumps") takeFail(`a selection ending at an element takes the nodes before it whole, got ${JSON.stringify(toElement)}`);
const nowhere = sandbox.offsetsOfRange(rangeOver(block, 0, block, 0, []));
if (nowhere !== null) takeFail(`a selection over no text is nothing, got ${JSON.stringify(nowhere)}`);
let ranges = [rangeOver(runNodes[2].node, 5, runNodes[2].node, 8, [runNodes[2].node])];
let cleared = 0;
sandbox.document.getSelection = () => ({
  rangeCount: ranges.length, isCollapsed: ranges.length === 0, getRangeAt: (i) => ranges[i],
  toString: () => "the", removeAllRanges: () => { cleared++; ranges = []; },
});
const taken = sandbox.takeSelection();
if (!taken || taken.text !== "the") takeFail(`the page's selection is taken as a run, got ${JSON.stringify(taken)}`);
if (cleared !== 1) takeFail("the selection goes with its handles once taken");
if (sandbox.takeSelection() !== null) takeFail("with nothing selected there is nothing to take");
ranges = [rangeOver(runNodes[2].node, 5, runNodes[2].node, 8, [runNodes[2].node])];
sandbox.document.activeElement = { tagName: "INPUT" };
if (sandbox.takeSelection() !== null) takeFail("a selection inside a field is the field's, not the book's");
if (cleared !== 1) takeFail("a field's selection is left alone");
sandbox.document.activeElement = null;
const takeCases = 9;


// A beat answered before the reader left the chapter comes back answered:
// the native side keeps every response and hands them to the page with
// the chapter, and the page shows each beat as it was left (the option
// chosen and its reveal, the answer typed and its verdict) without
// posting the answer over the bridge a second time.
const fakeEl = (extra = {}) => Object.assign({
  hidden: true, innerHTML: "", disabled: false, value: "", style: {}, dataset: {}, childNodes: [],
  classList: { list: [], add(c) { this.list.push(c); }, contains(c) { return this.list.includes(c); } },
  addEventListener: noop, focus: noop, remove() { this.removed = true; },
}, extra);
const fakeHolder = (beat) => {
  const options = (beat.options || []).map((_, i) => fakeEl({ dataset: { i: String(i) } }));
  const reveal = fakeEl(), input = fakeEl(), commit = fakeEl();
  const holder = fakeEl({
    querySelector: (sel) => {
      const m = /button\.option\[data-i="(\d+)"\]/.exec(sel);
      if (m) return options[Number(m[1])] || null;
      if (sel === ".reveal") return reveal;
      if (sel === "input, textarea") return input;
      if (sel === "button.commit") return commit;
      return null;
    },
    querySelectorAll: (sel) => sel === "button.option" ? options : [],
  });
  return { holder, options, reveal, input, commit };
};
let restoreFailed = 0;
const restoreFail = (msg) => { restoreFailed++; console.log(`FAIL restore beat: ${msg}`); };
sandbox.window.webkit = { messageHandlers: { bridge: { postMessage: (m) => posted.push(m) } } };
posted.length = 0;
const choiceBeat = { id: "u1-b1", type: "predict", prompt: "Which?", options };
const choice = fakeHolder(choiceBeat);
sandbox.renderBeat(choice.holder, choiceBeat);
sandbox.restoreBeat(choice.holder, choiceBeat, { beat_id: "u1-b1", response: "overflow", selected_index: 1, mechanical_verdict: "fail" });
if (!choice.options[1].classList.contains("chosen")) restoreFail("the option chosen is marked chosen");
if (!choice.options.every((o) => o.disabled)) restoreFail("an answered choice takes no second tap");
if (choice.reveal.hidden || !choice.reveal.innerHTML.includes("Not quite.") || !choice.reveal.innerHTML.includes("Numerics, not gradients."))
  restoreFail(`the reveal adjudicates the option chosen, got ${JSON.stringify(choice.reveal.innerHTML)}`);
if (!choice.holder.classList.contains("done")) restoreFail("a restored beat is done");
const computeBeat = { id: "u1-b2", type: "compute", prompt: "6 x 7?", check: "numeric(0.5)", answer: "42" };
const compute = fakeHolder(computeBeat);
sandbox.renderBeat(compute.holder, computeBeat);
sandbox.restoreBeat(compute.holder, computeBeat, { beat_id: "u1-b2", response: "42", mechanical_verdict: "pass" });
if (compute.input.value !== "42" || !compute.input.disabled) restoreFail(`the answer typed is shown and kept, got ${JSON.stringify(compute.input.value)}`);
if (!compute.commit.removed) restoreFail("an answered beat has no commit button");
if (compute.reveal.hidden || !compute.reveal.innerHTML.includes("Correct.")) restoreFail(`the verdict is shown again, got ${JSON.stringify(compute.reveal.innerHTML)}`);
const explainBeat = { id: "u1-b3", type: "self-explain", prompt: "Why?", check: "llm", answer: "Because.", rubric: "Says because." };
const explain = fakeHolder(explainBeat);
sandbox.renderBeat(explain.holder, explainBeat);
sandbox.restoreBeat(explain.holder, explainBeat, { beat_id: "u1-b3", response: "since", self_verdict: "partial" });
if (explain.reveal.hidden || !explain.reveal.innerHTML.includes("Reference:")) restoreFail("the reference is shown again");
if (!explain.reveal.innerHTML.includes('data-v="partial" disabled style="opacity: 1"') || explain.reveal.innerHTML.includes('data-v="pass" disabled style')
    || !explain.reveal.innerHTML.includes('data-v="pass" disabled'))
  restoreFail(`the self-check given is the one marked, all three closed, got ${JSON.stringify(explain.reveal.innerHTML)}`);
if (posted.length !== 0) restoreFail(`restoring posts nothing over the bridge, got ${JSON.stringify(posted)}`);
// A response the chapter no longer has a beat for (the beat was
// rewritten) is left alone; one with an index past the options is too.
const stale = fakeHolder(choiceBeat);
sandbox.renderBeat(stale.holder, choiceBeat);
sandbox.restoreBeat(stale.holder, choiceBeat, { beat_id: "u1-b1", response: "gone", selected_index: 7 });
if (stale.options.some((o) => o.disabled) || !stale.reveal.hidden) restoreFail("a response that matches no option leaves the beat open");
// The chapter's payload carries the responses, and initChapter replays them.
const holders = {};
for (const b of [choiceBeat, computeBeat]) holders[b.id] = fakeHolder(b);
sandbox.document.getElementById = (id) => id === "chapter" ? {
  innerHTML: "", childNodes: [],
  querySelector: (sel) => { const m = /data-beat-id="([^"]+)"/.exec(sel); return m && holders[m[1]] ? holders[m[1]].holder : null; },
  querySelectorAll: () => [],
} : null;
sandbox.initChapter({ title: "T", minutes: 1, html: "", beats: [choiceBeat, computeBeat],
  responses: [{ beat_id: "u1-b2", response: "41", mechanical_verdict: "fail" }] }, 0);
if (!holders["u1-b2"].holder.classList.contains("done") || !holders["u1-b2"].reveal.innerHTML.includes("Not quite."))
  restoreFail("a chapter opened again shows the beats answered before");
if (holders["u1-b1"].holder.classList.contains("done")) restoreFail("a beat not answered is still open");
if (posted.some((m) => m.type === "beat")) restoreFail("opening a chapter again posts no answers");
sandbox.window.webkit = undefined;
const restoreCount = 12;

const total = cases.length + choiceCases.length + positionCases.length + 1 + restoreCases.length + 1 + selectionCases + mathCases.length + 3 + 2 + 4 + takeCases + restoreCount;
failed += selectionFailed + reportFailed + takeFailed + mathFailed + renderFailed + layoutFailed + restoreFailed;
console.log(`${total - failed}/${total} book.js cases pass`);
process.exit(failed ? 1 : 0);
