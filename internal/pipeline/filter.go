package pipeline

import (
	"context"
	"regexp"
	"strings"
	"time"

	"urgentry/internal/domain"
	"urgentry/internal/normalize"
	"urgentry/internal/sqlite"

	"github.com/rs/zerolog/log"
)

// FilterStore is the subset of InboundFilterStore the pipeline needs.
type FilterStore interface {
	ListFilters(ctx context.Context, projectID string) ([]*domain.InboundFilter, error)
}

// filterReason is returned when an event is filtered, indicating which filter
// matched so the outcome can be recorded.
type filterReason struct {
	FilterID string // e.g. "browser-extensions"
	Category string // "error" or "transaction"
}

// checkFilters returns a non-nil filterReason if the event should be dropped.
func checkFilters(ctx context.Context, fs FilterStore, projectID string, evt *normalize.Event) *filterReason {
	if fs == nil {
		return nil
	}
	filters, err := fs.ListFilters(ctx, projectID)
	if err != nil {
		log.Warn().Err(err).Str("project_id", projectID).Msg("pipeline: failed to load inbound filters, admitting event")
		return nil
	}

	active := make(map[domain.FilterType]*domain.InboundFilter)
	for _, f := range filters {
		if f.Active {
			active[f.Type] = f
		}
	}
	if len(active) == 0 {
		return nil
	}

	category := evt.EventType()
	ua := eventUserAgent(evt)

	// browser-extensions (mapped to FilterLocalhost in domain)
	if _, ok := active[domain.FilterLocalhost]; ok {
		if matchBrowserExtension(evt) {
			return &filterReason{FilterID: "browser-extensions", Category: category}
		}
	}

	// legacy-browsers
	if _, ok := active[domain.FilterLegacyBrowser]; ok {
		if matchLegacyBrowser(ua) {
			return &filterReason{FilterID: "legacy-browsers", Category: category}
		}
	}

	// web-crawlers
	if _, ok := active[domain.FilterCrawler]; ok {
		if matchWebCrawler(ua) {
			return &filterReason{FilterID: "web-crawlers", Category: category}
		}
	}

	// filtered-transaction (mapped to FilterIPRange in domain)
	if f, ok := active[domain.FilterIPRange]; ok {
		if matchFilteredTransaction(evt, f.Pattern) {
			return &filterReason{FilterID: "filtered-transaction", Category: category}
		}
	}

	return nil
}

// eventUserAgent extracts the User-Agent string from the event request headers
// or from the browser context.
func eventUserAgent(evt *normalize.Event) string {
	if evt.Request != nil {
		for k, v := range evt.Request.Headers {
			if strings.EqualFold(k, "User-Agent") {
				return v
			}
		}
	}
	if evt.Contexts != nil {
		if browser, ok := evt.Contexts["browser"]; ok {
			if m, ok := browser.(map[string]any); ok {
				if name, _ := m["name"].(string); name != "" {
					version, _ := m["version"].(string)
					return name + "/" + version
				}
			}
		}
	}
	return ""
}

// matchBrowserExtension checks if the error was caused by a browser extension.
// Looks at stack frame paths and error messages for common extension patterns.
func matchBrowserExtension(evt *normalize.Event) bool {
	if evt.Exception != nil {
		for _, exc := range evt.Exception.Values {
			// Check error messages for extension-related patterns.
			val := strings.ToLower(exc.Value)
			for _, pattern := range browserExtensionMessagePatterns {
				if strings.Contains(val, pattern) {
					return true
				}
			}
			// Check stack frames for extension URLs.
			if exc.Stacktrace != nil {
				for _, frame := range exc.Stacktrace.Frames {
					path := strings.ToLower(frame.AbsPath + " " + frame.Filename)
					for _, pattern := range browserExtensionPathPatterns {
						if strings.Contains(path, pattern) {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

var browserExtensionPathPatterns = []string{
	"chrome-extension://",
	"moz-extension://",
	"safari-extension://",
	"safari-web-extension://",
	"webkit-masked-url://",
}

var browserExtensionMessagePatterns = []string{
	"chrome-extension://",
	"moz-extension://",
	"safari-extension://",
}

// matchLegacyBrowser returns true if the user agent belongs to a legacy browser.
// Targets: IE <= 10, old Edge (non-Chromium), Opera < 15, old Android browsers,
// and Safari < 9.
func matchLegacyBrowser(ua string) bool {
	if ua == "" {
		return false
	}
	return legacyBrowserRe.MatchString(ua)
}

// legacyBrowserRe matches common legacy browser user-agent strings.
var legacyBrowserRe = regexp.MustCompile(`(?i)` +
	`MSIE [1-9][^0-9]|MSIE 10[^0-9]|` + // IE <= 10
	`Trident/.*rv:11|` + // IE 11 (also legacy)
	`Opera/[1-9]\.[0-9]|Opera/1[0-4]\.|` + // Opera < 15
	`Android [1-4]\.[0-9].*Version/[0-4]\.|` + // Android browser <= 4.x
	`(?:^|[^A-Za-z])UCBrowser/`)

// matchWebCrawler returns true if the user agent matches a known bot/crawler.
var crawlerRe = regexp.MustCompile(`(?i)` +
	`bot[/\s;)]|crawl|spider|slurp|mediapartners|` +
	`facebookexternalhit|twitterbot|linkedinbot|embedly|` +
	`quora link preview|showyoubot|outbrain|pinterest|` +
	`bingpreview|google web preview|googlebot|baiduspider|` +
	`yandexbot|developers\.google\.com/\+/web/snippet|` +
	`Applebot|duckduckbot|ia_archiver|archive\.org_bot|` +
	`HeadlessChrome|PhantomJS|Lighthouse|PTST|Screaming Frog`)

func matchWebCrawler(ua string) bool {
	if ua == "" {
		return false
	}
	return crawlerRe.MatchString(ua)
}

// matchFilteredTransaction returns true if the event is a transaction whose
// name matches one of the configured patterns (comma-separated in the filter's
// Pattern field). Patterns use simple glob matching: * matches any substring.
func matchFilteredTransaction(evt *normalize.Event, patterns string) bool {
	if evt.EventType() != "transaction" || patterns == "" {
		return false
	}
	txName := strings.TrimSpace(evt.Transaction)
	if txName == "" {
		return false
	}
	for _, raw := range strings.Split(patterns, ",") {
		pat := strings.TrimSpace(raw)
		if pat == "" {
			continue
		}
		if globMatch(pat, txName) {
			return true
		}
	}
	return false
}

// globMatch performs simple glob matching where * matches any sequence.
func globMatch(pattern, s string) bool {
	// Convert glob to regex: escape special chars, replace * with .*
	escaped := regexp.QuoteMeta(pattern)
	escaped = strings.ReplaceAll(escaped, `\*`, `.*`)
	re, err := regexp.Compile("^" + escaped + "$")
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// recordFilteredOutcome saves a filtered-event outcome.
func recordFilteredOutcome(ctx context.Context, outcomes *sqlite.OutcomeStore, projectID, eventID string, reason *filterReason) {
	if outcomes == nil || reason == nil {
		return
	}
	_ = outcomes.SaveOutcome(ctx, &sqlite.Outcome{
		ProjectID:   projectID,
		EventID:     eventID,
		Category:    reason.Category,
		Reason:      reason.FilterID,
		Quantity:    1,
		Source:      "inbound_filter",
		RecordedAt:  time.Now().UTC(),
		DateCreated: time.Now().UTC(),
	})
}
