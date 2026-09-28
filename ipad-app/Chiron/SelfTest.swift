import Foundation
import os

/// Hands-free verification of the full exchange loop, for use from a script
/// when nobody is present to tap through the UI. Launch with:
///
///     xcrun simctl launch --console-pty <device> dev.mjbraun.chiron selftest
///
/// Progress is written to stdout (which simctl relays) and to the system log
/// (subsystem com.mjbraun.chiron). Every step drives the same code paths the
/// real UI drives - no shortcuts around the session.
@MainActor
enum SelfTest {
    static let log = Logger(subsystem: "com.mjbraun.chiron", category: "chiron-selftest")

    /// os.Logger output does not reach `idevicesyslog`, which reads the legacy
    /// syslog stream - so a run over USB looked like it had produced nothing at
    /// all. Everything goes to stdout as well, where the launcher relays it.
    static func say(_ line: String) {
        print("chiron-selftest \(line)")
        fflush(stdout)
        log.notice("\(line)")
    }

    static var requested: Bool {
        CommandLine.arguments.contains("selftest") || weakRequested
    }

    /// Answer everything wrong, to exercise the path that actually matters
    /// pedagogically: gate failure, grader diagnosis, and the repair
    /// chapter on the misses. `simctl launch <dev> <id> selftestweak`.
    static var weakRequested: Bool {
        CommandLine.arguments.contains("selftestweak")
    }

    /// `subject=<id>` picks the book; otherwise the server's active book,
    /// then the first on the shelf.
    static var subjectArg: String? { argument("subject=") }

    /// `level=<1-5>` answers the placement screener. Three is the middle of
    /// the ladder, where the series mixes floor probes and ceiling probes.
    static var level: Int { min(max(Int(argument("level=") ?? "") ?? 3, 1), 5) }

    static func argument(_ prefix: String) -> String? {
        CommandLine.arguments.first { $0.hasPrefix(prefix) }
            .map { String($0.dropFirst(prefix.count)) }
    }

    /// `server=http://127.0.0.1:8084` on the command line points the app at a
    /// different host for one launch. The simulator needs this: it shares the
    /// Mac's network stack, but a fresh app container has no local-network
    /// grant, so the Mac's LAN address is unreachable while loopback is not.
    static var serverOverride: String? { argument("server=") }

    /// `showteach` opens the Teach-me conversation directly; `showteach demo`
    /// seeds a transcript so the layout can be inspected without typing.
    static var teachRequested: Bool { CommandLine.arguments.contains("showteach") }
    static var teachDemo: Bool { teachRequested && CommandLine.arguments.contains("demo") }

    static func run(_ library: Library) async {
        say("START base=\(library.sync.baseURL)")

        await library.sync.probe()
        say("PROBE connected=\(library.sync.connected) llm=\(library.sync.llmConnected)")

        await library.refresh()
        say("SUBJECTS n=\(library.subjects.count) active=\(library.activeSubjectID ?? "-")")
        let wanted = subjectArg ?? library.activeSubjectID
        guard let subject = library.subjects.first(where: { $0.id == wanted }) ?? library.subjects.first else {
            say("FAIL no subjects available: \(library.shelfError ?? "")")
            return
        }

        await library.open(subject.id)
        guard let session = library.session else {
            say("FAIL no session")
            return
        }
        // Always from the top: a chapter cached by an earlier run would
        // otherwise stand in for the one the server delivers now.
        await session.start()

        // A book opens on the placement screener, which delivers the
        // calibration series, which ends in results and the first chapter. A
        // book already past calibration opens on a teaching chapter, which
        // ends in results too. Four steps therefore always reach the results
        // and the chapter after them.
        for _ in 0..<4 {
            switch session.screen {
            case .placement:
                say("PLACE level=\(level) of \(session.chapter?.screener?.options?.count ?? 0)")
                await session.place(level: level)
            case .series, .reading, .pretest, .check:
                guard let chapter = session.chapter else {
                    say("FAIL no chapter: \(session.errorMessage ?? "no error reported")")
                    return
                }
                say("CHAPTER unit=\(chapter.unit) calibration=\(chapter.isCalibration) html=\(chapter.html.count) beats=\(chapter.beats.count) pretest=\(chapter.pretest.count) check=\(chapter.check.count)")
                let responses = answers(for: chapter, weak: weakRequested, llm: library.sync.llmConnected)
                say("CHECK submitting n=\(responses.count) idk=\(responses.filter { $0.idk == true }.count) mode=\(weakRequested ? "weak" : "strong")")
                await session.submitCheck(responses)
            case .results(let doc, let gate):
                let pct = Int((gate.score ?? 0) * 100)
                say("GATE score=\(pct)% passed=\(gate.passed) calibration=\(gate.calibration == true) entries=\(doc.entries.count) headline=\(doc.headline)")
                if gate.passed || doc.isCalibration {
                    await session.proceed()
                } else {
                    // Nothing is written after a failed gate until the
                    // reader chooses; the walk chooses the misses alone.
                    await session.repair()
                }
                if case .takingBreak(let b) = session.screen {
                    say("BREAK minutes=\(b.minutes) kind=\(b.kind)")
                    await session.breakFinished(minutes: 0.1)
                }
                if case .reading = session.screen, let next = session.chapter {
                    say("NEXT unit=\(next.unit) html=\(next.html.count)")
                    say("PASS self-test completed the full loop")
                } else {
                    say("FAIL after results: \(session.screen)")
                }
                return
            case .authoring(let unit):
                say("FAIL still authoring \(unit)")
                return
            case .takingBreak, .error, .empty:
                say("FAIL unexpected screen: \(session.screen)")
                return
            }
        }
        say("FAIL four steps and no results")
    }

    /// Strong path answers from the shipped reference answers; weak path
    /// answers with a confident, plausible-sounding misconception, which is
    /// exactly what the grader is built to catch. Free-text items need a model
    /// to grade them; without one they are answered "I don't know", which the
    /// server grades mechanically.
    static func answers(for chapter: ChapterPayload, weak: Bool, llm: Bool) -> [ItemResponse] {
        chapter.check.map { item in
            if item.kind == "mcq" {
                let options = item.reveal?.options ?? []
                let idx = weak
                    ? (options.firstIndex(where: { !$0.correct }) ?? 0)
                    : (options.firstIndex(where: { $0.correct }) ?? 0)
                return ItemResponse(itemId: item.id, response: nil, selectedIndex: idx, confidence: 4)
            }
            if item.check == "llm" && !llm {
                return ItemResponse(itemId: item.id, response: nil, selectedIndex: nil, confidence: 1, idk: true)
            }
            let answer = weak
                ? "Softmax gives the probability that each option is factually correct, so the model picks the true one."
                : (item.reveal?.answer ?? "see reference")
            return ItemResponse(itemId: item.id, response: answer, selectedIndex: nil, confidence: 4)
        }
    }
}
