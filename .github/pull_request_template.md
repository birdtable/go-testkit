## What changes

<!-- What this pull request changes, and why. Link the issue if there is one. -->

## Every exported constant or default this adds

<!--
For each exported constant, default, message or fixed table this pull request adds or changes,
answer: would a second consumer running a different business ever want a different value?

- No: it is a fact of the machine or protocol. Say which.
- Yes: it comes from the caller, with no default. The one allowed default is a safe one:
  fail-closed, documented as "the safe default" in its doc, and overridable.

Name each one here, or write "none". internal/example/secondconsumer sets every value a caller
gives with one unlike ours, in the commit tier; a new value of the caller's gets a row there.
-->
