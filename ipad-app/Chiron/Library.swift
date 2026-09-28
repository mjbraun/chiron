import BackgroundTasks
import Foundation
import PDFKit
import PencilKit

/// The bookshelf: every subject the server offers, which one is open, and
/// the connection they share. One `BookSession` per subject is kept alive
/// once opened, so switching books later costs nothing.
@MainActor
final class Library: ObservableObject {
    /// One shell for the app, kept across books; the sheet shows it.
    let shell = ShellSession()
    /// An imported book's pictures, fetched once and kept for reading
    /// where there is no server.
    private(set) lazy var bookAssets = BookAssets(service: service, storage: storage)
    @Published var shellShown = false
    @Published var requestsShown = false
    /// The "set up another device" code, on screen.
    @Published var deviceSetupShown = false
    @Published var settingsShown = false
    /// The shelves of the library, and the one open (a path of one id).
    @Published var shelves: [ShelfInfo] = []
    /// What the library's list shows: everything, one kind, or one shelf.
    /// Nil only on a narrow screen, where the sidebar stands alone.
    @Published var scope: LibraryScope? = .all
    /// How the list is ordered, in every scope; the reader's choice, kept
    /// on the device.
    @Published var order: LibrarySort {
        didSet { defaults.set(order.rawValue, forKey: "shelfOrder") }
    }
    private let defaults: UserDefaults
    /// Whether the book takes the breaks the server suggests. The reader's
    /// choice, off unless turned on in the settings; every session reads
    /// it from the same store.
    var breakTime: Bool {
        get { defaults.bool(forKey: BookSession.breakTimeKey) }
        set { defaults.set(newValue, forKey: BookSession.breakTimeKey); objectWillChange.send() }
    }
    #if DEBUG
    /// The last URL the app was opened with, for the harness.
    var lastOpenedURL: String?
    #endif
    /// The sprite agent's line in, off unless the reader turns it on.
    let agent = AgentLink()
    /// A capture waiting for its question; the capture card shows it.
    @Published var pendingCapture: Capture?
    /// A summary or a detail, answered in the capture card.
    @Published var captureAnswer: String?
    /// A draft being planned in conversation; the planning card shows it.
    @Published var planning: PlanState?
    @Published var planBusy = false
    @Published var planError: String?
    private var shelfPoller: Task<Void, Never>?
    /// The check of the followed blogs in flight, so the shelf asks once.
    private var feedCheck: Task<Void, Never>?
    /// How often the shelf checks back while a primer is being written.
    var shelfPollInterval: TimeInterval = 4
    /// A word to the reader when something they asked for is done.
    var notices: NoticeSender = Notices.shared
    /// What the server is writing for the reader, as of the last look, by
    /// the id it will have when done (a book draft under its book's id)
    /// with the title to name it by.
    private var writing: [String: String] = [:]
    /// Change requests in the agent's hands, as of the last look.
    private var openRequests: Set<String> = []
    private var requestPoller: Task<Void, Never>?
    /// How often the requests are checked while one is in the agent's hands.
    var requestPollInterval: TimeInterval = 15
    /// The primer the reader just asked for: it opens on its own once the
    /// server has written it, as long as the reader is still on the shelf.
    private var awaitedPrimer: String?
    @Published var subjects: [SubjectInfo] = []
    /// The book the server says was last open, across every client.
    @Published var activeSubjectID: String?
    /// The open book, or nil for the shelf.
    @Published var session: BookSession?
    /// A PDF open in the reader; a book and a document are never open together.
    @Published var document: DocumentSession?
    /// How long after a page turn the position goes to the server.
    var documentPositionDelay: TimeInterval = 2
    private var documentPush: Task<Void, Never>?
    private var documentDirty = false
    @Published var teaching = false
    @Published var shelfError: String?
    @Published var loadingShelf = false

    let sync = Sync()
    /// What the shelf and its books talk to: the connection, or a stand-in
    /// under test.
    let service: ChironService
    private var sessions: [String: BookSession] = [:]
    let storage: URL

    /// A newer build of the app than the one running, when the server has
    /// one; the shelf offers it.
    @Published var availableBuild: AppBuild?
    /// This app's own build number, CFBundleVersion.
    var runningBuild: Int = Int(Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "") ?? 0

