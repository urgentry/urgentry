# Urgentry Tiny Analytics Snapshots

Tiny mode now supports shareable analytics snapshots and scheduled report delivery.

Use a snapshot when you want to freeze the current result from a saved query or a dashboard widget and hand someone a stable link that does not depend on the live query changing underneath them.

When the source is a dashboard widget, Tiny mode freezes the effective widget result after the dashboard's own filters have been applied. If the board is scoped to one release or transaction, the snapshot keeps that scoped result instead of rerunning the raw widget query later.

## What ships today

You can create a snapshot from:

- `/discover/queries/{id}/`
- `/dashboards/{id}/`

Each snapshot stores:

- the frozen table or stat result
- a share token
- the source type and source ID
- the source label, dataset, visualization kind, and effective filter summary
- the normalized query explain payload used to produce the frozen result
- the creation time
- the expiry time

The share link is:

```text
/analytics/snapshots/{token}/
```

That page is readable without a session. It shows the frozen result, the widget or saved-query contract that produced it, and still supports CSV or JSON export. The page now renders the full share URL so callers can copy the exact frozen link instead of reconstructing it from the token.

## Retention rule

Tiny mode keeps analytics snapshots in SQLite for 30 days.

Expired rows are removed before new snapshots are created and before existing links are loaded.

That gives Tiny mode a simple rule:

- snapshots are for sharing and short-lived reporting
- snapshots are not a permanent archive

## Scheduled reports

Saved-query detail pages and dashboard-widget drilldowns can each create scheduled reports.

Tiny mode currently supports:

- one or more schedules per source for the current signed-in user
- `daily` or `weekly` cadence
- delivery through the Tiny notification outbox as queued email records
- automatic snapshot creation on the scheduler tick before the email is recorded

Each scheduled report keeps:

- the recipient
- the cadence
- the next scheduled run
- the last run attempt
- the last frozen snapshot link
- the last error if the source stops executing cleanly

Set `URGENTRY_BASE_URL` to the public Tiny-mode URL if you want those queued report emails to carry absolute links instead of relative `/analytics/snapshots/{token}/` paths.

## When to use it

Use a snapshot when:

- you need a stable link for a handoff
- you want to capture the state of a dashboard widget before it changes
- you need to export a frozen result later in CSV or JSON with the same filter and query context attached

Use a scheduled report when:

- you want the same frozen handoff every day or every week
- you want queued delivery to reuse the same snapshot contract instead of rerunning exports in a browser tab
- you want to verify report generation through the Tiny outbox before adding a real SMTP transport later

Use a saved query or a dashboard when:

- you want the query to stay live
- you expect the result to update with new events
- you are still iterating on the shape of the analysis

## Related docs

- [launch-gate](launch-gate.md)
- [Operations guide](../architecture/operations-guide.md)
- [Release process](../architecture/release-process-and-versioning.md)
