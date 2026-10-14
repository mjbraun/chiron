import XCTest
@testable import Chiron

/// A chapter the tutor wrote carries the calls that wrote it; the page is
/// handed the chapter without them; a mark keeps the brief of its answer's
/// call; and the card formats what it shows.
final class ProvenanceTests: XCTestCase {
    private let call = """
    {"role":"author","name":"rewrite","model":"claude-fable-5-1","system":"S","prompt":"Write it.","at":"2026-10-14T13:45:00Z","seconds":90.5}
    """

    func testAChapterDecodesItsProvenanceAndAnOlderOneHasNone() throws {
        let json = """
        {"unit":"u1","title":"T","minutes":9,"html":"<p>x</p><footer class=\\"colophon\\">Written by claude-fable-5-1</footer>",
         "beats":[],"pretest":[],"check":[],"next_action":"read",
         "provenance":[{"role":"planner","name":"directives","model":"claude-fable-5-1","prompt":"Plan.","at":"2026-10-14T13:44:00Z"},\(call)]}
        """
        let ch = try JSONDecoder().decode(ChapterPayload.self, from: Data(json.utf8))
        XCTAssertEqual(ch.provenance.count, 2)
        XCTAssertEqual(ch.provenance[1].prompt, "Write it.")
        XCTAssertEqual(ch.provenance.models, ["claude-fable-5-1"], "one model, named once")
        XCTAssertEqual(ch.provenance[0].purpose, "Planning the chapter")
        XCTAssertEqual(ch.provenance[1].purpose, "Writing the chapter")

        let old = """
        {"unit":"u1","title":"T","minutes":9,"html":"<p>x</p>","beats":[],"pretest":[],"check":[],"next_action":"read"}
        """
        let plain = try JSONDecoder().decode(ChapterPayload.self, from: Data(old.utf8))
        XCTAssertTrue(plain.provenance.isEmpty)
        XCTAssertNil(Provenance.byline(plain.provenance))

        // The chapter's own cache keeps the record through a round trip.
        let again = try JSONDecoder().decode(ChapterPayload.self, from: JSONEncoder().encode(ch))
        XCTAssertEqual(again.provenance, ch.provenance)
    }

    func testThePageIsHandedTheChapterWithoutThePrompts() throws {
        let json = """
        {"unit":"u1","title":"T","minutes":9,"html":"<p>x</p>","beats":[{"id":"b1"}],"pretest":[],"check":[],
         "next_action":"read","provenance":[\(call)]}
        """
        let ch = try JSONDecoder().decode(ChapterPayload.self, from: Data(json.utf8))
        let page = try JSONSerialization.jsonObject(with: JSONEncoder().encode(PagePayload(ch))) as? [String: Any]
        XCTAssertEqual(page?["html"] as? String, "<p>x</p>")
        XCTAssertEqual((page?["beats"] as? [[String: Any]])?.count, 1)
        XCTAssertNil(page?["provenance"], "the prompts are a chapter's size; the page shows the colophon it has")
    }

    func testACallParsesItsTimeAndSaysWhatItWasFor() throws {
        let c = try JSONDecoder().decode(GeneratedCall.self, from: Data(call.utf8))
        XCTAssertEqual(c.date, Date(timeIntervalSince1970: 1_791_985_500))
        XCTAssertTrue(c.hasPrompts)
        XCTAssertEqual(GeneratedCall.parse("2026-10-14T13:45:00.5Z"), Date(timeIntervalSince1970: 1_791_985_500.5))
        XCTAssertNil(GeneratedCall.parse("yesterday"))
        XCTAssertEqual(GeneratedCall(role: "author", name: "primer", at: "x").purpose, "Writing the primer")
        XCTAssertEqual(GeneratedCall(role: "author", name: "capture-summary", at: "x").purpose, "Answering the capture")
        XCTAssertEqual(GeneratedCall(role: "grader", at: "x").purpose, "Grader")
        XCTAssertEqual(ProvenanceCard.took(4.2), "4 s")
        XCTAssertEqual(ProvenanceCard.took(150), "2.5 min")
        XCTAssertEqual(Provenance.byline([c]), "Written by claude-fable-5-1")
        let two = Provenance.byline([c, GeneratedCall(role: "planner", model: "gpt-oss-20b", at: "x")]) ?? ""
        XCTAssertTrue(two.hasPrefix("Written by ") && two.contains("claude-fable-5-1") && two.contains("gpt-oss-20b"), two)
        XCTAssertEqual(Provenance.when(GeneratedCall(role: "author", at: "yesterday")), "yesterday", "a stamp that does not parse shows as it is")
    }

    func testAMarkKeepsTheBriefOfItsAnswer() throws {
        let before = """
        {"id":"m1","kind":"question","start":0,"end":5,"text":"words","question":"why?","answer":"because",
         "thread":[{"question":"and?","answer":"so"}]}
        """
        let old = try JSONDecoder().decode(Mark.self, from: Data(before.utf8))
        XCTAssertNil(old.generated, "a mark from before carries no record")
        XCTAssertNil(old.thread?.first?.generated)

        var m = old
        m.generated = GeneratedCall(role: "planner", name: "answer", model: "claude-fable-5-1", at: "2026-10-14T13:45:00Z", ref: "ask-3")
        m.thread?[0].generated = GeneratedCall(role: "planner", name: "answer", model: "claude-fable-5-1", at: "2026-10-14T13:46:00Z", ref: "ask-4")
        let back = try JSONDecoder().decode(Mark.self, from: JSONEncoder().encode(m))
        XCTAssertEqual(back, m)
        XCTAssertEqual(back.generated?.ref, "ask-3")
        XCTAssertFalse(back.generated?.hasPrompts ?? true)
        XCTAssertEqual(back.history.map { $0.generated?.ref }, ["ask-3", "ask-4"])
    }

    func testAnAnswerAndACaptureDecodeTheirRecord() throws {
        let ask = try JSONDecoder().decode(AskResponse.self, from: Data("""
        {"unit":"u1","answer_md":"Because.","n":3,"generated":{"role":"planner","name":"answer","model":"m","at":"2026-10-14T13:45:00Z","ref":"ask-3"}}
        """.utf8))
        XCTAssertEqual(ask.generated?.ref, "ask-3")
        let bare = try JSONDecoder().decode(AskResponse.self, from: Data(#"{"unit":"u1","answer_md":"Because."}"#.utf8))
        XCTAssertNil(bare.generated, "a server before records sends none")
        let cap = try JSONDecoder().decode(CaptureResponse.self, from: Data("""
        {"scale":"summary","answer_md":"In short.","generated":\(call)}
        """.utf8))
        XCTAssertEqual(cap.generated?.model, "claude-fable-5-1")
        XCTAssertTrue(cap.generated?.hasPrompts ?? false, "the card gets the whole record")
    }
}
