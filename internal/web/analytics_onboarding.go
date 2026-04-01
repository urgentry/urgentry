package web

import "strings"

type analyticsGuide struct {
	Title       string
	Body        string
	Steps       []string
	ActionLabel string
	ActionURL   string
}

func discoverGuide(title, dataset string) analyticsGuide {
	switch strings.ToLower(strings.TrimSpace(dataset)) {
	case "logs":
		return analyticsGuide{
			Title: "Logs gives you raw event context.",
			Body:  "Use it when you need to inspect recent messages, group by logger, or export a slice of logs for another tool.",
			Steps: []string{
				"Start with a simple text query or add an environment filter.",
				"Save a shared query when the team reuses the same slice.",
				"Promote that query into a dashboard widget after it proves useful.",
			},
			ActionLabel: "Open starter dashboards",
			ActionURL:   "/dashboards/",
		}
	default:
		return analyticsGuide{
			Title: title + " is the ad hoc analysis surface.",
			Body:  "Use it to explore issues, logs, or transactions before you save a query or turn the result into a dashboard.",
			Steps: []string{
				"Run a query with the builder and check the result table first.",
				"Save the query with a description and tags when you want to keep it.",
				"Reuse the saved query in a dashboard or export it as CSV or JSON.",
			},
			ActionLabel: "Browse starter dashboards",
			ActionURL:   "/dashboards/",
		}
	}
}

func analyticsHomeGuide() analyticsGuide {
	return analyticsGuide{
		Title: "Analytics home ties the live surfaces together.",
		Body:  "Use this page to spot the current problem, then jump into the right surface instead of opening Discover or dashboards blind.",
		Steps: []string{
			"Start with the banner, metrics, and issue watchlist to see whether the system is noisy or stable.",
			"Use the logs, transactions, replay, profile, and release sections to choose the next surface with real context.",
			"Promote repeated analysis into a saved query, dashboard widget, snapshot, or scheduled report once the team trusts it.",
		},
		ActionLabel: "Open Discover",
		ActionURL:   "/discover/",
	}
}

func dashboardsGuide() analyticsGuide {
	return analyticsGuide{
		Title: "Dashboards are where repeat analysis should land.",
		Body:  "Start with a pack, keep the widgets that matter, then replace the rest with saved queries from Discover.",
		Steps: []string{
			"Use a starter pack for ops, releases, or performance.",
			"Add saved queries after the team trusts the underlying search.",
			"Export widget results when a report needs to leave Urgentry.",
		},
		ActionLabel: "Go to Discover",
		ActionURL:   "/discover/",
	}
}

func replaysGuide() analyticsGuide {
	return analyticsGuide{
		Title: "Replays help you watch what happened before an error.",
		Body:  "Use this page to find recent sessions, then open one replay to inspect the timeline, linked issues, and trace context.",
		Steps: []string{
			"Filter your SDK flow until at least one replay lands here.",
			"Open a replay and jump between the recording, snapshots, and issue links.",
			"Check release and environment fields before you compare two sessions.",
		},
		ActionLabel: "Review replay setup",
		ActionURL:   "/settings/",
	}
}

func profilesGuide() analyticsGuide {
	return analyticsGuide{
		Title: "Profiles show where time was spent inside a request.",
		Body:  "Use the list to narrow by transaction, release, or environment, then open one profile to inspect the call tree and hot path.",
		Steps: []string{
			"Filter by transaction name when you know the slow path already.",
			"Open a profile from the same release you are debugging.",
			"Jump from a profile into its trace when you need request context.",
		},
		ActionLabel: "Open performance dashboards",
		ActionURL:   "/dashboards/",
	}
}

func traceGuide() analyticsGuide {
	return analyticsGuide{
		Title: "Trace detail ties transactions, spans, and profiles together.",
		Body:  "Use it when one request spans multiple services or when you need to line profile data up with the stored trace.",
		Steps: []string{
			"Start with the transaction list to confirm the request path.",
			"Use spans to spot the slow section.",
			"Open a related profile when you need function-level detail.",
		},
		ActionLabel: "Back to profiles",
		ActionURL:   "/profiles/",
	}
}
