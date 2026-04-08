# Sentry synthetic baseline

This document records the upstream Sentry baseline used to judge Urgentry's Sentry-compatible ingest behavior.

## Target

- Product: `getsentry/self-hosted`
- Tag: `26.3.1`
- Commit: `22f76c8270153d8bb23f2f41b6ec908bd9dd7272`
- Runtime: Docker Compose on localhost
- UI base: `http://127.0.0.1:9000`

## What changed in the corpus

The original synthetic corpus was valid for Urgentry's local harness but not fully valid for real Sentry.

The Sentry-facing fixes required were:

- positive `event_id` values now use 32-character lowercase hex strings
- generated envelope headers for session, client-report, and check-in cases now use valid Sentry event IDs
- generated session IDs and check-in IDs now use UUID-shaped values
- store fixtures with `s030...`, `s031...`, and `s032...` IDs now use valid hex event IDs
- profile and native materialization paths now emit Sentry-valid IDs instead of synthetic string placeholders
- the synthetic manifest drift was removed:
  - session aggregate release now matches `synthetic@2.0.0`
  - check-in monitor slug now matches `synthetic-cron`

## Upstream-verified behaviors

These behaviors were validated against the live Dockerized Sentry instance through HTTP APIs and the browser UI.

- Store ingest: `POST /api/{project_id}/store/` accepted the corrected core store payload with `200`, and the event was retrievable through `GET /api/0/projects/{org}/{project}/events/{event_id}/`.
- Envelope ingest: `POST /api/{project_id}/envelope/` accepted corrected event envelopes with `200`, and the envelope event was retrievable through the event detail API.
- Check-ins / monitors: corrected check-in envelopes created the `synthetic-cron` monitor and check-ins were retrievable through `GET /api/0/projects/{org}/{project}/monitors/{slug}/checkins/`.
- Feedback: feedback envelopes only attach correctly when their target event is fresh. Sentry rejects or suppresses user-report linkage for events older than 30 minutes. A fresh event plus fresh feedback envelope roundtrip produced `userReport` data on the event detail API and rendered in the UI.
- Envelope attachment: the envelope-backed attachment case ingested successfully and surfaced as a real issue in the UI.

## Repeated validation

The following core loop was replayed repeatedly against the same Sentry project:

- corrected store event
- corrected envelope event
- corrected check-in envelope
- fresh event + fresh feedback envelope

Observed stable result:

- store: `200`
- envelope event: `200`
- check-in: `200`
- fresh event: `200`
- fresh feedback: `200`
- feedback linkage: visible on the event detail API after asynchronous processing

## Visual verification

The following screenshots were captured with `agent-browser`:

- issue feed with synthetic store, envelope, cron, and attachment issues:
  - [issues-feed.png](../../sessions/sentry-baseline/browser/issues-feed.png)
- issue detail showing the attached user feedback section:
  - [issue-detail-feedback.png](../../sessions/sentry-baseline/browser/issue-detail-feedback.png)
- user feedback list showing the fresh feedback submission:
  - [user-feedback.png](../../sessions/sentry-baseline/browser/user-feedback.png)

## Upstream divergences

These surfaces did not validate as upstream Sentry surfaces in this Dockerized baseline and should not be treated as required Sentry compatibility targets unless upstream evidence changes.

- standalone attachment API at `/api/0/projects/{org_slug}/{proj_slug}/attachments/`: returned `404`
- project and organization release file upload APIs for source-map fixture upload: returned `404` in this self-hosted baseline even after creating the target release
- project release ProGuard route: returned `404`
- Urgentry import/export API: no upstream Sentry equivalent in this baseline exercise

## Baseline rules for future Urgentry comparisons

- A Sentry-compatible positive event must use a 32-character lowercase hex `event_id`.
- Feedback baselines must use freshly materialized events, not stale fixture timestamps.
- Check-in baselines should use UUID-shaped `check_in_id` values and verify both the issue stream and the monitor/check-in API.
- A surface is only "Sentry-baselined" when it is both accepted by upstream Sentry and observable through upstream read or UI surfaces.
- The checked-in synthetic corpus is the runtime contract for both Tiny and serious self-hosted validation. When Urgentry diverges, fix the runtime behavior instead of editing the generator or generated corpus to match the bug.

## Tiny-mode replay command

Use the checked-in Tiny-mode gate to replay the upstream-baselined corpus against a live Urgentry binary:

```bash
cd .
make tiny-sentry-baseline
```

## Serious self-hosted replay command

Use the serious self-hosted gate to replay the same checked-in corpus against the Compose-backed operator baseline:

```bash
cd .
make selfhosted-sentry-baseline
```
