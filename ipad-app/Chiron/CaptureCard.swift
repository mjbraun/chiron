import SwiftUI
import Combine

/// "What do you want to know about this?" A capture arrives with its text
/// or image; the reader adds the question and says how much they want
/// back. A summary or a detail is answered here in the card; a
/// primer or a smart book goes on to its planning conversation. From the
/// shelf's Capture button the text is the reader's to paste.
struct CaptureCard: View {
    @EnvironmentObject var library: Library
    @Environment(\.dismiss) private var dismiss
    @State var capture: Capture
    @State private var prompt = ""
    @State private var scale: CaptureScale = .primer
    @State private var sending = false
    @State private var error: String?
    @FocusState private var typing: Bool
    @State private var about: Provenance?

    private var fromElsewhere: Bool { capture.sourceApp != nil || capture.sourceURL != nil || capture.imagePNG != nil }

    var body: some View {
        NavigationStack {
            Form {
                // The answer, when there is one, above everything else.
                if let answer = library.captureAnswer {
                    Section {
                        MathText(text: answer, size: 16, rich: true)
                        if let g = library.captureGenerated {
                            GeneratedLine(calls: [g]) { about = Provenance(title: "This answer", calls: [g]) }
                        }
                        Button {
                            scale = .primer
                            submit()
                        } label: {
                            Label("Go on: make it a primer", systemImage: "doc.text")
                        }
                        .disabled(sending)
                    } header: {
                        Text("The answer")
                    }
                }
                // A whole page was shared, not a passage from one: the
                // question worth asking is usually of the page itself.
                if let url = capture.link, library.captureAnswer == nil {
                    Section {
                        Button {
                            Task { await library.readPage(url) }
                        } label: {
                            Label("Read it in Chiron", systemImage: "book")
                        }
                        .disabled(sending)
                        Button {
                            Task { await library.followFeed(url) }
                        } label: {
                            Label("Follow this blog", systemImage: "dot.radiowaves.up.forward")
                        }
                        .disabled(sending)
                    } footer: {
                        Text("A page goes on the shelf to read and highlight. A feed is followed: its posts arrive as chapters.")
                    }
                }
                Section {
                    if let png = capture.imagePNG, let image = UIImage(data: png) {
                        Image(uiImage: image)
                            .resizable().scaledToFit()
                            .frame(maxHeight: 220)
                            .clipShape(RoundedRectangle(cornerRadius: 10))
                    }
                    // The reader's to edit wherever it came from: a PDF
                    // selection picks up a running header or drops a word.
                    TextEditor(text: $capture.text)
                        .font(Typography.serif(16))
                        .frame(minHeight: 120, maxHeight: 260)
                } header: {
                    Text(capture.imagePNG != nil && capture.text.isEmpty ? "Captured image" : "Captured text")
                } footer: {
                    if let src = sourceLine { Text(src) }
                    else if !fromElsewhere { Text("Paste or type what you want a primer on.") }
                }
                Section {
                    TextField("What do you want to know about this?", text: $prompt, axis: .vertical)
                        .font(Typography.serif(17))
                        .lineLimit(1...4)
                        .focused($typing)
                        .onSubmit { submit() }
                    if let error {
                        Text(error).font(.footnote).foregroundStyle(.red)
                    }
                } header: {
                    Text("Your question")
                }
                Section {
                    Picker("How much", selection: $scale) {
                        ForEach(CaptureScale.allCases, id: \.self) { Text($0.label).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    .accessibilityLabel("How much you want back")
                } header: {
                    Text("How much")
                } footer: {
                    Text(scale.footer)
                }
            }
            .navigationTitle(library.captureAnswer == nil ? "Capture" : "Answered")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(library.captureAnswer == nil ? "Cancel" : "Done") {
                        library.pendingCapture = nil
                        library.captureAnswer = nil
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        submit()
                    } label: {
                        if sending { ProgressView() } else { Text(submitLabel) }
                    }
                    .disabled(!canSubmit)
                }
            }
            .onAppear { typing = true }
            .onChange(of: library.captureAnswer) { _, answer in
                if answer != nil { typing = false }
            }
            .sheet(item: $about) { ProvenanceCard(provenance: $0) }
        }
        .presentationSizing(.form)
    }

    private var canSubmit: Bool {
        !sending && !capture.isEmpty && !prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private var submitLabel: String {
        switch scale {
        case .summary: return "Summarise"
        case .detail: return "Go into detail"
        case .primer: return "Plan a primer"
        case .book: return "Plan a book"
        }
    }

    private var sourceLine: String? {
        var parts: [String] = []
        if let app = capture.sourceApp { parts.append("from \(app)") }
        if let url = capture.sourceURL { parts.append(url) }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    private func submit() {
        guard canSubmit else { return }
        sending = true
        error = nil
        Task {
            do {
                try await library.submitCapture(capture, prompt: prompt, scale: scale)
            } catch {
                self.error = "The server did not take the capture. Try again."
            }
            sending = false
        }
    }
}
