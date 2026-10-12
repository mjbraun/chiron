/* Math on a page: which runs of text are TeX, and setting them with KaTeX.
 *
 * The server decides what is math when it renders a chapter
 * (server-go/render/math.go) and leaves everything else as prose. KaTeX's
 * own auto-render has a looser rule: any two dollars in a run of text are
 * a span, so "about $33K MRR ... (Nous at $74.8K MRR" was set as one italic
 * equation. The page applies the server's rules itself and renders only
 * those spans, so a dollar amount is a dollar amount on both sides. Loaded
 * before book.js by chapter.html, and by the native math card. */

function mathIsSpace(c) {
  return c === " " || c === "\t" || c === "\n" || c === "\r" || c === "\f" || c === "\v";
}

function mathIsPunct(c) {
  return ".,;:!?)]}'\"".includes(c);
}

/* The exclusive end of the inline span opening at `open`, or 0 if this
 * dollar opens none: an opener is not followed by whitespace; a closer is
 * not preceded by it, or is but is followed by none of a word, a digit or
 * another dollar ("$\rho = $ sheets" is a span, "$5 or $10" two prices);
 * and a span never crosses a blank line. */
function mathInlineEnd(s, open) {
  if (open + 1 >= s.length || mathIsSpace(s[open + 1]) || s[open + 1] === "$") return 0;
  for (let j = open + 1; j < s.length; j++) {
    const c = s[j];
    if (c === "\\") { j++; continue; }
    if (c === "\n") {
      if (j + 1 < s.length && (s[j + 1] === "\n" || s[j + 1] === "\r")) return 0;
      continue;
    }
    if (c === "$") {
      if (mathIsSpace(s[j - 1]) && j + 1 < s.length && !mathIsSpace(s[j + 1]) && !mathIsPunct(s[j + 1])) return 0;
      return j + 1;
    }
  }
  return 0;
}

/* The math spans of a run of text as [{start, end, display}], end
 * exclusive: $$...$$ first, then inline spans by the rules above. A
 * backslash-escaped dollar is prose. */
function mathSpans(s) {
  const out = [];
  let i = 0;
  while (i < s.length) {
    if (s[i] !== "$") { i++; continue; }
    if (i > 0 && s[i - 1] === "\\") { i++; continue; }
    if (s.startsWith("$$", i)) {
      const end = s.indexOf("$$", i + 2);
      if (end >= 0) {
        out.push({ start: i, end: end + 2, display: true });
        i = end + 2;
        continue;
      }
      i += 2;
      continue;
    }
    const end = mathInlineEnd(s, i);
    if (end > 0) {
      out.push({ start: i, end, display: false });
      i = end;
      continue;
    }
    i++;
  }
  return out;
}

const MATH_SKIP_TAGS = new Set(["SCRIPT", "NOSCRIPT", "STYLE", "TEXTAREA", "PRE", "CODE", "OPTION"]);

/* Set every math span under el with KaTeX, leaving prose as it is. Text
 * already set (inside .katex) and code are left alone, so it is safe to
 * call again on an element that was rendered before. */
function renderMathIn(el) {
  if (typeof katex === "undefined" || !el) return;
  const texts = [];
  const walk = (node) => {
    for (const child of Array.from(node.childNodes)) {
      if (child.nodeType === 3) { texts.push(child); continue; }
      if (child.nodeType !== 1) continue;
      if (MATH_SKIP_TAGS.has(child.tagName) || child.classList.contains("katex")) continue;
      walk(child);
    }
  };
  walk(el);
  for (const text of texts) {
    const s = text.nodeValue;
    const spans = mathSpans(s);
    if (!spans.length) continue;
    const doc = text.ownerDocument;
    const frag = doc.createDocumentFragment();
    let prev = 0;
    for (const sp of spans) {
      if (sp.start > prev) frag.appendChild(doc.createTextNode(s.slice(prev, sp.start)));
      const tex = s.slice(sp.start + (sp.display ? 2 : 1), sp.end - (sp.display ? 2 : 1));
      const holder = doc.createElement("span");
      katex.render(tex, holder, { displayMode: sp.display, throwOnError: false });
      frag.appendChild(holder);
      prev = sp.end;
    }
    if (prev < s.length) frag.appendChild(doc.createTextNode(s.slice(prev)));
    text.parentNode.replaceChild(frag, text);
  }
}
