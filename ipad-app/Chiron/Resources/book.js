/* Chapter renderer + interaction beats.
 *
 * The native side calls initChapter(payload) once the page reports ready.
 * Beats render as interactive cards; mechanical beats (check: exact/numeric)
 * grade locally so reading works fully detached; llm-checked beats reveal the
 * reference + rubric for self-comparison and the committed response is sent to
 * the bridge for authoritative grading at the next exchange.
 */

let CH = null;
const answered = {};

document.addEventListener("DOMContentLoaded", () => {
  post({ type: "ready" });
});

function post(msg) {
  if (window.webkit?.messageHandlers?.bridge) {
    window.webkit.messageHandlers.bridge.postMessage(msg);
  }
}

/* Where an imported book's pictures come from. The chapter refers to
 * them by the name they have beside the book; the native side serves
 * them, from its own copy when there is no server to ask. */
let ASSET_BASE = "";
function setAssetBase(base) { ASSET_BASE = base || ""; }

/* A picture the chapter refers to is beside the book, not beside the
 * page: point it at whoever is serving the book. */
function resolveAssets(root) {
  if (!ASSET_BASE) return;
  for (const img of root.querySelectorAll('img[src^="assets/"]')) {
    img.setAttribute("src", ASSET_BASE + img.getAttribute("src").slice("assets/".length));
  }
}

/* position: where this chapter was left, as a fraction of its scroll,
 * from the native side's memory. */
function initChapter(payload, position) {
  CH = payload;
  const root = document.getElementById("chapter");
  root.innerHTML =
    `<h1>${escapeHtml(payload.title)}</h1>` +
    `<div class="chapter-meta">~${payload.minutes} min${(payload.beats || []).length ? " · work every beat before its reveal" : ""}</div>` +
    payload.html;

  for (const beat of payload.beats) {
    const holder = root.querySelector(`.beat[data-beat-id="${beat.id}"]`);
    if (holder) renderBeat(holder, beat);
  }
  resolveAssets(root);
  renderMathIn(root);
  window.scrollTo(0, scrollTop(position));
  reportControls();
  if (typeof ResizeObserver !== "undefined") new ResizeObserver(reportControls).observe(root);
  if (typeof MutationObserver !== "undefined") {
    new MutationObserver(reportControls).observe(root, { childList: true, subtree: true, attributes: true });
  }
}

/* Where the page's controls are, in document coordinates, so the ink
 * layer over the page can hand a touch on a button or a field through
 * while the pen is up. Sent whenever the page's layout changes. */
let controlsTimer = null;
function reportControls() {
  clearTimeout(controlsTimer);
  controlsTimer = setTimeout(() => {
    const rects = [];
    for (const el of document.querySelectorAll("button, input, textarea, select")) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      rects.push([r.left + window.scrollX, r.top + window.scrollY, r.width, r.height]);
    }
    post({ type: "controls", rects });
  }, 100);
}

/* What the page focused, so the native side can let go of the keyboard:
 * PencilKit's canvas over the page holds first responder while it is up,
 * and the keyboard follows first responder, not the DOM. */
document.addEventListener("focusin", (e) => {
  post({ type: "focus", tag: (e.target.tagName || "").toLowerCase() });
});
document.addEventListener("focusout", () => {
  post({ type: "focus", tag: "" });
});

/* The page's own selection, a finger's press and drag with WebKit's
 * handles, reported as it changes so the native side can offer "Send to
 * Chiron" in the selection's menu. A selection inside a beat's field is
 * the field's, not the book's. */
let reportedSelection = "";
function pageSelection() {
  const el = document.activeElement;
  const inField = !!el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA");
  return inField || !document.getSelection ? null : document.getSelection();
}
function reportSelection() {
  const sel = pageSelection();
  const text = sel ? String(sel).trim() : "";
  if (text === reportedSelection) return;
  reportedSelection = text;
  post({ type: "selection", text });
}
document.addEventListener("selectionchange", reportSelection);

/* The first element matching a selector, for the harness. */
function elementRect(selector) {
  const el = document.querySelector(selector);
  if (!el) return null;
  const r = el.getBoundingClientRect();
  return { x: r.left, y: r.top, width: r.width, height: r.height };
}

/* How far the page can scroll. */
function scrollRange() {
  return document.documentElement.scrollHeight - window.innerHeight;
}

/* The reading position as a fraction of the chapter's scroll, 0 at the
 * top and 1 at the bottom, so the same place holds on a phone's narrower
 * page and reads as a percentage. */
