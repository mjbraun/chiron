import XCTest
@testable import Chiron

/// Every file in fixtures/ is a real server response, captured against a
/// fresh two-subject server by scripts/capture-fixtures.py. The wire models
/// must decode all of them: a single missing key is a total decode failure
/// for the whole exchange, and that is how the app went blind to the
/// calibration series for two months.
final class FixtureDecodingTests: XCTestCase {
    private var fixtures: URL {
        get throws {
            try XCTUnwrap(Bundle(for: Self.self).url(forResource: "fixtures", withExtension: nil))
        }
    }

    private func decode<T: Decodable>(_ type: T.Type, _ name: String) throws -> T {
        let url = try fixtures.appendingPathComponent("\(name).json")
        return try JSONDecoder().decode(type, from: Data(contentsOf: url))
    }

    func testEveryExchangeFixtureDecodes() throws {
        let files = try FileManager.default.contentsOfDirectory(at: try fixtures, includingPropertiesForKeys: nil)
            .filter { $0.lastPathComponent.hasPrefix("exchange-") }
        XCTAssertGreaterThanOrEqual(files.count, 4, "expected start, screener, series and fail exchanges")
        for f in files {
            XCTAssertNoThrow(try JSONDecoder().decode(ExchangeResponse.self, from: Data(contentsOf: f)), f.lastPathComponent)
        }
    }

    func testStartDeliversTheScreenerAlone() throws {
        let r = try decode(ExchangeResponse.self, "exchange-start")
        let ch = try XCTUnwrap(r.chapter)
        XCTAssertEqual(ch.calibration, true)
        XCTAssertEqual(ch.check.count, 1)
        let screener = ch.check[0]
        XCTAssertEqual(screener.check, "screener")
        XCTAssertEqual(screener.options?.count, 5, "five self-rating levels")
        XCTAssertNil(r.gate, "placement is never gated")
    }

    func testScreenerDeliversTheSeriesWithRevealsIntact() throws {
        let r = try decode(ExchangeResponse.self, "exchange-screener")
        let ch = try XCTUnwrap(r.chapter)
        XCTAssertEqual(ch.calibration, true)
        XCTAssertGreaterThan(ch.check.count, 1)
        XCTAssertNil(r.gate)
        for item in ch.check {
            XCTAssertNotEqual(item.check, "screener")
            if item.kind == "mcq" {
                let reveal = try XCTUnwrap(item.reveal?.options, item.id)
                XCTAssertEqual(reveal.filter { $0.correct }.count, 1, "\(item.id): exactly one correct option")
                XCTAssertEqual(reveal.count, item.options?.count, "\(item.id): a reveal per option")
            } else {
                XCTAssertNotNil(item.reveal?.answer, "\(item.id): constructed items carry a reference answer")
            }
        }
    }

    func testGradedSeriesCarriesACalibrationGateAndAuthorsAsync() throws {
        let r = try decode(ExchangeResponse.self, "exchange-series")
        let gate = try XCTUnwrap(r.gate)
        XCTAssertEqual(gate.calibration, true)
        XCTAssertTrue(gate.passed, "calibration is measurement, never a verdict")
        XCTAssertNotNil(gate.score)
        XCTAssertFalse(r.results.isEmpty)
        XCTAssertEqual(r.state.spine.first?.unit, "u0")
        XCTAssertNil(r.chapter, "async: the chapter comes from /chapter later")
        XCTAssertEqual(r.authoring, "u1")

        let doc = try XCTUnwrap(r.resultsDoc)
        XCTAssertTrue(doc.isCalibration)
        XCTAssertTrue(doc.headline.hasPrefix("Calibration complete"))
        XCTAssertNotNil(doc.tally)
        XCTAssertEqual(doc.entries.count, r.results.count)
        XCTAssertEqual(doc.entries.map(\.n), Array(1...doc.entries.count), "entries carry the chapter's own numbering")
        for e in doc.entries where e.kind == "constructed" && !e.isIDK {
            XCTAssertNotNil(e.readAs, "\(e.n): READ AS is mandatory on constructed answers")
        }
    }

    func testFailedGateCarriesTheChoicesAndABreak() throws {
        let r = try decode(ExchangeResponse.self, "exchange-fail")
        let gate = try XCTUnwrap(r.gate)
        XCTAssertFalse(gate.passed)
        XCTAssertNil(r.chapter)
        XCTAssertNil(r.authoring, "nothing is written until the reader chooses")
        XCTAssertTrue(try XCTUnwrap(r.resultsDoc).dek.contains("misses"), "the results offer the misses")
        let doc = try XCTUnwrap(r.resultsDoc)
        XCTAssertTrue(doc.headline.hasPrefix("Below the gate"))
        XCTAssertFalse(doc.isCalibration)
        XCTAssertNil(doc.tally)
        XCTAssertFalse(doc.entries.filter { !$0.passed && $0.why != nil }.isEmpty, "misses explain WHY")
        let brk = try XCTUnwrap(r.breakSuggestion)
        XCTAssertEqual(brk.kind, "short")
        XCTAssertGreaterThan(brk.minutes, 0)
    }

    func testChapterStatusDecodes() throws {
        let st = try decode(ChapterStatus.self, "chapter")
        XCTAssertFalse(st.authoring)
        XCTAssertEqual(st.authoringError ?? "", "")
        let ch = try XCTUnwrap(st.chapter)
        XCTAssertEqual(ch.unit, "u1")
        XCTAssertFalse(ch.html.isEmpty)
        XCTAssertFalse(ch.check.isEmpty)
        XCTAssertNotEqual(ch.isCalibration, true)
    }

    func testSubjectsCarryTheActiveBook() throws {
        let r = try decode(SubjectsResponse.self, "subjects")
        XCTAssertEqual(r.subjects.map(\.id), ["ai", "data"])
        XCTAssertEqual(r.active, "data", "the last book exchanged with is the open one")
    }

    func testStateDecodes() throws {
        let st = try decode(BookState.self, "state")
        XCTAssertFalse(st.spine.isEmpty)
        XCTAssertEqual(st.spine[0].unit, "u0")
    }
}

final class ChapterDecodingTests: XCTestCase {
    func testNullListsMeanEmpty() throws {
        let json = """
        {"unit":"u1","title":"T","minutes":25,"html":"<p>x</p>","beats":null,"pretest":null,"check":null,"next_action":null}
        """
        let ch = try JSONDecoder().decode(ChapterPayload.self, from: Data(json.utf8))
        XCTAssertEqual(ch.unit, "u1")
        XCTAssertTrue(ch.pretest.isEmpty)
        XCTAssertTrue(ch.check.isEmpty)
        XCTAssertTrue(ch.beats.isEmpty)
        XCTAssertFalse(ch.isCalibration)
        XCTAssertNil(ch.screener)
    }
}

final class MathTextTests: XCTestCase {
    func testSoftLineBreaksReflowAndParagraphsStay() {
        XCTAssertEqual(MathText.reflow("Three of them are\n0.1, 0.2 and\n0.3. What is the fourth?"),
                       "Three of them are 0.1, 0.2 and 0.3. What is the fourth?")
        XCTAssertEqual(MathText.reflow("First line\nstill first.\n\nSecond paragraph.\n"),
                       "First line still first.\n\nSecond paragraph.")
    }
}
