import Foundation

/// The server as the book sees it. `Sync` talks HTTP; tests hand a
/// `BookSession` a fake.
protocol ChironService: AnyObject {
    func subjects() async throws -> SubjectsResponse
    func state(subject: String) async throws -> BookState
    func chapter(subject: String) async throws -> ChapterStatus
    /// One chapter of a book read as it is, without reading it.
    func chapter(subject: String, unit: String) async throws -> ChapterStatus
    /// The names of the pictures a book carries.
    func bookAssetNames(subject: String) async throws -> [String]
    func exchange(_ request: ExchangeRequest) async throws -> ExchangeResponse
    func ink(subject: String, _ submission: InkSubmission) async throws -> ExchangeResponse
    func ask(subject: String, unit: String, quote: String, question: String, history: [QA]) async throws -> AskResponse
    /// The reader thinks an item, its reference answer or its grading is
    /// wrong; `response` is what they answered, if they had.
    func flag(subject: String, unit: String, item: String, text: String, response: ItemResponse?) async throws -> FlagResponse
    func capture(_ request: CaptureRequest) async throws -> CaptureResponse
    func plan(subject: String) async throws -> PlanState
    func planTurn(subject: String, text: String) async throws -> CaptureResponse
    func build(subject: String) async throws -> BuildResponse
    func discard(subject: String) async throws
    func extend(subject: String, quote: String, note: String) async throws -> ExtendResponse
    func reset(subject: String) async throws -> BookState
    func createShelf(name: String) async throws -> ShelfInfo
    func renameShelf(_ id: String, name: String) async throws -> ShelfInfo
    func deleteShelf(_ id: String) async throws
    func move(subject: String, toShelf shelf: String?) async throws
    func annotations(subject: String, unit: String) async throws -> Annotations?
    func putAnnotations(subject: String, unit: String, _ a: Annotations, baseVersion: Int) async throws -> AnnotationsPut
    func reconcileAnnotations(subject: String, unit: String, mine: Annotations, theirs: Annotations) async throws -> ReconciledAnnotations
    func uploadDocument(title: String, pages: Int, data: Data) async throws -> Document
    func importBook(title: String, data: Data) async throws -> ImportedBook
    /// A page on the web, read as its article and kept as a reading.
    func readPage(url: String) async throws -> ImportedBook
    /// A blog to follow: its feed becomes a card whose chapters are posts.
    func followFeed(url: String) async throws -> ImportedBook
    /// Ask the followed blogs what is new. The server does the fetching;
    /// this is what wakes it to do it.
    func checkFeeds() async throws -> FeedCheck
    /// A chapter the reader has seen, on every device.
    func markRead(subject: String, unit: String) async throws
    /// Take an imported book, a page or a followed blog off the shelf.
    func forgetReading(id: String) async throws
    /// Take a primer off the shelf, written or still a draft.
    func deletePrimer(id: String) async throws
    /// One of an imported book's pictures, as bytes.
    func bookAsset(subject: String, name: String) async throws -> Data
    func documentData(id: String) async throws -> Data
    func documentPosition(id: String, page: Int, position: Double) async throws
    func deleteDocument(id: String) async throws
    func documentInk(id: String) async throws -> [Int: PageInk]
    func putDocumentInk(id: String, page: Int, inkB64: String, baseVersion: Int) async throws -> PageInkPut
    func latestBuild() async throws -> AppBuild
    func requestChange(text: String, state: [String: Any], screenshotPNG: Data?) async throws -> ChangeRequest
    func changeRequests() async throws -> [ChangeRequest]
    func answerRequest(id: String, answer: String) async throws -> ChangeRequest
    func alerts(since: String?) async throws -> [MachineAlert]
}

enum ServiceError: Error {
    case badURL
    case status(Int)
}

/// HTTP client for the book server, with bearer auth from the saved server's
/// key and a launch-environment override for development.
@MainActor
final class Sync: ObservableObject, ChironService {
    @Published var baseURL: String {
        didSet { UserDefaults.standard.set(baseURL, forKey: "baseURL") }
    }
    /// Saved servers. The selected one drives baseURL and supplies its own key.
    let servers = ServerStore()
    var serverID: UUID? { servers.selected?.id }
    @Published var connected = false
    /// Whether the server has a model behind it (/health llm.connected).
    /// Without one, mechanical grading and pre-authored chapters still work;
    /// free-text grading and adaptive authoring do not.
    @Published var llmConnected = false

