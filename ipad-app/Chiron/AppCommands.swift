import Foundation
import GameController
import PencilKit
import UIKit

#if DEBUG
/// Why the keyboard is up or down, for the harness and the agent link.
/// iPadOS shows no software keyboard while a hardware one is attached, and
/// none for a view that does not hold first responder; both look the same
/// to a reader who cannot type.
@MainActor
final class KeyboardProbe {
    static let shared = KeyboardProbe()
    private(set) var up = false

    func start() {
        let c = NotificationCenter.default
        c.addObserver(forName: UIResponder.keyboardDidShowNotification, object: nil, queue: .main) { _ in
            Task { @MainActor in KeyboardProbe.shared.up = true }
        }
        c.addObserver(forName: UIResponder.keyboardDidHideNotification, object: nil, queue: .main) { _ in
            Task { @MainActor in KeyboardProbe.shared.up = false }
        }
    }

    /// A hardware keyboard, which iPadOS lets stand in for the software one.
    var hardware: Bool { GCKeyboard.coalesced != nil }

    /// What holds first responder, which is what the keyboard follows.
    var firstResponder: String {
        guard let window = UIApplication.shared.connectedScenes
            .compactMap({ $0 as? UIWindowScene }).flatMap(\.windows).first(where: \.isKeyWindow) else { return "no window" }
        return Self.holder(in: window).map { String(describing: type(of: $0)) } ?? "none"
    }

    private static func holder(in view: UIView) -> UIView? {
        if view.isFirstResponder { return view }
        for sub in view.subviews {
            if let found = holder(in: sub) { return found }
        }
        return nil
    }
}
#endif

/// The verbs a script or the sprite's agent can run against the app. They
/// drive the same session calls the buttons do, so every screen is reached
/// the way a learner reaches it. The debug harness and the agent link both
/// dispatch here.
@MainActor
enum AppCommands {
    enum Failure: Error, CustomStringConvertible {
        case unknownVerb(String), noBook, badArguments(String)
        var description: String {
            switch self {
            case .unknownVerb(let v): return "unknown verb \(v)"
            case .noBook: return "no book is open"
            case .badArguments(let why): return why
            }
        }
    }

