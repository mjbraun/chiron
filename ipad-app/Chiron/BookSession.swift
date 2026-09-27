import Foundation
import PencilKit
import UIKit

/// One book, open: its chapter, the learner's position in it, and the state
/// machine from placement through checks, results, and the next chapter.
/// A `Library` holds one per subject; the UI shows one at a time.
///
/// Everything needed to survive a relaunch mid-flight persists under
/// Application Support, keyed by subject: the current chapter, collected beat
/// responses, the cached book state, and a pending authoring wait.
@MainActor
final class BookSession: ObservableObject {
    enum Screen {
        case placement
        case series
        case reading
        case pretest
        case check
        case results(ResultsDoc, Gate)
        case authoring(String)
        case takingBreak(BreakSuggestion)
        case error(String)
        /// Nothing to show yet: opening, or the server is away and there is
        /// no cached chapter.
        case empty
    }

    /// What the learner is waiting on, in the tablet's words.
    enum Wait: String {
        case opening = "Opening the book."
        case grading = "Grading your answers."
        case resetting = "Starting over."
    }

    static let unreachable = "The server can't be reached."

    @Published var screen: Screen = .empty
    @Published var wait: Wait?
    /// While the next chapter is being written: the server's stage and
    /// when the build began, for the waiting screen.
    @Published var authoringStage: String?
    @Published var authoringSince: Date?
    @Published var chapter: ChapterPayload?
    @Published var bookState: BookState?
    @Published var lastResults: [GradeResult] = []
    /// A non-fatal problem worth a line on the current screen.
    @Published var errorMessage: String?
    /// Reader chrome (top strip, bottom bar) hidden by a tap on the page.
    @Published var chromeHidden = false
    /// The contents beside the reader (regular width) or over it (compact).
    @Published var contentsShown = false

    let subjectID: String
    let title: String
    let service: ChironService
    /// Seconds between /chapter polls while the server writes.
    var pollInterval: TimeInterval = 2

    private var beatResponses: [BeatResponse] = []
    private var chapterOpenedAt: Date?
    private var pretestDone = false
    /// Where the reader left each chapter: a fraction of its scroll, 0 at
    /// the top and 1 at the bottom.
    private var positions: [String: Double] = [:]
    /// The reader's marks on the current chapter: highlights and questions.
    @Published private(set) var marks: [Mark] = []
    /// The reader's ink over the current chapter, as PencilKit data.
    /// The page's ink, as PencilKit data. Not published: every stroke
    /// updates it, and a view update per stroke re-ran the reader's update
    /// while the Pencil was still down, which showed as the page flickering.
    var inkData: Data?
    private var inkSave: Task<Void, Never>?
    /// What undo takes back, the latest last: a mark placed, or the ink as
    /// it was before a stroke. Published, so the palette's button follows
    /// it and an undone stroke reaches the page, whose ink is not.
    enum Undoable: Equatable {
        case mark(String)
        case ink(Data?)
    }
    @Published private(set) var undoable: [Undoable] = []
    var canUndo: Bool { !undoable.isEmpty }
    /// A highlight tapped on the page, waiting on the reader's word to go,
    /// and where it is, in the page's coordinates, for the question to sit
    /// beside it.
    struct Removing {
        let mark: Mark
        let at: CGRect
    }
    @Published var removing: Removing?

    /// The pen's colour, kept across books.
    enum PenColor: String, CaseIterable {
        case red, blue, green, black
        var uiColor: UIColor {
            switch self {
            case .red: return .systemRed
            case .blue: return .systemBlue
            case .green: return .systemGreen
            case .black: return .label
            }
        }
    }
    @Published var penColor: PenColor = PenColor(rawValue: UserDefaults.standard.string(forKey: "penColor") ?? "") ?? .red {
        didSet { UserDefaults.standard.set(penColor.rawValue, forKey: "penColor") }
    }
    /// A question being asked or answered, shown in the ask card.
    @Published var asking: Asking?
    /// The device's own model, asked only when the tutor cannot be
    /// reached. The library sets it; a session made bare has none, so a
    /// test's "tutor away" is the same on a Mac with a model and without.
    var localTutor: PassageAnswerer?

    struct Asking: Equatable {
        var mark: Mark
        var busy = false
        var error: String?
    }

    /// The palette's tools over the page. Pen and eraser are ink; the
    /// highlighter and ask tools select runs of text.
    enum Tool: String, CaseIterable {
        case none, pen, highlighter, ask, eraser
        /// Primers only: a margin note that extends the document.
        case note
        /// A passage of this book into a capture: a summary, a primer or a
        /// book of its own.
        case capture
    }

    /// Where a captured passage goes: the shelf's capture card.
    var onCapture: ((String) -> Void)?

    /// The capture tool's drag ended on a passage; the tool goes down and
    /// the card opens with the words.
    func capturePassage(_ text: String) {
        tool = .none
        onCapture?(text)
    }
    @Published var tool: Tool = .none {
        didSet { if oldValue != tool && oldValue != .none { lastTool = oldValue } }
    }
    /// The tool that was up before this one, for the Pencil's "switch to
    /// the previous tool" and for the eraser to hand back to.
    private var lastTool: Tool = .pen