function readingPosition() {
  const range = scrollRange();
  if (!(range > 0)) return 0;
  return Math.min(1, Math.max(0, window.scrollY / range));
}

/* The scroll offset for a position; anything that is not a fraction
 * starts at the top. */
function scrollTop(position) {
  if (!(position >= 0 && position <= 1)) return 0;
  return position * Math.max(0, scrollRange());
}

/* A position that arrives after the page loaded: where another device
 * left off, from the server. The page moves there. */
function setPosition(position) {
  window.scrollTo(0, scrollTop(position));
}

/* The native side keeps the reading position; report it as the page
 * settles rather than on every scroll event. */
let scrollTimer = null;
window.addEventListener("scroll", () => {
  clearTimeout(scrollTimer);
  scrollTimer = setTimeout(() => {
    post({ type: "scroll", position: readingPosition() });
  }, 300);
});

/* A tap on the page itself (not on anything that answers or links) toggles
 * the reader's chrome. */
/* The last segment of a mark carries its badge; its box on screen, for a
 * script that wants to tap it the way a reader would. */
function markRect(id) {
  const last = document.querySelector(`mark[data-id="${id}"][data-last="1"]`)
    || document.querySelector(`mark[data-id="${id}"]`);
  if (!last) return null;
  // A mark that wraps is one element over two lines; its badge sits on
  // the last line, and the box of both lines together is mostly prose.
  const boxes = last.getClientRects();
  const r = boxes.length ? boxes[boxes.length - 1] : last.getBoundingClientRect();
  return { x: r.left, y: r.top, width: r.width, height: r.height };
}

/* The mark under a point of the page, for the eraser. */
function markAt(x, y) {
  const el = document.elementFromPoint(x, y);
  const m = el && el.closest("mark.mark");
  return m && m.dataset.id !== "preview" ? m.dataset.id : null;
}

document.addEventListener("click", (e) => {
  if (e.target.closest("a, button, input, textarea, select, .beat, mark.mark")) return;
  post({ type: "tap" });
});

/* ---- Marks: highlights and questions anchored to the chapter's text ----
 *
 * A mark is a run of the article's text by character offsets. The offset
 * space is the visible prose in document order, skipping interaction beats
 * (their DOM is rewritten when answered) and KaTeX's hidden MathML copy, so
 * offsets survive re-rendering and answering. The native side owns the list
 * of marks; this side turns offsets into <mark> wrappers and back.
 */

function markableTextNodes() {
  const root = document.getElementById("chapter");
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: (n) => {
      if (!n.nodeValue) return NodeFilter.FILTER_REJECT;
      for (let el = n.parentElement; el && el !== root; el = el.parentElement) {
        if (el.classList.contains("beat") || el.classList.contains("katex-mathml")) {
          return NodeFilter.FILTER_REJECT;
        }
        if (el.tagName === "SCRIPT" || el.tagName === "STYLE") return NodeFilter.FILTER_REJECT;
      }
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  const nodes = [];
  let offset = 0;
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    nodes.push({ node: n, start: offset, end: offset + n.nodeValue.length });
    offset += n.nodeValue.length;
  }
  return nodes;
}

function articleText() {
  return markableTextNodes().map((t) => t.node.nodeValue).join("");
}

/* Character offset of a (node, offset) DOM position, or -1 if it is not in
 * the markable text. */
function offsetOf(node, off, nodes) {
  if (node.nodeType !== Node.TEXT_NODE) {
    // An element position: take the start of the first text node inside it
    // at or after the child index.
    const child = node.childNodes[Math.min(off, node.childNodes.length - 1)];
    if (!child) return -1;
    const walker = document.createTreeWalker(child, NodeFilter.SHOW_TEXT);
    const first = child.nodeType === Node.TEXT_NODE ? child : walker.nextNode();
    if (!first) return -1;
    node = first; off = 0;
  }
  for (const t of nodes) if (t.node === node) return t.start + off;
  return -1;
}

/* The caret at a point, or at the nearest point inward that has one. The
 * page answers nothing for a point outside the part of it the bars leave
 * uncovered, and a drag reaches there whenever the reader selects up to
 * the first line under the title bar or down past the last; its edge is
 * not quite where the native side thinks either, so from a miss the
 * lookup steps toward the middle of the page until something answers. */
function caretNear(x, y, caret = (px, py) => document.caretRangeFromPoint(px, py)) {
  const w = window.innerWidth, h = window.innerHeight;
  const cx = Math.min(Math.max(x, 0), w - 1);
  let cy = Math.min(Math.max(y, 0), h - 1);
  const step = cy < h / 2 ? 12 : -12;
  for (let i = 0; i < 8; i++, cy += step) {
    if (cy < 0 || cy >= h) break;
    const r = caret(cx, cy);
    if (r) return r;
  }
  return null;
}

