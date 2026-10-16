import SwiftUI
import WebKit
import PencilKit

struct ReaderContainer: View {
    @EnvironmentObject var library: Library
    @EnvironmentObject var session: BookSession
    @Environment(\.preferredPencilDoubleTapAction) private var doubleTapAction
    @Environment(\.preferredPencilSqueezeAction) private var squeezeAction
    /// Passed in rather than read from the session. Leaving the reader to
    /// unwrap `session.chapter` itself crashed on the way out: closing the
    /// book nils the chapter and switches screen, and SwiftUI re-evaluated
    /// this body against the nil chapter before the screen change took effect.
    let chapter: ChapterPayload
    @Environment(\.horizontalSizeClass) private var sizeClass
    private var compact: Bool { sizeClass == .compact }

    var body: some View {
        // The page runs under the bars, as the system's bars expect; the
        // web view insets its content by what they cover. The palette and
        // the card stay within the safe area.
        ReaderView(chapter: chapter, assets: library.bookAssets)
            .ignoresSafeArea()
            // A tapped highlight asks before it goes, beside the highlight;
            // a question has its card.
            .popover(isPresented: Binding(
                get: { session.removing != nil },
                set: { if !$0 { session.removing = nil } }),
                attachmentAnchor: .rect(.rect(session.removing?.at ?? .zero)), arrowEdge: .top) {
                VStack(alignment: .leading, spacing: 12) {
                    Text(session.removing?.mark.text ?? "")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                    Button("Remove highlight", role: .destructive) { session.removeMarkInHand() }
                        .buttonStyle(.bordered)
                }
                .padding(16)
                .frame(maxWidth: 320, alignment: .leading)
                .presentationCompactAdaptation(.popover)
            }
            .overlay(alignment: compact ? .bottomTrailing : .trailing) {
                Palette()
                    .padding(.trailing, 10)
                    .padding(.bottom, compact ? 10 : 0)
            }
            .overlay(alignment: .bottomTrailing) {
                if !compact, session.asking != nil {
                    AskCard()
                        .padding(16)
                        .transition(.move(edge: .bottom).combined(with: .opacity))
                }
            }
        .animation(.easeInOut(duration: 0.2), value: session.asking != nil)
        // In compact width the card is a bottom sheet: half the page stays
        // in view above it, with the passage it is about.
        .sheet(isPresented: Binding(
            get: { compact && session.asking != nil },
            set: { if !$0 { session.closeAsking() } })) {
            AskCard(sheet: true)
                .environmentObject(session)
                .environmentObject(session)
                .presentationDetents([.medium, .large])
                .presentationBackgroundInteraction(.enabled(upThrough: .medium))
        }
        // The Pencil Pro's gestures do what the reader chose for them in
        // Settings, as the Pencil settings promise; "show the palette" is
        // read as "next tool", since the palette is already on the page.
        .onPencilDoubleTap { _ in perform(doubleTapAction, from: "double-tap") }
        .onPencilSqueeze { phase in
            if case .ended = phase { perform(squeezeAction, from: "squeeze") }
        }
    }

    private func perform(_ action: PencilPreferredAction, from gesture: String) {
        #if DEBUG
        session.lastPencil = "\(gesture): \(action)"
        #endif
        if action == .ignore || action == .runSystemShortcut { return }
        if action == .switchEraser { session.flipEraser() }
        else if action == .switchPrevious { session.switchPrevious() }
        else if action == .showColorPalette || action == .showInkAttributes { session.tool = .pen }
        else { session.cycleTool() }
    }

}

/// The chapter page: a web view for the prose, the reader's ink as a
/// layer over it that follows the page's scrolling, and a pan on the page
/// that turns a drag into a run of text for the highlighter and the ask
/// tool.
struct ReaderView: UIViewRepresentable {
    let chapter: ChapterPayload
    /// Where the open book's pictures come from.
    let assets: BookAssets?
    @EnvironmentObject var session: BookSession
    @Environment(\.sizeCategory) private var sizeCategory
    /// Read so that evening turning the page re-runs the update: the ink
    /// is converted for the page it is on.
    @Environment(\.colorScheme) private var colorScheme

