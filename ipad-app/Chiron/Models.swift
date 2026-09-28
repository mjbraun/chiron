import Foundation

// Chapter payloads carry pieces the webview owns (html, beats) as opaque JSON
// and pieces native UI owns (check items) as typed models.

struct ChapterPayload: Codable, Identifiable {
    let unit: String
    let title: String
    let minutes: Int
    let html: String
    let beats: [JSONValue]
    let pretest: [CheckItem]
    let check: [CheckItem]
    // True for the placement screener and the calibration series: no reveal,
    // no gate verdict - the score is measurement.
    let calibration: Bool?
    let nextAction: String?

    var id: String { unit }
    var isCalibration: Bool { calibration == true }

    /// The chapter as words: tags gone, blocks kept as paragraph breaks,
    /// entities decoded. What a model reads when it stands in for the tutor.
    var plainText: String {
        var s = html.replacingOccurrences(of: "(?is)<(script|style)\\b.*?</\\1>", with: "", options: .regularExpression)
        s = s.replacingOccurrences(of: "(?i)</(p|div|h[1-6]|li|blockquote|pre|tr|section|article)>", with: "\n\n", options: .regularExpression)
        s = s.replacingOccurrences(of: "(?i)<br\\s*/?>", with: "\n", options: .regularExpression)
        s = s.replacingOccurrences(of: "<[^>]+>", with: "", options: .regularExpression)
        for (entity, char) in [("&amp;", "&"), ("&lt;", "<"), ("&gt;", ">"), ("&quot;", "\""), ("&#39;", "'"), ("&nbsp;", " ")] {
            s = s.replacingOccurrences(of: entity, with: char)
        }
        s = s.replacingOccurrences(of: "\n{3,}", with: "\n\n", options: .regularExpression)
        return s.trimmingCharacters(in: .whitespacesAndNewlines)
    }
    /// The placement screener travels as a one-item calibration chapter.
    var screener: CheckItem? {
        check.count == 1 && check[0].check == "screener" ? check[0] : nil
    }

    enum CodingKeys: String, CodingKey {
        case unit, title, minutes, html, beats, pretest, check, calibration
        case nextAction = "next_action"
    }

    /// A chapter with no pretest arrives as `pretest: null` from some
    /// server paths and `[]` from others; both mean none.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        unit = try c.decode(String.self, forKey: .unit)
        title = try c.decode(String.self, forKey: .title)
        minutes = try c.decodeIfPresent(Int.self, forKey: .minutes) ?? 0
        html = try c.decodeIfPresent(String.self, forKey: .html) ?? ""
        beats = try c.decodeIfPresent([JSONValue].self, forKey: .beats) ?? []
        pretest = try c.decodeIfPresent([CheckItem].self, forKey: .pretest) ?? []
        check = try c.decodeIfPresent([CheckItem].self, forKey: .check) ?? []
        calibration = try c.decodeIfPresent(Bool.self, forKey: .calibration)
        nextAction = try c.decodeIfPresent(String.self, forKey: .nextAction)
    }
}

struct CheckItem: Codable, Identifiable {
    let id: String
    let unit: String?
    let concept: String?
    let kind: String            // constructed | mcq
    let prompt: String
    let check: String
    let difficulty: String?
    let options: [MCQOption]?
    let reveal: Reveal?

    struct MCQOption: Codable { let text: String }
    struct Reveal: Codable {
        let answer: String?
        let rubric: String?
        let options: [OptionReveal]?
        struct OptionReveal: Codable {
            let explain: String
            let correct: Bool
        }

        // Compute items answer with a bare number (`answer: 6`). Strict String
        // decoding turns one such item into a total decode failure for the
        // whole book, so accept either shape and normalise to text. The server
        // also stringifies these; this is the belt to that suspenders.
        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            answer = try c.decodeLenientString(forKey: .answer)
            rubric = try c.decodeLenientString(forKey: .rubric)
            options = try c.decodeIfPresent([OptionReveal].self, forKey: .options)
        }
    }
}

