import XCTest

/// The wrench opens the requests card, on the shelf and in a book: a tap,
/// not the harness, is what the reader has.
final class RequestButtonUITests: HarnessTestCase {
    func testTheWrenchOpensTheCardOnTheShelfAndInABook() throws {
        _ = app
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
