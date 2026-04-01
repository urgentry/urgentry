package ingest

import (
	"testing"

	"urgentry/internal/domain"
)

func TestEvaluateFilters_LegacyBrowser(t *testing.T) {
	filters := []*domain.InboundFilter{
		{ID: "1", ProjectID: "p1", Type: domain.FilterLegacyBrowser, Active: true},
	}

	tests := []struct {
		name      string
		ua        string
		wantMatch bool
	}{
		{"IE8", "Mozilla/5.0 (compatible; MSIE 8.0; Windows NT 6.1)", true},
		{"IE10", "Mozilla/5.0 (compatible; MSIE 10.0; Windows NT 6.1)", true},
		{"Chrome", "Mozilla/5.0 Chrome/120.0", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EvaluateFilters(filters, tt.ua, "", "")
			if result.Filtered != tt.wantMatch {
				t.Errorf("EvaluateFilters(%q) filtered=%v, want %v", tt.ua, result.Filtered, tt.wantMatch)
			}
			if tt.wantMatch && result.Reason != domain.FilterLegacyBrowser {
				t.Errorf("reason=%v, want %v", result.Reason, domain.FilterLegacyBrowser)
			}
		})
	}
}

func TestEvaluateFilters_Localhost(t *testing.T) {
	filters := []*domain.InboundFilter{
		{ID: "1", ProjectID: "p1", Type: domain.FilterLocalhost, Active: true},
	}

	tests := []struct {
		name       string
		ip         string
		serverName string
		wantMatch  bool
	}{
		{"loopback v4", "127.0.0.1", "", true},
		{"loopback v6", "::1", "", true},
		{"server localhost", "", "localhost", true},
		{"public ip", "8.8.8.8", "prod.example.com", false},
		{"empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EvaluateFilters(filters, "", tt.ip, tt.serverName)
			if result.Filtered != tt.wantMatch {
				t.Errorf("EvaluateFilters(ip=%q, server=%q) filtered=%v, want %v",
					tt.ip, tt.serverName, result.Filtered, tt.wantMatch)
			}
		})
	}
}

func TestEvaluateFilters_Crawler(t *testing.T) {
	filters := []*domain.InboundFilter{
		{ID: "1", ProjectID: "p1", Type: domain.FilterCrawler, Active: true},
	}

	tests := []struct {
		name      string
		ua        string
		wantMatch bool
	}{
		{"googlebot", "Mozilla/5.0 (compatible; Googlebot/2.1)", true},
		{"slackbot", "Slackbot-LinkExpanding 1.0", true},
		{"chrome", "Mozilla/5.0 Chrome/120.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EvaluateFilters(filters, tt.ua, "", "")
			if result.Filtered != tt.wantMatch {
				t.Errorf("EvaluateFilters(%q) filtered=%v, want %v", tt.ua, result.Filtered, tt.wantMatch)
			}
		})
	}
}

func TestEvaluateFilters_IPRange(t *testing.T) {
	filters := []*domain.InboundFilter{
		{ID: "1", ProjectID: "p1", Type: domain.FilterIPRange, Active: true, Pattern: "10.0.0.0/8,192.168.1.0/24"},
	}

	tests := []struct {
		name      string
		ip        string
		wantMatch bool
	}{
		{"in range", "10.1.2.3", true},
		{"in second range", "192.168.1.55", true},
		{"out of range", "8.8.8.8", false},
		{"empty ip", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EvaluateFilters(filters, "", tt.ip, "")
			if result.Filtered != tt.wantMatch {
				t.Errorf("EvaluateFilters(ip=%q) filtered=%v, want %v", tt.ip, result.Filtered, tt.wantMatch)
			}
		})
	}
}

func TestEvaluateFilters_InactiveSkipped(t *testing.T) {
	filters := []*domain.InboundFilter{
		{ID: "1", ProjectID: "p1", Type: domain.FilterCrawler, Active: false},
	}
	result := EvaluateFilters(filters, "Googlebot/2.1", "", "")
	if result.Filtered {
		t.Error("inactive filter should not match")
	}
}

func TestEvaluateFilters_NoFilters(t *testing.T) {
	result := EvaluateFilters(nil, "Googlebot/2.1", "127.0.0.1", "localhost")
	if result.Filtered {
		t.Error("nil filters should pass through")
	}
}

func TestNewInboundFilter(t *testing.T) {
	f := NewInboundFilter("p1", domain.FilterIPRange, "10.0.0.0/8")
	if f.ID == "" {
		t.Error("ID should be generated")
	}
	if f.ProjectID != "p1" {
		t.Errorf("ProjectID=%v, want p1", f.ProjectID)
	}
	if f.Type != domain.FilterIPRange {
		t.Errorf("Type=%v, want %v", f.Type, domain.FilterIPRange)
	}
	if !f.Active {
		t.Error("new filter should be active")
	}
}
