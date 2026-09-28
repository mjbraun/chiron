import SwiftUI
import PencilKit

/// Shared flow for the calibration series, pretests and terminal checks:
/// one item at a time, confidence committed BEFORE any reveal, elaborated
/// feedback after. Free-text items show the reference answer for
/// self-comparison; the authoritative grade arrives with the exchange (the
/// results screen). With `reveal` off - the calibration series, which is
/// measurement - committing moves straight to the next item.
struct ItemFlowView: View {
    let title: String
    let subtitle: String
    let items: [CheckItem]
    var reveal = true
    let submitLabel: String
    /// Present on the terminal check, absent on the pretest (which the learner
    /// never enters by accident - it opens itself).
    var onExit: (() -> Void)? = nil
    /// Items already flagged as wrong, and the way to flag one: the
    /// concern goes to the server at once, with the answer given so far.
    var flagged: Set<String> = []
    var onFlag: ((CheckItem, String, ItemResponse?) async throws -> Void)? = nil
    let onSubmit: ([ItemResponse]) async -> Void

    @State private var index = 0
    @State private var text = ""
    @State private var selected: Int?
    @State private var confidence = 2
    @State private var revealed = false
    /// The item was answered "I don't know": the reveal names the answer
    /// without dressing it as the reader's pick.
    @State private var passed = false
    @State private var responses: [ItemResponse] = []
    @State private var confirmingExit = false
    /// Typed is the default; handwriting is a toggle per item (a finger on
    /// a 7.9" screen is a poor pen, and the typed path must be excellent).
    @State private var handwriting = false
    @State private var drawing = PKDrawing()
    @State private var inkAnswer: InkAnswer?
    @State private var flagging = false
    @FocusState private var typing: Bool

    var item: CheckItem { items[index] }

    /// Short mechanical answers (a number, an exact form) take one line and
    /// Return commits; explained answers take a paragraph.
    private var singleLine: Bool { item.check != "llm" }
    private var numeric: Bool { item.check.hasPrefix("numeric") }

