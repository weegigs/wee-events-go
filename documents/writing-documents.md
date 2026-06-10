# Writing Documents

Documents produced for this ecosystem follow one rule: **do not ship the AI
draft**. A draft is raw material; the edit — selection, compression,
judgment — is what makes it a document. The failure mode is *workslop*:
polished-looking output that decides nothing and dumps the thinking on the
reader.

The test that runs through everything below: **could the document be cut from
the bottom and keep its impact?** If not, restructure it.

## Order: cut from the bottom

1. Point
2. Decision or recommendation
3. Reasoning
4. Trade-offs
5. Operational detail, edge cases, appendix

The reader who stops early loses depth, not meaning. The conclusion is never
a reward for finishing. Open with the answer — the conclusion, the decision,
the request — then explain why.

## Keep what cannot be regenerated

The reader has the same tools the author has. Definitions, background, and
obvious objections can be rebuilt on demand — leave them out. Spend the
document on what cannot be rebuilt: the numbers, the context, what was
decided and why. Compression beats coverage: the question is never "is this
comprehensive?" but "can the right reader understand, decide, or act with
minimal friction?"

## Voice

- Objective third person, no personal pronouns (house rule; overrides any
  example elsewhere written as "we should…").
- No throat-clearing: "It is important to note that…", "This document aims
  to…", "In conclusion…" — replace each with a direct claim.
- Specific over smooth: concrete nouns, verbs, numbers, owners. "Cut
  onboarding from seven steps to four", not "improve the onboarding journey".
- Headings carry the argument: reading the headings alone should still
  follow it.

## Length budgets

| Job | Target |
|---|---|
| Decision ask | 1 page |
| Technical design | 3–6 pages + appendix |
| Progress update | 5–10 bullets |
| Summary | 150–300 words |

Longer is allowed; the burden of proof rises with length.

## Exception: normative reference documents

Specs, grammars, and conformance documents optimise for **precision and
lookup**, not narrative brevity. In a normative document an unstated case is
undefined behaviour — completeness is the point. The voice and
regenerable-content rules still apply; the cut-from-the-bottom rule does not.

## Pre-publish checklist

1. Is the point visible in the first 30 seconds?
2. Does the opening start with the answer?
3. Could it be 30 percent shorter?
4. Do the headings mean something on their own?
5. Are generic claims replaced with specific facts?
6. Has detail moved down or out?