struct ItemResponse: Codable {
    let itemId: String
    var response: String?
    var selectedIndex: Int?
    var confidence: Int
    // An explicit "I don't know - move on". The server grades it as a fail
    // without a model call.
    var idk: Bool?
    /// A handwritten answer. Never on the exchange wire: a check with any
    /// ink goes through the ink check-in, where the server rasterizes and
    /// transcribes it and keeps the audit trail.
    var ink: InkAnswer?

    enum CodingKeys: String, CodingKey {
        case itemId = "item_id"
        case response
        case selectedIndex = "selected_index"
        case confidence
        case idk
    }
}

/// Strokes as normalized polylines (0..1 in the box they were drawn in),
/// with the box's width/height ratio so the raster is not distorted.
struct InkAnswer: Codable, Equatable {
    struct Point: Codable, Equatable {
        let x: Double
        let y: Double
    }
    var strokes: [[Point]]
    var aspect: Double
}

/// POST /ink/{subject}: one item per answer, typed or inked or chosen or
/// passed on; the response is an exchange response plus the transcripts.
struct InkSubmission: Codable {
    struct Item: Codable {
        let itemId: String
        var strokes: [[InkAnswer.Point]]
        var selectedIndex: Int?
        var idk: Bool?
        var text: String?
        var confidence: Int?
        var aspect: Double?

        enum CodingKeys: String, CodingKey {
            case strokes, idk, text, confidence, aspect
            case itemId = "item_id"
            case selectedIndex = "selected_index"
        }
    }
    let unit: String
    let items: [Item]
    var chunkMinutes: Double?

    enum CodingKeys: String, CodingKey {
        case unit, items
        case chunkMinutes = "chunk_minutes"
    }

    /// Every response becomes an item; the ink travels as strokes, a typed
    /// answer as text, a choice as its index, and a pass as the flag.
    init(unit: String, responses: [ItemResponse], chunkMinutes: Double?) {
        self.unit = unit
        self.chunkMinutes = chunkMinutes
        items = responses.map { r in
            Item(itemId: r.itemId,
                 strokes: r.ink?.strokes ?? [],
                 selectedIndex: r.selectedIndex,
                 idk: r.idk == true ? true : nil,
                 text: r.ink == nil ? r.response : nil,
                 confidence: r.confidence,
                 aspect: r.ink?.aspect)
        }
    }
}

struct BeatResponse: Codable {
    let beatId: String
    let response: String
    var selfVerdict: String?
    var mechanicalVerdict: String?
    var selectedIndex: Int?

    enum CodingKeys: String, CodingKey {
        case beatId = "beat_id"
        case response
        case selfVerdict = "self_verdict"
        case mechanicalVerdict = "mechanical_verdict"
        case selectedIndex = "selected_index"
    }
}

struct SubjectInfo: Codable, Identifiable {
    let id: String
    let title: String
    let unitsTotal: Int?
    let unitsCleared: Int?
    let currentUnit: String?
    let debt: Int?
    /// "book" or "primer"; a server before primers sends nothing.
    let kind: String?
    /// Primers only: planning | building | authoring | ready | failed, why
    /// it failed, and where the capture came from.
    let status: String?
    let error: String?
    let source: PrimerSource?
    let capturedAt: String?
    /// Drafts only: what the capture is to become ("primer" or "book"),
    /// the book it is becoming, and how far that is.
    let scale: String?
    let book: String?
    let progress: String?
    /// The shelf it is filed on; none means the top of the library.
    let shelf: String?
    /// Documents only: pages in the PDF and the page last read.
    let pages: Int?
    let page: Int?
    /// Followed blogs only: posts not opened on any device.
    let unread: Int?
    /// When it last changed or was last opened (RFC3339), for ordering the
    /// shelf by recency; a server before this sends nothing.
    let updatedAt: String?