    private var typedAnswer: String { text.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var canCommit: Bool {
        if item.kind == "mcq" { return selected != nil }
        return handwriting ? inkAnswer != nil : !typedAnswer.isEmpty
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            VStack(alignment: .leading, spacing: 6) {
                if onExit != nil {
                    Button {
                        // The check is closed-book on purpose - reading the
                        // chapter mid-check is the crutch effect the design
                        // exists to prevent. An accidental tap with nothing
                        // answered costs nothing; leaving with answers on the
                        // board discards them so a re-entered check starts
                        // honest rather than half-open-book.
                        if responses.isEmpty && !revealed {
                            onExit?()
                        } else {
                            confirmingExit = true
                        }
                    } label: {
                        Label("Back to the chapter", systemImage: "chevron.left")
                            .font(.callout)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(.secondary)
                }
                Text(title).font(Typography.serif(24, weight: .semibold, relativeTo: .title2))
                Text(subtitle).font(.callout).foregroundStyle(.secondary)
                ProgressView(value: Double(index), total: Double(max(items.count, 1)))
                Text("\(index + 1) of \(items.count)")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .accessibilityLabel("Question \(index + 1) of \(items.count)")
            }

            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    MathText(text: item.prompt, size: 20)

                    if item.kind == "mcq", let options = item.options {
                        ForEach(options.indices, id: \.self) { i in
                            Button {
                                if !revealed { selected = i }
                            } label: {
                                HStack(alignment: .top, spacing: 10) {
                                    Image(systemName: iconFor(i))
                                        .padding(.top, 3)
                                    Text(String(UnicodeScalar(65 + i)!))
                                        .font(Typography.sans(17, weight: .semibold))
                                        .foregroundStyle(.secondary)
                                        .padding(.top, 1)
                                    MathText(text: options[i].text, size: 17)
                                    Spacer(minLength: 0)
                                }
                                .padding(10)
                                .background(backgroundFor(i), in: RoundedRectangle(cornerRadius: 10))
                            }
                            .buttonStyle(.plain)
                            .hoverEffect()
                            // The lettered options answer to their number key.
                            .keyboardShortcut(KeyEquivalent(Character("\(i + 1)")), modifiers: [])
                            .accessibilityLabel("Option \(String(UnicodeScalar(65 + i)!))")
                            if revealed, let r = item.reveal?.options?[i] {
                                if passed {
                                    if r.correct {
                                        let why = PassReveal.explanation(r.explain)
                                        VStack(alignment: .leading, spacing: 4) {
                                            Text(PassReveal.headline(correct: i))
                                                .font(.callout.weight(.semibold))
                                            if !why.isEmpty {
                                                MathText(text: why, size: 15)
                                            }
                                        }
                                        .foregroundStyle(.secondary)
                                        .padding(.leading, 30)
                                    }
                                } else if selected == i || r.correct {
                                    MathText(text: r.explain, size: 15)
                                        .foregroundStyle(r.correct ? .green : .orange)
                                        .padding(.leading, 30)
                                }
                            }
                        }
                    } else {
                        answerEntry
                        if revealed, let reveal = item.reveal {
                            VStack(alignment: .leading, spacing: 8) {
                                Text("Reference answer").font(.headline)
                                MathText(text: reveal.answer ?? "", size: 17)
                                if item.check == "llm" {
                                    Text("Your answer will be graded against the rubric at the next check-in.")
                                        .font(.footnote).foregroundStyle(.secondary)
                                }
                            }
                            .padding(12)
                            .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 10))
                        }
                    }
                    flagRow
                }
            }

            if !revealed {
                ConfidencePills(confidence: $confidence)
                HStack(spacing: 16) {
                    // Not knowing is expected - especially on pretests - and
                    // saying so is better signal than typing filler to get
                    // past a required field. Always leftmost, always there.
                    Button("I don't know") {
                        // An option tapped before passing was not an answer.
                        selected = nil
                        passed = true
                        answered(ItemResponse(
                            itemId: item.id,
                            response: nil,
                            selectedIndex: nil,
                            confidence: 1,
                            idk: true))
                    }
                    .buttonStyle(.bordered)

                    Button(reveal ? "Commit answer" : (index == items.count - 1 ? submitLabel : "Next")) {
                        commit()
                    }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(!canCommit)
                }
            } else {
                Button(index == items.count - 1 ? submitLabel : "Next") { advance() }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(28)
        .onAppear { typing = item.kind != "mcq" }
        .alert("Leave the check?", isPresented: $confirmingExit) {
            Button("Stay", role: .cancel) { }
            Button("Discard answers and go back", role: .destructive) { onExit?() }
        } message: {
            Text("The check is closed-book, so answers so far are discarded - re-entering starts it fresh.")
        }
        .sheet(isPresented: $flagging) {
            FlagCard(item: item) { concern in
                guard let onFlag else { return }
                try await onFlag(item, concern, responses.last { $0.itemId == item.id })
            }
        }
        .frame(maxWidth: 760)
    }

    /// A question, a reference answer or a grade the reader thinks is wrong
    /// can be said so at any point on the item; once said, the item shows
    /// it. Absent where nothing takes a flag (a session without a server).
    @ViewBuilder private var flagRow: some View {
        if onFlag != nil {
            HStack {
                Spacer()
                if flagged.contains(item.id) {
                    Label("Flagged", systemImage: "flag.fill")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .accessibilityLabel("Flagged as wrong")
                } else {
                    Button {
                        flagging = true
                    } label: {
                        Label("Flag this question", systemImage: "flag")
                            .font(.callout)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(.secondary)
                    .hoverEffect()
                    .accessibilityHint("Say what looks wrong with the question or its answer")
                }
            }
        }
    }

    /// The answer surface for a constructed item: a line or a paragraph
    /// from the keyboard, or a box to write in by hand.
    @ViewBuilder private var answerEntry: some View {
        if handwriting {
            InkBox(drawing: $drawing, answer: $inkAnswer)
                .disabled(revealed)
        } else if singleLine {
            TextField(numeric ? "a number" : "your answer", text: $text)
                .textFieldStyle(.roundedBorder)
                .font(Typography.serif(19))
                .keyboardType(numeric ? .numbersAndPunctuation : .default)
                .autocapitalization(.none)
                .disableAutocorrection(true)
                .focused($typing)
                .submitLabel(.done)
                .onSubmit { if canCommit { commit() } }
                .disabled(revealed)
        } else {
            TextEditor(text: $text)
                .font(Typography.serif(19))
                .frame(minHeight: 140)
                .padding(6)
                .overlay(RoundedRectangle(cornerRadius: 10).stroke(.quaternary))
                .focused($typing)
                .disabled(revealed)
        }
        if !revealed {
            HStack {
                Toggle(isOn: $handwriting) {
                    Label("Write by hand", systemImage: "pencil.and.outline")
                        .font(.callout)
                }
                .toggleStyle(.button)
                .buttonStyle(.bordered)
                .controlSize(.small)
                .onChange(of: handwriting) { on in typing = !on }
                if handwriting && !drawing.strokes.isEmpty {
                    Button("Clear") { drawing = PKDrawing() }
                        .buttonStyle(.plain)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                Spacer()
            }
        }
    }

    private func commit() {
        answered(ItemResponse(
            itemId: item.id,
            response: item.kind == "mcq" || handwriting ? nil : typedAnswer,
            selectedIndex: selected,
            confidence: confidence,
            ink: item.kind == "mcq" || !handwriting ? nil : inkAnswer))
    }

    private func answered(_ r: ItemResponse) {
        responses.append(r)
        if reveal {
            withAnimation { revealed = true }
        } else {
            advance()
        }
    }

    private func advance() {
        if index == items.count - 1 {
            let out = responses
            Task { await onSubmit(out) }
        } else {
            index += 1
            text = ""; selected = nil; confidence = 2; revealed = false; passed = false
            drawing = PKDrawing()
            inkAnswer = nil
            typing = !handwriting && items[index].kind != "mcq"
        }
    }

    private func iconFor(_ i: Int) -> String {
        if !revealed { return selected == i ? "largecircle.fill.circle" : "circle" }
        guard let r = item.reveal?.options?[i] else { return "circle" }
        if r.correct { return passed ? "checkmark.circle" : "checkmark.circle.fill" }
        return selected == i ? "xmark.circle.fill" : "circle"
    }

    private func backgroundFor(_ i: Int) -> Color {
        if !revealed { return selected == i ? Color.accentColor.opacity(0.15) : Color.clear }
        guard let r = item.reveal?.options?[i] else { return .clear }
        if r.correct { return passed ? Color.secondary.opacity(0.10) : .green.opacity(0.12) }
        return selected == i ? .red.opacity(0.10) : .clear
    }
}

/// What the reader thinks is wrong with an item: the question, the
/// reference answer, or how it was graded. Sent on its own rather than
/// with the check, so it is on record even if the check is left; the
/// grade is not changed by it.
struct FlagCard: View {
    let item: CheckItem
    let send: (String) async throws -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var concern = ""
    @State private var sending = false
    @State private var error: String?
    @FocusState private var typing: Bool

    private var trimmed: String { concern.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 12) {
                MathText(text: item.prompt, size: 15)
                    .foregroundStyle(.secondary)
                    .frame(maxHeight: 120)
                Text("What looks wrong? The question, the answer, or how it was graded.")
                    .font(.callout)
                TextEditor(text: $concern)
                    .font(Typography.serif(17))
                    .frame(minHeight: 120)
                    .padding(6)
                    .overlay(RoundedRectangle(cornerRadius: 10).stroke(.quaternary))
                    .focused($typing)
                    .disabled(sending)
                    .accessibilityLabel("Concern")
                if let error {
                    Text(error).font(.callout).foregroundStyle(.red)
                }
                Text("The concern is kept with the item and shown on the results; the grade stays as it is.")
                    .font(.footnote).foregroundStyle(.secondary)
                Spacer(minLength: 0)
            }
            .padding(20)
            .navigationTitle("Flag this question")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .disabled(sending)
                }
                ToolbarItem(placement: .confirmationAction) {
                    if sending {
                        ProgressView()
                    } else {
                        Button("Send") { Task { await submit() } }
                            .disabled(trimmed.isEmpty)
                    }
                }
            }
        }
        .presentationDetents([.medium, .large])
        .onAppear { typing = true }
    }

    private func submit() async {
        sending = true
        error = nil
        defer { sending = false }
        do {
            try await send(trimmed)
            dismiss()
        } catch {
            // The text stays for another try.
            self.error = "Could not reach the server. The flag is not on record yet."
        }
    }
}

