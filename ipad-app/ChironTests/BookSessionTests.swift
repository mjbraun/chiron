import XCTest
@testable import Chiron

/// A server made of fixtures. Each hook is what the next call returns; the
/// requests made are kept for assertions.
final class FakeService: ChironService {
    var onSubjects: () throws -> SubjectsResponse = { throw URLError(.cannotConnectToHost) }
    var onState: (String) throws -> BookState = { _ in throw URLError(.cannotConnectToHost) }
    var onChapter: (String) throws -> ChapterStatus = { _ in throw URLError(.cannotConnectToHost) }
    var onExchange: (ExchangeRequest) throws -> ExchangeResponse = { _ in throw URLError(.cannotConnectToHost) }
    var onReset: (String) throws -> BookState = { _ in throw URLError(.cannotConnectToHost) }
    var onInk: (String, InkSubmission) throws -> ExchangeResponse = { _, _ in throw URLError(.cannotConnectToHost) }
    var onAsk: (String, String, String) throws -> AskResponse = { _, _, _ in throw URLError(.cannotConnectToHost) }
    var onCapture: (CaptureRequest) throws -> CaptureResponse = { _ in throw URLError(.cannotConnectToHost) }
    var onPlan: (String) throws -> PlanState = { _ in throw URLError(.cannotConnectToHost) }
    var onPlanTurn: (String, String) throws -> CaptureResponse = { _, _ in throw URLError(.cannotConnectToHost) }
    var onBuild: (String) throws -> BuildResponse = { _ in throw URLError(.cannotConnectToHost) }
    var onExtend: (String, String, String) throws -> ExtendResponse = { _, _, _ in throw URLError(.cannotConnectToHost) }
    var onAnnotations: (String, String) throws -> Annotations? = { _, _ in nil }
    var onPutAnnotations: (String, String, Annotations, Int) throws -> AnnotationsPut = { _, _, a, base in
        var stored = a; stored.version = base + 1; return .stored(stored)
    }
    var onReconcile: (Annotations, Annotations) throws -> ReconciledAnnotations = { mine, theirs in
        ReconciledAnnotations(version: max(mine.version, theirs.version), marks: mine.marks + theirs.marks.filter { t in !mine.marks.contains { $0.id == t.id } },
                              position: max(mine.position, theirs.position), inkB64: mine.inkB64 ?? theirs.inkB64, inkOtherB64: nil)
    }
    var annotationPuts: [(unit: String, annotations: Annotations, base: Int)] = []
    var annotationPulls: [String] = []
    var reconciles: [(mine: Annotations, theirs: Annotations)] = []
    var onCreateShelf: (String) throws -> ShelfInfo = { ShelfInfo(id: "s-\($0)", name: $0) }
    var shelvesMade: [String] = []
    var renames: [(id: String, name: String)] = []
    var shelvesDeleted: [String] = []
    var moves: [(subject: String, shelf: String?)] = []
    var captures: [CaptureRequest] = []
    var planTurns: [(subject: String, text: String)] = []
    var builds: [String] = []
    var discards: [String] = []
    var extends: [(subject: String, quote: String, note: String)] = []
    var exchanges: [ExchangeRequest] = []
    var inks: [InkSubmission] = []
    var asks: [(unit: String, quote: String, question: String)] = []
    var histories: [[QA]] = []
    var flags: [(unit: String, item: String, text: String, response: ItemResponse?)] = []
    var onFlag: (String, String, String) throws -> FlagResponse = { unit, item, _ in FlagResponse(unit: unit, item: item, n: 1) }
    var chapterPolls = 0
    var onUploadDocument: (String, Int, Data) throws -> Document = { title, pages, _ in Document(id: "doc-\(title)", title: title, pages: pages) }
    var onDocumentData: (String) throws -> Data = { _ in throw URLError(.cannotConnectToHost) }
    var uploads: [(title: String, pages: Int, data: Data)] = []
    var readings: [(title: String, data: Data)] = []
    var assetFetches: [(subject: String, name: String)] = []
    var chaptersByUnit: [String] = []
    var onChapterByUnit: (String, String) throws -> ChapterStatus = { _, _ in
        throw ServiceError.status(404)
    }
    var onBookAssetNames: (String) throws -> [String] = { _ in [] }
    var onBookAsset: (String, String) throws -> Data = { _, name in Data("bytes of \(name)".utf8) }
    var documentFetches: [String] = []
    var positions: [(id: String, page: Int, position: Double)] = []
    var documentsDeleted: [String] = []
    var onDocumentInk: (String) throws -> [Int: PageInk] = { _ in [:] }
    var onLatestBuild: () throws -> AppBuild = { throw URLError(.cannotConnectToHost) }
    var requests: [(text: String, state: [String: Any], png: Data?)] = []
    var onRequestChange: (String, [String: Any], Data?) throws -> ChangeRequest = { _, _, _ in throw URLError(.cannotConnectToHost) }
    var onChangeRequests: () throws -> [ChangeRequest] = { [] }
    var answers: [(id: String, answer: String)] = []
    var onAlerts: (String?) throws -> [MachineAlert] = { _ in [] }
    var onAnswerRequest: (String, String) throws -> ChangeRequest = { id, _ in
        ChangeRequest(id: id, text: "", status: "queued", createdAt: "", log: [], last: nil, summary: nil, reason: nil, commit: nil, build: nil)
    }
    var onPutDocumentInk: (Int, String, Int) throws -> PageInkPut = { _, ink, base in .stored(PageInk(version: base + 1, inkB64: ink)) }
    var inkPuts: [(page: Int, inkB64: String, base: Int)] = []

