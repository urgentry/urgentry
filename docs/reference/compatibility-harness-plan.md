# Urgentry compatibility harness plan

Status: starter test-plan doc
Last updated: 2026-03-19

## Purpose

Define how to prove compatibility without relying on licensed implementation code.

The harness should validate:
- wire compatibility
- normalized-event stability
- grouping behavior
- migration-critical workflows

## Clean-room rule

The harness may use:
- public SDKs
- public docs
- black-box request/response observation
- independently created fixtures and golden outputs

It must not depend on copying internal implementation behavior from licensed code.

## Harness goals

### Goal 1 — protocol confidence
Ensure common SDK paths can send data successfully with only DSN/base URI changes.

### Goal 2 — normalization confidence
Ensure important fields survive ingest in a deterministic internal form.

### Goal 3 — grouping confidence
Ensure similar events group together and clearly different events split.

### Goal 4 — migration confidence
Ensure releases, source maps, attachments, feedback, and alerts work well enough to support real design-partner trials.

## Harness layers

## Layer A — parser/unit tests
Test locally without network.

Covers:
- DSN parsing
- envelope parsing
- compressed payload decoding
- grouping input construction
- grouping hash stability

Location:
- `pkg/dsn/*_test.go`
- `internal/normalize/*_test.go`
- `internal/grouping/*_test.go`

## Layer B — request/response contract tests
Run against a local Urgentry instance.

Covers:
- legacy store endpoint status codes
- envelope endpoint status codes
- auth failures
- rate-limit responses later
- attachment acceptance
- user feedback acceptance

Location:
- `testdata/compat/contracts/`
- integration test package under `internal/compat/`

## Layer C — normalized snapshot tests
Input payload in, golden normalized JSON out.

Covers:
- error event extraction
- tags/contexts/user/request preservation
- release/environment extraction
- SDK metadata preservation
- source-map-related frame fields

Location:
- `testdata/compat/normalized/`

Suggested layout:
```text
testdata/compat/normalized/
  js/
  python/
  go/
  java/
  dotnet/
  ruby/
```

For each fixture:
- input payload
- expected normalized JSON
- notes about expected caveats

## Layer D — grouping snapshot tests
Input normalized events in, expected grouping decisions out.

Cases:
- same stacktrace, same message -> same group
- same stacktrace, different release -> same group
- fingerprint override -> forced group
- different top frame or exception type -> different group
- noisy values removed -> still same group

Location:
- `testdata/compat/grouping/`

## Layer E — workflow smoke tests
End-to-end scenarios against a local stack.

Scenarios:
- ingest -> normalize -> issue appears
- release linked -> issue filter works
- source map uploaded -> event detail enriches correctly in basic JS path
- attachment uploaded -> attachment retrievable in metadata flow
- user feedback linked to event/group
- alert rule triggers email/webhook path in test mode

Location:
- `testdata/compat/workflows/`

## Fixture categories

## 1. Error events by SDK family
Need at least:
- JavaScript browser
- JavaScript node
- Python
- Go
- Java
- .NET
- Ruby

## 2. Payload forms
Need:
- legacy store payload
- envelope payload
- compressed payload
- attachment-bearing envelope
- feedback payload

## 3. Error shape coverage
Need:
- plain exception
- chained exception
- message-only event
- fingerprint override
- tags + contexts + user + request rich event
- release/environment populated event
- minified JS stack for future source-map test

## 4. Negative cases
Need:
- malformed DSN
- invalid project key
- oversized payload
- malformed JSON
- unsupported content type where relevant

## Golden artifact strategy

For each fixture family, keep:
- `input.*`
- `expected-response.json` where useful
- `expected-normalized.json`
- `expected-grouping.json` where relevant
- `notes.md`

Example:
```text
testdata/compat/normalized/js/browser-basic/
  input-envelope.json
  expected-normalized.json
  notes.md
```

## Support matrix generation

The harness should eventually emit a support matrix with rows like:
- SDK family
- protocol path
- ingest pass/fail
- normalization pass/fail
- grouping pass/fail
- artifact workflow pass/fail
- notes/gaps

Suggested output:
- machine-readable JSON
- markdown summary for docs

## First harness milestone

The first milestone should prove only the core migration wedge:
- DSN parsing
- legacy store ingest
- envelope ingest
- error normalization
- grouping v1
- issue creation

Do not wait for source maps, traces, or native parity before creating the harness.

## Suggested file layout

```text
testdata/
  compat/
    contracts/
    normalized/
      js/
      python/
      go/
      java/
      dotnet/
      ruby/
    grouping/
    workflows/
    support-matrix/
```

## First 10 fixtures to create

1. JS browser envelope basic error
2. JS node envelope basic error
3. Python legacy store basic error
4. Go envelope error with tags and contexts
5. Java basic error with release/environment
6. .NET basic error with breadcrumbs
7. Ruby message-only event
8. JS event with fingerprint override
9. malformed DSN auth failure case
10. oversized payload rejection case

## Rules for stable goldens

To keep snapshots stable:
- scrub timestamps where needed
- scrub generated UUIDs in expected outputs
- scrub nondeterministic ordering
- version the grouping algorithm explicitly
- keep normalization version markers in expected outputs when helpful

## Success bar

The harness is useful when it can answer:
- can a common SDK send events successfully?
- what exact fields survive normalization?
- how does grouping behave on known cases?
- what compatibility claims are safe to make today?

## Related docs

- `docs/urgentry-monorepo-guidelines.md`
- `docs/urgentry-compatibility-matrix.md`
- `docs/urgentry-mvp-cut.md`
- `docs/urgentry-implementation-backlog.md`