    /// Run one verb; the result is the app's state afterwards, or for
    /// verbs that answer a question, that answer.
    static func run(_ verb: String, args: [String: Any], library: Library) async throws -> [String: Any] {
        switch verb {
        case "state":
            break
        case "open":
            guard let id = args["subject"] as? String else { throw Failure.badArguments("open needs subject") }
            await library.open(id)
        case "shelf":
            library.closeBook()
            await library.refresh()
        case "screenshot":
            return ["png_b64": try screenshot().base64EncodedString()]
        case "server":
            guard let url = args["url"] as? String else { throw Failure.badArguments("server needs url") }
            await library.adopt(ServerLink(name: args["name"] as? String ?? "", url: url, key: args["key"] as? String))
        case "server/url":
            // What the Camera app hands over: a chiron://server link.
            guard let raw = args["url"] as? String, let url = URL(string: raw), let link = ServerLink(url) else {
                throw Failure.badArguments("server/url needs a chiron://server?url=... link")
            }
            await library.adopt(link)
        case "settings":
            library.settingsShown = args["open"] as? Bool ?? true
        case "server/setup":
            library.deviceSetupShown = args["shown"] as? Bool ?? true
        case "shelf/create":
            guard let name = args["name"] as? String else { throw Failure.badArguments("shelf/create needs name") }
            await library.createShelf(named: name)
        case "shelf/rename":
            guard let id = args["id"] as? String, let name = args["name"] as? String else {
                throw Failure.badArguments("shelf/rename needs id and name")
            }
            await library.renameShelf(id, to: name)
        case "shelf/delete":
            guard let id = args["id"] as? String else { throw Failure.badArguments("shelf/delete needs id") }
            await library.deleteShelf(id)
        case "shelf/open":
            guard let id = args["id"] as? String, library.shelf(id) != nil else { throw Failure.badArguments("shelf/open needs the id of a shelf") }
            library.closeBook()
            library.scope = .shelf(id)
        case "shelf/close":
            library.scope = .all
        case "shelf/order":
            guard let raw = args["order"] as? String, let order = LibrarySort(rawValue: raw) else {
                throw Failure.badArguments("shelf/order needs order: recent | title | kind | unread")
            }
            library.order = order
        case "library/show":
            // What the sidebar picks: all, a kind (book, primer, feed,
            // reading, pdf), or shelf:<id>.
            guard let raw = args["scope"] as? String, let scope = LibraryScope(harness: raw) else {
                throw Failure.badArguments("library/show needs scope: all, a kind, or shelf:<id>")
            }
            library.closeBook()
            library.scope = scope
        case "import":
            guard let path = args["path"] as? String else { throw Failure.badArguments("import needs path") }
            await library.importPDF(at: URL(fileURLWithPath: path))
        case "pdf/page":
            guard let d = library.document else { throw Failure.noBook }
            guard let page = args["page"] as? Int else { throw Failure.badArguments("pdf/page needs page") }
            d.go(to: page)
        case "pdf/capture":
            guard let d = library.document else { throw Failure.noBook }
            d.captureRequested = (args["text"] as? String) ?? ""
        case "pdf/tool":
            guard let d = library.document else { throw Failure.noBook }
            guard let tool = (args["tool"] as? String).flatMap(DocumentSession.Tool.init(rawValue:)) else { throw Failure.badArguments("pdf/tool needs select, pen or eraser") }
            d.tool = tool
        case "pdf/select":
            // A Pencil drag under the select tool, from one page point to another.
            guard let d = library.document else { throw Failure.noBook }
            guard let page = args["page"] as? Int, let from = args["from"] as? [Double], let to = args["to"] as? [Double],
                  from.count == 2, to.count == 2 else {
                throw Failure.badArguments("pdf/select needs page, from [x,y] and to [x,y]")
            }
            d.selectRequested = DocumentSession.SelectRequest(page: page, from: CGPoint(x: from[0], y: from[1]), to: CGPoint(x: to[0], y: to[1]))
        case "pdf/undo":
            guard let d = library.document else { throw Failure.noBook }
            d.undo()
        case "pdf/stroke":
            // A stroke in page points, as a Pencil would leave it.
            guard let d = library.document else { throw Failure.noBook }
            guard let page = args["page"] as? Int, let points = args["points"] as? [[Double]], points.count >= 2 else {
                throw Failure.badArguments("pdf/stroke needs page and points [[x,y],...]")
            }
            let path = PKStrokePath(controlPoints: points.map {
                PKStrokePoint(location: CGPoint(x: $0[0], y: $0[1]), timeOffset: 0, size: CGSize(width: 3, height: 3), opacity: 1, force: 1, azimuth: 0, altitude: .pi / 2)
            }, creationDate: Date())
            let stroke = PKStroke(ink: PKInk(.pen, color: Ink.onPaper), path: path)
            d.drew(on: page, (d.ink[page] ?? PKDrawing()).appending(PKDrawing(strokes: [stroke])))
        case "move":
            guard let subject = args["subject"] as? String else { throw Failure.badArguments("move needs subject") }
            let shelf = (args["shelf"] as? String).flatMap { $0.isEmpty ? nil : $0 }
            await library.move(subject, to: shelf)
        case "breaks":
            library.breakTime = args["on"] as? Bool ?? true
        case "agent":
            let on = args["on"] as? Bool ?? true
            library.agent.enabled = on
            if on { library.agent.start() } else { library.agent.stop() }
            try? await Task.sleep(nanoseconds: 500_000_000)
        case "capture":
            guard let prompt = args["prompt"] as? String else { throw Failure.badArguments("capture needs prompt") }
            let scale = CaptureScale(rawValue: args["scale"] as? String ?? "primer")
            guard let scale else { throw Failure.badArguments("scale must be summary, detail, primer or book") }
            let c = Capture(text: args["text"] as? String ?? "", sourceURL: args["url"] as? String, sourceApp: args["app"] as? String)
            let reply = try await library.submitCapture(c, prompt: prompt, scale: scale)
            var out = state(library)
            out["subject"] = reply.subject ?? ""
            out["answer"] = reply.answerMd ?? ""
            return out
        case "read":
            // "Read a link", as the shelf and a shared page send it.
            guard let url = args["url"] as? String else { throw Failure.badArguments("read needs url") }
            await library.readPage(url)
            if let error = library.shelfError { throw Failure.badArguments(error) }
        case "follow":
            // "Follow the blog", as the shelf sends it.
            guard let url = args["url"] as? String else { throw Failure.badArguments("follow needs url") }
            await library.followFeed(url)
            if let error = library.shelfError { throw Failure.badArguments(error) }
        case "feeds":
            // What the app asks the server when the reader opens it.
            let check = try await library.service.checkFeeds()
            await library.refresh()
            return ["checked": check.checked, "added": check.added,
                    "unread": library.subjects.filter(\.isFeed).reduce(0) { $0 + ($1.unread ?? 0) }]
        case "forget":
            guard let subject = args["subject"] as? String else { throw Failure.badArguments("forget needs subject") }
            await library.forget(subject)
            if let error = library.shelfError { throw Failure.badArguments(error) }
        case "draft/open":
            guard let id = args["id"] as? String else { throw Failure.badArguments("draft/open needs id") }
            await library.openDraft(id)
        case "plan":
            guard let text = args["text"] as? String else { throw Failure.badArguments("plan needs text") }
            await library.planReply(text)
        case "build":
            await library.buildDraft()
        case "discard":
            await library.discardDraft()
        case "capture/card":
            // The card as the share extension would open it.
            let c = Capture(text: args["text"] as? String ?? "", sourceURL: args["url"] as? String, sourceApp: args["app"] as? String)
            try CaptureInbox.write(c)
            library.receiveCapture(id: c.id)
        case "request":
            // "Request a change", as the card sends it.
            guard let text = args["text"] as? String else { throw Failure.badArguments("request needs text") }
            guard let r = await library.requestChange(text, withPicture: args["picture"] as? Bool ?? true) else {
                throw Failure.badArguments(library.requestError ?? "the request was not taken")
            }
            return ["id": r.id, "status": r.status]
        case "requests/card":
            library.requestsShown = args["open"] as? Bool ?? true
        case "requests":
            await library.refreshRequests()
            return ["requests": library.requests.map { ["id": $0.id, "status": $0.status, "last": $0.last ?? "", "summary": $0.summary ?? "", "question": $0.question ?? ""] }]
        case "requests/answer":
            // The answer to what the agent asked, as the card sends it.
            guard let id = args["id"] as? String, let answer = args["answer"] as? String else {
                throw Failure.badArguments("requests/answer needs id and answer")
            }
            guard await library.answerRequest(id, answer) else {
                throw Failure.badArguments(library.requestError ?? "the answer was not taken")
            }
        case "capture/close":
            library.pendingCapture = nil
            library.captureAnswer = nil
            library.planning = nil
        case "note":
            guard let s = library.session else { throw Failure.noBook }
            guard let text = args["text"] as? String, let note = args["note"] as? String else {
                throw Failure.badArguments("note needs text and note")
            }
            if await s.mark(text: text, kind: .note) != nil {
                await s.extend(note)
            }
        case "shell":
            library.shellShown = true
            try? await Task.sleep(nanoseconds: 300_000_000)
        case "shell/close":
            library.shell.close()
            library.shellShown = false
        case "shell/type":
            guard let text = args["text"] as? String else { throw Failure.badArguments("shell/type needs text") }
            library.shell.send(text: text)
            try? await Task.sleep(nanoseconds: 300_000_000)
        case "reader/rect":
            // Development only: where the first element matching a selector
            // is, in page-view points, for a test to tap.
            guard let s = library.session, let selector = args["selector"] as? String else { throw Failure.badArguments("reader/rect needs selector") }
            guard let r = await s.page?.rect(matching: selector) else { return ["found": false] }
            return ["found": true, "x": r.minX, "y": r.minY, "width": r.width, "height": r.height]
        case "reader/eval":
            // Development only: a line of JavaScript against the open page,
            // for scrolling to a beat or tapping an option in a walk.
            guard let s = library.session, let js = args["js"] as? String else { throw Failure.badArguments("reader/eval needs js") }
            return ["result": await s.page?.eval(js) ?? ""]
        case "mark/rect":
            guard let s = library.session, let id = args["id"] as? String else { throw Failure.badArguments("mark/rect needs id") }
            guard let r = await s.page?.rect(of: id) else { return ["found": false] }
            return ["found": true, "x": r.minX, "y": r.minY, "width": r.width, "height": r.height]
        case "shell/screen":
            var out: [String: Any] = ["phase": shellPhase(library.shell.phase)]
            #if DEBUG
            if let lines = ShellScreen.shared.lines() { out["lines"] = lines }
            #endif
            return out
        default:
            guard let session = library.session else { throw Failure.noBook }
            try await run(verb, args: args, session: session, llm: library.sync.llmConnected)
        }
        return state(library)
    }