    init(id: String, title: String, unitsTotal: Int? = nil, unitsCleared: Int? = nil, currentUnit: String? = nil,
         debt: Int? = nil, kind: String? = nil, status: String? = nil, error: String? = nil,
         source: PrimerSource? = nil, capturedAt: String? = nil, scale: String? = nil, book: String? = nil,
         progress: String? = nil, shelf: String? = nil, pages: Int? = nil, page: Int? = nil,
         unread: Int? = nil, updatedAt: String? = nil) {
        self.id = id; self.title = title; self.unitsTotal = unitsTotal; self.unitsCleared = unitsCleared
        self.currentUnit = currentUnit; self.debt = debt; self.kind = kind; self.status = status
        self.error = error; self.source = source; self.capturedAt = capturedAt
        self.scale = scale; self.book = book; self.progress = progress
        self.shelf = shelf.flatMap { $0.isEmpty ? nil : $0 }
        self.pages = pages; self.page = page; self.unread = unread; self.updatedAt = updatedAt
    }

    /// The recency stamp as a moment; nil when the server sent none.
    var updated: Date? {
        guard let raw = updatedAt else { return nil }
        if let date = ISO8601DateFormatter().date(from: raw) { return date }
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fractional.date(from: raw)
    }

    var isPrimer: Bool { kind == "primer" }
    var isPDF: Bool { kind == "pdf" }
    /// A book the reader imported and reads as it is.
    var isReading: Bool { kind == "reading" }
    /// A blog the reader follows, whose chapters are its posts.
    var isFeed: Bool { kind == "feed" }
    /// The server is writing it: a primer being authored, a book being built.
    var authoring: Bool { isPrimer && (status == "authoring" || status == "building") }
    var failed: Bool { isPrimer && status == "failed" }
    /// A capture still being planned, or a draft that failed and can be
    /// planned again: opens the planning card, not a book.
    var drafting: Bool { isPrimer && scale != nil && (status == "planning" || status == "failed") }
    var building: Bool { isPrimer && status == "building" }

    var progressLine: String {
        if isPDF {
            guard let n = pages, n > 0 else { return "PDF" }
            if let p = page, p > 0 { return "page \(p + 1) of \(n)" }
            return "\(n) pages"
        }
        if drafting { return "Planning the \(scale == "book" ? "book" : "primer") · \(sourceLine)" }
        if building { return progress.map { "Building the book · \($0)" } ?? "Building the book" }
        if isPrimer { return sourceLine }
        if isFeed {
            let posts = unitsTotal ?? 0
            guard let n = unread, n > 0 else { return "\(posts) posts · all read" }
            return "\(n) unread of \(posts)"
        }
        var parts: [String] = []
        if let c = unitsCleared, let t = unitsTotal { parts.append("\(c)/\(t) units") }
        if let u = currentUnit { parts.append("reading \(u)") }
        if let d = debt, d > 0 { parts.append("\(d) in debt") }
        return parts.isEmpty ? "not started" : parts.joined(separator: " · ")
    }

    /// Where a primer came from and when: "from Safari · Sep 3".
    var sourceLine: String {
        var parts: [String] = []
        if let app = source?.app, !app.isEmpty {
            parts.append("from \(app)")
        } else if let url = source?.url, let host = URLComponents(string: url)?.host {
            parts.append("from \(host)")
        } else {
            parts.append("captured")
        }
        if let raw = capturedAt, let date = ISO8601DateFormatter().date(from: raw) {
            parts.append(date.formatted(.dateTime.month(.abbreviated).day()))
        }
        return parts.joined(separator: " · ")
    }

    enum CodingKeys: String, CodingKey {
        case id, title, debt, kind, status, error, source, scale, book, progress, shelf, pages, page, unread
        case unitsTotal = "units_total"
        case unitsCleared = "units_cleared"
        case currentUnit = "current_unit"
        case capturedAt = "captured_at"
        case updatedAt = "updated_at"
    }
}

struct PrimerSource: Codable, Equatable {
    let text: String?
    let url: String?
    let app: String?
}

/// How much a capture asks for.
enum CaptureScale: String, CaseIterable, Codable {
    case summary, detail, primer, book

    var label: String {
        switch self {
        case .summary: return "Summary"
        case .detail: return "Detail"
        case .primer: return "Primer"
        case .book: return "Smart book"
        }
    }

    /// Answered in the card, or planned and built.
    var immediate: Bool { self == .summary || self == .detail }