    init(storage: URL? = nil, service: ChironService? = nil, defaults: UserDefaults = .standard) {
        self.storage = storage ?? Library.defaultStorage()
        self.service = service ?? sync
        self.defaults = defaults
        self.order = LibrarySort(rawValue: defaults.string(forKey: "shelfOrder") ?? "") ?? .recent
        agent.attach(self)
        loadShelfCache()
    }

    /// Change requests, newest first, as the server last told them.
    @Published var requests: [ChangeRequest] = []
    @Published var requestError: String?

    /// Ask the agent on the sprite for a change, with where the reader is
    /// (the harness state and, if wanted, a screenshot) so it can see what
    /// was meant. Returns the queued request, or nil with requestError set.
    @discardableResult
    /// A change for the agent on the sprite. The picture is whatever shows
    /// the problem best: one the reader picked, or failing that a picture of
    /// where they were when they asked.
    func requestChange(_ text: String, withPicture: Bool, picture: Data? = nil) async -> ChangeRequest? {
        var state: [String: Any] = [:]
        var png: Data? = picture
        #if DEBUG
        state = AppCommands.state(self).filter { JSONSerialization.isValidJSONObject([$0.key: $0.value]) }
        if png == nil, withPicture { png = try? AppCommands.screenshot() }
        #endif
        do {
            let r = try await service.requestChange(text: text, state: state, screenshotPNG: png)
            requestError = nil
            notices.prepare()
            await refreshRequests()
            return r
        } catch {
            requestError = "The sprite did not take the request. Try again."
            return nil
        }
    }

    func refreshRequests() async {
        guard let list = try? await service.changeRequests() else { return }
        requests = list
        // A request that was in the agent's hands and is out of them now:
        // done, failed, or stopped on a question for the reader.
        for r in list where !r.open && openRequests.contains(r.id) {
            if r.waiting {
                notices.notify(id: "request-\(r.id)", title: "Chiron has a question", body: r.question ?? r.text)
            } else {
                notices.notify(id: "request-\(r.id)",
                               title: r.status == "ready" ? "Your change is built" : "The change could not be made",
                               body: r.text)
            }
        }
        openRequests = Set(list.filter(\.open).map(\.id))
        watchRequests()
    }

    /// What machines behind the book posted since the last look, each a
    /// notice once. A device's first look only marks the place: what was
    /// posted before it ever looked is not news.
    func checkAlerts() async {
        let seen = defaults.string(forKey: "alertsSeen")
        guard let list = try? await service.alerts(since: seen), let newest = list.last else { return }
        if seen != nil {
            for a in list {
                notices.notify(id: "alert-\(a.at)", title: a.text, body: "from \(a.source)")
            }
        }
        defaults.set(newest.at, forKey: "alertsSeen")
    }

    /// Requests the agent stopped on, waiting for the reader's answer.
    var waitingOnReader: Int { requests.filter(\.waiting).count }

    /// The reader's answer to what the agent asked; the request goes back
    /// to the agent. False, with requestError set, when it did not go.
    func answerRequest(_ id: String, _ answer: String) async -> Bool {
        let answer = answer.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !answer.isEmpty else { return false }
        do {
            _ = try await service.answerRequest(id: id, answer: answer)
            requestError = nil
            await refreshRequests()
            return true
        } catch {
            requestError = "The sprite did not take the answer. Try again."
            return false
        }
    }

    /// While a request is in the agent's hands the app checks back on its
    /// own, card open or not, so the reader hears when it is done.
    private func watchRequests() {
        guard !openRequests.isEmpty, requestPoller == nil else { return }
        requestPoller = Task { [weak self] in
            guard let self else { return }
            try? await Task.sleep(nanoseconds: UInt64(self.requestPollInterval * 1_000_000_000))
            self.requestPoller = nil
            await self.refreshRequests()
            await self.checkForBuild()
        }
    }

    /// Something the reader is waiting on: a primer or a book being
    /// written, a change request in the agent's hands.
    var awaiting: Bool { !writing.isEmpty || !openRequests.isEmpty }