    /// The verbs that act on the open book.
    static func run(_ verb: String, args: [String: Any], session: BookSession, llm: Bool) async throws {
        switch verb {
        case "start":
            await session.start()
        case "place":
            await session.place(level: args["level"] as? Int ?? 3)
        case "check":
            session.beginCheck()
        case "contents":
            session.contentsShown.toggle()
        case "chrome":
            session.toggleChrome()
        case "tool":
            guard let name = args["tool"] as? String, let t = BookSession.Tool(rawValue: name) else {
                throw Failure.badArguments("tool must be none | pen | highlighter | ask | note | capture | eraser")
            }
            session.tool = t
        case "pen":
            guard let name = args["color"] as? String, let c = BookSession.PenColor(rawValue: name) else {
                throw Failure.badArguments("pen color must be red | blue | green | black")
            }
            session.penColor = c
        case "passage":
            // The capture tool's drag, without the drag.
            guard let text = args["text"] as? String else { throw Failure.badArguments("passage needs text") }
            session.capturePassage(text)
        case "mark":
            guard let text = args["text"] as? String else { throw Failure.badArguments("mark needs text") }
            let kind: Mark.Kind = (args["kind"] as? String) == "question" ? .question : .highlight
            await session.mark(text: text, kind: kind)
        case "ask":
            guard let text = args["text"] as? String, let q = args["question"] as? String else {
                throw Failure.badArguments("ask needs text and question")
            }
            if await session.mark(text: text, kind: .question) != nil {
                await session.ask(q)
            }
        case "flag":
            // The reader's concern about an item: the one named, or the
            // first of the check.
            guard let ch = session.chapter else { throw Failure.noBook }
            guard let text = args["text"] as? String else { throw Failure.badArguments("flag needs text") }
            let id = args["item"] as? String
            guard let item = (ch.pretest + ch.check).first(where: { id == nil || $0.id == id }) else {
                throw Failure.badArguments("no item \(id ?? "") in this chapter")
            }
            try await session.flag(item: item, concern: text, response: nil)
        case "sync":
            // Push what changed here, or pull when nothing did.
            await session.pushAnnotations()
            await session.pullAnnotations()
        case "conflict/resolve":
            guard let raw = args["choice"] as? String, let choice = BookSession.Resolution(rawValue: raw) else {
                throw Failure.badArguments("conflict/resolve needs choice: mine, theirs or agent")
            }
            await session.resolveConflict(choice)
        case "close":
            session.closeAsking()
        case "delete":
            session.deleteAsking()
        case "unmark":
            guard let id = args["id"] as? String else { throw Failure.badArguments("unmark needs id") }
            session.removeMark(id)
        case "undo":
            session.undo()
        case "keep":
            // Take the whole book: every chapter and every picture.
            await session.keepOnDevice()
        case "answer":
            guard let ch = session.chapter else { throw Failure.noBook }
            await session.submitCheck(answers(for: ch, mode: args["mode"] as? String ?? "correct", llm: llm))
        case "proceed":
            if case .takingBreak = session.screen {
                await session.breakFinished(minutes: 0.1)
            } else {
                await session.proceed()
            }
        case "override":
            await session.override()
        case "reset":
            await session.startOver()
        case "retry":
            await session.retry()
        default:
            throw Failure.unknownVerb(verb)
        }
    }

