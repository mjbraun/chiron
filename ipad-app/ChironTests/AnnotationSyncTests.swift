import XCTest
@testable import Chiron

/// A unit's marks, ink and position follow the reader between devices
/// through the server; two copies that both changed are put to the reader.
@MainActor
final class AnnotationSyncTests: XCTestCase {
    private func session(_ fake: FakeService) -> BookSession {
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let s = BookSession(subjectID: "ai", title: "AI", service: fake, storage: storage)
        s.annotationPushDelay = 0.05
        return s
    }

    private func openChapter(_ s: BookSession) {
        let json = #"{"unit":"u1","title":"T","minutes":1,"html":"<p>predict the next token</p>","beats":[],"pretest":[],"check":[],"next_action":"read"}"#
        s.setChapterForTesting(try! JSONDecoder().decode(ChapterPayload.self, from: Data(json.utf8)))
    }

    private func settle() async throws { try await Task.sleep(nanoseconds: 300_000_000) }

    private func mark(_ id: String) -> Mark { Mark(id: id, kind: .highlight, start: 0, end: 7, text: "predict") }

    func testOpeningAUnitTakesTheServersCopyWhenNothingChangedHere() async throws {
        let fake = FakeService()
        fake.onAnnotations = { [unowned self] _, _ in
            Annotations(version: 3, updatedAt: nil, device: "iPhone", marks: [self.mark("from-phone")], inkB64: Data([9]).base64EncodedString(), position: 0.5)
        }
        let s = session(fake)
        openChapter(s)
        try await settle()
        XCTAssertEqual(s.marks.map(\.id), ["from-phone"])
        XCTAssertEqual(s.inkData, Data([9]))
        XCTAssertEqual(s.position(for: "u1"), 0.5)
        XCTAssertNil(s.conflict)
        XCTAssertTrue(fake.annotationPuts.isEmpty, "taking the server's copy is not a change to push")
    }

    /// The page loads with the position this device remembers; the server's
    /// copy lands after that. The reader is told where the other device
    /// left off, so the page moves there instead of staying at the top.
    func testAPositionTakenFromTheServerIsAnnouncedToTheReader() async throws {
        let fake = FakeService()
        fake.onAnnotations = { _, _ in
            Annotations(version: 3, updatedAt: nil, device: "iPhone", marks: [], inkB64: nil, position: 0.65)
        }
        let s = session(fake)
        XCTAssertNil(s.restoredPosition)
        openChapter(s)
        try await settle()
        XCTAssertEqual(s.restoredPosition?.unit, "u1")
        XCTAssertEqual(s.restoredPosition?.position, 0.65)
        XCTAssertEqual(s.position(for: "u1"), 0.65)
    }

    func testAMarkHereIsPushedOnTopOfTheVersionLastAgreed() async throws {
        let fake = FakeService()
        fake.onAnnotations = { [unowned self] _, _ in Annotations(version: 3, updatedAt: nil, device: nil, marks: [self.mark("a")], inkB64: nil, position: 0) }
        let s = session(fake)
        openChapter(s)
        try await settle()
        s.addMark(kind: .highlight, start: 8, end: 11, text: "the")
        try await settle()
        let put = try XCTUnwrap(fake.annotationPuts.last)
        XCTAssertEqual(put.base, 3)
        XCTAssertEqual(put.annotations.marks.count, 2)
        XCTAssertEqual(put.unit, "u1")
        // The stored version is the new base.
        s.recordPosition(unit: "u1", position: 0.9)
        try await settle()
        XCTAssertEqual(fake.annotationPuts.last?.base, 4)
        XCTAssertEqual(fake.annotationPuts.last?.annotations.position, 0.9)
    }

