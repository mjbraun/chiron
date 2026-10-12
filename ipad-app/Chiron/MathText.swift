import SwiftUI
import WebKit

/// Text that may contain LaTeX, rendered with the bundled KaTeX.
///
/// SwiftUI's `Text` renders markdown but not math, so check prompts like
/// "compare $q \cdot k_1$ with $q \cdot k_2$" would reach the learner as raw
/// TeX - and the checks are the most math-heavy moment in the app. This keeps
/// chapter typography and check typography identical, because both go through
/// KaTeX.
///
/// Falls back to plain SwiftUI text when there is no `$`, so the common case
/// costs nothing.
struct MathText: View {
    let text: String
    var size: CGFloat
    /// Markdown paragraphs, emphasis and code alongside the math, in one
    /// web view: for the tutor's answers, which use both.
    var rich = false

    @State private var height: CGFloat

    init(text: String, size: CGFloat = 19, rich: Bool = false) {
        self.text = text
        self.size = size
        self.rich = rich
        // Start from an estimate rather than a fixed small value: if the
        // JavaScript measurement never lands, over-estimating costs blank
        // space while under-estimating hides the question being asked.
        _height = State(initialValue: Self.estimatedHeight(text, size: Self.scaled(size)))
    }

    /// Rough layout guess: wrapped lines at ~60 characters, plus room for each
    /// display-math block, which sets on its own line and is taller than prose.
    static func estimatedHeight(_ text: String, size: CGFloat) -> CGFloat {
        let displayBlocks = text.components(separatedBy: "$$").count / 2
        let lines = max(1, Int(ceil(Double(text.count) / 60.0)))
        return CGFloat(lines) * size * 1.5
            + CGFloat(displayBlocks) * size * 2.2
            + 8
    }

    /// Authored text wraps at 80 columns; a single newline is a soft break
    /// and only a blank line means a paragraph. HTML collapses the former by
    /// itself, SwiftUI text does not.
    static func reflow(_ text: String) -> String {
        text.components(separatedBy: "\n\n")
            .map { $0.replacingOccurrences(of: "\n", with: " ").trimmingCharacters(in: .whitespaces) }
            .joined(separator: "\n\n")
    }

    /// The point size after the reader's text-size setting: the web view
    /// does not scale with Dynamic Type by itself, so the same metrics the
    /// system applies to body text are applied here.
    static func scaled(_ size: CGFloat) -> CGFloat {
        UIFontMetrics(forTextStyle: .body).scaledValue(for: size)
    }