/// Confidence before any reveal, as four pills: the second is the default,
/// and a confident miss is the miscalibration signal the results mark.
struct ConfidencePills: View {
    @Binding var confidence: Int
    private static let names = ["Unsure", "Shaky", "Confident", "Sure"]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("How confident are you?")
                .font(.callout)
            HStack(spacing: 8) {
                ForEach(1...4, id: \.self) { level in
                    Button(Self.names[level - 1]) { confidence = level }
                        .buttonStyle(.bordered)
                        .tint(confidence == level ? .accentColor : .secondary)
                        .foregroundStyle(confidence == level ? Color.accentColor : .primary)
                        .hoverEffect()
                        .accessibilityLabel("\(Self.names[level - 1])\(confidence == level ? ", selected" : "")")
                }
            }
        }
        .accessibilityElement(children: .contain)
    }
}

struct BreakView: View {
    @EnvironmentObject var session: BookSession
    let suggestion: BreakSuggestion
    @State private var remaining: Int = 0
    @State private var started = Date()

    var body: some View {
        VStack(spacing: 24) {
            Image(systemName: "moon.zzz").font(.system(size: 56)).foregroundStyle(.indigo)
            Text("Break time").font(Typography.display(34))
            Text(suggestion.note).multilineTextAlignment(.center).foregroundStyle(.secondary)
            Text(timeString)
                .font(.system(size: 54, weight: .light, design: .monospaced))
            Text("Genuinely unstimulated rest consolidates what you just learned.\nEyes closed beats a movie.")
                .font(.callout).multilineTextAlignment(.center).foregroundStyle(.secondary)
            Button("Back to the book") {
                Task { await session.breakFinished(minutes: Date().timeIntervalSince(started) / 60) }
            }
            .buttonStyle(.borderedProminent)
        }
        .onAppear { remaining = suggestion.minutes * 60 }
        .task {
            while remaining > 0 && !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                remaining -= 1
            }
        }
    }

    private var timeString: String {
        String(format: "%d:%02d", remaining / 60, remaining % 60)
    }
}

