import XCTest
@testable import Chiron

/// After "I don't know" a choice item still shows its answer, but not as
/// if the reader had chosen it: the option's explanation is written for
/// whoever tapped it ("Right; ..."), which reads as praise to someone who
/// passed.
final class PassRevealTests: XCTestCase {
    func testTheAnswerIsNamedAsPassedNotAsRight() {
        XCTAssertEqual(PassReveal.headline(correct: 1), "You passed on this one. The answer is B.")
        XCTAssertEqual(PassReveal.headline(correct: nil), "You passed on this one.")
    }

    func testAnAffirmationForTheChooserIsDropped() {
        XCTAssertEqual(PassReveal.explanation("Right; the client holds no secret, so it proves possession instead."),
                       "The client holds no secret, so it proves possession instead.")
        XCTAssertEqual(PassReveal.explanation("Right. The variance grows with the dimension."),
                       "The variance grows with the dimension.")
        XCTAssertEqual(PassReveal.explanation("Correct - the code is bound to the verifier."),
                       "The code is bound to the verifier.")
        XCTAssertEqual(PassReveal.explanation("Yes: PKCE binds the code to the client that started the flow."),
                       "PKCE binds the code to the client that started the flow.")
        XCTAssertEqual(PassReveal.explanation("Exactly, and that is why the secret never leaves the server."),
                       "And that is why the secret never leaves the server.")
    }

    func testAnExplanationWithoutAnAffirmationIsKept() {
        XCTAssertEqual(PassReveal.explanation("The variance grows with the dimension."),
                       "The variance grows with the dimension.")
        // "Rightly" is not "Right."
        XCTAssertEqual(PassReveal.explanation("Rightly or not, the server trusts the header."),
                       "Rightly or not, the server trusts the header.")
    }

    func testAnExplanationThatWasOnlyAnAffirmationIsEmpty() {
        XCTAssertEqual(PassReveal.explanation("Right."), "")
        XCTAssertEqual(PassReveal.explanation("  "), "")
    }
}