    func subjects() async throws -> SubjectsResponse { try onSubjects() }
    func state(subject: String) async throws -> BookState { try onState(subject) }
    func chapter(subject: String) async throws -> ChapterStatus {
        chapterPolls += 1
        return try onChapter(subject)
    }
    func exchange(_ request: ExchangeRequest) async throws -> ExchangeResponse {
        exchanges.append(request)
        return try onExchange(request)
    }
    func ink(subject: String, _ submission: InkSubmission) async throws -> ExchangeResponse {
        inks.append(submission)
        return try onInk(subject, submission)
    }
    func ask(subject: String, unit: String, quote: String, question: String, history: [QA]) async throws -> AskResponse {
        asks.append((unit, quote, question))
        histories.append(history)
        return try onAsk(unit, quote, question)
    }
    func flag(subject: String, unit: String, item: String, text: String, response: ItemResponse?) async throws -> FlagResponse {
        flags.append((unit, item, text, response))
        return try onFlag(unit, item, text)
    }
    func reset(subject: String) async throws -> BookState { try onReset(subject) }
    func capture(_ request: CaptureRequest) async throws -> CaptureResponse {
        captures.append(request)
        return try onCapture(request)
    }
    func plan(subject: String) async throws -> PlanState { try onPlan(subject) }
    func planTurn(subject: String, text: String) async throws -> CaptureResponse {
        planTurns.append((subject, text))
        return try onPlanTurn(subject, text)
    }
    func build(subject: String) async throws -> BuildResponse {
        builds.append(subject)
        return try onBuild(subject)
    }
    func discard(subject: String) async throws { discards.append(subject) }
    func createShelf(name: String) async throws -> ShelfInfo {
        shelvesMade.append(name)
        return try onCreateShelf(name)
    }
    func renameShelf(_ id: String, name: String) async throws -> ShelfInfo {
        renames.append((id, name))
        return ShelfInfo(id: id, name: name)
    }
    func deleteShelf(_ id: String) async throws { shelvesDeleted.append(id) }
    func move(subject: String, toShelf shelf: String?) async throws { moves.append((subject, shelf)) }
    func annotations(subject: String, unit: String) async throws -> Annotations? {
        annotationPulls.append(unit)
        return try onAnnotations(subject, unit)
    }
    func putAnnotations(subject: String, unit: String, _ a: Annotations, baseVersion: Int) async throws -> AnnotationsPut {
        annotationPuts.append((unit, a, baseVersion))
        return try onPutAnnotations(subject, unit, a, baseVersion)
    }
    func reconcileAnnotations(subject: String, unit: String, mine: Annotations, theirs: Annotations) async throws -> ReconciledAnnotations {
        reconciles.append((mine, theirs))
        return try onReconcile(mine, theirs)
    }
    func uploadDocument(title: String, pages: Int, data: Data) async throws -> Document {
        uploads.append((title, pages, data)); return try onUploadDocument(title, pages, data)
    }
    func importBook(title: String, data: Data) async throws -> ImportedBook {
        readings.append((title, data))
        return ImportedBook(id: "read-1", title: title, chapters: 3)
    }
    var forgotten: [String] = []
    func forgetReading(id: String) async throws { forgotten.append(id) }
    var primersDeleted: [String] = []
    func deletePrimer(id: String) async throws { primersDeleted.append(id) }
    var feedsFollowed: [String] = []
    var onFollowFeed: (String) throws -> ImportedBook = { _ in throw URLError(.cannotConnectToHost) }
    func followFeed(url: String) async throws -> ImportedBook {
        feedsFollowed.append(url)
        return try onFollowFeed(url)
    }
    var feedChecks = 0
    var onCheckFeeds: () throws -> FeedCheck = { FeedCheck(checked: 0, added: 0) }
    func checkFeeds() async throws -> FeedCheck {
        feedChecks += 1
        return try onCheckFeeds()
    }
    var readMarks: [(subject: String, unit: String)] = []
    func markRead(subject: String, unit: String) async throws { readMarks.append((subject, unit)) }
    var pagesRead: [String] = []
    var onReadPage: (String) throws -> ImportedBook = { _ in throw URLError(.cannotConnectToHost) }
    func readPage(url: String) async throws -> ImportedBook {
        pagesRead.append(url)
        return try onReadPage(url)
    }
    func chapter(subject: String, unit: String) async throws -> ChapterStatus {
        chaptersByUnit.append(unit)
        return try onChapterByUnit(subject, unit)
    }
    func bookAssetNames(subject: String) async throws -> [String] { try onBookAssetNames(subject) }
    func bookAsset(subject: String, name: String) async throws -> Data {
        assetFetches.append((subject, name))
        return try onBookAsset(subject, name)
    }
    func documentData(id: String) async throws -> Data { documentFetches.append(id); return try onDocumentData(id) }
    func documentPosition(id: String, page: Int, position: Double) async throws { positions.append((id, page, position)) }
    func deleteDocument(id: String) async throws { documentsDeleted.append(id) }
    func documentInk(id: String) async throws -> [Int: PageInk] { try onDocumentInk(id) }
    func latestBuild() async throws -> AppBuild { try onLatestBuild() }
    func requestChange(text: String, state: [String: Any], screenshotPNG: Data?) async throws -> ChangeRequest {
        requests.append((text, state, screenshotPNG))
        return try onRequestChange(text, state, screenshotPNG)
    }
    func changeRequests() async throws -> [ChangeRequest] { try onChangeRequests() }
    func alerts(since: String?) async throws -> [MachineAlert] { try onAlerts(since) }
    func answerRequest(id: String, answer: String) async throws -> ChangeRequest {
        let r = try onAnswerRequest(id, answer)
        answers.append((id, answer))
        return r
    }
    func putDocumentInk(id: String, page: Int, inkB64: String, baseVersion: Int) async throws -> PageInkPut {
        inkPuts.append((page, inkB64, baseVersion)); return try onPutDocumentInk(page, inkB64, baseVersion)
    }
    func extend(subject: String, quote: String, note: String) async throws -> ExtendResponse {
        extends.append((subject, quote, note))
        return try onExtend(subject, quote, note)
    }
}

@MainActor
final class BookSessionTests: XCTestCase {
    private var fake: FakeService!
    private var storage: URL!
    /// The reader's options, fresh for each test: what one test switches
    /// on must not reach the next.
    private var defaults: UserDefaults!
    private var suite: String!

