import PhotosUI
import SwiftUI

/// "Request a change": what the reader wants different, sent to the
/// development agent on the sprite with, if they want, a picture of
/// where they were,
/// and every request so far with what the agent has done about it. A
/// ready request whose build is newer than this app offers Install.
struct RequestsCard: View {
    @EnvironmentObject var library: Library
    @State private var text = ""
    @State private var withPicture = false
    @State private var sending = false
    @State private var picked: PhotosPickerItem?
    @State private var picture: Data?
    @FocusState private var typing: Bool
    @Environment(\.horizontalSizeClass) private var width

    var body: some View {
        NavigationStack {
            List {
                Section {
                    TextField("What should change?", text: $text, axis: .vertical)
                        .lineLimit(3...8)
                        .focused($typing)
                        .accessibilityLabel("The change you want")
                    Toggle("Send a picture of this screen", isOn: $withPicture)
                        .disabled(picture != nil)
                    // A screenshot from elsewhere, a photo of the thing, a
                    // sketch of what it should look like: often the clearest
                    // way to say what is wrong.
                    PhotosPicker(selection: $picked, matching: .images) {
                        Label(picture == nil ? "Attach a picture" : "Change the picture", systemImage: "photo")
                    }
                    if let picture, let image = UIImage(data: picture) {
                        HStack {
                            Image(uiImage: image)
                                .resizable().scaledToFit()
                                .frame(maxHeight: 140)
                                .clipShape(RoundedRectangle(cornerRadius: 8))
                            Spacer()
                            Button(role: .destructive) {
                                self.picture = nil
                                picked = nil
                            } label: { Label("Remove", systemImage: "xmark.circle") }
                            .labelStyle(.iconOnly)
                        }
                    }
                    if let err = library.requestError {
                        Text(err).foregroundStyle(.red).font(.callout)
                    }
                    Button {
                        send()
                    } label: {
                        if sending { ProgressView() } else { Text("Send to the agent") }
                    }
                    .disabled(sending || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                } footer: {
                    Text("The agent on the sprite makes the change, runs the tests, and builds the app on the MacBook. Watch it here; Install appears when the build is ready.")
                }
                Section("Requests") {
                    if library.requests.isEmpty {
                        Text("None yet.").foregroundStyle(.secondary)
                    }
                    ForEach(library.requests) { r in
                        RequestRow(request: r)
                    }
                }
            }
            .navigationTitle("Request a change")
            .toolbarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { library.requestsShown = false }
                }
                ToolbarItem(placement: .topBarLeading) {
                    Button { Task { await library.refreshRequests() } } label: {
                        Label("Refresh", systemImage: "arrow.clockwise")
                    }
                }
            }
            .task {
                await library.refreshRequests()
                // A request in the agent's hands moves; keep up while the card is open.
                while !Task.isCancelled, library.requests.contains(where: \.open) {
                    try? await Task.sleep(for: .seconds(5))
                    await library.refreshRequests()
                    await library.checkForBuild()
                }
            }
            .onChange(of: picked) { _, item in
                guard let item else { return }
                Task { picture = try? await item.loadTransferable(type: Data.self) }
            }
            // On a phone the keyboard covers everything below the field, and
            // what is below is the list of requests and where they have got
            // to. Let the reader see that first and tap to type.
            .onAppear { typing = width != .compact }
        }
        .presentationSizing(.form)
    }

    private func send() {
        let t = text
        sending = true
        Task {
            if await library.requestChange(t, withPicture: withPicture, picture: picture) != nil {
                text = ""
                picture = nil
                picked = nil
            }
            sending = false
        }
    }
}