    /// Answers for every item of the chapter: correct | idk | wrong | mixed
    /// (mixed inks one item, passes on one, types the rest).
    static func answers(for ch: ChapterPayload, mode: String, llm: Bool) -> [ItemResponse] {
        switch mode {
        case "idk":
            return ch.check.map {
                ItemResponse(itemId: $0.id, response: nil, selectedIndex: nil, confidence: 1, idk: true)
            }
        case "wrong":
            return SelfTest.answers(for: ch, weak: true, llm: llm)
        case "mixed":
            var inked = false, passed = false
            return SelfTest.answers(for: ch, weak: false, llm: llm).map { r in
                var r = r
                guard r.selectedIndex == nil else { return r }
                if !inked {
                    inked = true
                    r.response = nil
                    r.idk = nil
                    r.ink = InkAnswer(strokes: [
                        [InkAnswer.Point(x: 0.1, y: 0.3), InkAnswer.Point(x: 0.3, y: 0.7), InkAnswer.Point(x: 0.5, y: 0.3)],
                        [InkAnswer.Point(x: 0.6, y: 0.2), InkAnswer.Point(x: 0.6, y: 0.8)],
                    ], aspect: InkBox.aspect)
                } else if !passed {
                    passed = true
                    r.response = nil
                    r.idk = true
                    r.confidence = 1
                }
                return r
            }
        default:
            return SelfTest.answers(for: ch, weak: false, llm: llm)
        }
    }