    /// Whatever was being written at the last look and is done now gets
    /// a word to the reader; whatever is being written now is remembered.
    private func noticeWriting() {
        let byID = Dictionary(subjects.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        for (id, title) in writing {
            if let s = byID[id] {
                guard !s.authoring else { continue }
                if s.failed {
                    notices.notify(id: id, title: "The primer could not be written", body: s.error ?? s.title)
                } else {
                    notices.notify(id: id, title: s.isPrimer ? "Your primer is ready" : "Your book is ready", body: s.title)
                }
            } else if let draft = subjects.first(where: { $0.book == id }) {
                // A book draft stays on the shelf until its book arrives or
                // its generation fails.
                guard !draft.authoring else { continue }
                if draft.failed {
                    notices.notify(id: id, title: "The book could not be built", body: draft.error ?? title)
                }
            }
            // Gone from the shelf altogether: discarded, or another device's
            // doing; nothing to say.
            writing[id] = nil
        }
        for s in subjects where s.authoring {
            writing[s.book ?? s.id] = s.title
        }
    }

    // MARK: - Away from the app

    /// The system's name for a look at the shelf while the app is in the
    /// background, so a notice can reach the reader in another app.
    nonisolated static let backgroundCheckID = "dev.mjbraun.chiron.check"

    /// Ask for a look at the shelf while the app is away: soon while
    /// something is awaited, and now and then otherwise, for the alerts a
    /// machine behind the book may post. The system decides when, if at all.
    func scheduleBackgroundCheck() {
        let request = BGAppRefreshTaskRequest(identifier: Self.backgroundCheckID)
        request.earliestBeginDate = Date().addingTimeInterval(awaiting ? 60 : 15 * 60)
        try? BGTaskScheduler.shared.submit(request)
    }

    /// The look itself: the shelf, the requests and the alerts, then the
    /// next look asked for.
    func backgroundCheck() async {
        await refresh()
        await refreshRequests()
        scheduleBackgroundCheck()
    }

    /// Back in front: what was waited on may be done.
    func resume() async {
        guard awaiting else { return }
        await refresh()
        await refreshRequests()
    }

    /// Ask the server for its latest build and offer it if it is newer
    /// than this one and finished; nothing to say otherwise.
    func checkForBuild() async {
        guard let latest = try? await service.latestBuild(), latest.status == "ready", latest.build > runningBuild else {
            availableBuild = nil
            return
        }
        availableBuild = latest
    }

    /// What a tap on Install opens: on iOS the itms-services link that
    /// has the system install over this app; on the Mac the zip itself.
    var installURL: URL? {
        guard let b = availableBuild, var base = URL(string: sync.baseURL) else { return nil }
        #if targetEnvironment(macCatalyst)
        base.append(path: b.macPath)
        return base
        #else
        base.append(path: b.manifestPath)
        var c = URLComponents()
        c.scheme = "itms-services"
        c.host = ""
        c.queryItems = [URLQueryItem(name: "action", value: "download-manifest"), URLQueryItem(name: "url", value: base.absoluteString)]
        return c.url
        #endif
    }

    /// Per-subject caches live in Application Support, not Documents: they
    /// are rebuilt from the server and have no business in a backup.
    static func defaultStorage() -> URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        var dir = base.appendingPathComponent("Chiron", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try? dir.setResourceValues(values)
        return dir
    }

    func refresh() async {
        loadingShelf = true
        defer { loadingShelf = false }
        do {
            let shelf = try await service.subjects()
            subjects = shelf.subjects
            shelves = shelf.shelves ?? []
            activeSubjectID = shelf.activeID
            shelfError = nil
            noticeWriting()
            // A shelf deleted elsewhere closes here.
            if case .shelf(let id) = scope, !shelves.contains(where: { $0.id == id }) { scope = .all }
            saveShelfCache(shelf)
            // The server is back: whatever was marked up while it was away goes up.
            await session?.pushAnnotations()
            await checkForBuild()
            await checkAlerts()
            checkFeeds()
        } catch {
            shelfError = subjects.isEmpty ? BookSession.unreachable : Library.offline
        }
        if let id = awaitedPrimer, let primer = subjects.first(where: { $0.id == id }), !primer.authoring {
            awaitedPrimer = nil
            if !primer.failed && session == nil { await open(id) }
        }
        // A primer still authoring turns into a book without the reader
        // asking: the shelf checks back while any card is grey.
        if subjects.contains(where: \.authoring), shelfPoller == nil {
            shelfPoller = Task { [weak self] in
                guard let self else { return }
                try? await Task.sleep(nanoseconds: UInt64(self.shelfPollInterval * 1_000_000_000))
                self.shelfPoller = nil
                if self.session == nil || self.awaitedPrimer != nil || !self.writing.isEmpty { await self.refresh() }
            }
        }
    }

    /// A capture with its question and its scale: a summary or a
    /// detail comes back into the card; a primer or a book opens its
    /// planning conversation.
    @discardableResult
    func submitCapture(_ c: Capture, prompt: String, scale: CaptureScale = .primer) async throws -> CaptureResponse {
        var req = CaptureRequest(prompt: prompt, scale: scale)
        let text = c.text.trimmingCharacters(in: .whitespacesAndNewlines)
        if !text.isEmpty { req.text = text }
        if let png = c.imagePNG { req.imagePngB64 = png.base64EncodedString() }
        req.sourceUrl = c.sourceURL
        req.sourceApp = c.sourceApp
        let reply = try await service.capture(req)
        if scale.immediate {
            captureAnswer = reply.answerMd ?? ""
            return reply
        }
        guard let id = reply.subject else { throw URLError(.badServerResponse) }
        var plan: [PlanMessage] = []
        if let line = reply.replyMd, !line.isEmpty { plan.append(PlanMessage(role: "tutor", text: line)) }
        let state = PlanState(id: id, title: reply.title ?? prompt, scale: scale.rawValue, status: reply.status ?? "planning",
                              error: nil, prompt: prompt,
                              source: PrimerSource(text: text.isEmpty ? nil : text, url: c.sourceURL, app: c.sourceApp),
                              brief: reply.brief, done: reply.done ?? false, plan: plan, book: nil)
        pendingCapture = nil
        captureAnswer = nil
        await refresh()
        // One sheet gives way to the next; presenting both in the same
        // beat leaves the second unshown.
        try? await Task.sleep(nanoseconds: 350_000_000)
        planning = state
        return reply
    }

    /// A draft on the shelf reopens its conversation.
    func openDraft(_ id: String) async {
        do {
            planning = try await service.plan(subject: id)
            planError = nil
        } catch {
            shelfError = BookSession.unreachable
        }
    }

    /// The reader's next line to the tutor.
    func planReply(_ text: String) async {
        guard var state = planning else { return }
        let line = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !line.isEmpty else { return }
        state.plan.append(PlanMessage(role: "learner", text: line))
        planning = state
        planBusy = true
        planError = nil
        defer { planBusy = false }
        do {
            let turn = try await service.planTurn(subject: state.id, text: line)
            guard var now = planning, now.id == state.id else { return }
            if let reply = turn.replyMd { now.plan.append(PlanMessage(role: "tutor", text: reply)) }
            if turn.done == true {
                now.done = true
                now.brief = turn.brief
                if let t = turn.title, !t.isEmpty { now.title = t }
            }
            planning = now
        } catch {
            // The line stays in the transcript; the next send carries on.
            planError = "The tutor could not be reached. Send again."
        }
    }

    /// Build what the plan has: the reader lands on the shelf and the
    /// primer, or the book, opens when it is done.
    func buildDraft() async {
        guard let state = planning else { return }
        planBusy = true
        defer { planBusy = false }
        do {
            let built = try await service.build(subject: state.id)
            awaitedPrimer = built.book ?? built.subject
            planning = nil
            planError = nil
            notices.prepare()
            session?.persist()
            session = nil
            await refresh()
        } catch {
            planError = "The server could not start the build. Try again."
        }
    }

    /// The draft is not wanted after all.
    func discardDraft() async {
        guard let state = planning else { return }
        do {
            try await service.discard(subject: state.id)
            planning = nil
            planError = nil
            await refresh()
        } catch {
            planError = "The server could not discard it. Try again."
        }
    }

    static let offline = "The server is out of reach; this is the library as it was last seen."

    // The library as last seen, kept on disk so it shows without the server.

    private var shelfCacheURL: URL { storage.appendingPathComponent("library.json") }

    private func saveShelfCache(_ shelf: SubjectsResponse) {
        try? FileManager.default.createDirectory(at: storage, withIntermediateDirectories: true)
        if let d = try? JSONEncoder().encode(shelf) { try? d.write(to: shelfCacheURL) }
    }

    private func loadShelfCache() {
        guard let d = try? Data(contentsOf: shelfCacheURL),
              let shelf = try? JSONDecoder().decode(SubjectsResponse.self, from: d) else { return }
        subjects = shelf.subjects
        shelves = shelf.shelves ?? []
        activeSubjectID = shelf.activeID
    }

    // The library's shape: what is on no shelf, and what is on one, each
    // in the reader's order.

    var unfiled: [SubjectInfo] { order.apply(subjects.filter { $0.shelf == nil }) }

    func subjects(on shelf: String) -> [SubjectInfo] { order.apply(subjects.filter { $0.shelf == shelf }) }

    func shelf(_ id: String) -> ShelfInfo? { shelves.first { $0.id == id } }

    /// What the sidebar's pick holds, in the reader's order.
    func listed(in scope: LibraryScope) -> [SubjectInfo] {
        switch scope {
        case .all: return order.apply(subjects)
        case .kind(let k): return order.apply(subjects.filter { $0.libraryKind == k })
        case .shelf(let id): return subjects(on: id)
        }
    }

    /// Posts waiting across every followed blog.
    var unreadTotal: Int { subjects.reduce(0) { $0 + ($1.isFeed ? $1.unread ?? 0 : 0) } }

    /// What the reader is in the middle of: the one open, then whatever is
    /// started and not finished, and a primer still being written. A feed
    /// only while it is the one open; its unread count says the rest.
    var continuing: [SubjectInfo] {
        let open = subjects.filter { $0.id == activeSubjectID }
        return open + subjects.filter { $0.id != activeSubjectID && $0.underway }
    }

    func createShelf(named name: String) async {
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return }
        do {
            _ = try await service.createShelf(name: trimmed)
            shelfError = nil
        } catch {
            shelfError = "The server could not make the shelf."
        }
        await refresh()
    }

