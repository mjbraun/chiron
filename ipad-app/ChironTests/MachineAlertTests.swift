import XCTest
@testable import Chiron

/// A machine behind the book (Truman gone onto battery) posts an alert to
/// the server; the app shows each new one once, as a notice, whenever it
/// looks.
@MainActor
final class MachineAlertTests: XCTestCase {
    private func library(_ fake: FakeService, _ defaults: UserDefaults) -> Library {
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let l = Library(storage: storage, service: fake, defaults: defaults)
        l.notices = FakeNotices()
        return l
    }

    private func alert(_ text: String, _ at: String) -> MachineAlert {
        MachineAlert(source: "truman", text: text, at: at)
    }

    func testEachNewAlertIsANoticeOnce() async throws {
        let fake = FakeService()
        let defaults = UserDefaults(suiteName: "alerts-\(UUID().uuidString)")!
        fake.onAlerts = { [unowned self] _ in [self.alert("an old one", "2026-09-27T10:00:00Z")] }
        let library = library(fake, defaults)
        let notices = try XCTUnwrap(library.notices as? FakeNotices)

        await library.checkAlerts()
        XCTAssertTrue(notices.sent.isEmpty, "what was there before this device first looked is not news")

        fake.onAlerts = { [unowned self] since in
            XCTAssertEqual(since, "2026-09-27T10:00:00Z", "asks only for what came after")
            return [self.alert("Truman is on battery", "2026-09-27T11:00:00Z")]
        }
        await library.checkAlerts()
        XCTAssertEqual(notices.sent.map(\.title), ["Truman is on battery"])

        fake.onAlerts = { _ in [] }
        await library.checkAlerts()
        XCTAssertEqual(notices.sent.count, 1, "said once")

        // The place is kept on the device, across launches.
        var asked: String?
        fake.onAlerts = { since in asked = since; return [] }
        await self.library(fake, defaults).checkAlerts()
        XCTAssertEqual(asked, "2026-09-27T11:00:00Z")
    }

    func testAServerWithoutAlertsIsQuiet() async throws {
        let fake = FakeService()
        fake.onAlerts = { _ in throw ServiceError.status(404) }
        let library = library(fake, UserDefaults(suiteName: "alerts-\(UUID().uuidString)")!)
        await library.checkAlerts()
        XCTAssertTrue(try XCTUnwrap(library.notices as? FakeNotices).sent.isEmpty)
    }
}
