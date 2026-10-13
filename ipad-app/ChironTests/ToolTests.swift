import XCTest
import UIKit
@testable import Chiron

/// The tools as the Pencil's gestures drive them.
@MainActor
final class ToolTests: XCTestCase {
    private func session() -> BookSession {
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        return BookSession(subjectID: "ai", title: "How AI Works", service: FakeService(), storage: storage)
    }

    func testSqueezeCyclesTheTools() {
        let s = session()
        XCTAssertEqual(s.tool, .none)
        s.cycleTool(); XCTAssertEqual(s.tool, .pen)
        s.cycleTool(); XCTAssertEqual(s.tool, .highlighter)
        s.cycleTool(); XCTAssertEqual(s.tool, .ask)
        s.cycleTool(); XCTAssertEqual(s.tool, .capture, "a book has no note tool")
        s.cycleTool(); XCTAssertEqual(s.tool, .eraser)
        s.cycleTool(); XCTAssertEqual(s.tool, .pen, "round again, never to nothing")
        s.kind = "primer"
        s.tool = .ask
        s.cycleTool(); XCTAssertEqual(s.tool, .note)
        s.cycleTool(); XCTAssertEqual(s.tool, .capture)
    }

    func testDoubleTapFlipsTheEraser() {
        let s = session()
        s.tool = .pen
        s.flipEraser(); XCTAssertEqual(s.tool, .eraser)
        s.flipEraser(); XCTAssertEqual(s.tool, .pen)
        s.tool = .highlighter
        s.flipEraser(); XCTAssertEqual(s.tool, .eraser)
        s.flipEraser(); XCTAssertEqual(s.tool, .highlighter, "back to the tool that was up, not always the pen")
    }

    /// "Switch to the last used tool" in the Pencil settings.
    func testSwitchPreviousTradesTheLastTwoTools() {
        let s = session()
        s.switchPrevious(); XCTAssertEqual(s.tool, .pen, "nothing was up before: the pen is the first tool")
        s.tool = .highlighter
        s.switchPrevious(); XCTAssertEqual(s.tool, .pen)
        s.switchPrevious(); XCTAssertEqual(s.tool, .highlighter)
        s.tool = .none
        s.switchPrevious(); XCTAssertEqual(s.tool, .highlighter, "putting a tool down and switching back picks it up again")
    }

    /// A selecting tool's drag is measured from the top of the page's
    /// content, below the title bar, and a drag that reaches under the bar
    /// or past the bottom lands on the page's edge rather than off it:
    /// the page answers nothing for a point it does not show, and a
    /// selection up to the first line under the bar was being dropped.
    func testADragIsKeptOnThePageTheBarsLeaveUncovered() {
        let inset = UIEdgeInsets(top: 74, left: 0, bottom: 20, right: 0)
        let bounds = CGRect(x: 0, y: 0, width: 1024, height: 1366)
        let middle = ReaderView.Coordinator.pagePoint(CGPoint(x: 300, y: 500), inset: inset, in: bounds)
        XCTAssertEqual(middle, CGPoint(x: 300, y: 426), "a point on the page is measured from below the bar")
        let underBar = ReaderView.Coordinator.pagePoint(CGPoint(x: 300, y: 40), inset: inset, in: bounds)
        XCTAssertEqual(underBar, CGPoint(x: 300, y: 0), "a point under the title bar lands on the first line below it")
        let pastBottom = ReaderView.Coordinator.pagePoint(CGPoint(x: 300, y: 1360), inset: inset, in: bounds)
        XCTAssertEqual(pastBottom, CGPoint(x: 300, y: 1366 - 74 - 20 - 1), "a point past the bottom lands on the last line shown")
        let offSide = ReaderView.Coordinator.pagePoint(CGPoint(x: -5, y: 500), inset: inset, in: bounds)
        XCTAssertEqual(offSide.x, 0, "a point off the side lands on the edge")
    }

    /// Text the reader has selected with the page's own handles is the
    /// run a selecting tool works on when it is picked: the words are
    /// chosen already, so the question tool opens on them with no second
    /// drag. The ink tools have no use for a selection, and a blank one is
    /// not a selection.
    func testASelectingToolPickedWithASelectionUpTakesIt() {
        XCTAssertTrue(ReaderView.Coordinator.takesSelection(.ask, selection: "the implicit flow"))
        XCTAssertTrue(ReaderView.Coordinator.takesSelection(.highlighter, selection: "the implicit flow"))
        XCTAssertTrue(ReaderView.Coordinator.takesSelection(.note, selection: "the implicit flow"))
        XCTAssertTrue(ReaderView.Coordinator.takesSelection(.capture, selection: "the implicit flow"))
        XCTAssertFalse(ReaderView.Coordinator.takesSelection(.pen, selection: "the implicit flow"), "the pen draws; a selection is not its business")
        XCTAssertFalse(ReaderView.Coordinator.takesSelection(.eraser, selection: "the implicit flow"))
        XCTAssertFalse(ReaderView.Coordinator.takesSelection(.none, selection: "the implicit flow"))
        XCTAssertFalse(ReaderView.Coordinator.takesSelection(.ask, selection: " \n"), "a blank selection is nothing to ask about")
    }

    /// Ink needs a Pencil: a phone's palette has no pen and no eraser; the
    /// iPad's keeps the pen first and the eraser last.
    func testAPhoneHasNoInkTools() {
        XCTAssertEqual(Palette.tools(primer: false, inkable: false).map(\.0), [.highlighter, .ask, .capture])
        XCTAssertEqual(Palette.tools(primer: true, inkable: false).map(\.0), [.highlighter, .ask, .note, .capture])
        XCTAssertEqual(Palette.tools(primer: false, inkable: true).map(\.0), [.pen, .highlighter, .ask, .capture, .eraser])
    }
}