    /// "book" or "primer". A primer has no check; its loop is read,
    /// annotate, and extend from margin notes.
    var kind: String = "book"
    /// Told which chapter is now open, for a blog that counts what the
    /// reader has not seen.
    var onChapterRead: ((String) -> Void)?
    var isPrimer: Bool { kind == "primer" }
    /// A primer and an imported book are read as they are: their chapters
    /// are whole and there is nothing to answer at the end of one.
    var readsAsIs: Bool { kind == "primer" || kind == "reading" || kind == "feed" }

    /// The page, when one is loaded: what a script or a gesture needs from it.
    weak var page: PageBridge?

    /// Squeeze on a Pencil Pro: the next tool round the palette.
    /// Squeeze on a Pencil: the next tool on the palette, round and round;
    /// with nothing up, the pen. Putting a tool down is a tap on it.
    func cycleTool() {
        let order: [Tool] = isPrimer ? [.pen, .highlighter, .ask, .note, .capture, .eraser] : [.pen, .highlighter, .ask, .capture, .eraser]
        guard let i = order.firstIndex(of: tool) else { tool = .pen; return }
        tool = order[(i + 1) % order.count]
    }

    #if DEBUG
    /// The last Pencil gesture and what Settings asked of it, for the
    /// harness: the Simulator has no Pencil, so the iPad reports instead.
    var lastPencil = ""
    #endif

    /// Double-tap on a Pencil: the eraser and whatever was up trade places.
    func flipEraser() {
        tool = tool == .eraser ? lastTool : .eraser
    }

    /// The Pencil's "switch to the last used tool".
    func switchPrevious() {
        tool = tool == lastTool ? .pen : lastTool
    }

    /// Mark the first occurrence of a run of the chapter's text (scripts,
    /// and restoring); the page finds the offsets.
    @discardableResult
    func mark(text: String, kind: Mark.Kind) async -> Mark? {
        guard let page, let r = await page.find(text) else { return nil }
        return addMark(kind: kind, start: r.start, end: r.end, text: r.text)
    }
    /// The reader's options, kept on the device.
    private let defaults: UserDefaults
    /// Whether a break the server suggests is taken: the break screen with
    /// its timer between the results and the next chapter. Off unless the
    /// reader turns it on in the settings; read when the suggestion
    /// arrives, so a change applies to the next check.
    static let breakTimeKey = "breakTime"
    var breakTime: Bool {
        get { defaults.bool(forKey: Self.breakTimeKey) }
        set { defaults.set(newValue, forKey: Self.breakTimeKey); objectWillChange.send() }
    }
    /// Held from a graded exchange until the learner leaves the results.
    private var pendingBreak: BreakSuggestion?
    private var pendingAuthoring: String?
    /// What "Try again" does after a failure.
    private var retryAction: (() async -> Void)?
    private let dir: URL

