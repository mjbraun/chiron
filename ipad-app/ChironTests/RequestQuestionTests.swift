import XCTest
@testable import Chiron

/// The agent can stop on a request and ask. The reader hears about it,
/// sees the question in the requests card, and answers there; the answer
/// sends the request back to the agent.
@MainActor
final class RequestQuestionTests: XCTestCase {
    private func library(_ fake: FakeService) -> Library {
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let l = Library(storage: storage, service: fake)
        l.requestPollInterval = 0.05
        l.notices = FakeNotices()
        return l
    }

    private func request(_ status: String, question: String? = nil, thread: [ChangeRequest.Exchange]? = nil) -> ChangeRequest {
        ChangeRequest(id: "req-1", text: "make the shelf nicer", status: status, createdAt: "2026-09-25T10:00:00Z",
                      log: [], last: nil, summary: nil, reason: nil, commit: nil, build: nil,
                      question: question, thread: thread)
    }

    func testAWaitingRequestDecodesWithItsQuestionAndThread() throws {
        let json = #"""
        {"id":"req-1","text":"nicer","status":"waiting","created_at":"2026-09-25T10:00:00Z","log":[],
         "question":"Covers, or denser rows?",
         "thread":[{"question":"Which shelf?","answer":"The library","at":"2026-09-25T10:01:00Z"}]}
        """#
        let r = try JSONDecoder().decode(ChangeRequest.self, from: Data(json.utf8))
        XCTAssertTrue(r.waiting)
        XCTAssertFalse(r.open, "waiting is in the reader's hands, not the agent's")
        XCTAssertEqual(r.question, "Covers, or denser rows?")
        XCTAssertEqual(r.thread?.first?.answer, "The library")
    }

    /// A request the agent stops on is news, once, with the question; the
    /// wrench counts what waits on the reader.
    func testTheReaderIsToldWhenTheAgentAsks() async throws {
        let fake = FakeService()
        fake.onChangeRequests = { [unowned self] in [self.request("working")] }
        let library = library(fake)
        let notices = try XCTUnwrap(library.notices as? FakeNotices)
        await library.refreshRequests()
        XCTAssertEqual(library.waitingOnReader, 0)

        fake.onChangeRequests = { [unowned self] in [self.request("waiting", question: "Covers, or denser rows?")] }
        await library.refreshRequests()
        XCTAssertEqual(notices.sent.map(\.title), ["Chiron has a question"])
        XCTAssertEqual(notices.sent.first?.body, "Covers, or denser rows?")
        XCTAssertEqual(library.waitingOnReader, 1)
        XCTAssertFalse(library.awaiting, "nothing to watch until the reader answers")

        await library.refreshRequests()
        XCTAssertEqual(notices.sent.count, 1, "said once")
    }

    func testAnAnswerGoesUpAndTheRequestIsWatchedAgain() async throws {
        let fake = FakeService()
        fake.onChangeRequests = { [unowned self] in [self.request("waiting", question: "Covers?")] }
        let library = library(fake)
        await library.refreshRequests()

        fake.onChangeRequests = { [unowned self] in [self.request("queued", thread: [.init(question: "Covers?", answer: "Denser rows.", at: nil)])] }
        let ok = await library.answerRequest("req-1", "  Denser rows. ")
        XCTAssertTrue(ok)
        XCTAssertEqual(fake.answers.map(\.answer), ["Denser rows."])
        XCTAssertEqual(fake.answers.first?.id, "req-1")
        XCTAssertEqual(library.waitingOnReader, 0)
        XCTAssertTrue(library.awaiting, "back in the agent's hands")

        let blank = await library.answerRequest("req-1", "   ")
        XCTAssertFalse(blank)
        XCTAssertEqual(fake.answers.count, 1, "a blank answer is not sent")

        fake.onAnswerRequest = { _, _ in throw ServiceError.status(409) }
        let refused = await library.answerRequest("req-1", "again")
        XCTAssertFalse(refused)
        XCTAssertNotNil(library.requestError)
    }
}
