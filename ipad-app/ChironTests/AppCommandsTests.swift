import XCTest
@testable import Chiron

/// The verbs act on the book the way the buttons do, and a verb that does
/// not exist is refused rather than ignored.
@MainActor
final class AppCommandsTests: XCTestCase {
    private func session() -> BookSession {
        let fake = FakeService()
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        return BookSession(subjectID: "ai", title: "How AI Works", service: fake, storage: storage)
    }

    func testToolVerbSetsTheTool() async throws {
        let s = session()
        try await AppCommands.run("tool", args: ["tool": "highlighter"], session: s, llm: false)
        XCTAssertEqual(s.tool, .highlighter)
        try await AppCommands.run("contents", args: [:], session: s, llm: false)
        XCTAssertTrue(s.contentsShown)
    }

    func testBadToolIsRefused() async {
        let s = session()
        do {
            try await AppCommands.run("tool", args: ["tool": "crayon"], session: s, llm: false)
            XCTFail("accepted a crayon")
        } catch AppCommands.Failure.badArguments {
        } catch {
            XCTFail("\(error)")
        }
    }

    /// Break time is off until the breaks verb turns it on, and the state
    /// reports which.
    func testBreaksVerbTurnsBreakTimeOn() async throws {
        let suite = "app-commands-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        addTeardownBlock { defaults.removePersistentDomain(forName: suite) }
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let library = Library(storage: storage, service: FakeService(), defaults: defaults)
        XCTAssertEqual(AppCommands.state(library)["break_time"] as? Bool, false)
        _ = try await AppCommands.run("breaks", args: [:], library: library)
        XCTAssertTrue(library.breakTime)
        XCTAssertEqual(AppCommands.state(library)["break_time"] as? Bool, true)
        _ = try await AppCommands.run("breaks", args: ["on": false], library: library)
        XCTAssertFalse(library.breakTime)
    }

    /// The flag verb flags the item named, or the first of the check, and
    /// the state lists what is flagged.
    func testFlagVerbFlagsAnItemOfTheChapter() async throws {
        let fake = FakeService()
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let s = BookSession(subjectID: "ai", title: "How AI Works", service: fake, storage: storage)
        let json = #"{"unit":"u1","title":"T","minutes":1,"html":"<p>x</p>","beats":[],"pretest":[{"id":"u1-p1","kind":"mcq","prompt":"P?","check":"choice","options":[{"text":"a"}]}],"check":[{"id":"u1-q1","kind":"constructed","prompt":"Q?","check":"numeric"}],"next_action":"read"}"#
        s.setChapterForTesting(try JSONDecoder().decode(ChapterPayload.self, from: Data(json.utf8)))

        try await AppCommands.run("flag", args: ["text": "the answer is wrong"], session: s, llm: false)
        XCTAssertEqual(fake.flags.first?.item, "u1-p1")
        XCTAssertEqual(fake.flags.first?.text, "the answer is wrong")
        try await AppCommands.run("flag", args: ["text": "so is this", "item": "u1-q1"], session: s, llm: false)
        XCTAssertEqual(fake.flags.last?.item, "u1-q1")
        XCTAssertEqual(s.flagged, ["u1-p1", "u1-q1"])
        do {
            try await AppCommands.run("flag", args: ["text": "x", "item": "u9-q9"], session: s, llm: false)
            XCTFail("flagged an item the chapter does not have")
        } catch AppCommands.Failure.badArguments {
        }
    }

    func testUnknownVerbIsRefused() async {
        let s = session()
        do {
            try await AppCommands.run("levitate", args: [:], session: s, llm: false)
            XCTFail("accepted levitate")
        } catch AppCommands.Failure.unknownVerb(let v) {
            XCTAssertEqual(v, "levitate")
        } catch {
            XCTFail("\(error)")
        }
    }

    func testAnswerModesCoverEveryItem() throws {
        let data = try fixture("chapter")
        let payload = try XCTUnwrap(JSONDecoder().decode(ChapterStatus.self, from: data).chapter)
        for mode in ["correct", "idk", "wrong", "mixed"] {
            let answers = AppCommands.answers(for: payload, mode: mode, llm: false)
            XCTAssertEqual(answers.count, payload.check.count, mode)
        }
        XCTAssertTrue(AppCommands.answers(for: payload, mode: "idk", llm: false).allSatisfy { $0.idk == true })
    }

    private func fixture(_ name: String) throws -> Data {
        let dir = try XCTUnwrap(Bundle(for: Self.self).url(forResource: "fixtures", withExtension: nil))
        return try Data(contentsOf: dir.appendingPathComponent("\(name).json"))
    }
}
