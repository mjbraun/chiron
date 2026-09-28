#if DEBUG
import Foundation
import Network

/// A script's hands on the app: a tiny HTTP server on localhost (the
/// simulator shares the Mac's loopback) that reports where the app is and
/// drives the same session calls the buttons do. Screens are reached the
/// way a learner reaches them; nothing is faked into view. Launch with the
/// `harness` argument (`harness_port=8088` to pick a port).
///
///   GET  /state              {screen, subject, unit, items, wait, error, shelf}
///   POST /open   {subject}   open a book from the shelf
///   POST /shelf              close the book
///   POST /shelf/order {order}  order the list: recent | title | kind | unread
///   POST /library/show {scope} what the sidebar picks: all, a kind, or shelf:<id>
///   POST /start              start exchange from the top
///   POST /place  {level}     answer the placement screener
///   POST /check              enter the check from the reader
///   POST /contents           toggle the contents (sidebar or sheet)
///   POST /chrome             toggle the reader chrome, as a tap on the page does
///   POST /tool {tool}        pick a palette tool: none | pen | highlighter | ask | eraser
///   POST /mark {text, kind}  highlight the first occurrence of text (kind: highlight | question)
///   POST /ask {text, question}  mark text as a question and ask it
///   POST /flag {text, item}  flag an item as wrong, with the concern (item: an id; default the first of the check)
///   POST /close              close the ask card (the mark and its badge stay)
///   POST /delete             delete the question in hand with its highlight
///   POST /mark/rect {id}     where a mark's badge is, in page-view points
///   POST /undo               take back the last mark or stroke (pdf/undo for a document)
///   POST /keep               take the whole imported book onto this device
///   POST /reader/rect {selector}  where the first matching element is, in page-view points
///   POST /answer {mode}      answer every item of the current chapter:
///                            correct | idk | wrong | mixed (default correct;
///                            mixed inks one item, passes on one, types the rest)
///   POST /proceed            leave the results (or the break)
///   POST /override           override a failed gate
///   POST /repair             below the gate: a short chapter on the misses, then those items again
///   POST /remediate          below the gate: the whole chapter again from a different angle
///   POST /reset              start the book over
///   POST /retry              the error screen's Try again
///   POST /server {url, key, name}  save and select a server (the shell needs one)
///   POST /capture {text, prompt, url, app}  capture and ask: a primer starts on the shelf
///   POST /capture/card {text, url, app}     open the capture card as the share sheet would
///   POST /capture/close      dismiss the capture card
///   POST /note {text, note}  a margin note on a primer's passage; the primer grows
///   POST /agent {on}         let the sprite's agent drive the app (default on)
///   POST /breaks {on}        take the breaks the server suggests (default on; the app's default is off)
///   POST /shell              open the shell sheet;  POST /shell/close closes it
///   POST /shell/type {text}  type into the shell
///   GET  /shell/screen       {phase, lines}: what the terminal shows
///
/// Every path is a verb of AppCommands; the sprite's agent runs the same
/// verbs over the agent link.
///
/// Every POST waits for the action to settle and returns the new state.
@MainActor
final class Harness {
    static let shared = Harness()
    static var requested: Bool { CommandLine.arguments.contains("harness") }
    static var port: UInt16 { UInt16(SelfTest.argument("harness_port=") ?? "") ?? 8087 }

    private var listener: NWListener?
    private weak var library: Library?

    func start(_ library: Library) {
        self.library = library
        guard let l = try? NWListener(using: .tcp, on: NWEndpoint.Port(rawValue: Self.port)!) else {
            SelfTest.say("HARNESS could not listen on \(Self.port)")
            return
        }
        listener = l
        l.newConnectionHandler = { [weak self] conn in
            conn.start(queue: .global())
            self?.receive(on: conn, buffer: Data())
        }
        l.start(queue: .global())
        SelfTest.say("HARNESS listening on \(Self.port)")
    }

    private nonisolated func receive(on conn: NWConnection, buffer: Data) {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 1 << 20) { [weak self] data, _, done, err in
            guard let self, err == nil else { conn.cancel(); return }
            var buf = buffer
            if let data { buf.append(data) }
            if let request = HTTPRequest(raw: buf) {
                Task { @MainActor in await self.route(request, conn) }
            } else if !done {
                self.receive(on: conn, buffer: buf)
            } else {
                conn.cancel()
            }
        }
    }

    private func route(_ req: HTTPRequest, _ conn: NWConnection) async {
        let body = (try? JSONSerialization.jsonObject(with: req.body)) as? [String: Any] ?? [:]
        guard let library else { respond(conn, status: "500 Internal Server Error", body: nil); return }
        let verb = req.path == "/state" ? "state" : String(req.path.dropFirst())
        do {
            let result = try await AppCommands.run(verb, args: body, library: library)
            respond(conn, status: "200 OK", body: try? JSONSerialization.data(withJSONObject: result))
        } catch AppCommands.Failure.unknownVerb {
            respond(conn, status: "404 Not Found", body: nil)
        } catch {
            respond(conn, status: "409 Conflict", body: Data("{\"error\":\"\(error)\"}".utf8))
        }
    }

    private func respond(_ conn: NWConnection, status: String, body: Data?) {
        var head = "HTTP/1.1 \(status)\r\nContent-Type: application/json\r\n"
        head += "Content-Length: \(body?.count ?? 0)\r\nConnection: close\r\n\r\n"
        var out = Data(head.utf8)
        if let body { out.append(body) }
        conn.send(content: out, completion: .contentProcessed { _ in conn.cancel() })
    }
}

/// Just enough HTTP parsing for the harness.
private struct HTTPRequest {
    let method: String
    let path: String
    let body: Data

    init?(raw: Data) {
        guard let headerEnd = raw.range(of: Data("\r\n\r\n".utf8)) else { return nil }
        guard let head = String(data: raw[..<headerEnd.lowerBound], encoding: .utf8) else { return nil }
        let lines = head.components(separatedBy: "\r\n")
        let parts = lines[0].components(separatedBy: " ")
        guard parts.count >= 2 else { return nil }
        method = parts[0]
        path = parts[1]
        var contentLength = 0
        for line in lines.dropFirst() where line.lowercased().hasPrefix("content-length:") {
            contentLength = Int(line.dropFirst("content-length:".count).trimmingCharacters(in: .whitespaces)) ?? 0
        }
        let bodyData = raw[headerEnd.upperBound...]
        guard bodyData.count >= contentLength else { return nil }  // wait for more
        body = Data(bodyData.prefix(contentLength))
    }
}
#endif
