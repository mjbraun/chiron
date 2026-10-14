import SwiftUI

/// Where a piece of the book came from: the model calls that wrote it. A
/// chapter's calls come with their prompts; an answer's come as a brief
/// with a ref, and the prompt is fetched from the server when asked for.
struct Provenance: Identifiable, Equatable {
    let id = UUID()
    /// What the record is of: the chapter's title, "This answer".
    let title: String
    /// The subject a ref is fetched under; nil when nothing needs fetching.
    let subject: String?
    let calls: [GeneratedCall]

    init(title: String, subject: String? = nil, calls: [GeneratedCall]) {
        self.title = title; self.subject = subject; self.calls = calls
    }

    /// The line under a generated thing: the model, or models, that wrote it.
    static func byline(_ calls: [GeneratedCall]) -> String? {
        let models = calls.models
        guard !models.isEmpty else { return nil }
        return "Written by " + ListFormatter.localizedString(byJoining: models)
    }

    /// When the thing was written, as the reader's clock says it.
    static func when(_ call: GeneratedCall) -> String {
        guard let date = call.date else { return call.at }
        return date.formatted(date: .abbreviated, time: .shortened)
    }
}

/// The card behind "About": one block per call, the prompt in full and
/// selectable, with the model, the time and how long it took.
struct ProvenanceCard: View {
    @Environment(\.dismiss) private var dismiss
    let provenance: Provenance
    /// Fetches the whole record for a call that came without its prompts.
    var fetch: ((String, String) async throws -> GeneratedCall)?
    @State private var fetched: [String: GeneratedCall] = [:]
    @State private var fetchErrors: [String: String] = [:]
    @State private var fetching: Set<String> = []

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    if let line = Provenance.byline(provenance.calls) {
                        Text(line)
                            .font(Typography.serif(17))
                            .foregroundStyle(.secondary)
                    }
                    ForEach(Array(provenance.calls.enumerated()), id: \.offset) { _, call in
                        block(for: shown(call))
                    }
                }
                .padding(20)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .navigationTitle(provenance.title)
            .toolbarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
    }

    /// The call with its prompts once they have been fetched.
    private func shown(_ call: GeneratedCall) -> GeneratedCall {
        if let ref = call.ref, let full = fetched[ref] { return full }
        return call
    }

    @ViewBuilder
    private func block(for call: GeneratedCall) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(call.purpose)
                .font(Typography.sans(17, weight: .semibold))
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 4) {
                GridRow {
                    Text("Model").foregroundStyle(.secondary)
                    Text(call.model ?? "unknown")
                }
                GridRow {
                    Text("When").foregroundStyle(.secondary)
                    Text(Provenance.when(call))
                }
                if let s = call.seconds, s > 0 {
                    GridRow {
                        Text("Took").foregroundStyle(.secondary)
                        Text(Self.took(s))
                    }
                }
            }
            .font(Typography.sans(15))
            if call.hasPrompts {
                if let system = call.system, !system.isEmpty {
                    promptBlock("System prompt", system)
                }
                if let prompt = call.prompt, !prompt.isEmpty {
                    promptBlock("Prompt", prompt)
                }
            } else if let ref = call.ref {
                if fetching.contains(ref) {
                    HStack(spacing: 8) {
                        ProgressView()
                        Text("Fetching the prompt.").foregroundStyle(.secondary)
                    }
                    .font(Typography.sans(15))
                } else {
                    Button("Show the prompt") { Task { await load(ref) } }
                        .buttonStyle(.bordered)
                        .disabled(fetch == nil || provenance.subject == nil)
                    if let err = fetchErrors[ref] {
                        Text(err).font(Typography.sans(14)).foregroundStyle(.red)
                    }
                }
            } else {
                Text("The prompt was not kept.")
                    .font(Typography.sans(15))
                    .foregroundStyle(.secondary)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.fill.tertiary, in: .rect(cornerRadius: 16))
    }

    private func promptBlock(_ label: String, _ text: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(label).font(.footnote.weight(.semibold)).foregroundStyle(.secondary)
                Spacer()
                Button {
                    UIPasteboard.general.string = text
                } label: {
                    Label("Copy", systemImage: "doc.on.doc").font(.footnote)
                }
                .buttonStyle(.borderless)
            }
            SelectableText(text: text, font: Typography.uiMono(13), colour: .label)
        }
    }

    static func took(_ seconds: Double) -> String {
        if seconds < 60 { return String(format: "%.0f s", seconds) }
        return String(format: "%.1f min", seconds / 60)
    }

    private func load(_ ref: String) async {
        guard let fetch, let subject = provenance.subject else { return }
        fetching.insert(ref)
        fetchErrors[ref] = nil
        do {
            fetched[ref] = try await fetch(subject, ref)
        } catch {
            fetchErrors[ref] = "The server can't be reached; the prompt is kept there."
        }
        fetching.remove(ref)
    }
}

/// The quiet line under a generated answer: the model that wrote it, and
/// the way to the card.
struct GeneratedLine: View {
    let calls: [GeneratedCall]
    let open: () -> Void

    var body: some View {
        if let line = Provenance.byline(calls) {
            HStack(spacing: 6) {
                Text(line)
                    .font(Typography.sans(12))
                    .foregroundStyle(.tertiary)
                Button(action: open) {
                    Image(systemName: "info.circle")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
                .buttonStyle(.borderless)
                .accessibilityLabel("About this answer")
                .accessibilityHint("The model, the time and the prompt")
            }
        }
    }
}