    init() {
        baseURL = UserDefaults.standard.string(forKey: "baseURL") ?? "http://192.168.2.1:8080"
        // Anything configured before servers were a list becomes the first
        // saved entry, so upgrading does not read as losing the setup.
        servers.migrateIfNeeded(currentURL: baseURL)
        if let selected = servers.selected { baseURL = selected.url }
        // Development override: point the app at a server for this launch only,
        // without touching saved servers. Simulator runs use
        //   SIMCTL_CHILD_CHIRON_SERVER=http://localhost:8084 xcrun simctl launch ...
        if let override = ProcessInfo.processInfo.environment["CHIRON_SERVER"],
           !override.isEmpty {
            baseURL = override
        }
        Task { await probe() }
    }

    func probe() async {
        if let health: Health = try? await get("/health", timeout: 3) {
            connected = true
            llmConnected = health.llm.connected
            return
        }
        connected = false
    }

    // MARK: - ChironService

    func subjects() async throws -> SubjectsResponse {
        try await get("/subjects", timeout: 5)
    }

    func state(subject: String) async throws -> BookState {
        try await get("/state?subject=\(subject)", timeout: 10)
    }

    func chapter(subject: String) async throws -> ChapterStatus {
        try await get("/chapter/\(subject)", timeout: 30)
    }

    func chapter(subject: String, unit: String) async throws -> ChapterStatus {
        try await get("/chapter/\(subject)?unit=\(unit)", timeout: 60)
    }

    func bookAssetNames(subject: String) async throws -> [String] {
        struct List: Codable {
            struct Asset: Codable { let name: String }
            let assets: [Asset]
        }
        let list: List = try await get("/readings/\(subject)/assets", timeout: 60)
        return list.assets.map(\.name)
    }

    /// Grading a check is a model call; the timeout is generous but finite
    /// so a server that hangs surfaces as an error rather than a spinner.
    func exchange(_ request: ExchangeRequest) async throws -> ExchangeResponse {
        try await post("/exchange", body: try JSONEncoder().encode(request), timeout: 600)
    }

    /// The ink check-in transcribes before it grades; same budget as an
    /// exchange.
    func ink(subject: String, _ submission: InkSubmission) async throws -> ExchangeResponse {
        try await post("/ink/\(subject)", body: try JSONEncoder().encode(submission), timeout: 600)
    }

    /// One model call; the answer comes back in the same request.
    func ask(subject: String, unit: String, quote: String, question: String, history: [QA]) async throws -> AskResponse {
        struct Body: Encodable {
            let unit, quote, question: String
            let history: [QA]
        }
        return try await post("/ask/\(subject)",
                              body: try JSONEncoder().encode(Body(unit: unit, quote: quote, question: question, history: history)),
                              timeout: 120)
    }

    func flag(subject: String, unit: String, item: String, text: String, response: ItemResponse?) async throws -> FlagResponse {
        struct Body: Encodable {
            let unit, item, text: String
            var answer: String?
            var selectedIndex: Int?
            var idk: Bool?

            enum CodingKeys: String, CodingKey {
                case unit, item, text, answer, idk
                case selectedIndex = "selected_index"
            }
        }
        var body = Body(unit: unit, item: item, text: text)
        if let r = response {
            // Ink is not read on the device; the server has the strokes
            // from the check-in, so the flag just says how it was answered.
            body.answer = r.ink != nil ? "(written by hand)" : r.response
            body.selectedIndex = r.selectedIndex
            body.idk = r.idk == true ? true : nil
        }
        return try await post("/flag/\(subject)", body: try JSONEncoder().encode(body), timeout: 30)
    }

    // MARK: - device keys

    /// Put this device's ssh key on the server. Possessing the shared key
    /// is what authorizes it; a server without key enrolment says so.
    func enrolDeviceKey(name: String) async throws -> EnrolResponse {
        guard let id = serverID else { throw ServiceError.badURL }
        struct Body: Encodable { let pubkey, name: String }
        return try await post("/agent/pubkey",
                              body: try JSONEncoder().encode(Body(pubkey: DeviceKey.authorizedKeysLine(for: id), name: name)),
                              timeout: 30)
    }

    func serverKeys() async throws -> [EnrolledKey] {
        struct Reply: Decodable { let keys: [EnrolledKey] }
        let r: Reply = try await get("/agent/keys", timeout: 15)
        return r.keys
    }

    func revokeKey(fingerprint: String) async throws {
        struct Body: Encodable { let fingerprint: String }
        struct Reply: Decodable { let removed: Int }
        let _: Reply = try await post("/agent/keys/revoke", body: try JSONEncoder().encode(Body(fingerprint: fingerprint)), timeout: 15)
    }

