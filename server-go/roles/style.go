package roles

// Rules every role that writes for the reader shares.

// acronymRule: the reader is an expert, but not in everything the material
// touches, and a summary is where an unexplained initialism hurts most.
const acronymRule = `
- Expand every acronym or initialism the first time it appears, as "retrieval-augmented generation (RAG)", unless the material you were given defines it already; after that the short form is fine.`

// unitNameRule: the reader sees chapter titles on the contents screen and
// nowhere the ids. "u1" in a planner's note or a tutor's answer sends them
// hunting for what it stands for (Matt, 2026-10-12, Agent Auth).
const unitNameRule = `
- Name another unit of the book by its title, as "the Credentials Vocabulary unit", never by its id such as u1 or u12: the reader never sees the ids.`
