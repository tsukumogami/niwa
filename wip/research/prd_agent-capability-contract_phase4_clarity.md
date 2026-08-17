# Clarity Review (serial-self-jury)

## Verdict: FAIL (minor, fixable in place)

The document is mostly specific, but one term breaks the public-standalone
rule and two passages read ambiguously.

## Ambiguities Found

1. R11 and Decisions 1: "the mandate" -> the term names an originating
   document a public reader cannot see and this artifact must not lean on.
   -> Replace with self-contained phrasing ("the standing scope rule").
2. R11: "the settings-document builder" -> undefined term -> add a
   parenthetical naming what it produces.
3. AC "Disabling, removing, or renaming nothing: applying three times adds
   nothing" -> garbled lead-in -> rewrite as a plain idempotence criterion.
4. AC "no existing test modified except by addition" -> ambiguous (modifying
   a test by adding lines?) -> "no existing test is modified or deleted; new
   tests are added".

## Suggested Improvements

1. In the collision AC, state the observable ("generation reports the
   collision") rather than the universal negative alone.

## Summary

Fix the "mandate" leak first -- it is a standalone-readability defect, not
just word choice. The rest are local rewordings.
