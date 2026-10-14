import SwiftUI

/// A question about a highlighted passage, the tutor's answer, and the
/// exchange that follows. A card over the page's corner rather than a
/// sheet: the passage stays readable while the question is typed and the
/// answer read. Closing keeps the highlight and its badge on the page;
/// the bin removes the question with its highlight. On a primer the same
/// card carries a margin note, whose answer is a new section on the page.
struct AskCard: View {
    @EnvironmentObject var session: BookSession
    /// In a sheet (compact width) the card fills it and the sheet is the
    /// surface; on the page it is a card of its own width.
    var sheet = false
    @State private var question = ""
    @State private var confirmingDelete = false
    @FocusState private var typing: Bool

    private var asking: BookSession.Asking? { session.asking }
    private var isNote: Bool { asking?.mark.kind == .note }
    private var answered: Bool { asking?.mark.answer != nil }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top) {
                Text(asking?.mark.text ?? "")
                    .font(Typography.serifItalic(15))
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
                Spacer(minLength: 8)
                if answered || asking?.mark.question != nil {
                    // The bin sits a thumb's width from the close button, so
                    // it asks before it acts.
                    Button {
                        confirmingDelete = true
                    } label: {
                        Image(systemName: "trash")
                            .font(.body)
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.borderless)
                    .accessibilityLabel("Delete")
                    .accessibilityHint(isNote ? "Removes the note; the section it added stays" : "Removes the question and its highlight")
                    .confirmationDialog(isNote ? "Delete this note?" : "Delete this question?",
                                        isPresented: $confirmingDelete, titleVisibility: .visible) {
                        Button(isNote ? "Delete the note" : "Delete the question and its highlight", role: .destructive) {
                            session.deleteAsking()
                        }
                        Button("Keep it", role: .cancel) {}
                    } message: {
                        Text(isNote ? "The section it added to the primer stays." : "The exchange with the tutor goes with it.")
                    }
                }
                Button {
                    session.closeAsking()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.borderless)
                .accessibilityLabel("Close")
                .keyboardShortcut(.cancelAction)
            }

            if let a = asking, let q = a.mark.question {
                ScrollView {
                    VStack(alignment: .leading, spacing: 10) {
                        turn(question: q, answer: a.mark.answer, generated: a.mark.generated)
                        ForEach(Array((a.mark.thread ?? []).enumerated()), id: \.offset) { _, t in
                            Divider()
                            turn(question: t.question, answer: t.answer, generated: t.generated)
                        }
                    }
                }
                .frame(maxHeight: 360)
                .fixedSize(horizontal: false, vertical: true)
                if a.busy {
                    HStack(spacing: 10) {
                        ProgressView()
                        Text(isNote ? "Extending the primer." : "Asking the tutor.")
                            .font(Typography.serif(16))
                            .foregroundStyle(.secondary)
                    }
                } else if let err = a.error {
                    Text(err)
                        .font(Typography.serif(16))
                        .foregroundStyle(.red)
                    Button("Try again") { Task { await retry(a) } }
                        .buttonStyle(.bordered)
                } else if answered && !isNote {
                    // Keep chatting: the next question joins the thread.
                    askField(placeholder: "Ask more about this", label: "Ask")
                }
            } else {
                askField(placeholder: isNote ? "Your note on this passage" : "Your question about this passage",
                         label: isNote ? "Add to the primer" : "Ask")
            }
        }
        .padding(18)
        .modifier(Surface(sheet: sheet))
        .onAppear { typing = asking?.mark.question == nil }
        .onChange(of: asking?.mark.id) { _, _ in
            question = ""
            typing = asking?.mark.question == nil
        }
    }

    private struct Surface: ViewModifier {
        let sheet: Bool
        func body(content: Content) -> some View {
            if sheet {
                content.frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            } else {
                // Material, not glass: a glass surface takes the taps meant
                // for the buttons on it (the UI test proves it), and the card
                // carries text to read, which the guidance keeps off glass.
                content
                    .frame(width: 400)
                    .background(.regularMaterial, in: .rect(cornerRadius: 26))
                    .shadow(color: .black.opacity(0.1), radius: 16, y: 4)
            }
        }
    }

    @ViewBuilder
    private func turn(question: String, answer: String?, generated: GeneratedCall? = nil) -> some View {
        Text(question)
            .font(Typography.sans(17, weight: .semibold))
        if let answer {
            if isNote {
                // The section is on the page; the card only says where.
                Label("Added at the end: \(answer)", systemImage: "text.append")
                    .font(Typography.serif(16))
            } else {
                MathText(text: answer, size: 16, rich: true)
                if let generated {
                    GeneratedLine(calls: [generated]) { session.showAbout(answer: generated) }
                }
            }
        }
    }

    private func askField(placeholder: String, label: String) -> some View {
        Group {
            TextField(placeholder, text: $question, axis: .vertical)
                .font(Typography.serif(17))
                .lineLimit(1...4)
                .textFieldStyle(.roundedBorder)
                .focused($typing)
                .onSubmit { submit() }
            HStack {
                Spacer()
                Button(label) { submit() }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(question.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
    }

    private func submit() {
        let q = question
        guard !q.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        question = ""
        Task { await send(q) }
    }

    private func send(_ q: String) async {
        if isNote { await session.extend(q) } else { await session.ask(q) }
    }

    /// The question that failed is the last one in hand.
    private func retry(_ a: BookSession.Asking) async {
        let q = a.mark.thread?.last.flatMap { $0.answer == nil ? $0.question : nil } ?? a.mark.question ?? ""
        await send(q)
    }
}