/// The contents as a sheet, for compact width.
struct ContentsView: View {
    @EnvironmentObject var session: BookSession

    var body: some View {
        NavigationStack {
            ContentsList()
                .navigationTitle("Contents")
                .toolbar {
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Done") { session.contentsShown = false }
                    }
                }
        }
    }
}

/// The contents: every chapter of the book with its status, what is owed,
/// and the way to start the book over. A sidebar in regular width, the
/// body of a sheet in compact.
struct ContentsList: View {
    @EnvironmentObject var session: BookSession
    @State private var confirmingReset = false

    /// Anything that changes the page closes the contents: the reader
    /// should see the change, not a list over it.
    private func dismiss() { session.contentsShown = false }

    var body: some View {
            List {
                if session.bookState == nil {
                    Section {
                        Text("No progress loaded yet.")
                        Text("If the server is reachable this fills in on its own; pull to refresh otherwise.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
                // A book read as it is can be taken whole, so it reads
                // where there is no server: on a plane, which is what this
                // is all for.
                if session.readsAsIs, session.bookState != nil {
                    Section {
                        switch session.kept {
                        case .no:
                            Button {
                                Task { await session.keepOnDevice() }
                            } label: {
                                Label("Keep on this iPad", systemImage: "arrow.down.circle")
                            }
                        case .keeping(let done, let total):
                            HStack(spacing: 10) {
                                ProgressView(value: Double(done), total: Double(max(total, 1)))
                                    .frame(maxWidth: 120)
                                Text("Keeping \(done) of \(total)")
                                    .font(.footnote).foregroundStyle(.secondary)
                            }
                        case .yes:
                            Label("Kept on this iPad", systemImage: "checkmark.circle.fill")
                                .foregroundStyle(.green)
                        }
                    } footer: {
                        Text("Every chapter and every picture, so the book reads with no server to ask.")
                    }
                }
                if let state = session.bookState {
                    Section("Progress") {
                        ForEach(state.spine) { entry in
                            HStack {
                                Image(systemName: icon(entry.status))
                                    .foregroundStyle(color(entry.status))
                                VStack(alignment: .leading) {
                                    Text(entry.title)
                                    if let s = entry.score {
                                        Text("\(Int(s * 100))%").font(.caption).foregroundStyle(.secondary)
                                    }
                                }
                                Spacer()
                                if entry.inFringe && entry.status != "active" {
                                    Button("Read next") {
                                        dismiss()
                                        Task { await session.start(choice: entry.unit) }
                                    }
                                    .buttonStyle(.bordered).controlSize(.small)
                                }
                            }
                        }
                    }
                    if !state.debt.isEmpty {
                        Section("Knowledge debt") {
                            Text("\(state.debt.count) unit(s) skipped or below gate")
                                .foregroundStyle(.orange)
                            Button("Catch me up now") {
                                dismiss()
                                Task { await session.catchMeUp() }
                            }
                        }
                    }
                    Section("Where you are") {
                        Text(state.summary).font(.callout).foregroundStyle(.secondary)
                    }
                }
                if let err = session.errorMessage {
                    Section {
                        Text(err).foregroundStyle(.red).font(.callout)
                    }
                }
                Section {
                    Button(role: .destructive) {
                        confirmingReset = true
                    } label: {
                        Label("Start this subject over", systemImage: "arrow.counterclockwise")
                    }
                } footer: {
                    Text("Clears every grade, gate result and debt entry for this subject.")
                }
            }
            .listStyle(.sidebar)
            .refreshable { await session.refreshState() }
            .task { await session.refreshState() }
            .alert("Start over?", isPresented: $confirmingReset) {
                Button("Cancel", role: .cancel) { }
                Button("Start over", role: .destructive) {
                    // The contents may be a sheet over the screen startOver()
                    // changes. Without dismissing it the reset happens and
                    // the learner sees nothing at all.
                    dismiss()
                    Task { await session.startOver() }
                }
            } message: {
                Text("Every grade, gate result and debt entry for this subject is discarded. This cannot be undone from the app.")
            }
    }

    private func icon(_ s: String) -> String {
        switch s {
        case "passed": return "checkmark.circle.fill"
        case "overridden": return "exclamationmark.triangle.fill"
        case "active": return "book.fill"
        case "failed": return "arrow.counterclockwise.circle"
        default: return "circle.dotted"
        }
    }

    private func color(_ s: String) -> Color {
        switch s {
        case "passed": return .green
        case "overridden": return .orange
        case "active": return .blue
        case "failed": return .red
        default: return .secondary
        }
    }
}
