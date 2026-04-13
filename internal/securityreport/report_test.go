package securityreport

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTranslateJSONCSPReport(t *testing.T) {
	body := []byte(`{
		"csp-report": {
			"document-uri": "https://app.example.com/checkout",
			"violated-directive": "script-src-elem",
			"effective-directive": "script-src-elem",
			"blocked-uri": "https://cdn.bad.test/app.js",
			"disposition": "enforce",
			"status-code": 200
		}
	}`)

	payloads, err := TranslateJSON(body)
	if err != nil {
		t.Fatalf("TranslateJSON: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("len(payloads) = %d, want 1", len(payloads))
	}

	var event map[string]any
	if err := json.Unmarshal(payloads[0], &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if got := event["logger"]; got != "security.csp" {
		t.Fatalf("logger = %v, want security.csp", got)
	}
	if got := event["level"]; got != "error" {
		t.Fatalf("level = %v, want error", got)
	}
	if got := event["message"].(string); !strings.Contains(got, "script-src-elem") {
		t.Fatalf("message = %q, want directive", got)
	}
	request, ok := event["request"].(map[string]any)
	if !ok || request["url"] != "https://app.example.com/checkout" {
		t.Fatalf("request = %#v, want document URL", event["request"])
	}
}

func TestTranslateJSONReportingAPIArray(t *testing.T) {
	body := []byte(`[
		{
			"type": "network-error",
			"url": "https://app.example.com/api/orders",
			"user_agent": "Mozilla/5.0",
			"body": {
				"phase": "application",
				"status_code": 502
			}
		},
		{
			"type": "csp-violation",
			"url": "https://app.example.com/checkout",
			"body": {
				"documentURL": "https://app.example.com/checkout",
				"effectiveDirective": "img-src",
				"blockedURL": "https://bad.example.com/pixel.gif",
				"disposition": "report"
			}
		}
	]`)

	payloads, err := TranslateJSON(body)
	if err != nil {
		t.Fatalf("TranslateJSON: %v", err)
	}
	if len(payloads) != 2 {
		t.Fatalf("len(payloads) = %d, want 2", len(payloads))
	}

	var first map[string]any
	if err := json.Unmarshal(payloads[0], &first); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}
	if got := first["logger"]; got != "security.network.error" {
		t.Fatalf("logger = %v, want security.network.error", got)
	}

	var second map[string]any
	if err := json.Unmarshal(payloads[1], &second); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if got := second["logger"]; got != "security.csp" {
		t.Fatalf("second logger = %v, want security.csp", got)
	}
	if got := second["message"].(string); !strings.Contains(got, "img-src") {
		t.Fatalf("second message = %q, want img-src", got)
	}
}