struct RequestRow: View {
    @EnvironmentObject var library: Library
    let request: ChangeRequest

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                SelectableText(text: request.text, font: Typography.uiSans(16, weight: .semibold))
                Spacer()
                StatusPill(status: request.status)
            }
            if let last = request.last, request.open {
                Text(last).font(.callout).foregroundStyle(.secondary).lineLimit(3)
            }
            if let thread = request.thread, !thread.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(Array(thread.enumerated()), id: \.offset) { _, e in
                        Text("Asked: \(e.question)").font(.footnote).foregroundStyle(.secondary)
                        Text("You said: \(e.answer)").font(.footnote)
                    }
                }
            }
            if request.waiting {
                QuestionReply(request: request)
            }
            if let summary = request.summary, !summary.isEmpty, !request.open, !request.waiting {
                SelectableText(text: summary, font: Typography.uiSans(15), colour: .secondaryLabel)
            }
            if let reason = request.reason, request.status == "failed" {
                Text(reason).font(.footnote.monospaced()).foregroundStyle(.red).lineLimit(6)
            }
            HStack {
                if let c = request.commit { Text("commit \(c)").font(.footnote.monospaced()).foregroundStyle(.secondary) }
                if let b = request.build { Text("build \(b.version) (\(b.build))").font(.footnote).foregroundStyle(.secondary) }
                Spacer()
                if let b = request.build, b.build > library.runningBuild, let url = library.installURL, library.availableBuild?.build == b.build {
                    Button("Install") { UIApplication.shared.open(url) }.buttonStyle(.borderedProminent)
                }
            }
        }
        .padding(.vertical, 4)
        // See PlanCard: a context menu here would swallow the long press
        // that starts a selection. A waiting row keeps its answer field
        // reachable on its own.
        .accessibilityElement(children: request.waiting ? .contain : .combine)
    }
}

/// What the agent stopped to ask, and the reader's answer, which sends
/// the request back to it.
struct QuestionReply: View {
    @EnvironmentObject var library: Library
    let request: ChangeRequest
    @State private var answer = ""
    @State private var sending = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("The agent asks", systemImage: "questionmark.bubble")
                .font(.footnote.weight(.semibold))
                .foregroundStyle(.orange)
            SelectableText(text: request.question ?? "", font: Typography.uiSerif(16))
            TextField("Your answer", text: $answer, axis: .vertical)
                .lineLimit(1...6)
                .textFieldStyle(.roundedBorder)
                .accessibilityLabel("Your answer")
            HStack {
                Spacer()
                Button {
                    sending = true
                    Task {
                        if await library.answerRequest(request.id, answer) { answer = "" }
                        sending = false
                    }
                } label: {
                    if sending { ProgressView() } else { Text("Answer") }
                }
                .buttonStyle(.borderedProminent)
                .disabled(sending || answer.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(10)
        .background(Color.orange.opacity(0.08), in: .rect(cornerRadius: 10))
    }
}

struct StatusPill: View {
    let status: String

    var body: some View {
        Text(status)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 8).padding(.vertical, 3)
            .background(colour.opacity(0.18), in: Capsule())
            .foregroundStyle(colour)
    }

    private var colour: Color {
        switch status {
        case "ready": return .green
        case "failed": return .red
        case "queued": return .secondary
        case "waiting": return .orange
        default: return .blue
        }
    }
}

struct RequestButton: View {
    @EnvironmentObject var library: Library

    var body: some View {
        Button { library.requestsShown = true } label: {
            Label("Request a change", systemImage: "wrench.and.screwdriver")
        }
        // A request the agent stopped on waits on the reader: the wrench
        // says how many.
        .overlay(alignment: .topTrailing) {
            if library.waitingOnReader > 0 {
                Text("\(library.waitingOnReader)")
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(.white)
                    .padding(.horizontal, 5)
                    .background(Color.orange, in: .capsule)
                    .offset(x: 8, y: -6)
                    .allowsHitTesting(false)
            }
        }
        .keyboardShortcut("r", modifiers: [.command, .shift])
        .accessibilityValue(library.waitingOnReader > 0 ? "\(library.waitingOnReader) waiting on your answer" : "")
        .accessibilityHint("Ask the agent on the sprite to change the app")
    }
}