    /// A capture becomes a primer on the shelf; authoring runs on the
    /// server after this returns.
    /// A summary or a description is a model call answered in the request;
    /// a draft's opening question is one too.
    func capture(_ request: CaptureRequest) async throws -> CaptureResponse {
        try await post("/primer/capture", body: try JSONEncoder().encode(request), timeout: 180)
    }

    func plan(subject: String) async throws -> PlanState {
        try await get("/primer/\(subject)/plan", timeout: 10)
    }

    func planTurn(subject: String, text: String) async throws -> CaptureResponse {
        struct Body: Encodable { let text: String }
        return try await post("/primer/\(subject)/plan", body: try JSONEncoder().encode(Body(text: text)), timeout: 180)
    }

    func build(subject: String) async throws -> BuildResponse {
        try await post("/primer/\(subject)/build", body: Data("{}".utf8), timeout: 30)
    }

    func discard(subject: String) async throws {
        struct Reply: Decodable { let discarded: Bool }
        let _: Reply = try await post("/primer/\(subject)/discard", body: Data("{}".utf8), timeout: 30)
    }

    func createShelf(name: String) async throws -> ShelfInfo {
        try await send("POST", "/shelves", body: try JSONEncoder().encode(["name": name]), timeout: 15)
    }

    func renameShelf(_ id: String, name: String) async throws -> ShelfInfo {
        try await send("PUT", "/shelves/\(id)", body: try JSONEncoder().encode(["name": name]), timeout: 15)
    }

    func deleteShelf(_ id: String) async throws {
        struct Reply: Decodable { let deleted: Bool }
        let _: Reply = try await send("DELETE", "/shelves/\(id)", body: nil, timeout: 15)
    }

    func move(subject: String, toShelf shelf: String?) async throws {
        struct Reply: Decodable { let subject: String }
        let _: Reply = try await send("PUT", "/subjects/\(subject)/shelf",
                                      body: try JSONEncoder().encode(["shelf": shelf ?? ""]), timeout: 15)
    }

    func annotations(subject: String, unit: String) async throws -> Annotations? {
        do {
            return try await get("/annotations/\(subject)/\(unit)", timeout: 15)
        } catch ServiceError.status(404) {
            return nil
        }
    }

    func putAnnotations(subject: String, unit: String, _ a: Annotations, baseVersion: Int) async throws -> AnnotationsPut {
        struct Body: Encodable {
            let version: Int
            let device: String?
            let marks: [Mark]
            let ink_b64: String?
            let position: Double
            let base_version: Int
        }
        let body = try JSONEncoder().encode(Body(version: a.version, device: a.device, marks: a.marks, ink_b64: a.inkB64,
                                                 position: a.position, base_version: baseVersion))
        let (code, data) = try await sendRaw("PUT", "/annotations/\(subject)/\(unit)", body: body, timeout: 30)
        switch code {
        case 200:
            return .stored(try JSONDecoder().decode(Annotations.self, from: data))
        case 409:
            struct Reply: Decodable { let server: Annotations }
            return .conflict(server: try JSONDecoder().decode(Reply.self, from: data).server)
        default:
            throw ServiceError.status(code)
        }
    }

    func reconcileAnnotations(subject: String, unit: String, mine: Annotations, theirs: Annotations) async throws -> ReconciledAnnotations {
        struct Body: Encodable { let mine, theirs: Annotations }
        return try await send("POST", "/annotations/\(subject)/\(unit)/reconcile",
                              body: try JSONEncoder().encode(Body(mine: mine, theirs: theirs)), timeout: 120)
    }

    /// A margin note extends the primer; the whole document comes back.
    func extend(subject: String, quote: String, note: String) async throws -> ExtendResponse {
        struct Body: Encodable { let quote, note: String }
        return try await post("/primer/\(subject)/extend", body: try JSONEncoder().encode(Body(quote: quote, note: note)), timeout: 180)
    }

    func reset(subject: String) async throws -> BookState {
        // Hand-encoded: confirm is a bool on the wire, and a [String: String]
        // dictionary would send it as the string "true", which the server
        // rejects - deliberately, since this discards everything.
        try await post("/reset", body: Data("{\"subject\":\"\(subject)\",\"confirm\":true}".utf8), timeout: 30)
    }

    // MARK: - transport

    // MARK: - Documents

