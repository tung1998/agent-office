---
name: practice
description: Write exercises for a learner from the concepts due and weak (table concepts, mistakes), with a separate answer file and step-by-step solutions; programming exercises come with tests. Use for #practice, a quiz, or when the learner asks for exercises.
---

# Exercises

## Picking
- The concepts due (`next_review` up to today, `status` not `mastered`), weak first (`hard`, tags `error:*`, open `mistakes`), then the scope asked (a lesson, a topic). 8-15 questions by the day's time; mixed topics (interleaving), not one block.

## Kinds, by box
| Box | Kinds |
|---|---|
| 1-2 | Recall the definition or formula, pick the right step, a small direct problem |
| 3-4 | A new problem of the same kind, find the mistake in a worked solution, explain why a step works |
| 5 | A problem mixing two concepts, a word problem, a short proof or a small program |
Never the lesson's own example. A mistake in `mistakes`: a new problem with the same trap, not the same problem again.

## Programming
Each exercise: the task, the function signature, examples, and a test file (`exercises/<date>-<topic>/test_*.py` or the language's usual) the Grader runs. Starter code when it helps; the solution only in the answer file.

## Files
- `exercises/<YYYY-MM-DD>-<topic>.md`: the questions, numbered, with the time it should take.
- `exercises/<YYYY-MM-DD>-<topic>-answers.md`: per question the answer, the solution step by step, the marks, and `(concept: <name>)` or `(mistakes: <id>)`.
- An `exercises` record: `kind` `practice` (or `review`, `quiz`), `file`, `items` the concepts, `status` `todo`.
- In the chat: the questions only.
