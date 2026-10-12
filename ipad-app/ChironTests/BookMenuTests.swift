import UIKit
import XCTest
@testable import Chiron

/// The page's own selection, made with a finger, offers "Send to Chiron"
/// in its menu, the way a PDF's does: the passage goes to the capture
/// card and the selection, handles and all, goes with it.
@MainActor
final class BookMenuTests: XCTestCase {
    func testASelectionOffersSendToChiron() {
        XCTAssertNil(BookWebView.sendItem(for: "", send: {}), "nothing selected, nothing offered")
        XCTAssertNil(BookWebView.sendItem(for: " \n", send: {}), "whitespace is not a passage")
        let item = BookWebView.sendItem(for: "a passage", send: {})
        XCTAssertEqual((item?.children.first as? UIAction)?.title, "Send to Chiron")
        XCTAssertTrue(item?.options.contains(.displayInline) ?? false, "among the menu's own items, not under a submenu")
    }

    func testSendingHandsThePassageOverAndLetsTheSelectionGo() {
        let web = BookWebView()
        var sent: [String] = []
        web.onCapture = { sent.append($0) }
        web.selection = " a passage "
        web.send()
        XCTAssertEqual(sent, ["a passage"], "the passage, trimmed")
        XCTAssertEqual(web.selection, "", "the selection goes with the passage")
        web.send()
        XCTAssertEqual(sent, ["a passage"], "nothing selected, nothing sent")
    }
}
