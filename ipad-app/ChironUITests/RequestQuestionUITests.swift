import XCTest

/// A request the agent stopped on shows its question in the requests
/// card, and the answer typed there reaches the server and queues the
/// request again. The waiting request is written straight into the dev
/// server's queue, as the agent on the sprite would leave it.
final class RequestQuestionUITests: HarnessTestCase {
    private let id = "req-20260925-000000-q1test"

    /// The dev server's queue: sim-server.sh keeps its state per port.
    private var queue: URL {
        let port = URLComponents(string: Self.serverURL)?.port ?? 8084
        return URL(fileURLWithPath: "/tmp/chiron-sim-state-\(port)/state/requests")
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: queue.appendingPathComponent("\(id).json"))
        super.tearDown()
    }

    private func status() throws -> [String: Any] {
        let data = try Data(contentsOf: queue.appendingPathComponent("\(id).json"))
        return try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    func testTheQuestionIsAnsweredInTheCard() throws {
        try FileManager.default.createDirectory(at: queue, withIntermediateDirectories: true)
        let waiting = """
        {"id":"\(id)","text":"make the shelf nicer","created_at":"2026-09-25T00:00:00Z","updated_at":"2026-09-25T00:00:00Z",
         "status":"waiting","log":["asking: Covers, or denser rows?"],"last":"asking: Covers, or denser rows?",
         "question":"Covers, or denser rows?"}
        """
        try Data(waiting.utf8).write(to: queue.appendingPathComponent("\(id).json"))

        _ = app
        try post("requests")
        try waitUntil("the wrench to count the question") { (try self.state()["requests_waiting"] as? Int ?? 0) >= 1 }
        try post("requests/card", ["open": true])

        let field = app.textFields["Your answer"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the answer field in the card")
        // A selectable passage carries its words as its value, not its label.
        let asked = NSPredicate(format: "label CONTAINS %@ OR value CONTAINS %@", "Covers, or denser rows?", "Covers, or denser rows?")
        XCTAssertTrue(app.descendants(matching: .any).matching(asked).firstMatch.exists, "the question is shown")
        field.tap()
        field.typeText("Denser rows.")
        app.buttons["Answer"].firstMatch.tap()

        try waitUntil("the answer to reach the server") { (try self.status()["status"] as? String) == "queued" }
        let thread = try XCTUnwrap(try status()["thread"] as? [[String: Any]])
        XCTAssertEqual(thread.first?["answer"] as? String, "Denser rows.")
        XCTAssertEqual(thread.first?["question"] as? String, "Covers, or denser rows?")
        try post("requests/card", ["open": false])
    }
}