    func testBothChangedIsAConflictWithThreeAnswers() async throws {
        let fake = FakeService()
        let theirs = Annotations(version: 5, updatedAt: nil, device: "iPhone", marks: [mark("phone")], inkB64: nil, position: 0.2)
        fake.onPutAnnotations = { _, _, a, base in
            if base < 5 { return .conflict(server: theirs) }
            var stored = a; stored.version = base + 1; return .stored(stored)
        }
        let s = session(fake)
        openChapter(s)
        try await settle()
        s.addMark(kind: .highlight, start: 0, end: 7, text: "predict")
        try await settle()
        let c = try XCTUnwrap(s.conflict)
        XCTAssertEqual(c.mine.marks.count, 1)
        XCTAssertEqual(c.theirs.version, 5)

        // Keep the server's: the local copy becomes theirs, nothing is pushed.
        let puts = fake.annotationPuts.count
        await s.resolveConflict(.theirs)
        XCTAssertNil(s.conflict)
        XCTAssertEqual(s.marks.map(\.id), ["phone"])
        XCTAssertEqual(s.position(for: "u1"), 0.2)
        XCTAssertEqual(fake.annotationPuts.count, puts)

        // A new local change conflicts again; keep mine: pushed on top of theirs.
        fake.onPutAnnotations = { _, _, a, base in
            if base < 5 { return .conflict(server: theirs) }
            var stored = a; stored.version = base + 1; return .stored(stored)
        }
        s.addMark(kind: .highlight, start: 8, end: 11, text: "the")
        try await settle()
        XCTAssertNil(s.conflict, "the base is now 5, so the push goes through")
        XCTAssertEqual(fake.annotationPuts.last?.base, 5)
    }

    func testTheAgentReconcilesBothCopies() async throws {
        let fake = FakeService()
        let theirs = Annotations(version: 5, updatedAt: nil, device: "iPhone", marks: [mark("phone")], inkB64: nil, position: 0.6)
        var conflictOnce = true
        fake.onPutAnnotations = { _, _, a, base in
            if conflictOnce { conflictOnce = false; return .conflict(server: theirs) }
            var stored = a; stored.version = base + 1; return .stored(stored)
        }
        let s = session(fake)
        openChapter(s)
        try await settle()
        s.addMark(kind: .highlight, start: 0, end: 7, text: "predict")
        try await settle()
        XCTAssertNotNil(s.conflict)
        await s.resolveConflict(.agent)
        XCTAssertNil(s.conflict)
        XCTAssertEqual(fake.reconciles.count, 1)
        XCTAssertEqual(Set(s.marks.map(\.id)).count, 2, "every mark from both sides")
        XCTAssertEqual(s.position(for: "u1"), 0.6, "the further position")
        let put = try XCTUnwrap(fake.annotationPuts.last)
        XCTAssertEqual(put.base, 5, "the merge is put on top of the server's version")
        XCTAssertEqual(put.annotations.marks.count, 2)
    }

    /// A page that keeps reporting its position must not keep the push
    /// from ever happening.
    func testASteadyStreamOfChangesStillPushes() async throws {
        let fake = FakeService()
        let s = session(fake)
        openChapter(s)
        try await settle()
        for i in 0..<12 {
            s.recordPosition(unit: "u1", position: Double(i) / 100)
            try await Task.sleep(nanoseconds: 20_000_000)
        }
        try await settle()
        XCTAssertFalse(fake.annotationPuts.isEmpty, "a push happened within the stream")
        XCTAssertEqual(fake.annotationPuts.last?.annotations.position, 0.11, "and a later one sent the latest position")
    }

    /// A server that answers slowly (the sprite waking, a poor link) must
    /// not see two puts on the same base from one device: the second would
    /// be a conflict of the device with itself, and the card would show
    /// the reader two copies of their own. A change made while a put is in
    /// flight goes up after it, on the version it stored.
    func testAChangeMadeWhileAPushIsInFlightGoesUpAfterIt() async throws {
        let fake = FakeService()
        var current = 0
        fake.putDelay = 0.3
        fake.onPutAnnotations = { _, _, a, base in
            if base != current {
                return .conflict(server: Annotations(version: current, updatedAt: nil, device: "iPad", marks: a.marks, inkB64: nil, position: 0))
            }
            current += 1
            var stored = a; stored.version = current; return .stored(stored)
        }
        let s = session(fake)
        openChapter(s)
        try await settle()
        s.addMark(kind: .highlight, start: 0, end: 7, text: "predict")
        try await Task.sleep(nanoseconds: 150_000_000)
        XCTAssertEqual(fake.annotationPuts.count, 1, "the mark's put is in flight")
        s.recordPosition(unit: "u1", position: 0.4)
        try await Task.sleep(nanoseconds: 900_000_000)
        XCTAssertNil(s.conflict, "a device does not conflict with itself")
        XCTAssertEqual(fake.annotationPuts.count, 2)
        XCTAssertEqual(fake.annotationPuts.last?.base, 1, "the second put sits on the version the first stored")
        XCTAssertEqual(fake.annotationPuts.last?.annotations.position, 0.4)
    }

