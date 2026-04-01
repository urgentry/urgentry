// Package ingest provides ingest-time filtering for events before pipeline
// admission. Filters cover legacy browsers, localhost origins, known crawlers,
// and IP ranges.
package ingest

import (
	"net"
	"strings"
	"time"

	"urgentry/internal/domain"
	"urgentry/pkg/id"
)

// FilterResult captures whether an event was filtered and why.
type FilterResult struct {
	Filtered bool              `json:"filtered"`
	Reason   domain.FilterType `json:"reason,omitempty"`
	Pattern  string            `json:"pattern,omitempty"`
}

// knownCrawlerSubstrings are user-agent substrings for common web crawlers.
var knownCrawlerSubstrings = []string{
	"Googlebot", "bingbot", "Baiduspider", "YandexBot",
	"Sogou", "Exabot", "ia_archiver", "facebookexternalhit",
	"Twitterbot", "rogerbot", "linkedinbot", "embedly",
	"quora link preview", "showyoubot", "outbrain", "pinterest",
	"slackbot", "vkShare", "W3C_Validator", "redditbot",
	"Applebot", "WhatsApp", "flipboard", "tumblr",
	"bitlybot", "SkypeUriPreview", "nuzzel", "Discordbot",
	"Qwantify", "pinterestbot", "Bitrix link preview",
	"XING-contenttabreceiver", "Chrome-Lighthouse",
	"AhrefsBot", "SemrushBot", "DotBot", "MJ12bot",
}

// legacyBrowserSubstrings are UA substrings for legacy/unsupported browsers.
var legacyBrowserSubstrings = []string{
	"MSIE 8.0", "MSIE 9.0", "MSIE 10.0",
	"Trident/4.0", "Trident/5.0", "Trident/6.0",
	"Opera/9.", "Opera/10.", "Opera/11.", "Opera/12.",
}

// EvaluateFilters checks an event's metadata against active inbound filters.
// Returns the first matching filter result, or a pass-through result if none match.
func EvaluateFilters(filters []*domain.InboundFilter, userAgent, ipAddress, serverName string) FilterResult {
	for _, f := range filters {
		if !f.Active {
			continue
		}
		switch f.Type {
		case domain.FilterLegacyBrowser:
			if matchesLegacyBrowser(userAgent) {
				return FilterResult{Filtered: true, Reason: domain.FilterLegacyBrowser}
			}
		case domain.FilterLocalhost:
			if isLocalhost(ipAddress, serverName) {
				return FilterResult{Filtered: true, Reason: domain.FilterLocalhost}
			}
		case domain.FilterCrawler:
			if matchesCrawler(userAgent) {
				return FilterResult{Filtered: true, Reason: domain.FilterCrawler}
			}
		case domain.FilterIPRange:
			if f.Pattern != "" && matchesIPRange(ipAddress, f.Pattern) {
				return FilterResult{Filtered: true, Reason: domain.FilterIPRange, Pattern: f.Pattern}
			}
		}
	}
	return FilterResult{}
}

func matchesLegacyBrowser(ua string) bool {
	if ua == "" {
		return false
	}
	for _, sub := range legacyBrowserSubstrings {
		if strings.Contains(ua, sub) {
			return true
		}
	}
	return false
}

func isLocalhost(ip, serverName string) bool {
	if serverName == "localhost" || serverName == "127.0.0.1" || serverName == "::1" {
		return true
	}
	if ip == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return parsed.IsLoopback()
}

func matchesCrawler(ua string) bool {
	if ua == "" {
		return false
	}
	lower := strings.ToLower(ua)
	for _, sub := range knownCrawlerSubstrings {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

func matchesIPRange(ip, cidr string) bool {
	if ip == "" || cidr == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	// Support comma-separated CIDRs.
	for _, block := range strings.Split(cidr, ",") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		_, network, err := net.ParseCIDR(block)
		if err != nil {
			// Try as a single IP.
			if net.ParseIP(block) != nil && net.ParseIP(block).Equal(parsed) {
				return true
			}
			continue
		}
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// NewInboundFilter creates a new InboundFilter with a generated ID and timestamps.
func NewInboundFilter(projectID string, filterType domain.FilterType, pattern string) *domain.InboundFilter {
	now := time.Now().UTC()
	return &domain.InboundFilter{
		ID:        id.New(),
		ProjectID: projectID,
		Type:      filterType,
		Active:    true,
		Pattern:   pattern,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