    func uploadDocument(title: String, pages: Int, data: Data) async throws -> Document {
        var parts = URLComponents()
        parts.queryItems = [URLQueryItem(name: "title", value: title), URLQueryItem(name: "pages", value: String(pages))]
        var req = try request("/documents" + (parts.string ?? ""), timeout: 300)
        req.httpMethod = "POST"
        req.setValue("application/pdf", forHTTPHeaderField: "Content-Type")
        req.httpBody = data
        return try await perform(req)
    }

    func importBook(title: String, data: Data) async throws -> ImportedBook {
        var parts = URLComponents()
        parts.queryItems = [URLQueryItem(name: "title", value: title)]
        var req = try request("/readings" + (parts.string ?? ""), timeout: 300)
        req.httpMethod = "POST"
        req.setValue("application/epub+zip", forHTTPHeaderField: "Content-Type")
        req.httpBody = data
        return try await perform(req)
    }

    func readPage(url: String) async throws -> ImportedBook {
        // The server fetches the page and its pictures, which takes as
        // long as the site does.
        var req = try request("/readings/page", timeout: 180)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: ["url": url])
        return try await perform(req)
    }

    func followFeed(url: String) async throws -> ImportedBook {
        var req = try request("/feeds", timeout: 300)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: ["url": url])
        return try await perform(req)
    }

    func checkFeeds() async throws -> FeedCheck {
        var req = try request("/feeds/refresh", timeout: 300)
        req.httpMethod = "POST"
        return try await perform(req)
    }

    func markRead(subject: String, unit: String) async throws {
        var req = try request("/readings/\(subject)/read", timeout: 30)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: ["unit": unit])
        _ = try await URLSession.shared.data(for: req)
    }

    func forgetReading(id: String) async throws {
        var req = try request("/readings/\(id)", timeout: 60)
        req.httpMethod = "DELETE"
        let (_, resp) = try await URLSession.shared.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
    }

    func deletePrimer(id: String) async throws {
        var req = try request("/primer/\(id)", timeout: 60)
        req.httpMethod = "DELETE"
        let (_, resp) = try await URLSession.shared.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
    }

    func bookAsset(subject: String, name: String) async throws -> Data {
        let path = "/readings/\(subject)/assets/\(name.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? name)"
        let (data, resp) = try await URLSession.shared.data(for: try request(path, timeout: 120))
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard code == 200 else { throw ServiceError.status(code) }
        return data
    }

    func documentData(id: String) async throws -> Data {
        let (data, resp) = try await URLSession.shared.data(for: try request("/documents/\(id)/file", timeout: 300))
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard code == 200 else { throw ServiceError.status(code) }
        return data
    }

    /// The build of the app the MacBook made last (SPRITE-DEV-PLAN.md phase F).
    func latestBuild() async throws -> AppBuild {
        try await get("/builds/latest", timeout: 10)
    }

    /// "Request a change": the words, where the reader was, and a picture.
    func requestChange(text: String, state: [String: Any], screenshotPNG: Data?) async throws -> ChangeRequest {
        var body: [String: Any] = ["text": text, "state": state]
        if let png = screenshotPNG { body["screenshot_png_b64"] = png.base64EncodedString() }
        return try await post("/dev/requests", body: try JSONSerialization.data(withJSONObject: body), timeout: 30)
    }

    func changeRequests() async throws -> [ChangeRequest] {
        try await get("/dev/requests", timeout: 10)
    }

    func alerts(since: String?) async throws -> [MachineAlert] {
        var parts = URLComponents()
        if let since { parts.queryItems = [URLQueryItem(name: "since", value: since)] }
        return try await get("/alerts" + (parts.string ?? ""), timeout: 10)
    }

    func answerRequest(id: String, answer: String) async throws -> ChangeRequest {
        try await post("/dev/requests/\(id)/answer", body: try JSONSerialization.data(withJSONObject: ["answer": answer]), timeout: 30)
    }

    func documentPosition(id: String, page: Int, position: Double) async throws {
        let body = try JSONSerialization.data(withJSONObject: ["page": page, "position": position])
        let (code, _) = try await sendRaw("PUT", "/documents/\(id)/position", body: body, timeout: 15)
        guard code == 200 else { throw ServiceError.status(code) }
    }

    func deleteDocument(id: String) async throws {
        let (code, _) = try await sendRaw("DELETE", "/documents/\(id)", body: nil, timeout: 15)
        guard code == 200 else { throw ServiceError.status(code) }
    }

    func documentInk(id: String) async throws -> [Int: PageInk] {
        struct Reply: Decodable { let pages: [String: PageInk] }
        let reply: Reply = try await get("/documents/\(id)/ink", timeout: 30)
        var out: [Int: PageInk] = [:]
        for (k, v) in reply.pages { if let n = Int(k) { out[n] = v } }
        return out
    }

    func putDocumentInk(id: String, page: Int, inkB64: String, baseVersion: Int) async throws -> PageInkPut {
        let body = try JSONSerialization.data(withJSONObject: ["ink_b64": inkB64, "base_version": baseVersion])
        let (code, data) = try await sendRaw("PUT", "/documents/\(id)/ink/\(page)", body: body, timeout: 30)
        switch code {
        case 200:
            return .stored(try JSONDecoder().decode(PageInk.self, from: data))
        case 409:
            struct Reply: Decodable { let server: PageInk }
            return .conflict(server: try JSONDecoder().decode(Reply.self, from: data).server)
        default:
            throw ServiceError.status(code)
        }
    }

    private func request(_ path: String, timeout: TimeInterval) throws -> URLRequest {
        guard let url = URL(string: "\(baseURL)\(path)") else { throw ServiceError.badURL }
        var req = URLRequest(url: url, timeoutInterval: timeout)
        Credentials.authorize(&req, serverID: serverID)
        return req
    }

    // The sprite sleeps when nobody is reading and wakes on the first
    // request, which that request does not survive: the connection is
    // refused, or the gate answers 502 or 503 while the server comes up.
    // A request that never reached the server goes again, a few times, a
    // moment apart. One the server answered, or that timed out, does not:
    // the server may have taken it, and a capture sent twice is two drafts.

    /// The pauses between tries; one more try than pauses.
    nonisolated static let retryPauses: [TimeInterval] = [1, 2, 4]

    static func worthRetrying(_ error: Error) -> Bool {
        if let e = error as? URLError {
            switch e.code {
            case .cannotConnectToHost, .cannotFindHost, .dnsLookupFailed, .networkConnectionLost: return true
            default: return false
            }
        }
        if let s = error as? ServiceError, case .status(let code) = s { return gatewayDown(code) }
        return false
    }

    /// The gate is up and its server is not, or the platform is still
    /// bringing the sprite back.
    static func gatewayDown(_ code: Int) -> Bool { code == 502 || code == 503 || code == 504 }

    static func retrying<T>(pauses: [TimeInterval] = retryPauses, _ op: () async throws -> T) async throws -> T {
        var pauses = pauses[...]
        while true {
            do {
                return try await op()
            } catch {
                guard worthRetrying(error), let pause = pauses.popFirst() else { throw error }
                try? await Task.sleep(nanoseconds: UInt64(pause * 1_000_000_000))
            }
        }
    }

    /// One try: the status and the bytes, with a gateway's failure thrown
    /// so the retry sees it.
    private func fetch(_ req: URLRequest) async throws -> (Int, Data) {
        let (data, resp) = try await URLSession.shared.data(for: req)
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if Self.gatewayDown(code) { throw ServiceError.status(code) }
        return (code, data)
    }

    private func perform<T: Decodable>(_ req: URLRequest) async throws -> T {
        let (code, data) = try await Self.retrying { try await fetch(req) }
        guard code == 200 else {
            connected = false
            throw ServiceError.status(code)
        }
        connected = true
        return try JSONDecoder().decode(T.self, from: data)
    }

    private func get<T: Decodable>(_ path: String, timeout: TimeInterval) async throws -> T {
        try await perform(try request(path, timeout: timeout))
    }

    private func post<T: Decodable>(_ path: String, body: Data, timeout: TimeInterval) async throws -> T {
        try await send("POST", path, body: body, timeout: timeout)
    }

    /// A request whose status the caller reads itself (a 409 carries a body).
    private func sendRaw(_ method: String, _ path: String, body: Data?, timeout: TimeInterval) async throws -> (Int, Data) {
        var req = try request(path, timeout: timeout)
        req.httpMethod = method
        if let body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = body
        }
        do {
            return try await Self.retrying { try await fetch(req) }
        } catch ServiceError.status(let code) {
            return (code, Data())
        }
    }

    private func send<T: Decodable>(_ method: String, _ path: String, body: Data?, timeout: TimeInterval) async throws -> T {
        var req = try request(path, timeout: timeout)
        req.httpMethod = method
        if let body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = body
        }
        return try await perform(req)
    }
}

private struct Health: Decodable {
    struct LLM: Decodable { let connected: Bool }
    let llm: LLM
}