    func makeCoordinator() -> Coordinator {
        let c = Coordinator(session: session)
        #if DEBUG
        Coordinator.probe = c
        #endif
        return c
    }

    /// The system's body text scale, handed to the stylesheet so the page
    /// reflows with Dynamic Type rather than zooming.
    private var scale: CGFloat { UIFontMetrics(forTextStyle: .body).scaledValue(for: 100) / 100 }

    func makeUIView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        config.userContentController.add(context.coordinator, name: "bridge")
        // A book's own pictures answer under a scheme of their own: the
        // page cannot carry the server's key, and one already on this
        // device should not need the server at all.
        if let assets {
            context.coordinator.assetScheme = BookAssetScheme(assets: assets)
            config.setURLSchemeHandler(context.coordinator.assetScheme, forURLScheme: BookAssets.scheme)
        }
        let web = BookWebView(frame: .zero, configuration: config)
        web.isInspectable = true
        web.backgroundColor = .systemBackground
        web.scrollView.backgroundColor = .systemBackground
        // The bars over the page are the scroll view's safe area; the
        // content starts below the top one and scrolls beneath both.
        web.scrollView.contentInsetAdjustmentBehavior = .always
        context.coordinator.attach(to: web)
        load(into: web, context: context)
        return web
    }

    func updateUIView(_ web: WKWebView, context: Context) {
        let c = context.coordinator
        // Evening turns the page: the ink is converted for the page it is
        // on, so a change of appearance re-shows what is stored.
        _ = colorScheme
        // A new unit, or the same unit rewritten (a primer that grew from a
        // margin note), is a fresh page; marks re-apply on top.
        if c.loadedUnit != chapter.unit || c.loadedHTML != chapter.html.hashValue {
            load(into: web, context: context)
        } else if c.scale != scale {
            c.scale = scale
            web.evaluateJavaScript("setScale(\(scale))")
        }
        // Where another device left off, arriving from the server after
        // the page was asked for: the page moves there. Before the page is
        // ready the position waits for initChapter.
        if let restored = session.restoredPosition, restored.unit == chapter.unit,
           restored.n != c.restoredN {
            c.restoredN = restored.n
            c.position = restored.position
            if c.pageReady {
                web.evaluateJavaScript("setPosition(\(restored.position))")
            }
        }
        c.apply(tool: session.tool, color: session.penColor.uiColor)
        c.apply(marks: session.marks)
        c.apply(ink: session.inkData)
    }

    private func load(into web: WKWebView, context: Context) {
        let c = context.coordinator
        c.loadedUnit = chapter.unit
        c.loadedHTML = chapter.html.hashValue
        c.pendingChapter = chapter
        c.scale = scale
        c.position = session.position(for: chapter.unit)
        c.pageReady = false
        c.appliedMarks = nil
        guard let template = Bundle.main.url(forResource: "chapter", withExtension: "html") else { return }
        web.loadFileURL(template, allowingReadAccessTo: template.deletingLastPathComponent())
    }

    final class Coordinator: NSObject, WKScriptMessageHandler, PKCanvasViewDelegate, PageBridge {
        let session: BookSession
        weak var web: WKWebView?
        var loadedUnit: String?
        var loadedHTML: Int?
        var pendingChapter: ChapterPayload?
        var scale: CGFloat = 1
        var position: Double = 0
        /// The last position taken from the server that the page was told.
        var restoredN = 0
        var pageReady = false
        var appliedMarks: [Mark]?
        /// What the page last focused, for the harness.
        private(set) var pageFocus = ""
        /// Kept alive for as long as the web view is: the configuration
        /// holds it weakly.
        var assetScheme: BookAssetScheme?
        private var appliedTool: BookSession.Tool = .none
        private var appliedInk: Data?
        private var loadingInk = false

        private let canvas = PageInkCanvas()
        private let selector = UIPanGestureRecognizer()
        private var selectionStart: CGPoint = .zero
        private var scrollObservations: [NSKeyValueObservation] = []

        init(session: BookSession) {
            self.session = session
            super.init()
            session.page = self
        }

        func attach(to web: WKWebView) {
            self.web = web
            // "Send to Chiron" in the page's own selection menu: the
            // passage goes where the capture tool's does.
            (web as? BookWebView)?.onCapture = { [weak self] text in
                Task { @MainActor in self?.session.capturePassage(text) }
            }

            // The ink is a layer over the web view, not inside its scroll
            // view: WebKit holds touches inside its own scroll view for the
            // page's sake and hands them on late, so a stroke drawn there
            // only showed once the Pencil lifted. The canvas is itself a
            // scroll view; its content mirrors the page's size and offset,
            // so strokes are stored in page coordinates and stay with the
            // text they annotate. On the iPad only the Pencil draws, and a
            // finger scrolls: the canvas is the scroll view a finger moves
            // while the pen is up (the page follows it), whatever the
            // system's "only draw with Apple Pencil" setting says. The
            // Simulator has no Pencil; the tests draw with a finger.
            #if targetEnvironment(simulator)
            canvas.drawingPolicy = .anyInput
            #else
            canvas.drawingPolicy = .pencilOnly
            #endif
            canvas.backgroundColor = .clear
            canvas.isOpaque = false
            canvas.isScrollEnabled = false
            canvas.alwaysBounceVertical = true
            canvas.showsVerticalScrollIndicator = false
            canvas.contentInsetAdjustmentBehavior = .never
            canvas.delegate = self
            canvas.isUserInteractionEnabled = false
            canvas.onTouch = { [weak self] touch in self?.eraserTouched(touch) }
            canvas.translatesAutoresizingMaskIntoConstraints = false
            web.addSubview(canvas)
            NSLayoutConstraint.activate([
                canvas.leadingAnchor.constraint(equalTo: web.leadingAnchor),
                canvas.trailingAnchor.constraint(equalTo: web.trailingAnchor),
                canvas.topAnchor.constraint(equalTo: web.topAnchor),
                canvas.bottomAnchor.constraint(equalTo: web.bottomAnchor),
            ])
            scrollObservations = [
                web.scrollView.observe(\.contentSize, options: [.initial, .new]) { [weak self] sv, _ in
                    guard let self else { return }
                    self.canvas.contentSize = CGSize(width: max(sv.contentSize.width, sv.bounds.width),
                                                     height: max(sv.contentSize.height, sv.bounds.height))
                    self.canvas.contentInset = sv.adjustedContentInset
                    self.canvas.contentOffset = sv.contentOffset
                },
                web.scrollView.observe(\.contentOffset, options: [.initial, .new]) { [weak self] sv, _ in
                    guard let self else { return }
                    // The inset follows the bars, which come and go with a
                    // tap; the offset is measured from the same origin.
                    // While a finger moves the canvas, the page follows the
                    // canvas, not the other way round.
                    self.canvas.contentInset = sv.adjustedContentInset
                    if !self.fingerScrolling, self.canvas.contentOffset != sv.contentOffset {
                        self.canvas.contentOffset = sv.contentOffset
                    }
                },
            ]

            // Text selection is a pan on the web view itself rather than a
            // layer over it, so a tap still reaches the page: tapping a
            // mark's badge is how a question reopens. While a selecting
            // tool is up the page does not scroll; the drag is the selection.
            selector.maximumNumberOfTouches = 1
            selector.cancelsTouchesInView = false
            selector.isEnabled = false
            selector.addTarget(self, action: #selector(selectPan(_:)))
            web.addGestureRecognizer(selector)
        }

        // MARK: tools

        private var appliedColor: UIColor?

        #if DEBUG
        /// The harness reads what the canvas actually holds, not what the
        /// session thinks it asked for.
        static weak var probe: Coordinator?
        var canvasPen: String {
            guard let ink = canvas.tool as? PKInkingTool else { return String(describing: type(of: canvas.tool)) }
            var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
            ink.color.getRed(&r, green: &g, blue: &b, alpha: &a)
            return String(format: "pen rgb(%.0f,%.0f,%.0f) w%.1f", r * 255, g * 255, b * 255, ink.width)
        }
        var canvasTouches: Int { canvas.touchesSeen }
        var canvasHit: String { canvas.lastHitTest }
        var canvasFrame: String { "\(canvas.frame) content \(canvas.contentSize) offset \(canvas.contentOffset) enabled \(canvas.isUserInteractionEnabled)" }
        #endif

        func apply(tool: BookSession.Tool, color: UIColor) {
            guard tool != appliedTool || (tool == .pen && (color != appliedColor || style != shownStyle)) else { return }
            appliedTool = tool
            appliedColor = color
            switch tool {
            case .pen:
                canvas.tool = PKInkingTool(.pen, color: Ink.pen(color, on: style), width: 2.5)
                canvas.isUserInteractionEnabled = true
                canvas.isScrollEnabled = true
                selector.isEnabled = false
                web?.scrollView.isScrollEnabled = true
            case .eraser:
                canvas.tool = PKEraserTool(.vector)
                canvas.isUserInteractionEnabled = true
                canvas.isScrollEnabled = true
                selector.isEnabled = false
                web?.scrollView.isScrollEnabled = true
            case .highlighter, .ask, .note, .capture:
                canvas.isUserInteractionEnabled = false
                canvas.isScrollEnabled = false
                selector.isEnabled = true
                web?.scrollView.isScrollEnabled = false
            case .none:
                canvas.isUserInteractionEnabled = false
                canvas.isScrollEnabled = false
                selector.isEnabled = false
                web?.scrollView.isScrollEnabled = true
            }
            // Asked before the page is told the tool's drag is the
            // selection, since that turns the page's own selection off.
            takeSelection(for: tool)
            applySelecting()
        }

        /// Whether a tool picked while the reader has text selected with
        /// the page's own handles works on that text: the words are chosen
        /// already, so the question tool opens on them with no second drag.
        /// The ink tools have no use for a selection.
        static func takesSelection(_ tool: BookSession.Tool, selection: String) -> Bool {
            switch tool {
            case .highlighter, .ask, .note, .capture:
                return !selection.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            case .none, .pen, .eraser:
                return false
            }
        }

        /// The page's own selection becomes the tool's run, the way a
        /// drag's lift does, and the selection goes with its handles.
        private func takeSelection(for tool: BookSession.Tool) {
            guard let web = web as? BookWebView, pageReady,
                  Self.takesSelection(tool, selection: web.selection) else { return }
            web.selection = ""
            take(run: "takeSelection()")
        }

        /// The page is told whether a tool's drag is the selection, so its
        /// own text selection keeps out of the drag's way; told again when
        /// the page is reloaded, since the page starts over.
        private func applySelecting() {
            guard pageReady else { return }
            web?.evaluateJavaScript("setSelecting(\(selector.isEnabled))")
        }

        // MARK: a finger on the canvas

        private var fingerScrolling: Bool {
            canvas.isTracking || canvas.isDragging || canvas.isDecelerating
        }

        func scrollViewDidScroll(_ scrollView: UIScrollView) {
            guard scrollView === canvas, fingerScrolling, let web else { return }
            let sv = web.scrollView
            let top = -sv.adjustedContentInset.top
            let bottom = max(top, sv.contentSize.height - sv.bounds.height + sv.adjustedContentInset.bottom)
            let y = min(max(canvas.contentOffset.y, top), bottom)
            if abs(sv.contentOffset.y - y) > 0.5 {
                sv.contentOffset = CGPoint(x: sv.contentOffset.x, y: y)
            }
        }

        // MARK: ink

        func apply(ink: Data?) {
            guard ink != appliedInk || style != shownStyle else { return }
            appliedInk = ink
            shownStyle = style
            loadingInk = true
            // Ink is kept as it looks on a light page; the canvas shows it
            // as the page is now.
            canvas.drawing = Ink.shown(ink.flatMap { try? PKDrawing(data: $0) } ?? PKDrawing(), in: style)
            loadingInk = false
        }

        /// The appearance the page is in, which the ink has to suit.
        private var style: UIUserInterfaceStyle { canvas.traitCollection.userInterfaceStyle }
        private var shownStyle: UIUserInterfaceStyle = .unspecified

        func canvasViewDrawingDidChange(_ canvasView: PKCanvasView) {
            guard !loadingInk else { return }
            let kept = Ink.canonical(canvasView.drawing, drawnIn: style)
            let data = kept.strokes.isEmpty ? nil : kept.dataRepresentation()
            appliedInk = data
            Task { @MainActor in self.session.saveInk(data) }
        }

        // MARK: marks

        /// The eraser over a highlight takes it off, as it takes ink off.
        private func eraserTouched(_ touch: UITouch) {
            guard appliedTool == .eraser, let web else { return }
            let o = pageOrigin
            let p = touch.location(in: web)
            web.evaluateJavaScript("markAt(\(p.x - o.x), \(p.y - o.y))") { [weak self] result, _ in
                guard let self, let id = result as? String else { return }
                Task { @MainActor in self.session.erase(markID: id) }
            }
        }

        func apply(marks: [Mark]) {
            guard pageReady, marks != appliedMarks else { return }
            appliedMarks = marks
            let list = marks.map { ["id": $0.id, "kind": $0.kind.rawValue, "start": $0.start, "end": $0.end] as [String: Any] }
            if let data = try? JSONSerialization.data(withJSONObject: list),
               let json = String(data: data, encoding: .utf8) {
                web?.evaluateJavaScript("applyMarks(\(json))")
            }
        }

        /// The page measures from the top of its content, which sits below
        /// the bar; the view measures from its own top. Both ways.
        private var pageOrigin: CGPoint {
            guard let web else { return .zero }
            let inset = web.scrollView.adjustedContentInset
            return CGPoint(x: inset.left, y: inset.top)
        }

        /// Where a touch on the view falls on the page, kept within the
        /// part of the page the bars leave uncovered. The page answers
        /// nothing for a point under a bar or past the bottom edge, and a
        /// drag ends there whenever the reader selects up to the first
        /// line under the title bar: that is a selection all the same.
        static func pagePoint(_ location: CGPoint, inset: UIEdgeInsets, in bounds: CGRect) -> CGPoint {
            let width = max(bounds.width - inset.left - inset.right - 1, 0)
            let height = max(bounds.height - inset.top - inset.bottom - 1, 0)
            return CGPoint(x: min(max(location.x - inset.left, 0), width),
                           y: min(max(location.y - inset.top, 0), height))
        }

        @objc private func selectPan(_ g: UIPanGestureRecognizer) {
            guard let web else { return }
            let p = Self.pagePoint(g.location(in: web), inset: web.scrollView.adjustedContentInset, in: web.bounds)
            switch g.state {
            case .began: selectionStart = p
            case .changed: preview(from: selectionStart, to: p)
            // A drag the system cuts short (a second touch, a gesture of
            // its own) ends like any other: the reader saw the run
            // highlighted, and a highlight left on the page with no card
            // is the one thing that must not happen.
            case .ended, .cancelled: select(from: selectionStart, to: p)
            default: break
            }
        }

        private func preview(from a: CGPoint, to b: CGPoint) {
            web?.evaluateJavaScript("previewRange(\(a.x), \(a.y), \(b.x), \(b.y))")
        }

        private func select(from a: CGPoint, to b: CGPoint) {
            take(run: "endSelection(\(a.x), \(a.y), \(b.x), \(b.y))")
        }

        /// The run the page answers with goes to the tool that is up: a
        /// highlight, a question or a note on it, or a capture of it.
        private func take(run js: String) {
            web?.evaluateJavaScript(js) { [weak self] result, _ in
                guard let self, let r = result as? [String: Any],
                      let start = r["start"] as? Int, let end = r["end"] as? Int, let text = r["text"] as? String else { return }
                Task { @MainActor in
                    let kind: Mark.Kind
                    switch self.session.tool {
                    case .capture:
                        self.session.capturePassage(text)
                        return
                    case .ask: kind = .question
                    case .note: kind = .note
                    default: kind = .highlight
                    }
                    self.session.addMark(kind: kind, start: start, end: end, text: text)
                }
            }
        }

        /// Where a mark's badge is on screen, in the web view's coordinates.
        func rect(of markID: String) async -> CGRect? {
            guard let r = await pageRect(of: markID) else { return nil }
            let o = pageOrigin
            return r.offsetBy(dx: o.x, dy: o.y)
        }

        func pageRect(of markID: String) async -> CGRect? {
            let escaped = markID.replacingOccurrences(of: "'", with: "\\'")
            return await rect(from: "markRect('\(escaped)')")
        }

        /// The first element matching a selector, in the page view's
        /// coordinates, for the harness to tap.
        func rect(matching selector: String) async -> CGRect? {
            let escaped = selector.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "'", with: "\\'")
            guard let r = await rect(from: "elementRect('\(escaped)')") else { return nil }
            let o = pageOrigin
            return r.offsetBy(dx: o.x, dy: o.y)
        }

        private func rect(from js: String) async -> CGRect? {
            guard let web, pageReady else { return nil }
            guard let r = try? await web.evaluateJavaScript(js) as? [String: Any],
                  let x = r["x"] as? Double, let y = r["y"] as? Double,
                  let w = r["width"] as? Double, let h = r["height"] as? Double else { return nil }
            return CGRect(x: x, y: y, width: w, height: h)
        }

        /// The harness's window into the page: run a line of JavaScript and
        /// read back what it evaluates to.
        func eval(_ js: String) async -> String {
            guard let web, pageReady else { return "" }
            let out = try? await web.evaluateJavaScript(js)
            return out.map { String(describing: $0) } ?? ""
        }

        func find(_ text: String) async -> (start: Int, end: Int, text: String)? {
            guard let web, pageReady else { return nil }
            let escaped = text.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "'", with: "\\'")
            guard let r = try? await web.evaluateJavaScript("findText('\(escaped)')") as? [String: Any],
                  let start = r["start"] as? Int, let end = r["end"] as? Int, let found = r["text"] as? String else { return nil }
            return (start, end, found)
        }

        // MARK: the page's messages

        func userContentController(_ ucc: WKUserContentController,
                                   didReceive message: WKScriptMessage) {
            guard let body = message.body as? [String: Any],
                  let type = body["type"] as? String else { return }
            switch type {
            case "ready":
                if let ch = pendingChapter,
                   let data = try? JSONEncoder().encode(PagePayload(ch, responses: session.beatResponses)),
                   let json = String(data: data, encoding: .utf8) {
                    let base = BookAssets.base(subject: self.session.subjectID)
                    web?.evaluateJavaScript("setScale(\(scale)); setAssetBase('\(base)'); initChapter(\(json), \(position))") { [weak self] _, _ in
                        guard let self else { return }
                        self.pageReady = true
                        self.applySelecting()
                        Task { @MainActor in self.apply(marks: self.session.marks) }
                    }
                }
            case "focus":
                // The keyboard follows first responder. With a tool up the
                // canvas holds it, so a field the page focused would take
                // the typing with no keyboard to type on.
                pageFocus = body["tag"] as? String ?? ""
                if !pageFocus.isEmpty, canvas.isFirstResponder {
                    canvas.resignFirstResponder()
                    web?.becomeFirstResponder()
                }
            case "selection":
                // What the page has selected on its own, for its menu.
                (web as? BookWebView)?.selection = body["text"] as? String ?? ""
            case "controls":
                if let rects = body["rects"] as? [[Double]] {
                    canvas.controls = rects.filter { $0.count == 4 }.map { CGRect(x: $0[0], y: $0[1], width: $0[2], height: $0[3]) }
                }
            case "scroll":
                if let unit = loadedUnit, let position = body["position"] as? Double {
                    Task { @MainActor in self.session.recordPosition(unit: unit, position: position) }
                }
            case "tap":
                Task { @MainActor in self.session.toggleChrome() }
            case "mark":
                if let id = body["id"] as? String {
                    Task { @MainActor in await self.session.openMark(id) }
                }
            case "about":
                Task { @MainActor in self.session.showAbout() }
            case "beat":
                let r = BeatResponse(
                    beatId: body["beatId"] as? String ?? "",
                    response: body["response"] as? String ?? "",
                    selfVerdict: body["selfVerdict"] as? String,
                    mechanicalVerdict: body["mechanicalVerdict"] as? String,
                    selectedIndex: body["selectedIndex"] as? Int)
                Task { @MainActor in self.session.recordBeat(r) }
            default:
                break
            }
        }
    }
}