    init(subjectID: String, title: String, service: ChironService, storage: URL,
         defaults: UserDefaults = .standard) {
        self.subjectID = subjectID
        self.title = title
        self.service = service
        self.defaults = defaults
        dir = storage.appendingPathComponent(subjectID, isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    var busy: Bool { wait != nil }

    // MARK: - opening

    /// Open the book: restore what is cached, reconcile with the server, and
    /// land on whatever the book is waiting for. A book that has never been
    /// opened starts itself.
    func open() async {
        restore()
        wait = .opening
        do {
            try await reconcileState()
        } catch {
            wait = nil
            // Reading detached is the one thing the cache is for.
            if let ch = chapter, !ch.isCalibration {
                show(ch)
            } else {
                fail(Self.unreachable) { [weak self] in await self?.open() }
            }
            return
        }
        // The server's copy of the chapter wins over the cache whenever it
        // is reachable: a chapter re-authored server-side (or still being
        // written) must reach the reader, and the cache is only for reading
        // detached.
        if let status = try? await service.chapter(subject: subjectID) {
            if status.authoring, pendingAuthoring == nil {
                pendingAuthoring = status.chapter?.unit ?? chapter?.unit
            } else if let fresh = status.chapter,
                      chapter == nil || fresh.unit != chapter?.unit || fresh.html != chapter?.html {
                setChapter(fresh)
                persist()
            } else if status.chapter == nil, !status.authoring, chapter != nil {
                // The server dropped its copy (the unit's bank or beats were
                // rewritten under it): the cache is just as stale, and the
                // start below has the server write the chapter afresh.
                chapter = nil
            }
        }
        // The wait clears before anything below runs an exchange: an
        // exchange refuses to start while one is marked in flight.
        wait = nil
        if let unit = pendingAuthoring {
            await awaitChapter(unit)
        } else if let ch = chapter {
            show(ch)
        } else {
            await start()
        }
    }

    /// Pull the authoritative state from the server.
    ///
    /// The persisted copy is a cache for reading offline, not the truth. Left
    /// unrefreshed it drifts: a spine restored from disk showed units as passed
    /// that the server had no record of, because the only thing that ever
    /// updated it was the response to an exchange.
    func refreshState() async {
        try? await reconcileState()
    }

    private func reconcileState() async throws {
        let st = try await service.state(subject: subjectID)
        bookState = st
        // A cached chapter outlives the state it was generated from. After the
        // server's progress is reset or diverges, the app would otherwise keep
        // serving a chapter written for a learner who no longer exists - one
        // authored as "already fluent" for someone starting from scratch. If
        // the server does not have this unit in progress, the cached chapter is
        // stale and goes.
        if let ch = chapter, ch.unit != "catchup" {
            let known = st.spine.first { $0.unit == ch.unit }
            let inProgress = known?.status == "active" || known?.inFringe == true
            if !inProgress {
                chapter = nil
                beatResponses = []
            }
        }
        persist()
    }

    /// An imported book's pictures, for keeping it on this device.
    var assets: BookAssets?

    /// How much of a book read as it is this device holds: nothing, some
    /// of it while it is being fetched, or all of it.
    enum Kept: Equatable {
        case no
        case keeping(done: Int, total: Int)
        case yes
    }
    @Published private(set) var kept: Kept = .no

    private var chaptersDir: URL { dir.appendingPathComponent("chapters", isDirectory: true) }
    private func chapterFile(_ unit: String) -> URL {
        chaptersDir.appendingPathComponent("\(unit).json")
    }

    /// A chapter this device already holds, whatever the server is doing.
    func storedChapter(_ unit: String) -> ChapterPayload? {
        guard let d = try? Data(contentsOf: chapterFile(unit)) else { return nil }
        return try? JSONDecoder().decode(ChapterPayload.self, from: d)
    }

    /// Take the whole book: every chapter, and every picture in it, so it
    /// reads with no server to ask. Fetching a chapter does not read it.
    func keepOnDevice() async {
        guard readsAsIs, let spine = bookState?.spine, !spine.isEmpty else { return }
        var names: [String] = (try? await service.bookAssetNames(subject: subjectID)) ?? []
        names = names.filter { !$0.isEmpty }
        let total = spine.count + names.count
        kept = .keeping(done: 0, total: total)
        try? FileManager.default.createDirectory(at: chaptersDir, withIntermediateDirectories: true)
        var done = 0
        for entry in spine {
            if let status = try? await service.chapter(subject: subjectID, unit: entry.unit),
               let ch = status.chapter, let data = try? JSONEncoder().encode(ch) {
                try? data.write(to: chapterFile(entry.unit))
            }
            done += 1
            kept = .keeping(done: done, total: total)
        }
        for name in names {
            _ = try? await assets?.data(subject: subjectID, name: name)
            done += 1
            kept = .keeping(done: done, total: total)
        }
        kept = heldChapters() >= spine.count ? .yes : .no
    }

    /// How many of the book's chapters this device holds.
    func heldChapters() -> Int {
        (try? FileManager.default.contentsOfDirectory(at: chaptersDir, includingPropertiesForKeys: nil))?
            .filter { $0.pathExtension == "json" }.count ?? 0
    }

    /// Whether the whole book is here, checked when it is opened.
    func refreshKept() {
        guard readsAsIs, let spine = bookState?.spine, !spine.isEmpty else { kept = .no; return }
        if case .keeping = kept { return }
        kept = heldChapters() >= spine.count ? .yes : .no
    }

    /// The chapter after the open one in the book's own order, and the one
    /// before it: what a book read as it is turns between, since it has no
    /// check to carry the reader forward.
    var nextChapter: SpineEntry? { neighbour(+1) }
    var previousChapter: SpineEntry? { neighbour(-1) }

    private func neighbour(_ step: Int) -> SpineEntry? {
        guard readsAsIs, let spine = bookState?.spine, let unit = chapter?.unit,
              let i = spine.firstIndex(where: { $0.unit == unit }) else { return nil }
        let j = i + step
        return spine.indices.contains(j) ? spine[j] : nil
    }

    /// Turn to a chapter the reader picked, from the page or the contents.
    /// With no server to ask, a chapter this device holds is read from
    /// here: that is what taking the book is for.
    func turnTo(_ entry: SpineEntry?) async {
        guard let entry else { return }
        await start(choice: entry.unit)
        if case .error = screen, let held = storedChapter(entry.unit) {
            errorMessage = nil
            retryAction = nil
            setChapter(held)
            show(held)
        }
    }

    func start(choice: String? = nil) async {
        await run(ExchangeRequest(subject: subjectID, phase: "start", choice: choice), wait: .opening)
    }

    /// Discard all progress for this subject, server-side and locally, and
    /// open the book again from the top.
    func startOver() async {
        errorMessage = nil
        wait = .resetting
        do {
            bookState = try await service.reset(subject: subjectID)
        } catch {
            wait = nil
            errorMessage = "Could not reach the server to start over."
            return
        }
        chapter = nil
        beatResponses = []
        pendingBreak = nil
        pendingAuthoring = nil
        persist()
        wait = nil
        await start()
    }

    // MARK: - the flow

    /// Where a chapter lands the learner: the screener is a placement
    /// question, a calibration series is measurement, a pretest comes before
    /// the prose, and otherwise there is a chapter to read.
    private func show(_ ch: ChapterPayload) {
        chromeHidden = false
        if ch.screener != nil {
            screen = .placement
        } else if ch.isCalibration {
            screen = .series
        } else if !ch.pretest.isEmpty && !pretestDone {
            screen = .pretest
        } else {
            // The chunk clock runs from the reader opening, as on the
            // tablet - a chapter restored from disk counts from now, not
            // from when it was first delivered.
            if chapterOpenedAt == nil { chapterOpenedAt = Date() }
            screen = .reading
        }
    }

    /// Answer the placement screener: one tap, no gate, the series follows.
    func place(level: Int) async {
        guard let screener = chapter?.screener else { return }
        await submitCheck([ItemResponse(itemId: screener.id, response: nil,
                                        selectedIndex: level - 1, confidence: 3)])
    }

    func submitPretest(_ responses: [ItemResponse]) async {
        pretestDone = true
        var req = ExchangeRequest(subject: subjectID, phase: "pretest", unit: chapter?.unit)
        req.pretestResponses = responses
        await run(req, wait: .grading, keepReadingOnNil: true)
    }

    func beginCheck() { screen = .check }

    func leaveCheck() { screen = .reading }

    func submitCheck(_ responses: [ItemResponse]) async {
        var req = ExchangeRequest(subject: subjectID, unit: chapter?.unit)
        req.checkResponses = responses
        req.beatResponses = beatResponses
        req.chunkMinutes = chunkMinutes()
        if responses.contains(where: { $0.ink != nil }), let unit = chapter?.unit {
            // Handwriting goes through the ink check-in: the server
            // rasterizes, transcribes, keeps the audit trail, and grades
            // the transcription exactly as it grades typed text.
            await run(req, wait: .grading) { [service] in
                try await service.ink(subject: req.subject,
                                      InkSubmission(unit: unit, responses: responses, chunkMinutes: req.chunkMinutes))
            }
        } else {
            await run(req, wait: .grading)
        }
    }

    func skipCheck() async {
        var req = ExchangeRequest(subject: subjectID, unit: chapter?.unit)
        req.skippedCheck = true
        req.beatResponses = beatResponses
        req.chunkMinutes = chunkMinutes()
        await run(req, wait: .opening)
    }

    func catchMeUp() async {
        var req = ExchangeRequest(subject: subjectID, unit: chapter?.unit)
        req.catchMeUp = true
        req.beatResponses = beatResponses
        await run(req, wait: .opening)
    }

    /// Leave the results: a suggested break first, then the chapter the
    /// server delivered or is still writing.
    func proceed() async {
        if let b = pendingBreak {
            pendingBreak = nil
            screen = .takingBreak(b)
            return
        }
        if let unit = pendingAuthoring {
            await awaitChapter(unit)
        } else if let ch = chapter {
            show(ch)
        } else {
            await start()
        }
    }

    /// Below the gate, take the material anyway: the debt is recorded and the
    /// next chapter follows.
    func override() async {
        var req = ExchangeRequest(subject: subjectID, unit: chapter?.unit)
        req.override = true
        pendingAuthoring = nil
        await run(req, wait: .opening)
    }

    func breakFinished(minutes: Double) async {
        var req = ExchangeRequest(subject: subjectID, unit: chapter?.unit)
        req.breakMinutes = minutes
        // Reporting the break delivers nothing itself; whatever was pending
        // before the break (a chapter being written) follows.
        await run(req, wait: .opening, keepReadingOnNil: true)
    }

    /// The "Try again" on the error screen.
    func retry() async {
        guard let action = retryAction else { await open(); return }
        await action()
    }

    // MARK: - exchange

    /// `send` is the request on the wire - the exchange by default, the ink
    /// check-in for handwriting; `req` describes it for the handling after.
    private func run(_ req: ExchangeRequest, wait kind: Wait, keepReadingOnNil: Bool = false,
                     send: (() async throws -> ExchangeResponse)? = nil) async {
        // One exchange at a time. The buttons that trigger exchanges stay on
        // screen while one is in flight, and a second tap would grade the same
        // check twice - real model cost and duplicate learner-state events.
        guard !busy else { return }
        errorMessage = nil
        wait = kind
        defer { wait = nil }
        let resp: ExchangeResponse
        do {
            if let send {
                resp = try await send()
            } else {
                resp = try await service.exchange(req)
            }
        } catch {
            fail(Self.unreachable) { [weak self] in
                await self?.run(req, wait: kind, keepReadingOnNil: keepReadingOnNil, send: send)
            }
            return
        }
        bookState = resp.state
        lastResults = resp.results
        if let ch = resp.chapter { setChapter(ch) }
        if let unit = resp.authoring { pendingAuthoring = unit }
        if let b = resp.breakSuggestion, breakTime { pendingBreak = b }
        persist()
        // The exchange is over; what follows may need one of its own (an
        // authoring wait that ends in a fresh start), and that must not be
        // refused as a duplicate of this one.
        wait = nil

        if let doc = resp.resultsDoc, let gate = resp.gate {
            screen = .results(doc, gate)
        } else if resp.chapter != nil, let ch = chapter {
            show(ch)
        } else if let b = pendingBreak, !keepReadingOnNil {
            pendingBreak = nil
            screen = .takingBreak(b)
        } else if let unit = pendingAuthoring {
            await awaitChapter(unit)
        } else if keepReadingOnNil, chapter != nil {
            screen = .reading
        } else if let ch = chapter {
            show(ch)
        } else {
            fail("The server sent no chapter.") { [weak self] in await self?.start() }
        }
    }

    /// Wait for the server to finish writing `unit`, polling /chapter.
    private func awaitChapter(_ unit: String) async {
        screen = .authoring(unit)
        while true {
            let status: ChapterStatus
            do {
                status = try await service.chapter(subject: subjectID)
            } catch {
                fail(Self.unreachable) { [weak self] in await self?.awaitChapter(unit) }
                return
            }
            if status.authoring {
                authoringStage = status.authoringStage
                authoringSince = Date().addingTimeInterval(-Double(status.authoringSeconds ?? 0))
                try? await Task.sleep(nanoseconds: UInt64(pollInterval * 1_000_000_000))
                if Task.isCancelled { return }
                continue
            }
            pendingAuthoring = nil
            authoringStage = nil
            authoringSince = nil
            if let err = status.authoringError, !err.isEmpty {
                persist()
                fail("Chapter authoring failed:\n\(err)") { [weak self] in await self?.start() }
                return
            }
            if let ch = status.chapter, ch.unit == unit {
                setChapter(ch)
                persist()
                show(ch)
            } else {
                // Not writing and not the chapter we were promised (a server
                // restarted mid-build): ask for one from the top.
                persist()
                await start()
            }
            return
        }
    }

    private func fail(_ message: String, retry: @escaping () async -> Void) {
        retryAction = retry
        screen = .error(message)
    }

    private func setChapter(_ ch: ChapterPayload) {
        chapter = ch
        onChapterRead?(ch.unit)
        beatResponses = []
        pretestDone = false
        chapterOpenedAt = nil
        loadMarks()
        Task { await pullAnnotations() }
    }

    private func chunkMinutes() -> Double? {
        guard let t = chapterOpenedAt else { return nil }
        return Date().timeIntervalSince(t) / 60
    }

    // MARK: - the reader's bridge

    func recordBeat(_ r: BeatResponse) {
        beatResponses.removeAll { $0.beatId == r.beatId }
        beatResponses.append(r)
        persist()
    }

    /// The reader reports where it is as the page scrolls; the position is
    /// kept per chapter so a book reopens where it was left.
    func recordPosition(unit: String, position: Double) {
        guard positions[unit] != position else { return }
        positions[unit] = position
        annotationsChanged()
    }

    func position(for unit: String) -> Double {
        positions[unit] ?? 0
    }

    func toggleChrome() {
        chromeHidden.toggle()
    }

    // MARK: - marks on the page

    /// A highlight, or the start of a question: the mark is placed at once
    /// and the card opens for the question.
    @discardableResult
    func addMark(kind: Mark.Kind, start: Int, end: Int, text: String) -> Mark {
        // The same run marked the same way twice is one mark: a question
        // reopens rather than stacking a second badge on the passage.
        if let existing = marks.first(where: { $0.kind == kind && $0.start == start && $0.end == end }) {
            if kind == .question || kind == .note { asking = Asking(mark: existing) }
            return existing
        }
        let mark = Mark(id: UUID().uuidString, kind: kind, start: start, end: end, text: text)
        marks.append(mark)
        if kind == .question || kind == .note { asking = Asking(mark: mark) }
        remember(.mark(mark.id))
        persistMarks()
        return mark
    }

    func removeMark(_ id: String) {
        marks.removeAll { $0.id == id }
        undoable.removeAll { $0 == .mark(id) }
        if asking?.mark.id == id { asking = nil }
        if removing?.mark.id == id { removing = nil }
        persistMarks()
    }

    /// Reopen the card on an existing question; a highlight asks whether
    /// to remove it.
    func openMark(_ id: String) async {
        guard let m = marks.first(where: { $0.id == id }) else { return }
        if m.kind == .highlight {
            let at = await page?.pageRect(of: id) ?? .zero
            removing = Removing(mark: m, at: at)
        } else {
            asking = Asking(mark: m)
        }
    }

    /// The reader said yes to removing the tapped highlight.
    func removeMarkInHand() {
        guard let r = removing else { return }
        removeMark(r.mark.id)
    }

    /// The eraser over a highlight takes it off, as it takes ink off. A
    /// question is deleted from its card, not rubbed out.
    func erase(markID id: String) {
        guard let m = marks.first(where: { $0.id == id }), m.kind == .highlight else { return }
        removeMark(id)
    }

    private func remember(_ step: Undoable) {
        undoable.append(step)
        if undoable.count > 50 { undoable.removeFirst() }
    }

    /// Take back the last mark placed or stroke drawn.
    func undo() {
        guard let last = undoable.popLast() else { return }
        switch last {
        case .mark(let id): removeMark(id)
        case .ink(let before): setInk(before)
        }
    }

    func closeAsking() {
        // A question card closed with nothing asked leaves no mark behind.
        if let a = asking, a.mark.question == nil {
            removeMark(a.mark.id)
        }
        asking = nil
        // The tool goes down with the card: leaving it up turns the next
        // drag, which the reader means as a scroll, into another highlight.
        if tool == .ask || tool == .note { tool = .none }
    }

    /// Send the question with its passage; the answer lands on the mark. A
    /// second question on an answered mark is a follow-up: it joins the
    /// thread and the tutor sees the exchange so far.
    func ask(_ question: String) async {
        guard var a = asking, let unit = chapter?.unit else { return }
        let q = question.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !q.isEmpty, !a.busy else { return }
        let followUp = a.mark.answer != nil
        let history = a.mark.history
        a.busy = true
        a.error = nil
        if followUp {
            var thread = a.mark.thread ?? []
            // A failed follow-up is retried in place, not appended twice.
            if let last = thread.last, last.answer == nil { thread.removeLast() }
            thread.append(QA(question: q, answer: nil))
            a.mark.thread = thread
        } else {
            a.mark.question = q
        }
        asking = a
        do {
            let answer: String
            do {
                answer = try await service.ask(subject: subjectID, unit: unit, quote: a.mark.text, question: q, history: history).answerMd
            } catch let away {
                // The tutor out of reach: the device answers from the
                // chapter, and the reader is told so.
                guard let local = localTutor, let chapter else { throw away }
                let text = try await local.answer(question: q, about: a.mark.text, in: chapter.plainText, history: history)
                answer = text + "\n\n" + LocalTutor.signature
            }
            if followUp, var thread = a.mark.thread, !thread.isEmpty {
                thread[thread.count - 1].answer = answer
                a.mark.thread = thread
            } else {
                a.mark.answer = answer
            }
        } catch {
            a.error = "The tutor can't be reached. Try again."
        }
        a.busy = false
        asking = a
        if let i = marks.firstIndex(where: { $0.id == a.mark.id }) { marks[i] = a.mark }
        persistMarks()
    }

    /// The question, its thread and its highlight go together.
    func deleteAsking() {
        guard let a = asking else { return }
        removeMark(a.mark.id)
        if tool == .ask || tool == .note { tool = .none }
    }

    /// A margin note on a primer: the passage and the note go to the
    /// server, which writes a new section; the document comes back whole
    /// and the mark records the heading it added.
    func extend(_ note: String) async {
        guard var a = asking, a.mark.kind == .note else { return }
        let n = note.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !n.isEmpty, !a.busy else { return }
        a.busy = true
        a.error = nil
        a.mark.question = n
        asking = a
        do {
            let reply = try await service.extend(subject: subjectID, quote: a.mark.text, note: n)
            a.mark.answer = reply.heading
            setChapter(reply.chapter)
            persist()
        } catch {
            a.error = "The primer can't be extended right now. Try again."
        }
        a.busy = false
        asking = a
        if let i = marks.firstIndex(where: { $0.id == a.mark.id }) { marks[i] = a.mark }
        persistMarks()
    }

    #if DEBUG
    /// Tests put a chapter in hand without a server round trip.
    func setChapterForTesting(_ ch: ChapterPayload) { setChapter(ch) }
    func setBookStateForTesting(_ st: BookState) { bookState = st }
    #endif

    /// Strokes arrive many times a second; the file is written once the
    /// hand pauses.
    func saveInk(_ data: Data?) {
        remember(.ink(inkData))
        setInk(data)
    }

    private func setInk(_ data: Data?) {
        inkData = data
        inkDirty = true
        annotationsChanged()
        inkSave?.cancel()
        inkSave = Task {
            try? await Task.sleep(nanoseconds: 400_000_000)
            guard !Task.isCancelled else { return }
            flushInk()
        }
    }

    private var inkDirty = false

    // MARK: - annotations shared with the other devices

    /// The version of each unit's annotations this device last agreed
    /// with the server on, and the units changed here since.
    private var annotationVersions: [String: Int] = [:]
    private var annotationDirty: Set<String> = []
    private var annotationPush: Task<Void, Never>?
    /// How long a change waits before it is pushed; tests shorten it.
    var annotationPushDelay: TimeInterval = 2

    /// Two copies of a unit's annotations that both changed while apart:
    /// this device's and the server's, for the reader to choose between
    /// or to have reconciled.
    struct Conflict: Identifiable, Equatable {
        let unit: String
        let mine: Annotations
        let theirs: Annotations
        var id: String { unit }
    }
    @Published var conflict: Conflict?
    /// Set while a choice on a conflict is being carried out.
    @Published private(set) var resolving = false

    private var deviceName: String {
        #if os(iOS)
        UIDevice.current.name
        #else
        "device"
        #endif
    }

    /// What this device holds for the current unit, as the server keeps it.
    private func localAnnotations(_ unit: String) -> Annotations {
        Annotations(version: annotationVersions[unit] ?? 0, updatedAt: nil, device: deviceName,
                    marks: marks, inkB64: inkData?.base64EncodedString(), position: positions[unit] ?? 0)
    }

    /// Take a copy of the annotations as this device's own.
    private func adopt(_ a: Annotations, unit: String) {
        marks = a.marks
        inkData = a.ink
        inkDirty = true
        undoable = []  // history from before the copy would take it apart
        positions[unit] = a.position
        annotationVersions[unit] = a.version
        annotationDirty.remove(unit)
        persistMarks(changed: false)
        flushInk()
        persistAnnotationMeta()
        if let asking, !marks.contains(where: { $0.id == asking.mark.id }) { self.asking = nil }
    }

    /// On opening a unit: the server's copy is taken when it is newer and
    /// nothing changed here since the last agreement; when both changed,
    /// the reader is asked.
    func pullAnnotations() async {
        guard let unit = chapter?.unit else { return }
        guard let server = try? await service.annotations(subject: subjectID, unit: unit) else { return }
        guard chapter?.unit == unit else { return }
        let known = annotationVersions[unit] ?? 0
        if server.version <= known { return }
        if annotationDirty.contains(unit) || (known == 0 && (!marks.isEmpty || inkData != nil)) {
            let mine = localAnnotations(unit)
            if sameContent(mine, server) {
                annotationVersions[unit] = server.version
                annotationDirty.remove(unit)
                persistAnnotationMeta()
                return
            }
            conflict = Conflict(unit: unit, mine: mine, theirs: server)
            return
        }
        adopt(server, unit: unit)
    }

    private func sameContent(_ a: Annotations, _ b: Annotations) -> Bool {
        a.marks == b.marks && (a.inkB64 ?? "") == (b.inkB64 ?? "") && a.position == b.position
    }

    /// A change here: pushed after a pause, so a burst of strokes or a
    /// scroll is one put. The pause is not reset by each change (a page
    /// that reports its position as it settles would postpone the push
    /// forever); one push is due per pause, and it sends what is current.
    func annotationsChanged() {
        guard let unit = chapter?.unit else { return }
        annotationDirty.insert(unit)
        persistAnnotationMeta()
        guard annotationPush == nil else { return }
        annotationPush = Task { [weak self] in
            guard let self else { return }
            try? await Task.sleep(nanoseconds: UInt64(self.annotationPushDelay * 1_000_000_000))
            self.annotationPush = nil
            guard !Task.isCancelled else { return }
            await self.pushAnnotations()
        }
    }

    /// Push the current unit's annotations on top of the version last
    /// agreed. The server's answer is either the stored version or a
    /// conflict for the reader to settle.
    func pushAnnotations() async {
        guard let unit = chapter?.unit, annotationDirty.contains(unit), conflict == nil else { return }
        let mine = localAnnotations(unit)
        let base = annotationVersions[unit] ?? 0
        guard let reply = try? await service.putAnnotations(subject: subjectID, unit: unit, mine, baseVersion: base) else { return }
        switch reply {
        case .stored(let stored):
            annotationVersions[unit] = stored.version
            // Changes made while the put was in flight stay dirty.
            if sameContent(localAnnotations(unit), mine) { annotationDirty.remove(unit) }
            persistAnnotationMeta()
        case .conflict(let server):
            conflict = Conflict(unit: unit, mine: mine, theirs: server)
        }
    }

    /// The reader's three answers to a conflict.
    enum Resolution: String { case mine, theirs, agent }

    func resolveConflict(_ choice: Resolution) async {
        guard let c = conflict else { return }
        resolving = true
        defer { resolving = false }
        switch choice {
        case .theirs:
            if chapter?.unit == c.unit { adopt(c.theirs, unit: c.unit) }
            conflict = nil
        case .mine:
            annotationVersions[c.unit] = c.theirs.version
            annotationDirty.insert(c.unit)
            conflict = nil
            await pushAnnotations()
        case .agent:
            guard let merged = try? await service.reconcileAnnotations(subject: subjectID, unit: c.unit, mine: c.mine, theirs: c.theirs) else { return }
            var ink = merged.inkB64.flatMap { Data(base64Encoded: $0) }
            if let other = merged.inkOtherB64.flatMap({ Data(base64Encoded: $0) }) {
                ink = Self.overlay(ink, other)
            }
            let a = Annotations(version: c.theirs.version, updatedAt: nil, device: deviceName,
                                marks: merged.marks, inkB64: ink?.base64EncodedString(), position: merged.position)
            if chapter?.unit == c.unit { adopt(a, unit: c.unit) }
            annotationVersions[c.unit] = c.theirs.version
            annotationDirty.insert(c.unit)
            conflict = nil
            await pushAnnotations()
        }
    }

    /// Two drawings become one: the other's strokes over this one's.
    static func overlay(_ base: Data?, _ other: Data) -> Data? {
        guard let base else { return other }
        if let a = try? PKDrawing(data: base), let b = try? PKDrawing(data: other) {
            return a.appending(b).dataRepresentation()
        }
        return base
    }

    private struct AnnotationMeta: Codable {
        var versions: [String: Int]
        var dirty: [String]
    }

    private func persistAnnotationMeta() {
        if let d = try? JSONEncoder().encode(AnnotationMeta(versions: annotationVersions, dirty: Array(annotationDirty).sorted())) {
            try? d.write(to: file("annotations"))
        }
    }

    private func restoreAnnotationMeta() {
        if let d = try? Data(contentsOf: file("annotations")),
           let m = try? JSONDecoder().decode(AnnotationMeta.self, from: d) {
            annotationVersions = m.versions
            annotationDirty = Set(m.dirty)
        }
    }

    /// Write the ink now: the debounce's turn, and every persist (closing
    /// the book, backgrounding), so nothing is lost to a quick exit.
    private func flushInk() {
        guard inkDirty, let unit = chapter?.unit else { return }
        inkDirty = false
        inkSave?.cancel()
        let url = dir.appendingPathComponent("ink-\(unit).pkdrawing")
        if let data = inkData { try? data.write(to: url) } else { try? FileManager.default.removeItem(at: url) }
    }

    private func loadMarks() {
        marks = []
        inkData = nil
        asking = nil
        removing = nil
        undoable = []
        guard let unit = chapter?.unit else { return }
        if let d = try? Data(contentsOf: dir.appendingPathComponent("marks-\(unit).json")),
           let m = try? JSONDecoder().decode([Mark].self, from: d) {
            marks = m
        }
        inkData = try? Data(contentsOf: dir.appendingPathComponent("ink-\(unit).pkdrawing"))
    }

    /// Write the marks file. A change made here is also a change to push;
    /// a copy taken from the server is not.
    private func persistMarks(changed: Bool = true) {
        guard let unit = chapter?.unit else { return }
        if let d = try? JSONEncoder().encode(marks) {
            try? d.write(to: dir.appendingPathComponent("marks-\(unit).json"))
        }
        if changed { annotationsChanged() }
    }

    // MARK: - persistence

    private struct Position: Codable {
        var pendingAuthoring: String?
        var pretestDone: Bool
        var positions: [String: Double]?
    }

    private func file(_ name: String) -> URL {
        dir.appendingPathComponent("\(name).json")
    }

    private func restore() {
        chapter = nil
        bookState = nil
        beatResponses = []
        restoreAnnotationMeta()
        if let d = try? Data(contentsOf: file("chapter")),
           let ch = try? JSONDecoder().decode(ChapterPayload.self, from: d) {
            chapter = ch
            loadMarks()
        }
        if let d = try? Data(contentsOf: file("state")),
           let st = try? JSONDecoder().decode(BookState.self, from: d) {
            bookState = st
        }
        if let d = try? Data(contentsOf: file("beats")),
           let br = try? JSONDecoder().decode([BeatResponse].self, from: d) {
            beatResponses = br
        }
        if let d = try? Data(contentsOf: file("position")),
           let p = try? JSONDecoder().decode(Position.self, from: d) {
            pendingAuthoring = p.pendingAuthoring
            pretestDone = p.pretestDone
            positions = p.positions ?? [:]
        }
    }

    func persist() {
        flushInk()
        if let unit = chapter?.unit, annotationDirty.contains(unit) {
            Task { await pushAnnotations() }
        }
        if let ch = chapter, let d = try? JSONEncoder().encode(ch) {
            try? d.write(to: file("chapter"))
        } else {
            try? FileManager.default.removeItem(at: file("chapter"))
        }
        if let st = bookState, let d = try? JSONEncoder().encode(st) {
            try? d.write(to: file("state"))
        }
        if let d = try? JSONEncoder().encode(beatResponses) {
            try? d.write(to: file("beats"))
        }
        if let d = try? JSONEncoder().encode(Position(pendingAuthoring: pendingAuthoring, pretestDone: pretestDone, positions: positions)) {
            try? d.write(to: file("position"))
        }
    }
}

/// What the loaded page can do for the session.
@MainActor
protocol PageBridge: AnyObject {
    /// Offsets of the first occurrence of text in the chapter, or nil.
    func find(_ text: String) async -> (start: Int, end: Int, text: String)?
    /// Where a mark's badge is drawn, in the page view's coordinates.
    func rect(of markID: String) async -> CGRect?
    /// The same in the page's own coordinates, which start below the bars:
    /// what a popover over the page anchors to.
    func pageRect(of markID: String) async -> CGRect?
    /// The first element matching a selector, in the page view's
    /// coordinates, for the harness to tap.
    func rect(matching selector: String) async -> CGRect?
    /// A line of JavaScript against the page, for the harness.
    func eval(_ js: String) async -> String
}
