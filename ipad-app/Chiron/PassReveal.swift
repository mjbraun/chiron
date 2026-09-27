import Foundation

/// What a choice item shows after "I don't know". The answer is still
/// revealed - the reader asked to move on, not to be kept in the dark - but
/// named as the answer to a question they passed on, not as their pick. An
/// option's `explain` is authored for the person who tapped it, so the
/// affirmation it opens with ("Right; ...") comes off before it is shown.
enum PassReveal {
    static func headline(correct: Int?) -> String {
        guard let i = correct, let letter = UnicodeScalar(65 + i) else {
            return "You passed on this one."
        }
        return "You passed on this one. The answer is \(Character(letter))."
    }

    /// An opening word that agrees with the chooser, and the punctuation
    /// after it: "Right;", "Correct -", "Yes:", "Exactly,".
    private static let affirmation = try! NSRegularExpression(
        pattern: #"^(right|correct|yes|exactly|indeed|that's right|that's it|spot on)\s*[.;:,!-]+\s*"#,
        options: [.caseInsensitive])

    static func explanation(_ explain: String) -> String {
        let text = explain.trimmingCharacters(in: .whitespacesAndNewlines)
        let range = NSRange(text.startIndex..., in: text)
        guard let m = affirmation.firstMatch(in: text, range: range),
              let cut = Range(m.range, in: text) else { return text }
        let rest = String(text[cut.upperBound...])
        guard let first = rest.first else { return "" }
        return first.uppercased() + rest.dropFirst()
    }
}