    override func setUp() {
        fake = FakeService()
        storage = FileManager.default.temporaryDirectory
            .appendingPathComponent("chiron-tests-\(UUID().uuidString)", isDirectory: true)
        suite = "book-session-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suite)!
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: storage)
        defaults.removePersistentDomain(forName: suite)
    }

    /// A chapter as the server sends one, for a book with nothing to answer.
    private func chapterPayload(_ unit: String, _ title: String) -> ChapterPayload {
        let json = """
        {"unit":"\(unit)","title":"\(title)","minutes":9,"html":"<p>prose</p>",
         "beats":[],"pretest":[],"check":[],"calibration":false,"next_action":"read"}
        """
        return try! JSONDecoder().decode(ChapterPayload.self, from: Data(json.utf8))
    }

    private func fixture<T: Decodable>(_ type: T.Type, _ name: String) -> T {
        let url = Bundle(for: FixtureDecodingTests.self)
            .url(forResource: "fixtures", withExtension: nil)!
            .appendingPathComponent("\(name).json")
        return try! JSONDecoder().decode(type, from: try! Data(contentsOf: url))
    }

    private func session() -> BookSession {
        let s = BookSession(subjectID: "ai", title: "How AI Works", service: fake, storage: storage, defaults: defaults)
        s.pollInterval = 0
        return s
    }

    /// The fake behaves like a fresh server that authors instantly: start
    /// delivers the screener, the screener answer delivers the series, a
    /// graded series returns results with u1 authoring, and /chapter has u1.
    private func scriptFreshBook() {
        fake.onState = { [unowned self] _ in self.fixture(BookState.self, "state") }
        // Like a real server: no chapter until a graded series has had u1
        // authored, then u1.
        fake.onChapter = { [unowned self] _ in
            self.fake.exchanges.contains { $0.checkResponses.count > 1 }
                ? self.fixture(ChapterStatus.self, "chapter")
                : ChapterStatus(chapter: nil, authoring: false, authoringError: "")
        }
        fake.onExchange = { [unowned self] req in
            if req.phase == "start" { return self.fixture(ExchangeResponse.self, "exchange-start") }
            if req.checkResponses.count == 1, req.checkResponses[0].itemId == "u0-s1" {
                return self.fixture(ExchangeResponse.self, "exchange-screener")
            }
            return self.fixture(ExchangeResponse.self, "exchange-series")
        }
    }

    func testFreshBookRunsPlacementSeriesResultsThenTheChapter() async {
        scriptFreshBook()
        let s = session()

        await s.open()
        guard case .placement = s.screen else { return XCTFail("after open: \(s.screen)") }
        XCTAssertEqual(fake.exchanges.map(\.phase), ["start"])

        await s.place(level: 2)
        guard case .series = s.screen else { return XCTFail("after placement: \(s.screen)") }
        XCTAssertEqual(fake.exchanges.last?.checkResponses.first?.selectedIndex, 1, "level 2 is index 1")
        XCTAssertEqual(s.chapter?.check.count, 11)

        let answers = s.chapter!.check.map {
            ItemResponse(itemId: $0.id, response: nil, selectedIndex: nil, confidence: 1, idk: true)
        }
        await s.submitCheck(answers)
        guard case .results(let doc, let gate) = s.screen else { return XCTFail("after series: \(s.screen)") }
        XCTAssertTrue(doc.isCalibration)
        XCTAssertTrue(gate.passed)
        XCTAssertEqual(fake.exchanges.last?.async, true, "graded checks never wait for authoring")
        XCTAssertNil(s.wait)

        await s.proceed()
        guard case .reading = s.screen else { return XCTFail("after results: \(s.screen)") }
        XCTAssertEqual(s.chapter?.unit, "u1")
        XCTAssertGreaterThanOrEqual(fake.chapterPolls, 1)
    }

    func testAuthoringWaitPollsUntilTheChapterArrives() async {
        scriptFreshBook()
        var polls = 0
        fake.onChapter = { [unowned self] _ in
            polls += 1
            if polls < 3 {
                return ChapterStatus(chapter: nil, authoring: true, authoringError: "")
            }
            return self.fixture(ChapterStatus.self, "chapter")
        }
        let s = session()
        await s.open()
        await s.place(level: 3)
        await s.submitCheck(s.chapter!.check.map {
            ItemResponse(itemId: $0.id, response: nil, selectedIndex: nil, confidence: 1, idk: true)
        })
        await s.proceed()
        guard case .reading = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(polls, 3)
    }

    func testAuthoringFailureIsAnErrorWithAWayOut() async {
        scriptFreshBook()
        fake.onChapter = { _ in ChapterStatus(chapter: nil, authoring: false, authoringError: "model timed out") }
        let s = session()
        await s.open()
        await s.place(level: 3)
        await s.submitCheck(s.chapter!.check.map {
            ItemResponse(itemId: $0.id, response: nil, selectedIndex: nil, confidence: 1, idk: true)
        })
        await s.proceed()
        guard case .error(let message) = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertTrue(message.contains("model timed out"))

        // Try again asks the server for a chapter from the top.
        fake.onExchange = { [unowned self] _ in self.fixture(ExchangeResponse.self, "exchange-start") }
        await s.retry()
        XCTAssertEqual(fake.exchanges.last?.phase, "start")
        guard case .placement = s.screen else { return XCTFail("\(s.screen)") }
    }

    /// An exchange that delivers the persisted u1 chapter and nothing else.
    private func deliversU1() -> ExchangeResponse {
        let state = fixture(BookState.self, "state")
        let ch = fixture(ChapterStatus.self, "chapter").chapter!
        return ExchangeResponse(results: [], gate: nil, chapter: ch, state: state,
                                breakSuggestion: nil, authoring: nil, resultsDoc: nil)
    }

    private func deliversNothing() -> ExchangeResponse {
        ExchangeResponse(results: [], gate: nil, chapter: nil, state: fixture(BookState.self, "state"),
                         breakSuggestion: nil, authoring: nil, resultsDoc: nil)
    }

    /// Break time is off unless the reader turns it on: the server's
    /// suggestion is dropped, leaving the results goes straight to the
    /// chapter, and no break is reported.
    func testBreakTimeIsOffByDefault() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] req in
            if req.phase == "start" { return self.deliversU1() }
            if req.breakMinutes != nil { XCTFail("a break was reported with break time off") }
            return self.fixture(ExchangeResponse.self, "exchange-fail")
        }
        let s = session()
        XCTAssertFalse(s.breakTime)
        await s.open()
        await s.submitCheck([ItemResponse(itemId: "u1-q1", response: "wrong", selectedIndex: nil, confidence: 4)])
        guard case .results = s.screen else { return XCTFail("\(s.screen)") }
        await s.proceed()
        guard case .reading = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(s.chapter?.unit, "u1")
    }

    /// The option is the reader's, kept on the device: a session made later
    /// on the same device sees it.
    func testBreakTimeIsKeptOnTheDevice() {
        let s = session()
        s.breakTime = true
        XCTAssertTrue(session().breakTime)
        s.breakTime = false
        XCTAssertFalse(session().breakTime)
    }

    /// Below the gate nothing is written until the reader chooses. Each
    /// choice is its own exchange; the suggested break comes first, and
    /// the chapter the choice asked for follows it.
    func testBelowTheGateOffersTheMissesTheRewriteOrOverride() async {
        scriptFreshBook()
        let state = fixture(BookState.self, "state")
        var promised = ""
        fake.onExchange = { [unowned self] req in
            if req.phase == "start" { return self.deliversU1() }
            if req.override { return self.fixture(ExchangeResponse.self, "exchange-screener") }
            if req.repair || req.remediate {
                promised = req.repair ? "u1.repair" : "u1"
                return ExchangeResponse(results: [], gate: nil, chapter: nil, state: state,
                                        breakSuggestion: nil, authoring: promised, resultsDoc: nil)
            }
            if req.breakMinutes != nil { return self.deliversNothing() }
            return self.fixture(ExchangeResponse.self, "exchange-fail")
        }
        // Like a real server: no chapter until one was asked for, then that one.
        fake.onChapter = { [unowned self] _ in
            promised.isEmpty ? ChapterStatus(chapter: nil, authoring: false, authoringError: "")
                : ChapterStatus(chapter: self.chapterPayload(promised, "Tokens"), authoring: false, authoringError: "")
        }
        let s = session()
        s.breakTime = true
        await s.open()
        guard case .reading = s.screen else { return XCTFail("\(s.screen)") }
        await s.submitCheck([ItemResponse(itemId: "u1-q1", response: "wrong", selectedIndex: nil, confidence: 4)])
        guard case .results(let doc, let gate) = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertFalse(gate.passed)
        XCTAssertTrue(doc.headline.hasPrefix("Below the gate"))
        XCTAssertFalse(fake.exchanges.contains { $0.repair || $0.remediate }, "no chapter asked for before the choice")

        // The misses alone: the exchange carries the flag, the break comes
        // first, then the repair chapter it is writing.
        await s.repair()
        XCTAssertEqual(fake.exchanges.last?.repair, true)
        XCTAssertEqual(fake.exchanges.last?.unit, "u1")
        guard case .takingBreak(let b) = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(b.kind, "short")
        await s.breakFinished(minutes: 5)
        XCTAssertTrue(fake.exchanges.contains { $0.breakMinutes != nil }, "the break was reported")
        guard case .reading = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(s.chapter?.unit, "u1.repair")

        // The whole chapter again instead: the same shape, for the rewrite.
        await s.submitCheck([ItemResponse(itemId: "u1-q1", response: "wrong", selectedIndex: nil, confidence: 4)])
        await s.remediate()
        XCTAssertEqual(fake.exchanges.last?.remediate, true)
        guard case .takingBreak = s.screen else { return XCTFail("\(s.screen)") }
        await s.breakFinished(minutes: 5)
        guard case .reading = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(s.chapter?.unit, "u1")

        // Override instead: the exchange carries the flag and whatever it
        // delivers is shown.
        await s.submitCheck([ItemResponse(itemId: "u1-q1", response: "wrong", selectedIndex: nil, confidence: 4)])
        await s.override()
        XCTAssertEqual(fake.exchanges.last?.override, true)
        guard case .series = s.screen else { return XCTFail("\(s.screen)") }
    }

    /// A chapter with a pretest in front of it, as the server sends one.
    private func chapterWithPretest(_ unit: String) -> ChapterPayload {
        let json = """
        {"unit":"\(unit)","title":"Tokens","minutes":9,"html":"<p>prose</p>","beats":[],
         "pretest":[{"id":"\(unit)-p1","kind":"constructed","prompt":"What is a token?","check":"rubric"}],
         "check":[],"calibration":false,"next_action":"read"}
        """
        return try! JSONDecoder().decode(ChapterPayload.self, from: Data(json.utf8))
    }

    /// Below the gate with break time on, the remediation chapter is still
    /// authoring when the break starts. A server may hand that chapter over
    /// on the break report itself; the pretest in front of it then leads
    /// into the chapter. It must not lead back into a wait for the chapter
    /// already in hand, which fetched it again and reopened the pretest from
    /// its first question (2026-09-27).
    func testAChapterDeliveredAfterTheBreakEndsTheAuthoringWait() async {
        scriptFreshBook()
        let state = fixture(BookState.self, "state")
        fake.onChapter = { [unowned self] _ in
            ChapterStatus(chapter: self.chapterWithPretest("u1"), authoring: false, authoringError: "")
        }
        fake.onExchange = { [unowned self] req in
            if req.phase == "start" { return self.deliversU1() }
            if req.breakMinutes != nil {
                return ExchangeResponse(results: [], gate: nil, chapter: self.chapterWithPretest("u1"), state: state,
                                        breakSuggestion: nil, authoring: nil, resultsDoc: nil)
            }
            if req.phase == "pretest" { return self.deliversNothing() }
            return self.fixture(ExchangeResponse.self, "exchange-fail")
        }
        let s = session()
        s.breakTime = true
        await s.open()
        await s.submitCheck([ItemResponse(itemId: "u1-q1", response: "wrong", selectedIndex: nil, confidence: 4)])
        guard case .results = s.screen else { return XCTFail("\(s.screen)") }
        await s.proceed()
        guard case .takingBreak = s.screen else { return XCTFail("\(s.screen)") }
        await s.breakFinished(minutes: 5)
        guard case .pretest = s.screen else { return XCTFail("\(s.screen)") }

        let polls = fake.chapterPolls
        await s.submitPretest([ItemResponse(itemId: "u1-p1", response: "a string", selectedIndex: nil, confidence: 2)])
        XCTAssertEqual(fake.exchanges.last?.phase, "pretest")
        guard case .reading = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(s.chapter?.unit, "u1")
        XCTAssertEqual(fake.chapterPolls, polls, "no wait for a chapter already in hand")
    }

    func testServerAwayIsAnErrorScreenAndTryAgainRecovers() async {
        let s = session()
        await s.open()
        guard case .error(let message) = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(message, "The server can't be reached.")

        scriptFreshBook()
        await s.retry()
        guard case .placement = s.screen else { return XCTFail("\(s.screen)") }
    }

    func testCachedChapterReadsWhenTheServerIsAway() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        let s = session()
        await s.open()
        guard case .reading = s.screen else { return XCTFail("\(s.screen)") }

        // Relaunch with the server gone: the cached chapter is readable.
        let away = FakeService()
        let s2 = BookSession(subjectID: "ai", title: "How AI Works", service: away, storage: storage)
        await s2.open()
        guard case .reading = s2.screen else { return XCTFail("\(s2.screen)") }
        XCTAssertEqual(s2.chapter?.unit, "u1")
    }

    func testStaleCachedChapterIsDroppedWhenTheServerMovedOn() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        let s = session()
        await s.open()
        XCTAssertEqual(s.chapter?.unit, "u1")

        // The server was reset: u1 is locked and not in the fringe, so the
        // cached chapter goes and the book starts from the top.
        fake.onState = { _ in
            BookState(spine: [
                SpineEntry(unit: "u0", title: "Calibration", status: "locked", score: nil, inFringe: true),
                SpineEntry(unit: "u1", title: "The core bet", status: "locked", score: nil, inFringe: false),
            ], fringe: ["u0"], debt: [], activeMisconceptions: [], summary: "", sessionMinutes: 0)
        }
        fake.onExchange = { [unowned self] _ in self.fixture(ExchangeResponse.self, "exchange-start") }
        let s2 = BookSession(subjectID: "ai", title: "How AI Works", service: fake, storage: storage)
        await s2.open()
        guard case .placement = s2.screen else { return XCTFail("\(s2.screen)") }
    }

    func testRelaunchMidAuthoringResumesTheWait() async {
        scriptFreshBook()
        fake.onChapter = { _ in ChapterStatus(chapter: nil, authoring: true, authoringError: "") }
        let s = session()
        s.pollInterval = 60
        await s.open()
        await s.place(level: 3)
        await s.submitCheck(s.chapter!.check.map {
            ItemResponse(itemId: $0.id, response: nil, selectedIndex: nil, confidence: 1, idk: true)
        })
        // The authoring wait persists before the first poll sleeps.
        let waiting = Task { await s.proceed() }
        try? await Task.sleep(nanoseconds: 100_000_000)
        guard case .authoring(let unit) = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(unit, "u1")
        waiting.cancel()

        fake.onChapter = { [unowned self] _ in self.fixture(ChapterStatus.self, "chapter") }
        let s2 = session()
        await s2.open()
        guard case .reading = s2.screen else { return XCTFail("\(s2.screen)") }
        XCTAssertEqual(s2.chapter?.unit, "u1")
    }

    func testDuplicateSubmitsAreDroppedWhileOneIsInFlight() async {
        scriptFreshBook()
        let s = session()
        await s.open()
        await s.place(level: 3)
        let answers = s.chapter!.check.map {
            ItemResponse(itemId: $0.id, response: nil, selectedIndex: nil, confidence: 1, idk: true)
        }
        let before = fake.exchanges.count
        async let a: Void = s.submitCheck(answers)
        async let b: Void = s.submitCheck(answers)
        _ = await (a, b)
        XCTAssertEqual(fake.exchanges.count - before, 1)
    }

    func testStartOverResetsAndReopensFromTheTop() async {
        scriptFreshBook()
        fake.onReset = { [unowned self] _ in self.fixture(BookState.self, "state") }
        let s = session()
        await s.open()
        await s.place(level: 3)
        await s.startOver()
        guard case .placement = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(fake.exchanges.last?.phase, "start")
    }

    func testReadingPositionSurvivesRelaunch() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        let s = session()
        await s.open()
        s.recordPosition(unit: "u1", position: 0.35)
        s.persist()

        let s2 = session()
        await s2.open()
        XCTAssertEqual(s2.position(for: "u1"), 0.35)
        XCTAssertEqual(s2.position(for: "u2"), 0, "an unread chapter opens at the top")
    }

    func testAnyInkSendsTheWholeCheckThroughTheInkCheckIn() async {
        scriptFreshBook()
        fake.onInk = { [unowned self] _, _ in self.fixture(ExchangeResponse.self, "exchange-series") }
        let s = session()
        await s.open()
        await s.place(level: 3)
        let items = s.chapter!.check
        let strokes = [[InkAnswer.Point(x: 0.1, y: 0.2), InkAnswer.Point(x: 0.5, y: 0.6)]]
        var responses = [
            ItemResponse(itemId: items[0].id, response: nil, selectedIndex: nil, confidence: 3,
                         ink: InkAnswer(strokes: strokes, aspect: 3)),
            ItemResponse(itemId: items[1].id, response: "typed", selectedIndex: nil, confidence: 4),
            ItemResponse(itemId: items[2].id, response: nil, selectedIndex: nil, confidence: 1, idk: true),
        ]
        responses += items.dropFirst(3).map {
            ItemResponse(itemId: $0.id, response: nil, selectedIndex: 0, confidence: 2)
        }
        let exchangesBefore = fake.exchanges.count
        await s.submitCheck(responses)

        XCTAssertEqual(fake.exchanges.count, exchangesBefore, "handwriting never travels on the exchange")
        let sub = try! XCTUnwrap(fake.inks.last)
        XCTAssertEqual(sub.unit, "u0")
        XCTAssertEqual(sub.items.count, items.count)
        XCTAssertEqual(sub.items[0].strokes, strokes)
        XCTAssertEqual(sub.items[0].aspect, 3)
        XCTAssertNil(sub.items[0].text)
        XCTAssertEqual(sub.items[1].text, "typed")
        XCTAssertTrue(sub.items[1].strokes.isEmpty)
        XCTAssertEqual(sub.items[2].idk, true)
        XCTAssertEqual(sub.items[3].selectedIndex, 0)
        guard case .results = s.screen else { return XCTFail("\(s.screen)") }

        // The ink never leaks onto the exchange wire either.
        let encoded = String(data: try! JSONEncoder().encode(responses[0]), encoding: .utf8)!
        XCTAssertFalse(encoded.contains("strokes"))
    }

    func testTypedOnlyChecksStayOnTheExchange() async {
        scriptFreshBook()
        let s = session()
        await s.open()
        await s.place(level: 3)
        await s.submitCheck(s.chapter!.check.map {
            ItemResponse(itemId: $0.id, response: "x", selectedIndex: nil, confidence: 2)
        })
        XCTAssertTrue(fake.inks.isEmpty)
        guard case .results = s.screen else { return XCTFail("\(s.screen)") }
    }

    func testChunkMinutesCountFromTheReaderOpening() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] req in
            req.phase == "start" ? self.deliversU1() : self.fixture(ExchangeResponse.self, "exchange-fail")
        }
        let s = session()
        await s.open()
        guard case .reading = s.screen else { return XCTFail("\(s.screen)") }

        // A relaunch onto the cached chapter starts the clock again.
        let s2 = session()
        await s2.open()
        guard case .reading = s2.screen else { return XCTFail("\(s2.screen)") }
        await s2.submitCheck([ItemResponse(itemId: "u1-q1", response: "x", selectedIndex: nil, confidence: 2)])
        let minutes = try! XCTUnwrap(fake.exchanges.last?.chunkMinutes)
        XCTAssertGreaterThanOrEqual(minutes, 0)
        XCTAssertLessThan(minutes, 1, "the clock started at this open, not at delivery")

        // The series is measurement, not reading: no chunk is reported.
        fake.onExchange = { [unowned self] req in
            if req.phase == "start" { return self.fixture(ExchangeResponse.self, "exchange-start") }
            if req.checkResponses.count == 1 { return self.fixture(ExchangeResponse.self, "exchange-screener") }
            return self.fixture(ExchangeResponse.self, "exchange-series")
        }
        let s3 = BookSession(subjectID: "data", title: "d", service: fake, storage: storage)
        s3.pollInterval = 0
        await s3.open()
        await s3.place(level: 3)
        await s3.submitCheck(s3.chapter!.check.map {
            ItemResponse(itemId: $0.id, response: nil, selectedIndex: nil, confidence: 1, idk: true)
        })
        XCTAssertNil(fake.exchanges.last?.chunkMinutes)
    }

    func testTheServersChapterReplacesTheCachedOneOnOpen() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        let s = session()
        await s.open()
        XCTAssertEqual(s.chapter?.unit, "u1")
        let oldHTML = s.chapter!.html

        // Re-authored server-side: same unit, new prose.
        fake.onChapter = { [unowned self] _ in
            let ch = self.fixture(ChapterStatus.self, "chapter").chapter!
            let json = try! JSONEncoder().encode(ch)
            var obj = try! JSONSerialization.jsonObject(with: json) as! [String: Any]
            obj["html"] = "<p>rechained</p>"
            let fresh = try! JSONDecoder().decode(ChapterPayload.self, from: try! JSONSerialization.data(withJSONObject: obj))
            return ChapterStatus(chapter: fresh, authoring: false, authoringError: "")
        }
        let s2 = session()
        await s2.open()
        guard case .reading = s2.screen else { return XCTFail("\(s2.screen)") }
        XCTAssertEqual(s2.chapter?.html, "<p>rechained</p>")
        XCTAssertNotEqual(s2.chapter?.html, oldHTML)
        XCTAssertTrue(fake.exchanges.filter { $0.phase == "start" }.count == 1, "no second start: the server already had the chapter")

        // Still being rewritten: the wait screen, then the new chapter.
        var polls = 0
        fake.onChapter = { [unowned self] _ in
            polls += 1
            if polls < 2 { return ChapterStatus(chapter: nil, authoring: true, authoringError: "") }
            return self.fixture(ChapterStatus.self, "chapter")
        }
        let s3 = session()
        await s3.open()
        guard case .reading = s3.screen else { return XCTFail("\(s3.screen)") }
        XCTAssertEqual(polls, 2)
    }

    /// While the next chapter is being written the server says which stage
    /// the build is at and for how long; the session keeps both for the
    /// waiting screen, and drops them when the chapter arrives.
    func testTheAuthoringWaitCarriesTheServersStage() async throws {
        let json = #"{"chapter":null,"authoring":true,"authoring_error":"","authoring_stage":"writing","authoring_seconds":42}"#
        let mid = try JSONDecoder().decode(ChapterStatus.self, from: Data(json.utf8))
        XCTAssertEqual(mid.authoringStage, "writing")
        XCTAssertEqual(mid.authoringSeconds, 42)

        scriptFreshBook()
        let s = session()
        var polls = 0
        var seenStage: String?
        var seenSince: Date?
        fake.onChapter = { [unowned self] _ in
            // Nothing to say until a graded series has u1 authoring; then
            // one poll mid-build, and the chapter on the next.
            guard self.fake.exchanges.contains(where: { $0.checkResponses.count > 1 }) else {
                return ChapterStatus(chapter: nil, authoring: false, authoringError: "")
            }
            polls += 1
            if polls == 1 { return mid }
            seenStage = s.authoringStage
            seenSince = s.authoringSince
            return self.fixture(ChapterStatus.self, "chapter")
        }
        await s.open()
        await s.place(level: 3)
        await s.submitCheck(s.chapter!.check.map {
            ItemResponse(itemId: $0.id, response: nil, selectedIndex: nil, confidence: 1, idk: true)
        })
        await s.proceed()
        guard case .reading = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(seenStage, "writing", "the stage the first poll reported")
        let since = try XCTUnwrap(seenSince)
        XCTAssertEqual(Date().timeIntervalSince(since), 42, accuracy: 5, "the build started 42 s before the first poll")
        XCTAssertNil(s.authoringStage, "cleared once the chapter arrived")
    }

    func testACachedChapterTheServerDroppedIsWrittenAfresh() async {
        scriptFreshBook()
        var starts = 0
        fake.onExchange = { [unowned self] req in
            if req.phase == "start" { starts += 1 }
            return self.deliversU1()
        }
        let s = session()
        await s.open()
        XCTAssertEqual(s.chapter?.unit, "u1")
        XCTAssertEqual(starts, 1)

        // The server dropped its snapshot (the unit's bank was rewritten)
        // and is not writing: the cache is stale too, and a start rebuilds.
        fake.onChapter = { _ in ChapterStatus(chapter: nil, authoring: false, authoringError: "") }
        let s2 = session()
        await s2.open()
        guard case .reading = s2.screen else { return XCTFail("\(s2.screen)") }
        XCTAssertEqual(starts, 2, "the reopen asked the server to write the chapter again")
        XCTAssertEqual(s2.chapter?.unit, "u1")
    }

    /// A repair chapter is cached under its own id, which is on no spine;
    /// the unit it repairs is the one in progress. A reopen that cannot
    /// reach the server's copy must keep it, not start the unit afresh.
    func testACachedRepairChapterSurvivesAReopen() async {
        scriptFreshBook()
        let state = fixture(BookState.self, "state")
        var starts = 0
        fake.onExchange = { [unowned self] req in
            if req.phase == "start" { starts += 1; return self.deliversU1() }
            if req.repair {
                return ExchangeResponse(results: [], gate: nil, chapter: nil, state: state,
                                        breakSuggestion: nil, authoring: "u1.repair", resultsDoc: nil)
            }
            return self.fixture(ExchangeResponse.self, "exchange-fail")
        }
        // Like a real server: no chapter until the repair was asked for.
        fake.onChapter = { [unowned self] _ in
            self.fake.exchanges.contains { $0.repair }
                ? ChapterStatus(chapter: self.chapterPayload("u1.repair", "The misses: Tokens"), authoring: false, authoringError: "")
                : ChapterStatus(chapter: nil, authoring: false, authoringError: "")
        }
        let s = session()
        await s.open()
        await s.submitCheck([ItemResponse(itemId: "u1-q1", response: "wrong", selectedIndex: nil, confidence: 4)])
        await s.repair()
        guard case .reading = s.screen else { return XCTFail("\(s.screen)") }
        XCTAssertEqual(s.chapter?.unit, "u1.repair")
        XCTAssertEqual(s.chapter?.baseUnit, "u1")
        s.persist()

        fake.onChapter = { _ in throw URLError(.cannotConnectToHost) }
        let s2 = session()
        await s2.open()
        XCTAssertEqual(s2.chapter?.unit, "u1.repair", "the repair chapter in hand was dropped")
        XCTAssertEqual(starts, 1, "the reopen started the unit afresh")
    }

    func testMarksAndInkBelongToTheChapterAndSurviveRelaunch() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        let s = session()
        await s.open()
        XCTAssertTrue(s.marks.isEmpty)
        s.addMark(kind: .highlight, start: 10, end: 40, text: "a model trained to do nothing")
        s.saveInk(Data([1, 2, 3]))
        s.persist()  // as closing the book does

        let s2 = session()
        await s2.open()
        XCTAssertEqual(s2.marks.count, 1)
        XCTAssertEqual(s2.marks[0].kind, .highlight)
        XCTAssertEqual(s2.marks[0].text, "a model trained to do nothing")
        XCTAssertEqual(s2.inkData, Data([1, 2, 3]))
        s2.removeMark(s2.marks[0].id)
        s2.saveInk(nil)
        s2.persist()

        let s3 = session()
        await s3.open()
        XCTAssertTrue(s3.marks.isEmpty)
        XCTAssertNil(s3.inkData)

        // The same span marked twice is one mark.
        let first = s3.addMark(kind: .highlight, start: 10, end: 40, text: "a model trained to do nothing")
        let again = s3.addMark(kind: .highlight, start: 10, end: 40, text: "a model trained to do nothing")
        XCTAssertEqual(first.id, again.id)
        XCTAssertEqual(s3.marks.count, 1)
        s3.addMark(kind: .question, start: 10, end: 40, text: "a model trained to do nothing")
        XCTAssertEqual(s3.marks.count, 2, "a question on a highlighted span is its own mark")
    }

    /// A book read as it is has no check to carry the reader forward, so
    /// it turns pages: the next chapter of the book's own order, and the
    /// one before it.
    func testABookReadAsItIsTurnsToTheNextChapterAndBack() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        fake.onState = { _ in
            BookState(spine: [
                SpineEntry(unit: "u1", title: "Copyright", status: "active", score: nil, inFringe: true),
                SpineEntry(unit: "u2", title: "Introduction", status: "available", score: nil, inFringe: true),
                SpineEntry(unit: "u3", title: "Chapter 1", status: "available", score: nil, inFringe: true),
            ], fringe: ["u1", "u2", "u3"], debt: [], activeMisconceptions: [], summary: "", sessionMinutes: 0)
        }
        let s = session()
        s.kind = "reading"
        await s.open()
        await s.refreshState()
        XCTAssertEqual(s.chapter?.unit, "u1")

        XCTAssertNil(s.previousChapter, "the first chapter has nothing before it")
        XCTAssertEqual(s.nextChapter?.title, "Introduction")
        fake.exchanges.removeAll()
        await s.turnTo(s.nextChapter)
        XCTAssertEqual(fake.exchanges.last?.choice, "u2", "the next chapter was asked for")

        // From the middle, both ways; from the end, only back.
        fake.onState = { _ in
            BookState(spine: [
                SpineEntry(unit: "u1", title: "Copyright", status: "passed", score: nil, inFringe: true),
                SpineEntry(unit: "u2", title: "Introduction", status: "active", score: nil, inFringe: true),
                SpineEntry(unit: "u3", title: "Chapter 1", status: "available", score: nil, inFringe: true),
            ], fringe: ["u1", "u2", "u3"], debt: [], activeMisconceptions: [], summary: "", sessionMinutes: 0)
        }
        fake.onExchange = { [unowned self] _ in
            var r = self.deliversU1()
            return ExchangeResponse(results: r.results, gate: r.gate, chapter: self.chapterPayload("u2", "Introduction"),
                                    state: r.state, breakSuggestion: nil, authoring: nil, resultsDoc: nil)
        }
        await s.start(choice: "u2")
        await s.refreshState()
        XCTAssertEqual(s.previousChapter?.title, "Copyright")
        XCTAssertEqual(s.nextChapter?.title, "Chapter 1")
    }

    func testClosingTheAskCardPutsTheAskToolDown() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        let s = session()
        await s.open()
        // A question asked and answered: closing the card puts the tool
        // down, so the next drag scrolls instead of highlighting again.
        s.tool = .ask
        let m = s.addMark(kind: .question, start: 10, end: 40, text: "a model trained to do nothing")
        XCTAssertEqual(s.asking?.mark.id, m.id)
        s.closeAsking()
        XCTAssertEqual(s.tool, .none, "the ask tool is down")
        XCTAssertEqual(s.marks.count, 0, "a question with nothing asked leaves no mark")

        // The same when the card is deleted from, or dismissed after an
        // answer.
        s.tool = .ask
        s.addMark(kind: .question, start: 10, end: 40, text: "a model trained to do nothing")
        s.deleteAsking()
        XCTAssertEqual(s.tool, .none)
    }

    func testUndoTakesBackTheLastMarkOrStroke() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        let s = session()
        await s.open()
        XCTAssertFalse(s.canUndo)
        s.addMark(kind: .highlight, start: 10, end: 40, text: "a model trained to do nothing")
        s.saveInk(Data([1]))
        s.saveInk(Data([1, 2]))
        XCTAssertTrue(s.canUndo)
        s.undo()
        XCTAssertEqual(s.inkData, Data([1]), "the last stroke goes")
        s.undo()
        XCTAssertNil(s.inkData, "then the first")
        XCTAssertEqual(s.marks.count, 1)
        s.undo()
        XCTAssertTrue(s.marks.isEmpty, "then the highlight")
        XCTAssertFalse(s.canUndo)
        s.undo()
        XCTAssertTrue(s.marks.isEmpty, "nothing left to take back")

        // A mark already removed is not in the history; reopening the
        // chapter forgets the history.
        let m = s.addMark(kind: .highlight, start: 50, end: 60, text: "trained")
        s.removeMark(m.id)
        XCTAssertFalse(s.canUndo)
        s.addMark(kind: .highlight, start: 50, end: 60, text: "trained")
        s.setChapterForTesting(fixture(ChapterStatus.self, "chapter").chapter!)
        XCTAssertEqual(s.marks.count, 1, "the mark is kept")
        XCTAssertFalse(s.canUndo)
    }

    func testAHighlightComesOffByATapOrTheEraser() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        let s = session()
        await s.open()
        let h = s.addMark(kind: .highlight, start: 10, end: 40, text: "a model trained to do nothing")
        let q = s.addMark(kind: .question, start: 50, end: 60, text: "trained")
        s.asking = nil
        // A tap on a highlight offers to remove it; on a question it
        // reopens the card.
        await s.openMark(h.id)
        XCTAssertEqual(s.removing?.mark.id, h.id)
        XCTAssertNil(s.asking)
        s.removing = nil
        XCTAssertEqual(s.marks.count, 2, "kept")
        await s.openMark(h.id)
        s.removeMarkInHand()
        XCTAssertEqual(s.marks.map(\.id), [q.id])
        XCTAssertNil(s.removing)
        await s.openMark(q.id)
        XCTAssertNil(s.removing)
        XCTAssertEqual(s.asking?.mark.id, q.id)
        // The eraser takes a highlight off as it takes ink off; a question
        // is deleted from its card, not rubbed out.
        let h2 = s.addMark(kind: .highlight, start: 70, end: 80, text: "nothing")
        s.erase(markID: q.id)
        s.erase(markID: h2.id)
        XCTAssertEqual(s.marks.map(\.id), [q.id])
    }

    func testAskingSendsThePassageAndKeepsTheAnswerOnTheMark() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        fake.onAsk = { unit, quote, q in AskResponse(unit: unit, answerMd: "Because $\\ln$ is what the code computes.") }
        let s = session()
        await s.open()

        // A question mark opens the card; closing it unasked leaves nothing.
        s.addMark(kind: .question, start: 100, end: 140, text: "the loss is this same quantity")
        XCTAssertNotNil(s.asking)
        s.closeAsking()
        XCTAssertNil(s.asking)
        XCTAssertTrue(s.marks.isEmpty)

        let m = s.addMark(kind: .question, start: 100, end: 140, text: "the loss is this same quantity")
        await s.ask("why nats and not bits?")
        XCTAssertEqual(fake.asks.count, 1)
        XCTAssertEqual(fake.asks[0].unit, "u1")
        XCTAssertEqual(fake.asks[0].quote, "the loss is this same quantity")
        XCTAssertEqual(fake.asks[0].question, "why nats and not bits?")
        XCTAssertEqual(s.asking?.mark.answer, "Because $\\ln$ is what the code computes.")
        XCTAssertEqual(s.marks.first?.answer, s.asking?.mark.answer)
        XCTAssertNil(s.asking?.error)

        // The answered question survives a relaunch and reopens on tap.
        s.closeAsking()
        let s2 = session()
        await s2.open()
        XCTAssertEqual(s2.marks.count, 1)
        await s2.openMark(m.id)
        XCTAssertEqual(s2.asking?.mark.question, "why nats and not bits?")

        // The tutor away: the question stays on the mark, the card says so.
        fake.onAsk = { _, _, _ in throw URLError(.cannotConnectToHost) }
        s2.addMark(kind: .question, start: 1, end: 5, text: "One")
        await s2.ask("what?")
        XCTAssertNotNil(s2.asking?.error)
        XCTAssertNil(s2.asking?.mark.answer)
        XCTAssertEqual(s2.marks.count, 2)
    }

    /// With the tutor out of reach and a model on the device, the device
    /// answers from the chapter, and says that it did.
    func testTheDeviceAnswersWhenTheTutorIsAway() async {
        scriptFreshBook()
        fake.onExchange = { [unowned self] _ in self.deliversU1() }
        let s = session()
        await s.open()
        let local = FakeAnswerer()
        local.answer = "Because the chapter says so."
        s.localTutor = local
        fake.onAsk = { _, _, _ in throw URLError(.cannotConnectToHost) }
        s.addMark(kind: .question, start: 1, end: 5, text: "One")
        await s.ask("why?")
        XCTAssertNil(s.asking?.error)
        let answer = s.asking?.mark.answer ?? ""
        XCTAssertTrue(answer.hasPrefix("Because the chapter says so."), answer)
        XCTAssertTrue(answer.contains("on this device"), "the reader is told who answered: \(answer)")
        XCTAssertEqual(local.questions, ["why?"])
        XCTAssertEqual(local.quotes, ["One"])
        XCTAssertFalse(local.chapters[0].isEmpty, "the chapter is the context")
        XCTAssertNil(local.chapters[0].range(of: "</?(p|h2|div|em)>", options: .regularExpression), "as text, not markup")
        XCTAssertFalse(local.chapters[0].contains("&gt;"), "entities decoded")
        XCTAssertTrue(local.chapters[0].contains("> est"), "the chapter's &gt; is a greater-than sign")

        // The tutor back: the device is not asked.
        fake.onAsk = { unit, _, _ in AskResponse(unit: unit, answerMd: "From the tutor.") }
        s.addMark(kind: .question, start: 6, end: 9, text: "Two")
        await s.ask("and?")
        XCTAssertEqual(s.asking?.mark.answer, "From the tutor.")
        XCTAssertEqual(local.questions.count, 1)

        // Both away: the old message.
        fake.onAsk = { _, _, _ in throw URLError(.cannotConnectToHost) }
        local.fails = true
        s.addMark(kind: .question, start: 10, end: 14, text: "Three")
        await s.ask("so?")
        XCTAssertNotNil(s.asking?.error)
        XCTAssertNil(s.asking?.mark.answer)
    }
}