/* Offsets for a drag from (x1,y1) to (x2,y2) in viewport coordinates,
 * snapped outward to word boundaries. Null when the points miss the text. */
function offsetsFromPoints(x1, y1, x2, y2) {
  const nodes = markableTextNodes();
  const a = caretNear(x1, y1);
  const b = caretNear(x2, y2);
  if (!a || !b) return null;
  let s = offsetOf(a.startContainer, a.startOffset, nodes);
  let e = offsetOf(b.startContainer, b.startOffset, nodes);
  if (s < 0 || e < 0) return null;
  if (s > e) [s, e] = [e, s];
  return snappedRun(s, e);
}

/* The run [s, e) of the article text snapped outward to word boundaries
 * and in from whitespace. Null when nothing is left. */
function snappedRun(s, e) {
  const text = articleText();
  const isWord = (c) => /[\w$\\^_{}.,'’-]/.test(c);
  while (s > 0 && isWord(text[s - 1])) s--;
  while (e < text.length && isWord(text[e])) e++;
  while (s < e && /\s/.test(text[s])) s++;
  while (e > s && /\s/.test(text[e - 1])) e--;
  if (e - s < 1) return null;
  return { start: s, end: e, text: text.slice(s, e) };
}

/* Offsets for a DOM range, snapped like a drag's. A range's ends can sit
 * in an element rather than a text node (a selection dragged to the end
 * of a paragraph ends after its last child), so each markable node the
 * range touches is taken from where the range enters it to where it
 * leaves, and whole when the range's end is not inside it. Whitespace at
 * the range's ends is not words the reader chose (a paragraph's last node
 * ends in a space), so it comes off before the snap, which would
 * otherwise take in the next word. Null when the range covers none of
 * the text. */
function offsetsOfRange(range) {
  let s = -1, e = -1;
  for (const t of markableTextNodes()) {
    if (!range.intersectsNode(t.node)) continue;
    const from = t.node === range.startContainer ? range.startOffset : 0;
    const to = t.node === range.endContainer ? range.endOffset : t.node.nodeValue.length;
    if (s < 0) s = t.start + from;
    e = t.start + to;
  }
  if (s < 0 || e <= s) return null;
  const text = articleText();
  while (s < e && /\s/.test(text[s])) s++;
  while (e > s && /\s/.test(text[e - 1])) e--;
  return snappedRun(s, e);
}

/* The page's own selection as a run, for a selecting tool picked while
 * one is up: the reader chose the words already, so the tool takes them
 * instead of asking for a drag over them. The selection goes with its
 * handles, as a drag leaves none. A field's selection is the field's. */
function takeSelection() {
  const sel = pageSelection();
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return null;
  const run = offsetsOfRange(sel.getRangeAt(0));
  sel.removeAllRanges();
  return run;
}

/* Offsets of the first occurrence of query in the article text, ignoring
 * whitespace differences. Used to restore and to drive from scripts. */
function findText(query) {
  const text = articleText();
  const needle = query.trim().split(/\s+/).map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("\\s+");
  const m = new RegExp(needle).exec(text);
  if (!m) return null;
  return { start: m.index, end: m.index + m[0].length, text: m[0] };
}

/* Wrap [start, end) in <mark> elements, one per text-node segment. */
function markRange(start, end, kind, id) {
  unmark(id);
  const nodes = markableTextNodes();
  const pieces = [];
  for (const t of nodes) {
    if (t.end <= start || t.start >= end) continue;
    const from = Math.max(start, t.start) - t.start;
    const to = Math.min(end, t.end) - t.start;
    pieces.push({ node: t.node, from, to });
  }
  pieces.forEach((p, i) => {
    let node = p.node;
    if (p.to < node.nodeValue.length) node.splitText(p.to);
    if (p.from > 0) node = node.splitText(p.from);
    const mark = document.createElement("mark");
    mark.className = "mark mark-" + kind;
    mark.dataset.id = id;
    if (i === pieces.length - 1) mark.dataset.last = "1";
    node.parentNode.insertBefore(mark, node);
    mark.appendChild(node);
  });
}

function unmark(id) {
  document.querySelectorAll(`mark[data-id="${id}"]`).forEach((m) => {
    const parent = m.parentNode;
    while (m.firstChild) parent.insertBefore(m.firstChild, m);
    parent.removeChild(m);
    parent.normalize();
  });
}

function applyMarks(list) {
  document.querySelectorAll("mark.mark").forEach((m) => unmark(m.dataset.id));
  for (const m of list) markRange(m.start, m.end, m.kind, m.id);
}

/* A provisional highlight while the drag is in progress. A drag that
 * strays off the text (over a picture, under a bar) keeps the run it had
 * rather than losing it, as a selection anywhere else would. */
let preview = null;
function previewRange(x1, y1, x2, y2) {
  const r = offsetsFromPoints(x1, y1, x2, y2);
  if (!r) return preview;
  preview = r;
  markRange(r.start, r.end, "preview", "preview");
  return r;
}

/* The drag has ended: the run under its points, or, when the lift missed
 * the text, the run the reader was looking at. What was shown highlighted
 * is what they selected. */
function endSelection(x1, y1, x2, y2) {
  const shown = preview;
  preview = null;
  unmark("preview");
  return offsetsFromPoints(x1, y1, x2, y2) || shown;
}

/* While a tool's drag is the selection, the page's own selection stays
 * out of the way: a press held a moment before the drag would otherwise
 * start WebKit's, with its handles, and take the drag from the tool. */
function setSelecting(on) {
  document.body.classList.toggle("selecting", !!on);
}

document.addEventListener("click", (e) => {
  const m = e.target.closest("mark.mark");
  if (m && m.dataset.id !== "preview") {
    e.stopPropagation();
    post({ type: "mark", id: m.dataset.id });
  }
}, true);

/* Dynamic Type: the native side passes the system's body scale factor and
 * the stylesheet sizes everything from it. */
function setScale(scale) {
  document.documentElement.style.setProperty("--scale", String(scale));
}

/* renderMathIn comes from math.js, loaded first: the page sets only the
 * spans the server took for math, not any two dollars in a sentence. */

const BEAT_LABELS = {
  predict: "Predict before you read on",
  completion: "Fill in the blanked steps",
  "self-explain": "Explain it yourself first",
  compute: "Compute it by hand",
};

function renderBeat(holder, beat) {
  if (beat.options && beat.options.length) { renderChoiceBeat(holder, beat); return; }
  const mechanical = beat.check && beat.check !== "llm";
  const isCompute = beat.type === "compute";
  holder.innerHTML = `
    <div class="beat-label">${BEAT_LABELS[beat.type] || "Your turn"}</div>
    <div class="beat-prompt">${mdLite(beat.prompt)}</div>
    ${isCompute
      ? `<input type="text" inputmode="decimal" placeholder="your answer">`
      : `<textarea placeholder="write your answer - committing before the reveal is the point"></textarea>`}
    <button class="commit">Commit &amp; reveal</button>
    <div class="reveal" hidden></div>`;
  renderMathIn(holder);

  const input = holder.querySelector("input, textarea");
  const btn = holder.querySelector("button.commit");
  btn.addEventListener("click", () => {
    const response = input.value.trim();
    if (!response) { input.focus(); return; }
    input.disabled = true;
    btn.remove();
    const reveal = holder.querySelector(".reveal");
    reveal.hidden = false;

    if (mechanical) {
      const ok = gradeMechanical(beat.check, beat.answer, response);
      reveal.innerHTML =
        `<div class="${ok ? "verdict-ok" : "verdict-bad"}">${ok ? "Correct." : "Not quite."}</div>` +
        `<p><strong>Answer:</strong> ${mdLite(String(beat.answer))}</p>`;
      finishBeat(holder, beat, response, { mechanicalVerdict: ok ? "pass" : "fail" });
    } else {
      reveal.innerHTML =
        `<p><strong>Reference:</strong></p>${mdLite(beat.answer || "")}` +
        (beat.rubric ? `<p><strong>What a full answer contains:</strong></p>${mdLite(beat.rubric)}` : "") +
        `<p>Honest self-check - did you have it?</p>
         <button class="secondary" data-v="pass">I had it</button>
         <button class="secondary" data-v="partial">Partially</button>
         <button class="secondary" data-v="fail">I did not</button>`;
      reveal.querySelectorAll("button").forEach((b) =>
        b.addEventListener("click", () => {
          reveal.querySelectorAll("button").forEach((x) => (x.disabled = true));
          b.style.opacity = "1";
          finishBeat(holder, beat, response, { selfVerdict: b.dataset.v });
        })
      );
    }
    renderMathIn(reveal);
  });
}

/* A beat answered by a tap: one option is right, every other one carries
 * the misconception it was written for, and the reveal adjudicates the
 * option chosen, not just the right one. Graded here so reading works
 * detached; the server grades the same choice again from the same options. */
function renderChoiceBeat(holder, beat) {
  holder.innerHTML = `
    <div class="beat-label">${BEAT_LABELS[beat.type] || "Your turn"}</div>
    <div class="beat-prompt">${mdLite(beat.prompt)}</div>
    <div class="beat-options">${beat.options.map((o, i) =>
      `<button class="option" data-i="${i}"><span class="letter">${String.fromCharCode(65 + i)}</span> ${mdLite(o.text)}</button>`).join("")}</div>
    <div class="reveal" hidden></div>`;
  renderMathIn(holder);
  holder.querySelectorAll("button.option").forEach((b) =>
    b.addEventListener("click", () => {
      const i = Number(b.dataset.i);
      holder.querySelectorAll("button.option").forEach((x) => (x.disabled = true));
      b.classList.add("chosen");
      const g = gradeChoice(beat.options, i);
      const reveal = holder.querySelector(".reveal");
      reveal.hidden = false;
      reveal.innerHTML =
        `<div class="${g.correct ? "verdict-ok" : "verdict-bad"}">${g.correct ? "Correct." : "Not quite."}</div>` +
        (g.explain ? `<p>${mdLite(g.explain)}</p>` : "") +
        (g.correct ? "" : `<p><strong>Answer:</strong> ${mdLite(g.answer)}</p>`);
      renderMathIn(reveal);
      finishBeat(holder, beat, beat.options[i].text, { selectedIndex: i, mechanicalVerdict: g.correct ? "pass" : "fail" });
    })
  );
}

/* Mirrors checkers.CheckMCQ: the chosen option's own verdict and reveal. */
function gradeChoice(options, index) {
  const chosen = options[index];
  const right = options.find((o) => o.correct);
  if (!chosen) return { correct: false, explain: "no option selected", answer: right ? right.text : "" };
  return { correct: !!chosen.correct, explain: chosen.explain || "", answer: right ? right.text : "" };
}

function finishBeat(holder, beat, response, extra) {
  holder.classList.add("done");
  answered[beat.id] = true;
  post({ type: "beat", beatId: beat.id, response, ...extra });
}

/* Mirrors server/checkers.py for the detached path. */
function gradeMechanical(check, expected, given) {
  if (check === "exact") {
    // Whitespace inside an exact answer is never the meaning: "4x7" and
    // "4 x 7" are the same shape. Collapsed first, then gone - as the
    // server does.
    return norm(given) === norm(expected) || compact(given) === compact(expected);
  }
  const m = /^numeric\(([\d.eE+-]+)\)$/.exec(check.trim());
  if (m) {
    const tol = parseFloat(m[1]);
    const exp = parseNumber(expected), got = parseNumber(given);
    if (exp === null || got === null) return false;
    // Absolute tolerance - must match server/checkers.py exactly, or a beat
    // grades one way offline and the other way at the boundary.
    return Math.abs(got - exp) <= tol + Math.abs(exp) * 1e-9;
  }
  return norm(given) === norm(expected) || compact(given) === compact(expected);
}

function compact(s) {
  return String(s).replace(/\s+/g, "").toLowerCase();
}

function parseNumber(text) {
  const m = String(text).replace(/,/g, "").match(/-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?(?:\s*\/\s*-?\d+(?:\.\d+)?)?/);
  if (!m) return null;
  const t = m[0];
  if (t.includes("/")) {
    const [a, b] = t.split("/").map(parseFloat);
    return b ? a / b : null;
  }
  return parseFloat(t);
}

function norm(s) {
  // Must match server/checkers.py `_norm`: multi-value answers like
  // "4, 1; 11, 6" are the common exact-match case, and spacing around
  // separators carries no meaning. Diverging here means a beat grades one way
  // offline and the other way at the chapter boundary.
  return String(s).replace(/\s+/g, " ").trim().toLowerCase()
    .replace(/\s*([,;:])\s*/g, "$1");
}

/* Tiny markdown: paragraphs, bold, italics, inline code. The heavy conversion
 * happened server-side; beat prompts/answers are close to plain text. */
function mdLite(text) {
  return String(text)
    .split(/\n{2,}/)
    .map((p) =>
      "<p>" +
      escapeHtml(p)
        .replace(/`([^`]+)`/g, "<code>$1</code>")
        .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
        .replace(/\n/g, "<br>") +
      "</p>")
    .join("");
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"]/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}
