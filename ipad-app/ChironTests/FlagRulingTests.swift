import XCTest
@testable import Chiron

/// The grader rules on a flag as it grades the check; the results carry
/// the ruling under the reader's flag.
final class FlagRulingTests: XCTestCase {
    private func entry(_ extra: String) throws -> ResultsEntry {
        let json = #"{"n":1,"verdict":"pass","kind":"mcq","prompt":"When does the key stop working?","flag":"it was rotated at 16:00""# + extra + "}"
        return try JSONDecoder().decode(ResultsEntry.self, from: Data(json.utf8))
    }

    func testAnUpheldRulingIsRead() throws {
        let e = try entry(#","flag_ruling":"Right: rotated at 16:00.","flag_upheld":true"#)
        XCTAssertEqual(e.flagRuling, "Right: rotated at 16:00.")
        XCTAssertTrue(e.flagUpheld)
        XCTAssertEqual(e.rulingLabel, "UPHELD")
    }

    func testARejectedRulingIsRead() throws {
        let e = try entry(#","flag_ruling":"It is 18:00, not 16:00.""#)
        XCTAssertFalse(e.flagUpheld, "the server leaves out a false upheld")
        XCTAssertEqual(e.rulingLabel, "NOT UPHELD")
    }

    func testAFlagNotYetRuledShowsNoRuling() throws {
        let e = try entry("")
        XCTAssertNil(e.flagRuling)
        XCTAssertNil(e.rulingLabel)
    }
}
