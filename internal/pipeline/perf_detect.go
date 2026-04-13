package pipeline

import (
	"net/url"
	"regexp"
	"strings"

	"urgentry/internal/normalize"

	"github.com/rs/zerolog/log"
)

// PerfIssueType constants for the three detectable anti-patterns.
const (
	PerfIssueNPlusOneDB     = "n_plus_one_db"
	PerfIssueSlowDB         = "slow_db"
	PerfIssueConsecutiveHTTP = "consecutive_http"
)

// PerfIssue represents a detected performance anti-pattern within a transaction.
type PerfIssue struct {
	Type         string
	Description  string
	SpanIDs      []string
	ParentSpanID string
}

// nPlusOneThreshold is the minimum number of consecutive same-template DB spans
// that triggers an N+1 detection.
const nPlusOneThreshold = 5

// slowDBThresholdMS is the minimum span duration in milliseconds to flag a single
// DB span as slow.
const slowDBThresholdMS = 1000

// consecutiveHTTPThreshold is the minimum number of sequential http.client spans
// to the same host that could have been parallelized.
const consecutiveHTTPThreshold = 3

// reQueryLiterals strips literal values from SQL-like descriptions to produce a
// stable query template for grouping.
var reQueryLiterals = regexp.MustCompile(`'[^']*'|\b\d+\b`)

// queryTemplate normalises a DB span description into a template by removing
// literal string and numeric values.
func queryTemplate(desc string) string {
	return strings.TrimSpace(reQueryLiterals.ReplaceAllString(strings.ToLower(desc), "?"))
}

// spanDurationMS returns the duration of a span in milliseconds, computed from
// StartTimestamp and Timestamp. Returns 0 when timestamps are missing or zero.
func spanDurationMS(s normalize.Span) float64 {
	if s.StartTimestamp.IsZero() || s.Timestamp.IsZero() {
		return 0
	}
	d := s.Timestamp.Sub(s.StartTimestamp)
	if d < 0 {
		return 0
	}
	return float64(d.Milliseconds())
}

// extractHost returns the hostname from a URL-like string. Falls back to the
// raw string when parsing fails.
func extractHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil && u.Host != "" {
		return u.Host
	}
	// Fallback: take everything up to the first slash after a scheme, or the
	// first path component.
	if idx := strings.Index(rawURL, "//"); idx >= 0 {
		rest := rawURL[idx+2:]
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			return rest[:slash]
		}
		return rest
	}
	return rawURL
}

// detectPerformanceIssues analyses transaction spans for performance anti-patterns.
// Returns a list of detected issues, or nil when none are found. The caller is
// responsible for ensuring evt is a transaction event.
func detectPerformanceIssues(evt *normalize.Event) []PerfIssue {
	if len(evt.Spans) == 0 {
		return nil
	}

	var issues []PerfIssue

	issues = append(issues, detectNPlusOneDB(evt.Spans)...)
	issues = append(issues, detectSlowDB(evt.Spans)...)
	issues = append(issues, detectConsecutiveHTTP(evt.Spans)...)

	if len(issues) > 0 {
		log.Debug().
			Str("event_id", evt.EventID).
			Str("transaction", evt.Transaction).
			Int("issue_count", len(issues)).
			Msg("pipeline: performance issues detected")

		for _, iss := range issues {
			log.Debug().
				Str("event_id", evt.EventID).
				Str("type", iss.Type).
				Str("description", iss.Description).
				Strs("span_ids", iss.SpanIDs).
				Msg("pipeline: performance issue detail")
		}
	}

	return issues
}

// detectNPlusOneDB scans for 5+ consecutive db spans sharing the same query
// template.
func detectNPlusOneDB(spans []normalize.Span) []PerfIssue {
	var issues []PerfIssue

	i := 0
	for i < len(spans) {
		s := spans[i]
		if s.Op != "db" {
			i++
			continue
		}
		tmpl := queryTemplate(s.Description)
		// Collect a run of db spans with the same query template.
		j := i + 1
		for j < len(spans) && spans[j].Op == "db" && queryTemplate(spans[j].Description) == tmpl {
			j++
		}
		count := j - i
		if count >= nPlusOneThreshold {
			spanIDs := make([]string, 0, count)
			for k := i; k < j; k++ {
				spanIDs = append(spanIDs, spans[k].SpanID)
			}
			parentID := spans[i].ParentSpanID
			issues = append(issues, PerfIssue{
				Type: PerfIssueNPlusOneDB,
				Description: strings.TrimSpace(strings.Join([]string{
					"N+1 DB query detected:",
					s.Description,
					"repeated",
					itoa(count),
					"times",
				}, " ")),
				SpanIDs:      spanIDs,
				ParentSpanID: parentID,
			})
		}
		// Advance past the entire run (whether flagged or not).
		i = j
	}

	return issues
}

// detectSlowDB flags any single span whose Op starts with "db" and whose
// duration exceeds slowDBThresholdMS.
func detectSlowDB(spans []normalize.Span) []PerfIssue {
	var issues []PerfIssue

	for _, s := range spans {
		if !strings.HasPrefix(s.Op, "db") {
			continue
		}
		ms := spanDurationMS(s)
		if ms > slowDBThresholdMS {
			issues = append(issues, PerfIssue{
				Type: PerfIssueSlowDB,
				Description: strings.TrimSpace(strings.Join([]string{
					"Slow DB span:",
					s.Description,
					"(", fmtMS(ms), "ms )",
				}, " ")),
				SpanIDs:      []string{s.SpanID},
				ParentSpanID: s.ParentSpanID,
			})
		}
	}

	return issues
}

// detectConsecutiveHTTP finds 3+ sequential http.client spans to the same host.
func detectConsecutiveHTTP(spans []normalize.Span) []PerfIssue {
	var issues []PerfIssue

	i := 0
	for i < len(spans) {
		s := spans[i]
		if s.Op != "http.client" {
			i++
			continue
		}
		host := extractHost(s.Description)
		// Collect the run of consecutive http.client spans to the same host.
		j := i + 1
		for j < len(spans) && spans[j].Op == "http.client" && extractHost(spans[j].Description) == host {
			j++
		}
		count := j - i
		if count >= consecutiveHTTPThreshold {
			spanIDs := make([]string, 0, count)
			for k := i; k < j; k++ {
				spanIDs = append(spanIDs, spans[k].SpanID)
			}
			parentID := spans[i].ParentSpanID
			issues = append(issues, PerfIssue{
				Type: PerfIssueConsecutiveHTTP,
				Description: strings.TrimSpace(strings.Join([]string{
					"Consecutive HTTP requests to",
					host,
					"could be parallelized (",
					itoa(count),
					"spans)",
				}, " ")),
				SpanIDs:      spanIDs,
				ParentSpanID: parentID,
			})
		}
		// Advance past the entire run (whether flagged or not).
		i = j
	}

	return issues
}

// itoa converts an int to its decimal string representation without importing
// strconv (we already have strings in scope).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// fmtMS formats a float64 millisecond value as an integer string.
func fmtMS(ms float64) string {
	return itoa(int(ms))
}
