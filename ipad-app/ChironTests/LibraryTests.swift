import XCTest
@testable import Chiron

/// The shelf's own behaviour: what happens to a capture once its question
/// is sent, without a server.
@MainActor
final class LibraryTests: XCTestCase {
    private func subjects(_ status: String, scale: String = "primer", extra: String = "") -> SubjectsResponse {
        let json = """
        {"subjects":[
          {"id":"ai","title":"How AI Works","kind":"book"},
          {"id":"primer-why","title":"Why","kind":"primer","status":"\(status)","scale":"\(scale)","source":{"text":"words"},"captured_at":"2026-09-04T12:00:00Z"}
          \(extra)
        ],"active":"ai"}
        """
        return try! JSONDecoder().decode(SubjectsResponse.self, from: Data(json.utf8))
    }

    private func library(_ fake: FakeService) -> Library {
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let l = Library(storage: storage, service: fake)
        l.shelfPollInterval = 0.05
        l.requestPollInterval = 0.05
        l.notices = FakeNotices()
        return l
    }

    private func fakeNotices(_ library: Library) throws -> FakeNotices {
        try XCTUnwrap(library.notices as? FakeNotices)
    }

    private func decode(_ json: String) -> SubjectsResponse {
        try! JSONDecoder().decode(SubjectsResponse.self, from: Data(json.utf8))
    }

    private func wait(for what: String, _ done: () -> Bool) async throws {
        let deadline = Date().addingTimeInterval(3)
        while !done(), Date() < deadline {
            try await Task.sleep(nanoseconds: 20_000_000)
        }
        XCTAssertTrue(done(), "waited for \(what)")
    }

    private func planned(_ reply: String, done: Bool = false, brief: String? = nil, title: String? = nil) -> CaptureResponse {
        CaptureResponse(scale: "primer", answerMd: nil, subject: "primer-why", status: "planning", title: title ?? "Why",
                        replyMd: reply, done: done, brief: brief)
    }

    private func waitForSession(_ library: Library, _ id: String) async throws {
        let deadline = Date().addingTimeInterval(3)
        while library.session?.subjectID != id, Date() < deadline {
            try await Task.sleep(nanoseconds: 20_000_000)
        }
    }

    /// A build the MacBook made is offered when it is newer than the app
    /// running, by build number; the same or older says nothing, and a
    /// server with no build, or none reachable, says nothing either.
    func testANewerBuildIsOffered() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        let library = library(fake)
        library.sync.baseURL = "https://chiron.example"
        library.runningBuild = 295
        fake.onLatestBuild = { throw URLError(.cannotConnectToHost) }
        await library.checkForBuild()
        XCTAssertNil(library.availableBuild)

        fake.onLatestBuild = { AppBuild(version: "2026.9.19", build: 295, commit: "c97416a", status: "ready", manifestPath: "/builds/ab/manifest.plist", macPath: "/builds/ab/Chiron-mac.zip") }
        await library.checkForBuild()
        XCTAssertNil(library.availableBuild, "the build already running")

        fake.onLatestBuild = { AppBuild(version: "2026.9.20", build: 301, commit: "d00d1e5", status: "ready", manifestPath: "/builds/cd/manifest.plist", macPath: "/builds/cd/Chiron-mac.zip") }
        await library.checkForBuild()
        XCTAssertEqual(library.availableBuild?.build, 301)
        XCTAssertEqual(library.availableBuild?.label, "Chiron 2026.9.20 (301)")
        // The Mac installs a zip it can unpack; the iPad installs through
        // the manifest, which is the only way an app reaches it.
        #if targetEnvironment(macCatalyst)
        XCTAssertEqual(library.installURL?.absoluteString,
                       "https://chiron.example/builds/cd/Chiron-mac.zip")
        #else
        XCTAssertEqual(library.installURL?.absoluteString,
                       "itms-services://?action=download-manifest&url=https://chiron.example/builds/cd/manifest.plist")
        #endif

