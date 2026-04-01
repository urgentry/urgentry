package discover

import "testing"

func TestParseLegacyIssuesSnapshot(t *testing.T) {
	query, cost, err := ParseLegacy(LegacyInput{
		Dataset:      DatasetIssues,
		Organization: "acme",
		Filter:       "unresolved",
		Query:        "release:backend@1.2.3 env:production ImportError",
		Limit:        25,
	})
	if err != nil {
		t.Fatalf("ParseLegacy: %v", err)
	}
	if cost.Class != CostClassCheap {
		t.Fatalf("cost class = %s, want cheap", cost.Class)
	}
	data, err := MarshalQuery(query)
	if err != nil {
		t.Fatalf("MarshalQuery: %v", err)
	}
	want := `{"version":1,"dataset":"issues","scope":{"kind":"organization","organization":"acme"},"where":{"op":"and","args":[{"op":"=","field":"status","value":"unresolved"},{"op":"=","field":"release","value":"backend@1.2.3"},{"op":"or","args":[{"op":"contains","field":"title","value":"ImportError"},{"op":"contains","field":"culprit","value":"ImportError"}]},{"op":"=","field":"environment","value":"production"}]},"limit":25}`
	if string(data) != want {
		t.Fatalf("marshal mismatch\nwant: %s\ngot:  %s", want, data)
	}
}

func TestParseLegacyRejectsUnsupportedToken(t *testing.T) {
	_, _, err := ParseLegacy(LegacyInput{
		Dataset:      DatasetLogs,
		Organization: "acme",
		Query:        "foo:bar",
		Limit:        25,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	errs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("err type = %T, want ValidationErrors", err)
	}
	if len(errs) != 1 || errs[0].Code != "unsupported_token" {
		t.Fatalf("unexpected errors: %+v", errs)
	}
}

func TestParseLegacyRejectsEnvironmentConflict(t *testing.T) {
	_, _, err := ParseLegacy(LegacyInput{
		Dataset:      DatasetIssues,
		Organization: "acme",
		Query:        "env:staging",
		Environment:  "production",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	errs := err.(ValidationErrors)
	if errs[0].Code != "environment_conflict" {
		t.Fatalf("unexpected errors: %+v", errs)
	}
}
