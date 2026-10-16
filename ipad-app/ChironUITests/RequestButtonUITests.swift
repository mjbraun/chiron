import XCTest

/// The wrench opens the requests card, on the shelf and in a book: a tap,
/// not the harness, is what the reader has.
final class RequestButtonUITests: HarnessTestCase {
    private let id = "req-20260925-000000-badge1"

    /// The dev server's queue: sim-server.sh keeps its state per port.
    private var queue: URL {
        let port = URLComponents(string: Self.serverURL)?.port ?? 8084
        return URL(fileURLWithPath: "/tmp/chiron-sim-state-\(port)/state/requests")
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: queue.appendingPathComponent("\(id).json"))
        super.tearDown()
    }

    func testTheWrenchOpensTheCardOnTheShelfAndInABook() throws {
        // A request waiting on the reader turns the wrench orange; the
        // button must still be a button, and take the tap.
        try FileManager.default.createDirectory(at: queue, withIntermediateDirectories: true)
        let waiting = """
        {"id":"\(id)","text":"make the shelf nicer","created_at":"2026-09-25T00:00:00Z","updated_at":"2026-09-25T00:00:00Z",
         "status":"waiting","log":["asking: Covers, or denser rows?"],"last":"asking: Covers, or denser rows?",
         "question":"Covers, or denser rows?"}
        """
        try Data(waiting.utf8).write(to: queue.appendingPathComponent("\(id).json"))
        _ = app
        try post("requests")
        try waitUntil("the wrench to turn orange") { (try self.state()["requests_waiting"] as? Int ?? 0) >= 1 }
        let wrench = app.buttons["Request a change"].firstMatch
        XCTAssertTrue(wrench.waitForExistence(timeout: 10), "the wrench on the shelf")
        wrench.tap()
        try waitUntil("the card to open from the shelf") { (try self.state()["requests_card"] as? Bool) == true }
        let done = app.buttons["Done"].firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 10), "the card is on screen")
        done.tap()
        try waitUntil("the card to close") { (try self.state()["requests_card"] as? Bool) == false }

        try openBook()
        let again = app.buttons["Request a change"].firstMatch
        XCTAssertTrue(again.waitForExistence(timeout: 10), "the wrench in the book")
        again.tap()
        try waitUntil("the card to open from the book") { (try self.state()["requests_card"] as? Bool) == true }
        XCTAssertTrue(done.waitForExistence(timeout: 10), "the card is on screen again")
        try post("requests/card", ["open": false])
    }
}