/// A device model that answers whatever it is asked.
final class FakeAnswerer: PassageAnswerer {
    var answer = ""
    var fails = false
    var questions: [String] = []
    var quotes: [String] = []
    var chapters: [String] = []
    func answer(question: String, about quote: String, in chapter: String, history: [QA]) async throws -> String {
        questions.append(question); quotes.append(quote); chapters.append(chapter)
        if fails { throw URLError(.unknown) }
        return answer
    }
}

@MainActor
final class InkTests: XCTestCase {
    func testPenColorPersistsAndInkSavesAfterAPause() async throws {
        UserDefaults.standard.removeObject(forKey: "penColor")
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let s = BookSession(subjectID: "ai", title: "AI", service: FakeService(), storage: storage)
        XCTAssertEqual(s.penColor, .red)
        s.penColor = .blue
        XCTAssertEqual(UserDefaults.standard.string(forKey: "penColor"), "blue")
        let s2 = BookSession(subjectID: "ai", title: "AI", service: FakeService(), storage: storage)
        XCTAssertEqual(s2.penColor, .blue)

        let json = #"{"unit":"u1","title":"T","minutes":1,"html":"<p>x</p>","beats":[],"pretest":[],"check":[],"next_action":"read"}"#
        s.setChapterForTesting(try JSONDecoder().decode(ChapterPayload.self, from: Data(json.utf8)))
        let file = storage.appendingPathComponent("ai/ink-u1.pkdrawing")
        s.saveInk(Data([1]))
        s.saveInk(Data([1, 2]))
        XCTAssertFalse(FileManager.default.fileExists(atPath: file.path), "not written per stroke")
        try await Task.sleep(nanoseconds: 700_000_000)
        XCTAssertEqual(try Data(contentsOf: file), Data([1, 2]))
        UserDefaults.standard.removeObject(forKey: "penColor")
    }
}