    func renameShelf(_ id: String, to name: String) async {
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return }
        do {
            _ = try await service.renameShelf(id, name: trimmed)
            shelfError = nil
        } catch {
            shelfError = "The server could not rename the shelf."
        }
        await refresh()
    }

    /// Deleting a shelf returns what it held to the library.
    func deleteShelf(_ id: String) async {
        do {
            try await service.deleteShelf(id)
            shelfError = nil
        } catch {
            shelfError = "The server could not delete the shelf."
        }
        await refresh()
    }

    /// File a subject on a shelf, or, with nil, back in the library.
    func move(_ subject: String, to shelf: String?) async {
        do {
            try await service.move(subject: subject, toShelf: shelf)
            shelfError = nil
        } catch {
            shelfError = "The server could not move it."
        }
        await refresh()
    }

    /// A server from another device's code: saved, selected, probed, and
    /// this device's ssh key enrolled when the link carries the shared key,
    /// as the settings sheet does on save.
    func adopt(_ link: ServerLink) async {
        let server = sync.servers.adopt(link)
        sync.baseURL = server.url
        await sync.probe()
        await refresh()
        if link.key != nil {
            _ = try? await sync.enrolDeviceKey(name: DeviceKeySection.deviceName)
        }
    }

    /// A chiron://capture/<id> URL, from the share extension or an intent.
    func receiveCapture(id: String) {
        guard let c = CaptureInbox.take(id) else { return }
        if let name = c.pdfFile {
            // A PDF shared in goes on the shelf as itself, not as text.
            let url = CaptureInbox.directory.appendingPathComponent(name)
            Task {
                await importPDF(at: url, title: c.sourceApp)
                try? FileManager.default.removeItem(at: url)
            }
            return
        }
        pendingCapture = c
    }

    /// At launch the app lands on the shelf; the book last open, on
    /// whichever client, is the card marked "Open now".
    func launch() async {
        await refresh()
        // A request left with the agent last time is watched again.
        await refreshRequests()
    }

    func open(_ id: String) async {
        let info = subjects.first(where: { $0.id == id })
        if let info, info.isPDF {
            await openDocument(info)
            return
        }
        let s = sessions[id] ?? BookSession(
            subjectID: id,
            title: info?.title ?? id,
            service: service, storage: storage, defaults: defaults)
        if let k = info?.kind { s.kind = k }
        s.assets = bookAssets
        s.localTutor = LocalTutor.ifAvailable
        // A passage sent on from inside the book: the card opens over it,
        // naming the book as where the words came from.
        // A post opened in a followed blog stops counting as unread.
        if info?.isFeed == true {
            s.onChapterRead = { [weak self] unit in self?.markRead(subject: id, unit: unit) }
        }
        s.onCapture = { [weak self] text in
            self?.captureAnswer = nil
            self?.pendingCapture = Capture(text: text, sourceApp: s.title)
        }
        sessions[id] = s
        session = s
        activeSubjectID = id
        await s.open()
        s.refreshKept()
    }

    func closeBook() {
        session?.persist()
        session = nil
        if let d = document {
            documentPush?.cancel()
            document = nil
            Task { await pushDocumentPosition(d) }
        }
        Task { await refresh() }
    }

    // MARK: - Documents

    private var documentsDir: URL {
        let dir = storage.appendingPathComponent("documents", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    /// The file is fetched once and kept, so the document opens offline.
    private func openDocument(_ info: SubjectInfo) async {
        let file = documentsDir.appendingPathComponent("\(info.id).pdf")
        if !FileManager.default.fileExists(atPath: file.path) {
            do {
                let data = try await service.documentData(id: info.id)
                try data.write(to: file, options: .atomic)
            } catch {
                shelfError = "The PDF could not be fetched: \(error.localizedDescription)"
                return
            }
        }
        let d = DocumentSession(id: info.id, title: info.title, pages: info.pages ?? 0,
                                page: info.page ?? 0, position: 0, fileURL: file)
        // The ink drawn on the other device comes down with the document;
        // when the server is away the page opens clean and any ink drawn
        // now goes up on the next open.
        if let ink = try? await service.documentInk(id: info.id) {
            d.adopt(ink: ink)
        }
        d.onTurn = { [weak self, weak d] _, _ in
            guard let self, let d else { return }
            self.documentDirty = true
            self.scheduleDocumentPush(d)
        }
        d.onInk = { [weak self, weak d] _ in
            guard let self, let d else { return }
            self.scheduleDocumentPush(d)
        }
        // A passage of the PDF sent on: the card opens over the page,
        // naming the document and the page as where the words came from.
        d.onCapture = { [weak self, weak d] text, page in
            guard let self, let d else { return }
            self.captureAnswer = nil
            self.pendingCapture = Capture(text: text, sourceApp: "\(d.title), page \(page + 1)")
        }
        document = d
        shelfError = nil
    }

    private func scheduleDocumentPush(_ d: DocumentSession) {
        documentPush?.cancel()
        documentPush = Task { [weak self] in
            try? await Task.sleep(nanoseconds: UInt64((self?.documentPositionDelay ?? 2) * 1_000_000_000))
            guard !Task.isCancelled else { return }
            await self?.pushDocumentPosition(d)
        }
    }

    /// Whatever changed goes up: the page, and every page drawn on. A page
    /// the other device also drew on while apart comes back as a conflict
    /// with its ink, which lies over this one; nothing drawn is lost.
    private func pushDocumentPosition(_ d: DocumentSession) async {
        if documentDirty {
            documentDirty = false
            try? await service.documentPosition(id: d.id, page: d.page, position: d.position)
        }
        for page in d.takeDirtyInk() {
            guard let ink = d.ink[page] else { continue }
            let b64 = ink.dataRepresentation().base64EncodedString()
            do {
                switch try await service.putDocumentInk(id: d.id, page: page, inkB64: b64, baseVersion: d.inkVersions[page] ?? 0) {
                case .stored(let stored):
                    d.inkVersions[page] = stored.version
                case .conflict(let server):
                    if let theirs = Data(base64Encoded: server.inkB64).flatMap({ try? PKDrawing(data: $0) }) {
                        d.merge(theirs, on: page)
                    }
                    let merged = d.ink[page]?.dataRepresentation().base64EncodedString() ?? b64
                    if case .stored(let stored) = try await service.putDocumentInk(id: d.id, page: page, inkB64: merged, baseVersion: server.version) {
                        d.inkVersions[page] = stored.version
                    } else {
                        d.markInkDirty(page)
                    }
                }
            } catch {
                d.markInkDirty(page)
            }
        }
    }

    /// A PDF from Files or the share sheet goes to the server and onto the
    /// shelf; the title is the file's name unless the sender knew better.
    /// An EPUB the reader owns: it goes to the server whole, which reads
    /// it into a subject whose chapters are the book's own. Nothing about
    /// it is adapted; what the reader adds to it is theirs.
    func importBook(at url: URL, title: String? = nil) async {
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        // Every EPUB is a zip, and every zip starts the same way.
        guard let data = try? Data(contentsOf: url), data.starts(with: [0x50, 0x4B, 0x03, 0x04]) else {
            shelfError = "\(url.lastPathComponent) is not an EPUB."
            return
        }
        let name = title?.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            _ = try await service.importBook(title: (name?.isEmpty == false ? name! : url.deletingPathExtension().lastPathComponent),
                                             data: data)
            shelfError = nil
            await refresh()
        } catch {
            shelfError = "The book could not be sent: \(error.localizedDescription)"
        }
    }

    /// A blog to follow: the feed goes up, the server reads its posts in
    /// as chapters, and it lands on the shelf with a count of what has
    /// not been read.
    func followFeed(_ link: String) async {
        guard let url = webLink(link) else {
            shelfError = "That is not a link Chiron can follow."
            return
        }
        loadingShelf = true
        defer { loadingShelf = false }
        do {
            let blog = try await service.followFeed(url: url)
            shelfError = nil
            pendingCapture = nil
            captureAnswer = nil
            await refresh()
            await open(blog.id)
        } catch {
            shelfError = "That blog could not be followed: \(error.localizedDescription)"
        }
    }

    /// Nothing polls on the server: it sleeps between readers. Opening
    /// the app is what asks the blogs what is new, and what came back
    /// goes on the shelf straight away.
    func checkFeeds() {
        guard feedCheck == nil, subjects.contains(where: \.isFeed) else { return }
        feedCheck = Task { [weak self] in
            guard let self else { return }
            defer { self.feedCheck = nil }
            guard let check = try? await self.service.checkFeeds(), check.added > 0 else { return }
            await self.refresh()
        }
    }

    /// Off the shelf: a blog no longer followed, a page or an imported
    /// book no longer wanted. What the reader wrote on it goes with it.
    func forget(_ id: String) async {
        if session?.subjectID == id { closeBook() }
        if planning?.id == id { planning = nil }
        sessions[id] = nil
        let primer = subjects.first(where: { $0.id == id })?.isPrimer == true
        do {
            if primer {
                try await service.deletePrimer(id: id)
            } else {
                try await service.forgetReading(id: id)
            }
            shelfError = nil
        } catch {
            shelfError = "That could not be taken off the shelf: \(error.localizedDescription)"
        }
        await refresh()
    }

    /// A post the reader has opened is read, on every device.
    func markRead(subject: String, unit: String) {
        Task { try? await service.markRead(subject: subject, unit: unit) }
    }

    private func webLink(_ link: String) -> String? {
        let trimmed = link.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), let scheme = url.scheme?.lowercased(),
              scheme == "http" || scheme == "https", url.host != nil else { return nil }
        return trimmed
    }

    /// A page the reader is reading somewhere else: the link goes up, the
    /// server fetches the article and keeps it with its pictures, and it
    /// opens here as a reading. What is read is the writer's own words,
    /// so a highlight in it asks about the piece, not about a fragment
    /// of it.
    func readPage(_ link: String) async {
        guard let url = webLink(link) else {
            shelfError = "That is not a link Chiron can read."
            return
        }
        loadingShelf = true
        defer { loadingShelf = false }
        do {
            let page = try await service.readPage(url: url)
            shelfError = nil
            pendingCapture = nil
            captureAnswer = nil
            await refresh()
            await open(page.id)
        } catch {
            shelfError = "That page could not be read: \(error.localizedDescription)"
        }
    }

    func importPDF(at url: URL, title: String? = nil) async {
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        guard let data = try? Data(contentsOf: url), data.starts(with: Array("%PDF".utf8)) else {
            shelfError = "\(url.lastPathComponent) is not a PDF."
            return
        }
        let name = title?.trimmingCharacters(in: .whitespacesAndNewlines)
        let pages = PDFDocument(data: data)?.pageCount ?? 0
        do {
            _ = try await service.uploadDocument(title: (name?.isEmpty == false ? name! : url.deletingPathExtension().lastPathComponent),
                                                 pages: pages, data: data)
            shelfError = nil
            await refresh()
        } catch {
            shelfError = "The PDF could not be sent: \(error.localizedDescription)"
        }
    }
}
