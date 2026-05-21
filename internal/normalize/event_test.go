package normalize

import (
	"encoding/json"
	"testing"
)

func TestNormalizeAcceptsSentryJSBreadcrumbArray(t *testing.T) {
	evt, err := Normalize([]byte(`{
		"event_id": "11111111111111111111111111111111",
		"level": "error",
		"breadcrumbs": [
			{"type": "ui.click", "category": "ui", "message": "clicked checkout"}
		]
	}`))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if evt.Breadcrumbs == nil || len(evt.Breadcrumbs.Values) != 1 {
		t.Fatalf("breadcrumbs = %+v, want one breadcrumb", evt.Breadcrumbs)
	}
	if evt.Breadcrumbs.Values[0].Message != "clicked checkout" {
		t.Fatalf("breadcrumb message = %q", evt.Breadcrumbs.Values[0].Message)
	}
}

func TestBreadcrumbListAcceptsWrappedValues(t *testing.T) {
	var breadcrumbs BreadcrumbList
	if err := json.Unmarshal([]byte(`{"values":[{"message":"wrapped"}]}`), &breadcrumbs); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(breadcrumbs.Values) != 1 || breadcrumbs.Values[0].Message != "wrapped" {
		t.Fatalf("breadcrumbs = %+v", breadcrumbs)
	}
}