    var footer: String {
        switch self {
        case .summary: return "A paragraph answering the question, here in the card."
        case .detail: return "A few paragraphs, here in the card: more than a summary, without waiting for a primer."
        case .primer: return "A short document on the shelf. The tutor asks a question or two first, so it is the primer you meant; margin notes extend it later."
        case .book: return "A whole book with checks, as \"Teach me something else\" makes. The tutor asks what you want from it first; writing it takes a while."
        }
    }
}

/// POST /primer/capture: what was captured, what the reader asks of it,
/// and how much they want back.
struct CaptureRequest: Codable {
    var text: String?
    var imagePngB64: String?
    var sourceUrl: String?
    var sourceApp: String?
    var prompt: String
    var title: String?
    var scale: CaptureScale = .primer

    enum CodingKeys: String, CodingKey {
        case text, prompt, title, scale
        case imagePngB64 = "image_png_b64"
        case sourceUrl = "source_url"
        case sourceApp = "source_app"
    }
}

/// The capture's reply, and each planning turn's: an answer for a summary
/// or a detail; for a draft, the tutor's line, and the brief once
/// the tutor has one.
struct CaptureResponse: Codable {
    var scale: String?
    var answerMd: String?
    var subject: String?
    var status: String?
    var title: String?
    var replyMd: String?
    var done: Bool?
    var brief: String?

    enum CodingKeys: String, CodingKey {
        case scale, subject, status, title, done, brief
        case answerMd = "answer_md"
        case replyMd = "reply_md"
    }
}

/// One line of a draft's planning conversation.
struct PlanMessage: Codable, Equatable {
    let role: String    // learner | tutor
    let text: String
}

/// GET /primer/{id}/plan: a draft as the planning card shows it.
struct PlanState: Codable, Identifiable {
    let id: String
    var title: String
    let scale: String
    var status: String
    var error: String?
    let prompt: String
    let source: PrimerSource?
    var brief: String?
    var done: Bool
    var plan: [PlanMessage]
    var book: String?

    var isBook: Bool { scale == "book" }

    /// The whole conversation as plain text: what was captured, what was
    /// asked, everything said since, and the brief if there is one. This is
    /// what goes on the clipboard, so it has to stand on its own in another
    /// app - or in another agent's prompt - with no Chiron around it.
    var transcript: String {
        var out = [title]
        if let url = source?.url, !url.isEmpty { out.append(url) }
        if let text = source?.text, !text.isEmpty { out.append("\nCaptured:\n" + text) }
        out.append("\nAsked:\n" + prompt)
        if !plan.isEmpty {
            out.append("")
            for m in plan { out.append((m.role == "tutor" ? "Tutor: " : "You: ") + m.text) }
        }
        if let brief, !brief.isEmpty { out.append("\nThe brief:\n" + brief) }
        return out.joined(separator: "\n")
    }
}

/// POST /primer/{id}/build: what the draft is becoming.
struct BuildResponse: Codable {
    let subject: String
    let status: String
    let title: String?
    let book: String?
}

/// POST /primer/{id}/extend: the document with its new section.
struct ExtendResponse: Codable {
    let chapter: ChapterPayload
    let heading: String
    let entries: Int
}

/// GET /subjects: the shelf, and which book the reader last had open.
/// A shelf: a named folder in the library, and what is on it.
struct ShelfInfo: Codable, Identifiable, Hashable {
    let id: String
    var name: String
    var subjects: [String]

    init(id: String, name: String, subjects: [String] = []) {
        self.id = id; self.name = name; self.subjects = subjects
    }
}

struct SubjectsResponse: Codable {
    let subjects: [SubjectInfo]
    let active: String?
    /// A server before shelves sends none.
    var shelves: [ShelfInfo]? = nil

    /// The server sends "" before any book has been opened.
    var activeID: String? { (active?.isEmpty ?? true) ? nil : active }
}

struct ExchangeRequest: Codable {
    var subject: String = "ai"
    var phase: String = "boundary"
    var unit: String?
    var beatResponses: [BeatResponse] = []
    var pretestResponses: [ItemResponse] = []
    var checkResponses: [ItemResponse] = []
    var override: Bool = false
    var skippedCheck: Bool = false
    var catchMeUp: Bool = false
    var choice: String?
    var chunkMinutes: Double?
    var breakMinutes: Double?
    // Grades come back at once; the next chapter is authored in the
    // background and fetched from /chapter. A request that has to outlive
    // the app being backgrounded is a request that gets lost.
    var async: Bool = true

