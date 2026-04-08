# Urgentry Tiny Feedback Loop

Use this when Tiny mode is in front of design partners, early adopters, or anyone testing a launch candidate.

The point is to keep incoming feedback boring to triage. Every report should land in one obvious bucket, and every useful report should turn into product work instead of sitting in GitHub as a vague complaint.

## Where feedback goes

Use these issue forms:

- bug report
  - product breakage, failed setup, regressions, packaging problems
- docs problem
  - stale commands, wrong paths, missing setup details, broken links
- design-partner feedback
  - friction, confusion, missing analytics affordances, unclear page purpose, rough first-run experience
- feature request
  - requests for new product surface or broader workflow support

## Triage buckets

Sort each report into one of these outcomes:

- fix now
  - launch blocker, broken install path, auth failure, broken ingest, bad docs
- small product follow-up
  - rough empty state, missing onboarding clue, weak default dashboard, unclear copy
- roadmap item
  - valid request, but too large for the current launch window
- close with docs or support answer
  - the product already supports the workflow and the report exposed missing guidance

## Labels to use

Keep the labels simple:

- `tiny-mode`
- `design-partner`
- `bug`
- `documentation`
- `enhancement`

If the issue blocks a launch candidate, add a maintainer note that says so in plain language. Do not rely on a private mental list.

## How maintainers feed it back into the roadmap

Public GitHub issues are the intake layer. Beads stay the implementation layer.

When a report turns into work:

1. decide whether it is a bug, a docs fix, or a product improvement
2. create or link the matching bead in the repo tracker
3. add the bead link back to the GitHub issue
4. close the public issue only when the shipped change or docs update lands

That keeps public discussion readable without losing the dependency structure in the internal work graph.

## What counts as analytics UX friction

Use the design-partner form when someone says things like:

- "I got to Discover, but I do not know what query to run first."
- "The dashboard page is empty and I do not know what a good first widget looks like."
- "I can export this, but I do not understand when to use a saved query instead."
- "I have replay and profile data, but I cannot tell which page should answer my question."

Those are not random opinions. They are product gaps in first-run guidance, defaults, or workflow shape.

## Weekly review loop

For active design-partner periods:

1. review new `design-partner` and `tiny-mode` issues
2. group duplicates around the same workflow failure
3. turn repeated friction into one bead with a concrete acceptance bar
4. update docs or starter packs when the feedback points to onboarding instead of missing backend work

## Related docs

- [../SUPPORT.md](../../SUPPORT.md)
- [../CONTRIBUTING.md](../../CONTRIBUTING.md)
- [launch-gate](launch-gate.md)
- [docs-index](README.md)