/// What the page is handed of a chapter: the parts it draws. The record
/// of the chapter's writing stays native, since its prompts are a
/// chapter's size again and the page shows only the colophon, which is
/// in the HTML already.
struct PagePayload: Encodable {
    let title: String
    let minutes: Int
    let html: String
    let beats: [JSONValue]
    /// The beats answered already, so the page shows them answered.
    let responses: [BeatResponse]

    init(_ ch: ChapterPayload, responses: [BeatResponse] = []) {
        title = ch.title; minutes = ch.minutes; html = ch.html; beats = ch.beats
        self.responses = responses
    }
}

/// A drag across the page selects a run of text. Reports the drag as it
/// moves (for the provisional highlight) and when it ends.

/// The page's web view with one more item in its selection menu: the
/// selected passage goes to Chiron, the way a PDF's does from its menu.
/// The page reports its selection over the bridge as it changes, so the
/// menu is built knowing whether there is one.
final class BookWebView: WKWebView {
    /// What the page has selected on its own; empty when nothing is.
    var selection = ""
    var onCapture: ((String) -> Void)?

    override func buildMenu(with builder: any UIMenuBuilder) {
        super.buildMenu(with: builder)
        guard let item = Self.sendItem(for: selection, send: { [weak self] in self?.send() }) else { return }
        builder.insertChild(item, atStartOfMenu: .standardEdit)
    }