    enum CodingKeys: String, CodingKey {
        case subject, phase, unit, override, choice, async
        case beatResponses = "beat_responses"
        case pretestResponses = "pretest_responses"
        case checkResponses = "check_responses"
        case skippedCheck = "skipped_check"
        case catchMeUp = "catch_me_up"
        case chunkMinutes = "chunk_minutes"
        case breakMinutes = "break_minutes"
    }
}

struct ExchangeResponse: Codable {
    let results: [GradeResult]
    let gate: Gate?
    let chapter: ChapterPayload?
    let state: BookState
    let breakSuggestion: BreakSuggestion?
    /// The unit being authored in the background, when no chapter came.
    let authoring: String?
    /// The graded check as the typeset results pages say it.
    let resultsDoc: ResultsDoc?

    enum CodingKeys: String, CodingKey {
        case results, gate, chapter, state, authoring
        case breakSuggestion = "break_suggestion"
        case resultsDoc = "results_doc"
    }
}

/// GET /chapter/{subject}: the persisted current chapter, if any, and
/// whether the server is still writing the next one.
struct ChapterStatus: Codable {
    let chapter: ChapterPayload?
    let authoring: Bool
    let authoringError: String?
    /// While authoring: where the build is (planning, writing, pages) and
    /// how many seconds ago it began.
    let authoringStage: String?
    let authoringSeconds: Int?

    init(chapter: ChapterPayload?, authoring: Bool, authoringError: String?,
         authoringStage: String? = nil, authoringSeconds: Int? = nil) {
        self.chapter = chapter; self.authoring = authoring; self.authoringError = authoringError
        self.authoringStage = authoringStage; self.authoringSeconds = authoringSeconds
    }

    enum CodingKeys: String, CodingKey {
        case chapter, authoring
        case authoringError = "authoring_error"
        case authoringStage = "authoring_stage"
        case authoringSeconds = "authoring_seconds"
    }
}

/// The results document: headline, framing copy, and one entry per graded
/// item with the full audit trail. The same content the tablet typesets.
struct ResultsDoc: Codable {
    let unit: String
    let headLeft: String
    let calibration: Bool?
    let score: Double
    let gate: Double
    let passed: Bool
    let extensionUnlocked: Bool?
    let action: String
    let dek: String
    let headline: String
    let tally: String?
    let entries: [ResultsEntry]

    var isCalibration: Bool { calibration == true }

    enum CodingKeys: String, CodingKey {
        case unit, calibration, score, gate, passed, action, dek, headline, tally, entries
        case headLeft = "head_left"
        case extensionUnlocked = "extension_unlocked"
    }
}

struct ResultsEntry: Codable, Identifiable {
    let n: Int
    let verdict: String
    let idk: Bool?
    let kind: String
    let prompt: String
    let confidence: Int?
    let readAs: String?
    let chose: String?
    let answer: String?
    let why: String?
    /// The reader's concern, if they flagged the item during the check.
    let flag: String?

    var id: Int { n }
    var isIDK: Bool { idk == true }
    var passed: Bool { verdict == "pass" || verdict == "valid_alternative_path" }

    enum CodingKeys: String, CodingKey {
        case n, verdict, idk, kind, prompt, confidence, chose, answer, why, flag
        case readAs = "read_as"
    }
}

struct GradeResult: Codable, Identifiable {
    let itemId: String
    let verdict: String
    let misconceptions: [String]?
    let feedbackMd: String?
    let confidence: Int?

    var id: String { itemId }
    var passed: Bool { verdict == "pass" || verdict == "valid_alternative_path" }

    enum CodingKeys: String, CodingKey {
        case itemId = "item_id"
        case verdict, misconceptions, confidence
        case feedbackMd = "feedback_md"
    }
}

struct Gate: Codable {
    let score: Double?
    let passed: Bool
    let gate: Double
    let extensionUnlocked: Bool
    // A calibration result: always passed, the score is measurement.
    var calibration: Bool?

