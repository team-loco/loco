---
name: prose
description: Writing rules for any prose in the loco repo. Load before writing or editing a PR description, issue body, commit message body, code review comment, README, anything under docs/, or user-facing text in the CLI or web UI.
---

# Writing loco prose

Strangers read everything in this repo. Write so a reader who has never seen the
conversation that produced the text can act on it.

## Before writing

- Know who reads it and what they should do after: review a PR, reproduce a bug, run a
  command, choose an option. Cut anything that does not serve that.
- Lead with the point. A PR body opens with what changed, an issue with what is broken or
  missing, a doc with what the thing is.

## Rules

- **Be concrete.** Names, numbers, commands, file paths, error messages. "Cut deploy time
  from 40 minutes to 4", not "improved deploy performance".
- **Portability test.** If a sentence could move unchanged into another project's PR, it is
  filler. Replace it with a fact specific to this change or delete it.
- **Active voice, direct verbs.** "The controller deletes the namespace", not "the namespace
  is deleted". "Decided", not "made a decision". "Can", not "has the ability to".
- **Present tense, current behaviour.** Describe how the system works now. Leave history and
  plans out of docs; plans become issues.
- **Repeat the right word.** Call a thing by one name throughout. Don't rotate "agent",
  "assistant", "tool" for variety.
- **Show, don't label.** Don't tell the reader a point is important, subtle or surprising.
  State the fact and its consequence.
- **Name sources.** "Experts agree" and "studies show" either get a link or get cut.
- **Don't invent.** No made-up numbers, examples or claims. If a fact is missing, find it or
  leave the sentence out.
- **Format follows content.** Use a list for items that are actually parallel and two
  sentences of prose otherwise. No headers over two-sentence sections, no bold scattered
  mid-sentence, no emoji. Code, commands and paths go in backticks.
- **Unambiguous characters.** No homoglyphs or confusable Unicode in identifiers, commands
  or code samples.

## Words to cut

Banned: delve, foster, leverage, utilize, facilitate, empower, streamline, robust,
cutting-edge, seamless, game changer, tapestry, realm, beacon, multifaceted, meticulous,
intricate, paramount, transformative, elevate, embark, supercharge, harness, ever-evolving,
synergy, paradigm, blazing fast, battle-tested, under the hood, out of the box, first-class.

Usually empty: just, simply, actually, really, very, quite, truly, fundamentally,
importantly, crucially, critical, vital, essential (unless something breaks without it),
it's worth noting, it's important to note, at its core, when it comes to, in terms of,
in order to, going forward.

## Patterns to cut

- **Binary contrasts.** "It's not X, it's Y." State Y.
- **Throat-clearing and fake insight.** "Here's the thing", "What most people miss". State
  the point.
- **Colon reveals.** "The fix: a retry." Write "A retry fixes it." Colons are for lists,
  labels and quotes.
- **Trailing -ing analysis.** "..., highlighting our commitment to reliability." Replace with
  the actual consequence or delete.
- **Puffery.** "Marks a pivotal moment", "plays a vital role". State the fact.
- **Metadiscourse.** "As you can see", "The key point is", "This distinction matters".
- **Dramatic fragments and kickers.** "That's it. That's the whole fix." End on the last
  concrete point.
- **Recap endings.** "In summary", "Overall", or a closing paragraph that repeats the body.
- **Em dashes as rhythm.** None in short text; at most one or two in a long doc.

## Commit messages and PRs

- Subject: `type(scope): summary` in the imperative, under 72 characters (`fix(controller):
  requeue when the namespace is terminating`).
- Body: why the change exists and what a reviewer must know to judge it. Root cause for a
  fix. No list of files touched; the diff shows that.
- Nothing conversational and no AI attribution (see AGENTS.md, Repo Hygiene).

## Docs

Open with one sentence saying what the thing is, then why it exists, then how to use it
with a real command or config snippet.