    /// Markdown-lite to HTML: paragraphs, bullets, bold, italics, code.
    /// Math is left alone for KaTeX. Mirrors mdLite in book.js.
    static func html(fromMarkdown md: String) -> String {
        func inline(_ s: String) -> String {
            var t = s.replacingOccurrences(of: "&", with: "&amp;")
                .replacingOccurrences(of: "<", with: "&lt;")
                .replacingOccurrences(of: ">", with: "&gt;")
            t = t.replacingOccurrences(of: #"`([^`]+)`"#, with: "<code>$1</code>", options: .regularExpression)
            t = t.replacingOccurrences(of: #"\*\*([^*]+)\*\*"#, with: "<strong>$1</strong>", options: .regularExpression)
            t = t.replacingOccurrences(of: #"(^|[^*])\*([^*\n]+)\*"#, with: "$1<em>$2</em>", options: .regularExpression)
            return t
        }
        var out = ""
        for para in md.components(separatedBy: "\n\n") {
            let lines = para.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
            if lines.allSatisfy({ $0.trimmingCharacters(in: .whitespaces).hasPrefix("- ") || $0.trimmingCharacters(in: .whitespaces).isEmpty }) && lines.contains(where: { !$0.isEmpty }) {
                out += "<ul>" + lines.filter { !$0.isEmpty }.map { "<li>" + inline(String($0.trimmingCharacters(in: .whitespaces).dropFirst(2))) + "</li>" }.joined() + "</ul>"
            } else {
                out += "<p>" + inline(lines.joined(separator: " ")) + "</p>"
            }
        }
        return out
    }

    var body: some View {
        if rich {
            MathWebView(text: Self.html(fromMarkdown: text), size: Self.scaled(size), height: $height, preformatted: true)
                .frame(height: height)
        } else if text.contains("$") {
            MathWebView(text: text, size: Self.scaled(size), height: $height)
                .frame(height: height)
        } else {
            Text(.init(Self.reflow(text)))
                .font(Typography.serif(size))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

private struct MathWebView: UIViewRepresentable {
    let text: String
    let size: CGFloat
    @Binding var height: CGFloat
    /// The text is already HTML.
    var preformatted = false

    func makeCoordinator() -> Coordinator { Coordinator(height: $height) }

    func makeUIView(context: Context) -> WKWebView {
        let web = WKWebView()
        web.isOpaque = false
        web.backgroundColor = .clear
        web.scrollView.isScrollEnabled = false
        web.scrollView.backgroundColor = .clear
        web.navigationDelegate = context.coordinator
        context.coordinator.load(web, text: text, size: size, preformatted: preformatted)
        return web
    }

    func updateUIView(_ web: WKWebView, context: Context) {
        if context.coordinator.loadedText != text || context.coordinator.loadedSize != size {
            context.coordinator.load(web, text: text, size: size, preformatted: preformatted)
        }
    }

    final class Coordinator: NSObject, WKNavigationDelegate {
        @Binding var height: CGFloat
        var loadedText: String?
        var loadedSize: CGFloat = 0

        init(height: Binding<CGFloat>) { _height = height }

        func load(_ web: WKWebView, text: String, size: CGFloat, preformatted: Bool) {
            guard let res = Bundle.main.resourceURL else { return }
            loadedText = text
            loadedSize = size
            let escaped = preformatted ? text : text
                .replacingOccurrences(of: "&", with: "&amp;")
                .replacingOccurrences(of: "<", with: "&lt;")
                .replacingOccurrences(of: ">", with: "&gt;")
            let html = """
            <!DOCTYPE html><html><head><meta charset="utf-8">
            <meta name="viewport" content="width=device-width, initial-scale=1">
            <link rel="stylesheet" href="katex/katex.min.css">
            <style>
              :root { color-scheme: light dark; }
              html, body { margin:0; padding:0; background:transparent; }
              @font-face { font-family: "Source Serif 4"; src: url("fonts/SourceSerif4-Regular.ttf"); font-weight: 400; }
              @font-face { font-family: "Source Serif 4"; src: url("fonts/SourceSerif4-It.ttf"); font-weight: 400; font-style: italic; }
              @font-face { font-family: "Source Serif 4"; src: url("fonts/SourceSerif4-Semibold.ttf"); font-weight: 600; }
              body {
                font: \(size)px/1.5 \(Typography.webSerif);
                color: #1a1a1a;
                -webkit-text-size-adjust: 100%;
              }
              @media (prefers-color-scheme: dark) { body { color: #d8d3c8; } }
              .katex-display { overflow-x:auto; overflow-y:hidden; margin:0.4em 0; }
              code { font-family: ui-monospace, Menlo, monospace; font-size: 0.85em; }
              p { margin: 0 0 0.6em; } p:last-child { margin-bottom: 0; }
              ul { margin: 0.2em 0 0.6em 1.2em; padding: 0; }
            </style>
            <script src="katex/katex.min.js"></script>
            <script src="math.js"></script>
            </head><body><div id="c">\(escaped)</div>
            <script>renderMathIn(document.getElementById('c'));</script></body></html>
            """
            web.loadHTMLString(html, baseURL: res)
        }

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            // KaTeX lays out after load, so the first measurement can be short
            // and would clip tall display equations. Re-measure a few times.
            var tries = 0
            func measure() {
                webView.evaluateJavaScript("document.body.scrollHeight") { result, _ in
                    if let h = result as? CGFloat, h > 0, abs(h - self.height) > 1 {
                        self.height = h
                    }
                    tries += 1
                    if tries < 5 {
                        DispatchQueue.main.asyncAfter(deadline: .now() + 0.2, execute: measure)
                    }
                }
            }
            measure()
        }
    }
}