@MainActor
final class AskThreadTests: XCTestCase {
    private func session(_ fake: FakeService) -> BookSession {
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let s = BookSession(subjectID: "ai", title: "AI", service: fake, storage: storage)
        let json = #"{"unit":"u1","title":"T","minutes":1,"html":"<p>predict the next token</p>","beats":[],"pretest":[],"check":[],"next_action":"read"}"#
        s.setChapterForTesting(try! JSONDecoder().decode(ChapterPayload.self, from: Data(json.utf8)))
        return s
    }

    func testAFollowUpCarriesTheThreadAndCloseKeepsTheMark() async throws {
        let fake = FakeService()
        var n = 0
        fake.onAsk = { _, _, q in n += 1; return AskResponse(unit: "u1", answerMd: "answer \(n) to \(q)") }
        let s = session(fake)
        let mark = s.addMark(kind: .question, start: 0, end: 22, text: "predict the next token")
        await s.ask("why tokens?")
        XCTAssertEqual(s.asking?.mark.answer, "answer 1 to why tokens?")
        XCTAssertEqual(fake.histories.first?.count, 0)

        await s.ask("and subwords?")
        XCTAssertEqual(fake.histories.last, [QA(question: "why tokens?", answer: "answer 1 to why tokens?")])
        XCTAssertEqual(s.asking?.mark.thread?.count, 1)
        XCTAssertEqual(s.asking?.mark.thread?.first?.answer, "answer 2 to and subwords?")
        XCTAssertEqual(s.marks.first?.thread?.count, 1, "the thread lives on the mark")

        s.closeAsking()
        XCTAssertNil(s.asking)
        XCTAssertEqual(s.marks.count, 1, "closing keeps the mark and its thread")
        await s.openMark(mark.id)
        XCTAssertEqual(s.asking?.mark.history.count, 2)

        s.deleteAsking()
        XCTAssertNil(s.asking)
        XCTAssertTrue(s.marks.isEmpty, "delete removes the question and its highlight")
    }
}