    static func state(_ library: Library) -> [String: Any] {
        var out: [String: Any] = [
            // Which launch this app is, so a test can tell it apart from
            // one an earlier run left holding the same port.
            "nonce": SelfTest.argument("harness_nonce=") ?? "",
            "shelf": library.subjects.map(\.id),
            "active": library.activeSubjectID ?? "",
            "connected": library.sync.connected,
            "server_url": library.sync.baseURL,
            "servers": library.sync.servers.servers.map(\.name),
            "device_setup": library.deviceSetupShown,
            "last_url": library.lastOpenedURL ?? "",
            "agent": library.agent.connected,
            "capture_card": library.pendingCapture != nil,
            "build_offered": library.availableBuild?.label ?? "",
            "requests_card": library.requestsShown,
            "requests_waiting": library.waitingOnReader,
            "settings_card": library.settingsShown,
            "break_time": library.breakTime,
            "capture_answer": library.captureAnswer ?? "",
            "shelf_rows": library.subjects.map { ["id": $0.id, "kind": $0.kind ?? "book", "status": $0.status ?? "", "scale": $0.scale ?? "", "progress": $0.progress ?? "", "shelf": $0.shelf ?? "", "unread": $0.unread ?? 0, "updated": $0.updatedAt ?? ""] },
            "shelves": library.shelves.map { ["id": $0.id, "name": $0.name, "subjects": $0.subjects] },
            "open_shelf": { if case .shelf(let id) = library.scope { return id } else { return "" } }(),
            "scope": library.scope?.harness ?? "",
            "order": library.order.rawValue,
            "shelf_error": library.shelfError ?? "",
        ]
        if let d = library.document {
            out["document"] = ["id": d.id, "title": d.title, "page": d.page, "pages": d.pages, "tool": d.tool.rawValue,
                               "selection": d.selection,
                               "highlight": d.highlightProbe,
                               "ink_pages": d.ink.filter { !$0.value.strokes.isEmpty }.keys.sorted(),
                               "ink_strokes": d.ink.values.reduce(0) { $0 + $1.strokes.count },
                               "can_undo": d.canUndo,
                               "overlaid_pages": d.overlaidPages.sorted()]
        }
        if let p = library.planning {
            out["plan_card"] = ["id": p.id, "title": p.title, "scale": p.scale, "done": p.done, "turns": p.plan.count,
                                "last": p.plan.last?.text ?? "", "busy": library.planBusy, "error": library.planError ?? ""]
        }
        if let s = library.session {
            if let c = s.conflict {
                out["conflict"] = ["unit": c.unit, "mine_marks": c.mine.marks.count, "theirs_marks": c.theirs.marks.count,
                                   "theirs_version": c.theirs.version]
            }
            out["subject"] = s.subjectID
            out["kind"] = s.kind
            out["screen"] = screenName(s.screen)
            out["unit"] = s.chapter?.unit ?? ""
            out["items"] = s.chapter?.check.count ?? 0
            out["wait"] = s.wait?.rawValue ?? ""
            out["authoring_stage"] = s.authoringStage ?? ""
            out["contents"] = s.contentsShown
            out["chrome_hidden"] = s.chromeHidden
            out["tool"] = s.tool.rawValue
            out["pen_color"] = s.penColor.rawValue
            out["ink_strokes"] = s.inkData.flatMap { try? PKDrawing(data: $0) }?.strokes.count ?? 0
            out["can_undo"] = s.canUndo
            switch s.kept {
            case .no: out["kept"] = "no"
            case .keeping(let done, let total): out["kept"] = "keeping \(done)/\(total)"
            case .yes: out["kept"] = "yes"
            }
            out["removing"] = s.removing?.mark.id ?? ""
            out["position"] = s.chapter.map { s.position(for: $0.unit) } ?? 0
            #if DEBUG
            out["canvas_pen"] = ReaderView.Coordinator.probe?.canvasPen ?? ""
            out["canvas_touches"] = ReaderView.Coordinator.probe?.canvasTouches ?? -1
            out["canvas_hit"] = ReaderView.Coordinator.probe?.canvasHit ?? ""
            out["keyboard_up"] = KeyboardProbe.shared.up
            out["keyboard_hardware"] = KeyboardProbe.shared.hardware
            out["first_responder"] = KeyboardProbe.shared.firstResponder
            out["page_focus"] = ReaderView.Coordinator.probe?.pageFocus ?? ""
            out["pencil"] = s.lastPencil
            out["canvas_frame"] = ReaderView.Coordinator.probe?.canvasFrame ?? ""
            #endif
            out["flagged"] = Array(s.flagged).sorted()
            out["marks"] = s.marks.map { ["id": $0.id, "kind": $0.kind.rawValue, "text": $0.text, "answered": $0.answer != nil, "turns": $0.history.count] }
            if let a = s.asking {
                out["asking"] = ["question": a.mark.question ?? "", "busy": a.busy, "answered": a.mark.answer != nil, "error": a.error ?? "", "turns": a.mark.history.count]
            }
            out["error"] = s.errorMessage ?? ""
            if case .results(let doc, _) = s.screen {
                out["headline"] = doc.headline
                out["entries"] = doc.entries.count
            }
            if case .error(let message) = s.screen { out["error"] = message }
        } else {
            out["screen"] = library.teaching ? "teach" : "bookshelf"
        }
        return out
    }