        fake.onLatestBuild = { AppBuild(version: "2026.9.20", build: 302, commit: "bad", status: "failed", manifestPath: "", macPath: "") }
        await library.checkForBuild()
        XCTAssertNil(library.availableBuild, "a failed build is not offered")
    }

    /// "Request a change": the words go up with where the reader was (the
    /// harness state and a screenshot), the request comes back queued
    /// and joins the list, and the list is what the server says it is.
    func testAChangeRequestGoesUpWithWhereTheReaderWas() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        fake.onRequestChange = { text, state, png in
            ChangeRequest(id: "req-1", text: text, status: "queued", createdAt: "2026-09-20T10:00:00Z", log: [], last: nil, summary: nil, reason: nil, commit: nil, build: nil)
        }
        fake.onChangeRequests = { [ChangeRequest(id: "req-1", text: "the pen is too thin", status: "working", createdAt: "2026-09-20T10:00:00Z", log: ["working on req/req-1"], last: "working on req/req-1", summary: nil, reason: nil, commit: nil, build: nil)] }
        let library = library(fake)
        await library.launch()
        let sent = await library.requestChange("the pen is too thin", withPicture: true)
        XCTAssertEqual(sent.id, "req-1")
        XCTAssertEqual(fake.requests.count, 1)
        XCTAssertEqual(fake.requests[0].text, "the pen is too thin")
        XCTAssertEqual(fake.requests[0].state["screen"] as? String, "bookshelf", "the harness state travels")
        XCTAssertNotNil(fake.requests[0].png, "and a screenshot")
        XCTAssertEqual(library.requests.first?.status, "working", "the list is refreshed after sending")
        XCTAssertNil(library.requestError)

        _ = await library.requestChange("no picture", withPicture: false)
        XCTAssertNil(fake.requests[1].png)

        fake.onRequestChange = { _, _, _ in throw URLError(.cannotConnectToHost) }
        let kept = await library.requestChange("lost", withPicture: false)
        XCTAssertEqual(kept.status, ChangeRequest.keptHere, "a request the sprite does not take waits on the device")
        XCTAssertNil(library.requestError)
    }

    /// A request the sprite does not take (asleep, away, a body cut short
    /// on a poor link) is not lost and not the reader's to send again: it
    /// is kept on the device, shown among the requests, and sent when the
    /// sprite answers, with where the reader was when they asked.
    func testARequestTheSpriteDoesNotTakeIsKeptOnTheDeviceAndSentLater() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        fake.onRequestChange = { _, _, _ in throw ServiceError.status(400) }
        let library = library(fake)
        let notices = try fakeNotices(library)
        await library.launch()
        let kept = await library.requestChange("a thicker pen", withPicture: true)
        XCTAssertEqual(kept.status, ChangeRequest.keptHere)
        XCTAssertEqual(kept.text, "a thicker pen")
        XCTAssertNil(library.requestError, "nothing for the reader to do")
        XCTAssertEqual(library.queued.count, 1)
        XCTAssertEqual(library.shownRequests.map(\.status), [ChangeRequest.keptHere], "it shows among the requests")
        XCTAssertTrue(library.shownRequests[0].open, "and counts as in hand")
        XCTAssertEqual(notices.prepared, 0, "nothing is coming yet")

        // The queue survives a relaunch.
        let again = Library(storage: library.storage, service: fake)
        XCTAssertEqual(again.queued.map(\.text), ["a thicker pen"])

        // The sprite answers: the request goes as it was asked.
        let tries = fake.requests.count
        fake.onRequestChange = { text, _, _ in
            ChangeRequest(id: "req-1", text: text, status: "queued", createdAt: "2026-10-12T23:00:00Z", log: [], last: nil, summary: nil, reason: nil, commit: nil, build: nil)
        }
        fake.onChangeRequests = { [ChangeRequest(id: "req-1", text: "a thicker pen", status: "working", createdAt: "2026-10-12T23:00:00Z", log: [], last: "working", summary: nil, reason: nil, commit: nil, build: nil)] }
        try await wait(for: "the send") { library.queued.isEmpty }
        XCTAssertEqual(fake.requests.count, tries + 1)
        XCTAssertEqual(fake.requests.last?.text, "a thicker pen")
        XCTAssertEqual(fake.requests.last?.state["screen"] as? String, "bookshelf", "where the reader was when they asked")
        XCTAssertNotNil(fake.requests.last?.png, "and the picture")
        XCTAssertEqual(fake.requests.last?.clientID, kept.id, "under the id it had here, so a lost reply does not make two")
        XCTAssertEqual(notices.prepared, 1)
        try await wait(for: "the list") { library.requests.first?.id == "req-1" }
        XCTAssertEqual(library.shownRequests.map(\.id), ["req-1"])
        XCTAssertTrue(library.awaiting)
    }

    /// Launch lands on the shelf: the active book is known, and its card
    /// says so, but the book is not opened for the reader.
    func testLaunchStaysOnTheShelf() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        let library = library(fake)
        await library.launch()
        XCTAssertEqual(library.activeSubjectID, "ai")
        XCTAssertEqual(library.subjects.count, 2)
        XCTAssertNil(library.session, "the shelf is the first screen")
    }

    /// The library as last seen shows without the server, and says so.
    func testTheLibraryShowsFromItsCacheWhenTheServerIsAway() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let first = Library(storage: storage, service: fake)
        await first.refresh()
        XCTAssertEqual(first.subjects.count, 2)

        let away = FakeService()  // every call fails
        let second = Library(storage: storage, service: away)
        XCTAssertEqual(second.subjects.map(\.id), ["ai", "primer-why"], "loaded from disk before any call")
        XCTAssertEqual(second.activeSubjectID, "ai")
        await second.refresh()
        XCTAssertEqual(second.subjects.count, 2, "a failed refresh keeps what was seen")
        XCTAssertEqual(second.shelfError, Library.offline)

        let empty = Library(storage: FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString), service: away)
        await empty.refresh()
        XCTAssertEqual(empty.shelfError, BookSession.unreachable, "nothing cached reads as unreachable")
    }

    /// A summary comes back into the card; the shelf is untouched.
    func testASummaryIsAnsweredInTheCard() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        fake.onCapture = { _ in CaptureResponse(scale: "summary", answerMd: "It is a robots.txt line.") }
        let library = library(fake)
        library.pendingCapture = Capture(text: "words")
        let reply = try await library.submitCapture(Capture(text: "words"), prompt: "what is this?", scale: .summary)
        XCTAssertEqual(reply.answerMd, "It is a robots.txt line.")
        XCTAssertEqual(library.captureAnswer, "It is a robots.txt line.")
        XCTAssertNotNil(library.pendingCapture, "the card stays up with the answer in it")
        XCTAssertNil(library.planning)
        XCTAssertEqual(fake.captures.first?.scale, .summary)
    }

    /// A primer capture from inside a book: the tutor's first question
    /// opens the planning card, the reader answers, the brief arrives, the
    /// build sends them to the shelf, and the primer opens when written.
    func testAPrimerIsPlannedThenOpensWhenWritten() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("planning") }
        fake.onCapture = { [unowned self] _ in self.planned("Which part matters to you?") }
        let library = library(fake)
        await library.refresh()
        await library.open("ai")
        library.pendingCapture = Capture(text: "words")

        let reply = try await library.submitCapture(Capture(text: "words"), prompt: "why?")
        XCTAssertEqual(reply.subject, "primer-why")
        XCTAssertNil(library.pendingCapture, "the capture card gave way")
        let plan = try XCTUnwrap(library.planning, "the planning card opened")
        XCTAssertEqual(plan.plan, [PlanMessage(role: "tutor", text: "Which part matters to you?")])
        XCTAssertFalse(plan.done)
        XCTAssertEqual(library.session?.subjectID, "ai", "planning does not close the book")
        XCTAssertTrue(library.subjects.contains(where: { $0.id == "primer-why" && $0.drafting }), "the draft is on the shelf")

        fake.onPlanTurn = { [unowned self] _, _ in self.planned("A primer on the ai-input part, then.", done: true, brief: "I want the ai-input part.", title: "Content-Signal") }
        await library.planReply("the ai-input part")
        let after = try XCTUnwrap(library.planning)
        XCTAssertEqual(after.plan.count, 3)
        XCTAssertEqual(after.plan[1], PlanMessage(role: "learner", text: "the ai-input part"))
        XCTAssertTrue(after.done)
        XCTAssertEqual(after.brief, "I want the ai-input part.")
        XCTAssertEqual(after.title, "Content-Signal")
        XCTAssertEqual(fake.planTurns.first?.text, "the ai-input part")

        fake.onBuild = { id in BuildResponse(subject: id, status: "authoring", title: "Content-Signal", book: nil) }
        fake.onSubjects = { [unowned self] in self.subjects("authoring") }
        await library.buildDraft()
        XCTAssertEqual(fake.builds, ["primer-why"])
        XCTAssertNil(library.planning)
        XCTAssertNil(library.session, "the shelf shows the primer being written")

        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        try await waitForSession(library, "primer-why")
        XCTAssertEqual(library.session?.subjectID, "primer-why", "the primer opened on its own")
        XCTAssertEqual(library.session?.kind, "primer")
    }

    /// A book draft: the build starts a generation; the book opens when
    /// it arrives on the shelf under its own name.
    func testABookDraftOpensTheBookWhenItArrives() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("planning", scale: "book") }
        fake.onCapture = { _ in CaptureResponse(scale: "book", subject: "primer-why", status: "planning", title: "Why", replyMd: "Enough.", done: true, brief: "a book") }
        let library = library(fake)
        _ = try await library.submitCapture(Capture(text: "words"), prompt: "why?", scale: .book)
        XCTAssertEqual(library.planning?.isBook, true)
        fake.onBuild = { id in BuildResponse(subject: id, status: "building", title: nil, book: "why-book") }
        fake.onSubjects = { [unowned self] in self.subjects("building", scale: "book") }
        await library.buildDraft()
        XCTAssertTrue(library.subjects.contains(where: { $0.id == "primer-why" && $0.building && $0.authoring }))
        fake.onSubjects = { [unowned self] in
            self.subjects("building", scale: "book", extra: ",{\"id\":\"why-book\",\"title\":\"Why, the book\",\"kind\":\"book\"}")
        }
        try await waitForSession(library, "why-book")
        XCTAssertEqual(library.session?.subjectID, "why-book", "the book opened when it arrived")
    }

    /// Leaving the plan keeps the draft; the shelf reopens it with its
    /// conversation, and a discard removes it.
    func testADraftLeftOnTheShelfReopensAndCanBeDiscarded() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("planning") }
        fake.onCapture = { [unowned self] _ in self.planned("Which part?") }
        let library = library(fake)
        _ = try await library.submitCapture(Capture(text: "words"), prompt: "why?")
        library.planning = nil
        XCTAssertTrue(library.subjects.contains(where: { $0.id == "primer-why" && $0.drafting }))

        fake.onPlan = { id in
            PlanState(id: id, title: "Why", scale: "primer", status: "planning", error: nil, prompt: "why?",
                      source: PrimerSource(text: "words", url: nil, app: nil), brief: nil, done: false,
                      plan: [PlanMessage(role: "tutor", text: "Which part?")], book: nil)
        }
        await library.openDraft("primer-why")
        XCTAssertEqual(library.planning?.plan.count, 1)
        fake.onSubjects = { [unowned self] in self.subjects("ready", extra: "") }
        await library.discardDraft()
        XCTAssertEqual(fake.discards, ["primer-why"])
        XCTAssertNil(library.planning)
    }

    /// The capture tool inside a book: the passage lands in the capture
    /// card, credited to the book, and the tool goes down.
    func testAPassageOfTheOpenBookBecomesACapture() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        let library = library(fake)
        await library.refresh()
        await library.open("ai")
        let s = try XCTUnwrap(library.session)
        s.tool = .capture
        s.capturePassage("a model trained to do nothing but predict the next token")
        XCTAssertEqual(library.pendingCapture?.text, "a model trained to do nothing but predict the next token")
        XCTAssertEqual(library.pendingCapture?.sourceApp, "How AI Works")
        XCTAssertEqual(s.tool, .none, "the tool went down")
        XCTAssertEqual(library.session?.subjectID, "ai", "the book stays open under the card")
    }

    /// A primer that failed stays on the shelf with its error; nothing opens.
    func testAFailedPrimerStaysOnTheShelf() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("planning") }
        fake.onCapture = { [unowned self] _ in self.planned("Which part?") }
        fake.onBuild = { id in BuildResponse(subject: id, status: "authoring", title: "Why", book: nil) }
        let library = library(fake)
        _ = try await library.submitCapture(Capture(text: "words"), prompt: "why?")
        fake.onSubjects = { [unowned self] in self.subjects("authoring") }
        await library.buildDraft()
        fake.onSubjects = { [unowned self] in self.subjects("failed") }
        try await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertNil(library.session)
        XCTAssertTrue(library.subjects.contains(where: { $0.id == "primer-why" && $0.failed }))
        XCTAssertEqual(try fakeNotices(library).sent.map(\.title), ["The primer could not be written"], "and the reader hears why")
    }

    // The reader may put the iPad down while the server writes: a word
    // when it is done, once, and none before.

    func testTheReaderIsToldWhenThePrimerIsWritten() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("planning") }
        fake.onCapture = { [unowned self] _ in self.planned("Which part?", done: true) }
        fake.onBuild = { id in BuildResponse(subject: id, status: "authoring", title: "Why", book: nil) }
        let library = library(fake)
        let notices = try fakeNotices(library)
        _ = try await library.submitCapture(Capture(text: "words"), prompt: "why?")
        fake.onSubjects = { [unowned self] in self.subjects("authoring") }
        await library.buildDraft()
        XCTAssertEqual(notices.prepared, 1, "the system is asked once there is something coming")
        XCTAssertTrue(notices.sent.isEmpty, "nothing to say while it is written")
        XCTAssertTrue(library.awaiting)

        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        try await waitForSession(library, "primer-why")
        XCTAssertEqual(notices.sent.map(\.title), ["Your primer is ready"])
        XCTAssertEqual(notices.sent.first?.body, "Why")
        XCTAssertEqual(notices.sent.first?.id, "primer-why")
        await library.refresh()
        XCTAssertEqual(notices.sent.count, 1, "said once")
        XCTAssertFalse(library.awaiting)
    }

    /// A primer written while a book is open still gets its word: the
    /// shelf keeps checking back for it behind the book.
    func testAPrimerWrittenBehindAnOpenBookStillGetsItsWord() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("authoring") }
        let library = library(fake)
        let notices = try fakeNotices(library)
        await library.launch()
        await library.open("ai")
        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        try await wait(for: "the word") { !notices.sent.isEmpty }
        XCTAssertEqual(notices.sent.map(\.title), ["Your primer is ready"])
        XCTAssertEqual(library.session?.subjectID, "ai", "the book stays open; the primer was not asked for here")
    }

    /// A book draft is watched under its book's name: the draft leaves
    /// the shelf when the book takes its place, and the word names the book.
    func testTheReaderIsToldWhenTheBookArrives() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.decode("""
        {"subjects":[{"id":"primer-why","title":"Why","kind":"primer","status":"building","scale":"book","book":"why-book","progress":"writing chapter 2 of 5"}]}
        """) }
        let library = library(fake)
        let notices = try fakeNotices(library)
        await library.refresh()
        XCTAssertTrue(notices.sent.isEmpty)
        fake.onSubjects = { [unowned self] in self.decode("""
        {"subjects":[{"id":"why-book","title":"Why, the book","kind":"book"}]}
        """) }
        try await wait(for: "the word") { !notices.sent.isEmpty }
        XCTAssertEqual(notices.sent.map(\.title), ["Your book is ready"])
        XCTAssertEqual(notices.sent.first?.body, "Why, the book")
        XCTAssertEqual(notices.sent.first?.id, "why-book")
    }

    func testTheReaderIsToldWhenABookCannotBeBuilt() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.decode("""
        {"subjects":[{"id":"primer-why","title":"Why","kind":"primer","status":"building","scale":"book","book":"why-book"}]}
        """) }
        let library = library(fake)
        let notices = try fakeNotices(library)
        await library.refresh()
        fake.onSubjects = { [unowned self] in self.decode("""
        {"subjects":[{"id":"primer-why","title":"Why","kind":"primer","status":"failed","error":"the model timed out","scale":"book","book":"why-book"}]}
        """) }
        try await wait(for: "the word") { !notices.sent.isEmpty }
        XCTAssertEqual(notices.sent.map(\.title), ["The book could not be built"])
        XCTAssertEqual(notices.sent.first?.body, "the model timed out")
    }

    /// A change request is watched with the card closed, and the word
    /// comes when the agent is done with it, once; a request that was
    /// already done when the app looked is not news.
    func testTheReaderIsToldWhenAChangeIsBuilt() async throws {
        let fake = FakeService()
        fake.onSubjects = { [unowned self] in self.subjects("ready") }
        let old = ChangeRequest(id: "req-0", text: "an older wish", status: "ready", createdAt: "2026-09-19T10:00:00Z", log: [], last: nil, summary: "done", reason: nil, commit: "abc", build: nil)
        fake.onChangeRequests = { [old] }
        fake.onRequestChange = { text, _, _ in
            ChangeRequest(id: "req-1", text: text, status: "queued", createdAt: "2026-09-20T10:00:00Z", log: [], last: nil, summary: nil, reason: nil, commit: nil, build: nil)
        }
        let library = library(fake)
        let notices = try fakeNotices(library)
        await library.launch()
        XCTAssertTrue(notices.sent.isEmpty, "what was done before is not news")

        let working = ChangeRequest(id: "req-1", text: "a thicker pen", status: "working", createdAt: "2026-09-20T10:00:00Z", log: [], last: "working", summary: nil, reason: nil, commit: nil, build: nil)
        fake.onChangeRequests = { [working, old] }
        _ = await library.requestChange("a thicker pen", withPicture: false)
        XCTAssertEqual(notices.prepared, 1)
        XCTAssertTrue(notices.sent.isEmpty, "nothing to say while the agent works")
        XCTAssertTrue(library.awaiting)

        var built = working
        built.status = "ready"
        built.build = ChangeRequest.Build(version: "2026.9.24", build: 310, token: "t")
        fake.onChangeRequests = { [built, old] }
        // The card is closed; the library checks back on its own.
        try await wait(for: "the word") { !notices.sent.isEmpty }
        XCTAssertEqual(notices.sent.map(\.title), ["Your change is built"])
        XCTAssertEqual(notices.sent.first?.body, "a thicker pen")
        await library.refreshRequests()
        XCTAssertEqual(notices.sent.count, 1, "said once")
        XCTAssertFalse(library.awaiting)

        var failed = working
        failed.id = "req-2"
        failed.text = "the moon"
        fake.onChangeRequests = { [failed, built, old] }
        await library.refreshRequests()
        failed.status = "failed"
        failed.reason = "tests"
        fake.onChangeRequests = { [failed, built, old] }
        try await wait(for: "the second word") { notices.sent.count == 2 }
        XCTAssertEqual(notices.sent.last?.title, "The change could not be made")
    }
}

/// Notices as the tests see them: kept, not shown.
final class FakeNotices: NoticeSender {
    var sent: [(id: String, title: String, body: String)] = []
    var prepared = 0
    func prepare() { prepared += 1 }
    func notify(id: String, title: String, body: String) { sent.append((id, title, body)) }
}