    enum CodingKeys: String, CodingKey {
        case score, passed, gate, calibration
        case extensionUnlocked = "extension_unlocked"
    }
}

struct BookState: Codable {
    let spine: [SpineEntry]
    let fringe: [String]
    let debt: [JSONValue]
    let activeMisconceptions: [String]
    let summary: String
    let sessionMinutes: Double

    enum CodingKeys: String, CodingKey {
        case spine, fringe, debt, summary
        case activeMisconceptions = "active_misconceptions"
        case sessionMinutes = "session_minutes"
    }
}

struct SpineEntry: Codable, Identifiable {
    let unit: String
    let title: String
    let status: String   // locked|available|active|passed|overridden|failed
    let score: Double?
    let inFringe: Bool

    var id: String { unit }

    enum CodingKeys: String, CodingKey {
        case unit, title, status, score
        case inFringe = "in_fringe"
    }
}

struct BreakSuggestion: Codable {
    let minutes: Int
    let kind: String
    let note: String
}

extension KeyedDecodingContainer {
    /// Decode a field that should be text but may arrive as a number or bool.
    func decodeLenientString(forKey key: Key) throws -> String? {
        if let s = try? decodeIfPresent(String.self, forKey: key) { return s }
        if let d = try? decode(Double.self, forKey: key) {
            return d == d.rounded() ? String(Int(d)) : String(d)
        }
        if let b = try? decode(Bool.self, forKey: key) { return b ? "true" : "false" }
        return nil
    }
}

// Minimal JSON passthrough for payloads the webview owns.
enum JSONValue: Codable {
    case null, bool(Bool), number(Double), string(String)
    case array([JSONValue]), object([String: JSONValue])

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let b = try? c.decode(Bool.self) { self = .bool(b) }
        else if let n = try? c.decode(Double.self) { self = .number(n) }
        else if let s = try? c.decode(String.self) { self = .string(s) }
        else if let a = try? c.decode([JSONValue].self) { self = .array(a) }
        else { self = .object(try c.decode([String: JSONValue].self)) }
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case .bool(let b): try c.encode(b)
        case .number(let n): try c.encode(n)
        case .string(let s): try c.encode(s)
        case .array(let a): try c.encode(a)
        case .object(let o): try c.encode(o)
        }
    }
}

/// POST /ask/{subject}: the tutor's answer to a question about a passage.
struct AskResponse: Codable {
    let unit: String
    let answerMd: String

    enum CodingKeys: String, CodingKey {
        case unit
        case answerMd = "answer_md"
    }
}

/// The server's word that a flag is on record: the item, its unit, and
/// the flag's place in the learner's log.
struct FlagResponse: Codable {
    let unit: String
    let item: String
    let n: Int
}

/// A mark on the page: a run of the chapter's text, by character offsets
/// into the article's text content, that is highlighted or carries a
/// question. Offsets survive re-rendering because the page's text does.
struct Mark: Codable, Identifiable, Equatable {
    enum Kind: String, Codable {
        case highlight
        case question
        /// A primer's margin note: the passage, the note, and the heading
        /// of the section it added.
        case note
    }
    let id: String
    let kind: Kind
    let start: Int
    let end: Int
    let text: String
    var question: String?
    var answer: String?
    /// Follow-ups after the first question, oldest first.
    var thread: [QA]?

    /// Every turn so far, the first question included, for a follow-up's
    /// history: only turns that were answered.
    var history: [QA] {
        var turns: [QA] = []
        if let q = question, let a = answer { turns.append(QA(question: q, answer: a)) }
        for t in thread ?? [] where t.answer != nil { turns.append(t) }
        return turns
    }
}

/// One question and its answer in a thread on a passage.
struct QA: Codable, Equatable {
    var question: String
    var answer: String?
}

/// A key sshd on the sprite accepts; the app's own shows up here after
/// enrolment beside the Mac's.
struct EnrolledKey: Codable, Identifiable, Equatable {
    let fingerprint: String
    let type: String
    let name: String
    var id: String { fingerprint }
}

struct EnrolResponse: Codable {
    let fingerprint: String
    let name: String
    let installed: Bool
}