    static func screenName(_ screen: BookSession.Screen) -> String {
        switch screen {
        case .empty: return "empty"
        case .placement: return "placement"
        case .series: return "series"
        case .reading: return "reading"
        case .pretest: return "pretest"
        case .check: return "check"
        case .results: return "results"
        case .authoring: return "authoring"
        case .takingBreak: return "break"
        case .error: return "error"
        }
    }

    static func shellPhase(_ phase: ShellSession.Phase) -> String {
        switch phase {
        case .idle: return "idle"
        case .connecting: return "connecting"
        case .connected: return "connected"
        case .closed(let err): return "closed" + (err.map { ": " + $0 } ?? "")
        }
    }

    /// The window as the reader sees it, as PNG.
    /// The key window. On the Mac a sheet is hosted outside UIKit's windows,
    /// so a card up there is seen through accessibility, not here.
    static func screenshot() throws -> Data {
        guard let window = UIApplication.shared.connectedScenes
            .compactMap({ $0 as? UIWindowScene }).flatMap(\.windows).first(where: \.isKeyWindow) else {
            throw Failure.badArguments("no window to capture")
        }
        let renderer = UIGraphicsImageRenderer(bounds: window.bounds)
        let image = renderer.image { _ in
            window.drawHierarchy(in: window.bounds, afterScreenUpdates: true)
        }
        guard let png = image.pngData() else { throw Failure.badArguments("could not encode the screenshot") }
        return png
    }
}
