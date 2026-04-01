package hosted

import (
	"fmt"
	"slices"
)

type AbuseSurface string

const (
	AbuseSurfaceIngest   AbuseSurface = "ingest"
	AbuseSurfaceQuery    AbuseSurface = "query"
	AbuseSurfaceAuth     AbuseSurface = "auth"
	AbuseSurfaceArtifact AbuseSurface = "artifact"
)

var abuseSurfaceOrder = []AbuseSurface{
	AbuseSurfaceIngest,
	AbuseSurfaceQuery,
	AbuseSurfaceAuth,
	AbuseSurfaceArtifact,
}

type AbuseAction string

const (
	AbuseActionAllow    AbuseAction = "allow"
	AbuseActionThrottle AbuseAction = "throttle"
	AbuseActionBlock    AbuseAction = "block"
)

var abuseActionOrder = []AbuseAction{
	AbuseActionAllow,
	AbuseActionThrottle,
	AbuseActionBlock,
}

type AbuseControl struct {
	RateCeilingPerMinute int         `json:"rateCeilingPerMinute"`
	BurstPerMinute       int         `json:"burstPerMinute"`
	ConcurrentCap        int         `json:"concurrentCap"`
	TripAction           AbuseAction `json:"tripAction"`
	OverrideAllowed      bool        `json:"overrideAllowed"`
}

type AbusePolicy struct {
	Controls map[AbuseSurface]AbuseControl `json:"controls"`
}

type AbuseRequest struct {
	Surface           AbuseSurface `json:"surface"`
	RequestsPerMinute int          `json:"requestsPerMinute"`
	Concurrent        int          `json:"concurrent"`
	OperatorOverride  bool         `json:"operatorOverride,omitempty"`
}

type AbuseDecision struct {
	Surface                  AbuseSurface `json:"surface"`
	Action                   AbuseAction  `json:"action"`
	Allowed                  bool         `json:"allowed"`
	RequiresOperatorOverride bool         `json:"requiresOperatorOverride,omitempty"`
	Reason                   string       `json:"reason,omitempty"`
}

func DefaultAbusePolicy() AbusePolicy {
	return AbusePolicy{
		Controls: map[AbuseSurface]AbuseControl{
			AbuseSurfaceIngest:   {RateCeilingPerMinute: 24000, BurstPerMinute: 32000, ConcurrentCap: 256, TripAction: AbuseActionThrottle, OverrideAllowed: true},
			AbuseSurfaceQuery:    {RateCeilingPerMinute: 1200, BurstPerMinute: 1800, ConcurrentCap: 32, TripAction: AbuseActionThrottle, OverrideAllowed: true},
			AbuseSurfaceAuth:     {RateCeilingPerMinute: 120, BurstPerMinute: 240, ConcurrentCap: 16, TripAction: AbuseActionBlock, OverrideAllowed: true},
			AbuseSurfaceArtifact: {RateCeilingPerMinute: 600, BurstPerMinute: 1200, ConcurrentCap: 8, TripAction: AbuseActionBlock, OverrideAllowed: true},
		},
	}
}

func (p AbusePolicy) Validate() error {
	if len(p.Controls) != len(abuseSurfaceOrder) {
		return fmt.Errorf("expected %d abuse surfaces, got %d", len(abuseSurfaceOrder), len(p.Controls))
	}
	for _, surface := range abuseSurfaceOrder {
		control, ok := p.Controls[surface]
		if !ok {
			return fmt.Errorf("missing abuse control for %q", surface)
		}
		if control.RateCeilingPerMinute <= 0 {
			return fmt.Errorf("surface %q must define a positive rate ceiling", surface)
		}
		if control.BurstPerMinute < control.RateCeilingPerMinute {
			return fmt.Errorf("surface %q burst must be at least the rate ceiling", surface)
		}
		if control.ConcurrentCap <= 0 {
			return fmt.Errorf("surface %q must define a positive concurrency cap", surface)
		}
		if !slices.Contains(abuseActionOrder, control.TripAction) || control.TripAction == AbuseActionAllow {
			return fmt.Errorf("surface %q must define a throttling or blocking trip action", surface)
		}
	}
	return nil
}

func (p AbusePolicy) Evaluate(req AbuseRequest) (AbuseDecision, error) {
	if err := p.Validate(); err != nil {
		return AbuseDecision{}, err
	}
	control, ok := p.Controls[req.Surface]
	if !ok {
		return AbuseDecision{}, fmt.Errorf("unknown abuse surface %q", req.Surface)
	}
	if req.RequestsPerMinute < 0 || req.Concurrent < 0 {
		return AbuseDecision{}, fmt.Errorf("request rate and concurrency must be non-negative")
	}
	if req.OperatorOverride {
		if !control.OverrideAllowed {
			return AbuseDecision{}, fmt.Errorf("surface %q does not allow operator overrides", req.Surface)
		}
		return AbuseDecision{
			Surface: req.Surface,
			Action:  AbuseActionAllow,
			Allowed: true,
			Reason:  "operator override bypassed abuse controls",
		}, nil
	}
	if req.Concurrent > control.ConcurrentCap {
		return AbuseDecision{
			Surface:                  req.Surface,
			Action:                   control.TripAction,
			Allowed:                  false,
			RequiresOperatorOverride: control.OverrideAllowed,
			Reason:                   fmt.Sprintf("concurrency %d exceeds cap %d", req.Concurrent, control.ConcurrentCap),
		}, nil
	}
	if req.RequestsPerMinute > control.BurstPerMinute {
		return AbuseDecision{
			Surface:                  req.Surface,
			Action:                   control.TripAction,
			Allowed:                  false,
			RequiresOperatorOverride: control.OverrideAllowed,
			Reason:                   fmt.Sprintf("request rate %d exceeds burst %d", req.RequestsPerMinute, control.BurstPerMinute),
		}, nil
	}
	if req.RequestsPerMinute > control.RateCeilingPerMinute {
		return AbuseDecision{
			Surface:                  req.Surface,
			Action:                   AbuseActionThrottle,
			Allowed:                  false,
			RequiresOperatorOverride: control.OverrideAllowed,
			Reason:                   fmt.Sprintf("request rate %d exceeds ceiling %d", req.RequestsPerMinute, control.RateCeilingPerMinute),
		}, nil
	}
	return AbuseDecision{
		Surface: req.Surface,
		Action:  AbuseActionAllow,
		Allowed: true,
	}, nil
}