/// A flag on an item goes to the server on its own, with the item, the
/// concern and the answer given, and the item shows as flagged from then
/// on; a server out of reach marks nothing and says so.
@MainActor
final class FlagTests: XCTestCase {
    private func session(_ fake: FakeService) -> BookSession {
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let s = BookSession(subjectID: "ai", title: "AI", service: fake, storage: storage)
        let json = #"{"unit":"u1","title":"T","minutes":1,"html":"<p>x</p>","beats":[],"pretest":[],"check":[{"id":"u1-q1","kind":"constructed","prompt":"How many hours?","check":"numeric"},{"id":"u1-q2","kind":"mcq","prompt":"Which?","check":"choice","options":[{"text":"a"},{"text":"b"}]}],"next_action":"read"}"#
        s.setChapterForTesting(try! JSONDecoder().decode(ChapterPayload.self, from: Data(json.utf8)))
        return s
    }

    func testFlaggingSendsTheConcernWithTheAnswerAndMarksTheItem() async throws {
        let fake = FakeService()
        let s = session(fake)
        let item = s.chapter!.check[0]
        let given = ItemResponse(itemId: "u1-q1", response: "0", selectedIndex: nil, confidence: 3)
        try await s.flag(item: item, concern: "  the key would have been rotated at 16:00  ", response: given)
        XCTAssertEqual(fake.flags.count, 1)
        XCTAssertEqual(fake.flags.first?.unit, "u1")
        XCTAssertEqual(fake.flags.first?.item, "u1-q1")
        XCTAssertEqual(fake.flags.first?.text, "the key would have been rotated at 16:00")
        XCTAssertEqual(fake.flags.first?.response?.response, "0")
        XCTAssertEqual(s.flagged, ["u1-q1"])

        // Before any answer the flag travels without one.
        try await s.flag(item: s.chapter!.check[1], concern: "both b and a are right", response: nil)
        XCTAssertNil(fake.flags.last?.response)
        XCTAssertEqual(s.flagged, ["u1-q1", "u1-q2"])
    }

    func testAnEmptyConcernIsNotSent() async throws {
        let fake = FakeService()
        let s = session(fake)
        try await s.flag(item: s.chapter!.check[0], concern: "   ", response: nil)
        XCTAssertTrue(fake.flags.isEmpty)
        XCTAssertTrue(s.flagged.isEmpty)
    }

    func testAServerOutOfReachMarksNothing() async {
        let fake = FakeService()
        fake.onFlag = { _, _, _ in throw URLError(.cannotConnectToHost) }
        let s = session(fake)
        do {
            try await s.flag(item: s.chapter!.check[0], concern: "wrong", response: nil)
            XCTFail("a failed flag did not throw")
        } catch {}
        XCTAssertTrue(s.flagged.isEmpty)
    }
}
