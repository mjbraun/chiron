import SwiftUI

/// A graded check, as the results document says it: headline, the gate or
/// the calibration tally, then every item with its audit trail (what was
/// read, what was chosen, the answer, why). The actions depend on the gate.
struct ResultsView: View {
    @EnvironmentObject var session: BookSession
    @Environment(\.sizeCategory) private var sizeCategory
    let doc: ResultsDoc
    let gate: Gate

    var body: some View {
        VStack(spacing: 0) {
            ScrollView {
                // Lazy: every math prompt is a web view, and a results page
                // with a dozen of them must not hold a dozen web views on a
                // 2 GB iPad. Only the entries on screen are alive.
                LazyVStack(alignment: .leading, spacing: 22) {
                    VStack(alignment: .leading, spacing: 8) {
                        Text(doc.headLeft)
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(.secondary)
                        Text(doc.headline)
                            .font(Typography.display(32))
                            .fixedSize(horizontal: false, vertical: true)
                        Text(doc.dek)
                            .font(Typography.serif(19))
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                        if let tally = doc.tally {
                            Text(tally)
                                .font(.footnote.weight(.semibold))
                                .foregroundStyle(.secondary)
                                .padding(.top, 4)
                        } else {
                            GateBar(score: doc.score, gate: doc.gate, passed: doc.passed)
                                .padding(.top, 4)
                        }
                        if doc.extensionUnlocked == true {
                            Label("Extension material unlocked", systemImage: "sparkles")
                                .font(.callout)
                                .foregroundStyle(.purple)
                        }
                    }

                    ForEach(doc.entries) { entry in
                        ResultsEntryView(entry: entry)
                    }
                }
                .padding(28)
                .frame(maxWidth: 680)
                .frame(maxWidth: .infinity)
            }

            Divider()
            actions
                .disabled(session.busy)
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.bar)
        }
    }

    /// Side by side normally; stacked at accessibility text sizes, where
    /// three things in a row become three tall pillars.
    @ViewBuilder private var actions: some View {
        if doc.isCalibration || doc.passed {
            Button(doc.action) { Task { await session.proceed() } }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
        } else if sizeCategory.isAccessibilityCategory {
            VStack(alignment: .leading, spacing: 10) { gateActions }
        } else {
            HStack(alignment: .center, spacing: 14) { gateActions }
        }
    }

    @ViewBuilder private var gateActions: some View {
        Button("Just the misses") { Task { await session.repair() } }
            .buttonStyle(.borderedProminent)
            .keyboardShortcut(.defaultAction)
        Button("Explain it differently") { Task { await session.remediate() } }
            .buttonStyle(.bordered)
        Button("Override and continue anyway") { Task { await session.override() } }
            .buttonStyle(.bordered).tint(.orange)
        Text("Just the misses is a short chapter on what was missed, then those questions again. Overridden material lands in your debt; \"Catch me up\" collects it later.")
            .font(.footnote).foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }
}

/// Score against the gate, drawn rather than said: a bar with the gate line.
struct GateBar: View {
    let score: Double
    let gate: Double
    let passed: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    RoundedRectangle(cornerRadius: 4).fill(.fill.tertiary)
                    RoundedRectangle(cornerRadius: 4)
                        .fill(passed ? Color.green.opacity(0.7) : Color.orange.opacity(0.8))
                        .frame(width: geo.size.width * CGFloat(min(max(score, 0), 1)))
                    Rectangle()
                        .fill(Color.primary)
                        .frame(width: 2)
                        .offset(x: geo.size.width * CGFloat(gate) - 1)
                }
            }
            .frame(height: 10)
            Text("\(Int(score * 100 + 0.5))% · gate \(Int(gate * 100 + 0.5))%")
                .font(.footnote.weight(.semibold))
                .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Score \(Int(score * 100 + 0.5)) percent, gate \(Int(gate * 100 + 0.5)) percent, \(passed ? "cleared" : "below the gate")")
    }
}

struct ResultsEntryView: View {
    let entry: ResultsEntry

    private static let confidenceNames = ["", "UNSURE", "SHAKY", "CONFIDENT", "SURE"]

    private var mark: (glyph: String, color: Color) {
        if entry.isIDK || entry.verdict == "ungraded" { return ("—", .secondary) }
        if entry.verdict == "fail" { return ("✗", .red) }
        if entry.verdict == "partial" { return ("~", .orange) }
        return ("✓", .green)
    }

    private var miss: Bool { !entry.passed }

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Text(mark.glyph)
                .font(Typography.serif(22, weight: .bold))
                .foregroundStyle(mark.color)
                .frame(width: 24)
                .accessibilityLabel(entry.isIDK ? "Passed on" : entry.passed ? "Correct" : "Missed")
            VStack(alignment: .leading, spacing: 8) {
                HStack(alignment: .top) {
                    HStack(alignment: .top, spacing: 6) {
                        Text("\(entry.n).")
                            .font(Typography.serif(19, weight: .semibold))
                        MathText(text: entry.prompt, size: 19)
                    }
                    Spacer(minLength: 8)
                    if !entry.isIDK, let c = entry.confidence, (1...4).contains(c) {
                        // A confident miss is the miscalibration signal; it
                        // turns the tag accent.
                        Text(Self.confidenceNames[c])
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(c >= 3 && entry.verdict == "fail" ? .orange : .secondary)
                    }
                }
                if entry.isIDK {
                    MetaRow(label: "MARKED", value: "“I don't know.”")
                } else if entry.kind == "mcq" {
                    if let chose = entry.chose { MetaRow(label: "CHOSE", value: chose) }
                } else {
                    // The audit trail: every constructed entry shows what was
                    // read, even when correct.
                    MetaRow(label: "READ AS", value: "“\(entry.readAs ?? "")”")
                }
                if let answer = entry.answer, !answer.isEmpty,
                   miss || (entry.readAs ?? "").trimmingCharacters(in: .whitespacesAndNewlines) != answer.trimmingCharacters(in: .whitespacesAndNewlines) {
                    MetaRow(label: "ANSWER", value: answer)
                }
                if let why = entry.why, miss, !entry.isIDK {
                    MetaRow(label: "WHY", value: why)
                }
                if let flag = entry.flag, !flag.isEmpty {
                    MetaRow(label: "FLAGGED", value: "“\(flag)”")
                    if let label = entry.rulingLabel, let ruling = entry.flagRuling {
                        MetaRow(label: label, value: ruling)
                    }
                }
            }
        }
        .padding(.vertical, 6)
    }
}

struct MetaRow: View {
    let label: String
    let value: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(label)
                .font(.caption2.weight(.semibold))
                .foregroundStyle(.secondary)
                .fixedSize()
                .frame(minWidth: 64, alignment: .trailing)
            MathText(text: value, size: 16)
        }
    }
}
