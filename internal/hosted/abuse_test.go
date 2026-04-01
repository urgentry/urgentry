package hosted

import "testing"

func TestDefaultAbusePolicyValidate(t *testing.T) {
	if err := DefaultAbusePolicy().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEvaluateAbuseThrottlesQueryRateCeiling(t *testing.T) {
	policy := DefaultAbusePolicy()
	decision, err := policy.Evaluate(AbuseRequest{
		Surface:           AbuseSurfaceQuery,
		RequestsPerMinute: 1300,
		Concurrent:        4,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("Allowed = true, want false")
	}
	if got, want := decision.Action, AbuseActionThrottle; got != want {
		t.Fatalf("Action = %q, want %q", got, want)
	}
}

func TestEvaluateAbuseBlocksArtifactBurst(t *testing.T) {
	policy := DefaultAbusePolicy()
	decision, err := policy.Evaluate(AbuseRequest{
		Surface:           AbuseSurfaceArtifact,
		RequestsPerMinute: 1500,
		Concurrent:        4,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("Allowed = true, want false")
	}
	if got, want := decision.Action, AbuseActionBlock; got != want {
		t.Fatalf("Action = %q, want %q", got, want)
	}
}

func TestEvaluateAbuseAllowsOperatorOverride(t *testing.T) {
	policy := DefaultAbusePolicy()
	decision, err := policy.Evaluate(AbuseRequest{
		Surface:           AbuseSurfaceAuth,
		RequestsPerMinute: 1000,
		Concurrent:        40,
		OperatorOverride:  true,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !decision.Allowed {
		t.Fatal("Allowed = false, want true")
	}
}