    /// Keeping this device's copy puts it on top of the server's. A scroll
    /// while that put is in flight must not raise the card again with the
    /// copy just kept as "the server's".
    func testKeepingThisDevicesCopyHoldsWhileItGoesUp() async throws {
        let fake = FakeService()
        var current = 5
        let phone = Annotations(version: 5, updatedAt: nil, device: "iPhone", marks: [mark("phone")], inkB64: nil, position: 0.2)
        fake.onPutAnnotations = { _, _, a, base in
            if base != current { return .conflict(server: phone) }
            current += 1
            var stored = a; stored.version = current; return .stored(stored)
        }
        let s = session(fake)
        openChapter(s)
        try await settle()
        s.addMark(kind: .highlight, start: 0, end: 7, text: "predict")
        try await settle()
        XCTAssertNotNil(s.conflict)

        fake.putDelay = 0.3
        let resolving = Task { await s.resolveConflict(.mine) }
        try await Task.sleep(nanoseconds: 100_000_000)
        s.recordPosition(unit: "u1", position: 0.9)
        await resolving.value
        try await Task.sleep(nanoseconds: 900_000_000)
        XCTAssertNil(s.conflict, "the copy kept is not a conflict with itself")
        XCTAssertEqual(s.marks.count, 1)
        XCTAssertEqual(fake.annotationPuts.last?.base, 6, "the scroll went up on the version the kept copy got")
        XCTAssertEqual(fake.annotationPuts.last?.annotations.position, 0.9)
        XCTAssertEqual(current, 7)
    }

    /// Keeping this device's copy while the server is away: the choice is
    /// remembered on the device, so the next opening does not ask again,
    /// and the copy goes up then.
    func testKeepingThisDevicesCopyIsRememberedWhenTheServerIsAway() async throws {
        let fake = FakeService()
        let phone = Annotations(version: 5, updatedAt: nil, device: "iPhone", marks: [mark("phone")], inkB64: nil, position: 0.2)
        fake.onAnnotations = { _, _ in phone }
        let storage = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let s = BookSession(subjectID: "ai", title: "AI", service: fake, storage: storage)
        s.annotationPushDelay = 0.05
        openChapter(s)
        try await settle()
        XCTAssertEqual(s.marks.map(\.id), ["phone"], "nothing here yet: the server's copy is taken")
        s.addMark(kind: .highlight, start: 0, end: 7, text: "predict")
        fake.onAnnotations = { _, _ in var v = phone; v.version = 6; return v }
        fake.onPutAnnotations = { _, _, _, base in
            if base < 6 { var v = phone; v.version = 6; return .conflict(server: v) }
            throw URLError(.cannotConnectToHost)
        }
        try await settle()
        XCTAssertNotNil(s.conflict)
        await s.resolveConflict(.mine)
        XCTAssertNil(s.conflict)
        XCTAssertEqual(s.marks.count, 2)

        // The book is opened again, the server still at version 6.
        fake.onPutAnnotations = { _, _, a, base in var stored = a; stored.version = base + 1; return .stored(stored) }
        let again = BookSession(subjectID: "ai", title: "AI", service: fake, storage: storage)
        again.annotationPushDelay = 0.05
        again.restoreForTesting()
        openChapter(again)
        try await settle()
        XCTAssertNil(again.conflict, "the choice made was kept")
        XCTAssertEqual(again.marks.count, 2, "and so was the copy")
        XCTAssertEqual(fake.annotationPuts.last?.base, 6, "which goes up on opening, on top of the server's")
        XCTAssertEqual(fake.annotationPuts.last?.annotations.marks.count, 2)
    }

    func testTwoInksBecomeOne() {
        XCTAssertEqual(BookSession.overlay(nil, Data([1])), Data([1]))
        XCTAssertEqual(BookSession.overlay(Data([1]), Data([2])), Data([1]), "undecodable ink keeps the base")
    }
}