/// One unit's marks, ink and reading position as the server keeps them,
/// versioned so two devices that both changed it are caught.
struct Annotations: Codable, Equatable {
    var version: Int
    var updatedAt: String?
    var device: String?
    var marks: [Mark]
    var inkB64: String?
    var position: Double

    enum CodingKeys: String, CodingKey {
        case version, device, marks, position
        case updatedAt = "updated_at"
        case inkB64 = "ink_b64"
    }

    var ink: Data? { inkB64.flatMap { Data(base64Encoded: $0) } }
}

/// What a put comes back with: stored at a version, or the server's copy
/// because both changed.
enum AnnotationsPut: Equatable {
    case stored(Annotations)
    case conflict(server: Annotations)
}

/// The merge the server proposes for two copies: every mark, the further
/// position, and both inks when they differ, for the device to lay one
/// over the other.
struct ReconciledAnnotations: Codable, Equatable {
    var version: Int
    var marks: [Mark]
    var position: Double
    var inkB64: String?
    var inkOtherB64: String?

    enum CodingKeys: String, CodingKey {
        case version, marks, position
        case inkB64 = "ink_b64"
        case inkOtherB64 = "ink_other_b64"
    }
}

/// A book the reader imported, as the server made it: a subject whose
/// chapters are the book's own.
struct ImportedBook: Codable, Equatable {
    let id: String
    let title: String
    let chapters: Int
}

/// What a check of the followed blogs came back with.
struct FeedCheck: Codable, Equatable {
    let checked: Int
    let added: Int
}

/// A PDF the server keeps for the shelf, with where the reader is in it.
struct Document: Codable, Equatable {
    let id: String
    let title: String
    let pages: Int
    var page: Int
    var position: Double

    init(id: String, title: String, pages: Int, page: Int = 0, position: Double = 0) {
        self.id = id; self.title = title; self.pages = pages; self.page = page; self.position = position
    }
}

/// The ink on one page of a document, versioned like a unit's annotations.
struct PageInk: Codable, Equatable {
    let version: Int
    let inkB64: String

    enum CodingKeys: String, CodingKey {
        case version
        case inkB64 = "ink_b64"
    }
}

enum PageInkPut: Equatable {
    case stored(PageInk)
    case conflict(server: PageInk)
}

/// A build of this app made on the MacBook and offered by the server:
/// what it is, and where an installer finds it.
struct AppBuild: Codable, Equatable {
    var version: String
    var build: Int
    var commit: String
    var status: String
    var manifestPath: String
    var macPath: String

    enum CodingKeys: String, CodingKey {
        case version, build, commit, status
        case manifestPath = "manifest_path"
        case macPath = "mac_path"
    }

    var label: String { "Chiron \(version) (\(build))" }
}

/// A change asked of the development agent on the sprite, and what it
/// has done about it so far (SPRITE-DEV-PLAN.md phase G).
/// Something a machine behind the book posted for the reader, such as the
/// build Mac gone onto battery.
struct MachineAlert: Codable, Equatable {
    var source: String
    var text: String
    /// When the server took it (RFC3339), and where the next look starts.
    var at: String
}

struct ChangeRequest: Codable, Identifiable, Equatable {
    var id: String
    var text: String
    var status: String
    var createdAt: String
    var log: [String]
    var last: String?
    var summary: String?
    var reason: String?
    var commit: String?
    var build: Build?
    /// What the agent stopped to ask, while it waits on the reader; and
    /// every question it asked before, with the answer it got.
    var question: String? = nil
    var thread: [Exchange]? = nil

    struct Build: Codable, Equatable {
        var version: String
        var build: Int
        var token: String
    }

    struct Exchange: Codable, Equatable {
        var question: String
        var answer: String
        var at: String?
    }

    enum CodingKeys: String, CodingKey {
        case id, text, status, log, last, summary, reason, commit, build, question, thread
        case createdAt = "created_at"
    }

    /// Stopped on a question for the reader.
    var waiting: Bool { status == "waiting" }

    /// Still in the agent's hands.
    var open: Bool { status != "ready" && status != "failed" && !waiting }
}