    /// The menu's item, when there is a passage to send.
    static func sendItem(for selection: String, send: @escaping () -> Void) -> UIMenu? {
        guard !selection.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        let action = UIAction(title: "Send to Chiron", image: UIImage(systemName: "text.badge.plus")) { _ in send() }
        return UIMenu(options: .displayInline, children: [action])
    }

    /// The passage goes over, and the selection with its handles goes.
    func send() {
        let text = selection.trimmingCharacters(in: .whitespacesAndNewlines)
        selection = ""
        guard !text.isEmpty else { return }
        evaluateJavaScript("document.getSelection().removeAllRanges()")
        onCapture?(text)
    }
}

/// The ink layer over the page.
final class PageInkCanvas: PKCanvasView {
    /// Every touch that lands on the ink, for the eraser to look under.
    var onTouch: ((UITouch) -> Void)?
    #if DEBUG
    /// Touches that reached the canvas, and what the last hit test saw,
    /// for the harness.
    private(set) var touchesSeen = 0
    private(set) var lastHitTest = ""
    #endif

    /// Where the page's controls are, in the page's document coordinates,
    /// which are the canvas's own: its content follows the page's scroll.
    var controls: [CGRect] = []

    /// A touch over a beat's button or field goes to the page even with
    /// the pen up: the tool cannot tell a tap from a stroke, but a stroke
    /// over a control is not what anyone means. (The event does not show
    /// its touches at hit-test time, so the touch's kind cannot decide.)
    override func hitTest(_ point: CGPoint, with event: UIEvent?) -> UIView? {
        if isUserInteractionEnabled, controls.contains(where: { $0.insetBy(dx: -6, dy: -6).contains(point) }) {
            #if DEBUG
            lastHitTest = "page"
            #endif
            return nil
        }
        #if DEBUG
        lastHitTest = "canvas"
        #endif
        return super.hitTest(point, with: event)
    }
    override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent?) {
        #if DEBUG
        touchesSeen += touches.count
        #endif
        touches.forEach { onTouch?($0) }
        super.touchesBegan(touches, with: event)
    }
}
