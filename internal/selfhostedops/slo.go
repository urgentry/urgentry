package selfhostedops

import (
	"fmt"
	"slices"
)

type ServicePlane string

const (
	ServicePlaneControl   ServicePlane = "control"
	ServicePlaneAsync     ServicePlane = "async"
	ServicePlaneCache     ServicePlane = "cache"
	ServicePlaneBlob      ServicePlane = "blob"
	ServicePlaneTelemetry ServicePlane = "telemetry"
)

var servicePlaneOrder = []ServicePlane{
	ServicePlaneControl,
	ServicePlaneAsync,
	ServicePlaneCache,
	ServicePlaneBlob,
	ServicePlaneTelemetry,
}

type AlertSeverity string

const (
	AlertSeverityPage AlertSeverity = "page"
	AlertSeverityWarn AlertSeverity = "warn"
	AlertSeverityInfo AlertSeverity = "info"
)

type SLOObjective struct {
	Name    string `json:"name"`
	Measure string `json:"measure"`
	Target  string `json:"target"`
	Window  string `json:"window"`
}

type AlertRule struct {
	Name     string        `json:"name"`
	Severity AlertSeverity `json:"severity"`
	Trigger  string        `json:"trigger"`
	Runbook  string        `json:"runbook"`
}

type DashboardWidget struct {
	Title  string `json:"title"`
	Signal string `json:"signal"`
}

type DashboardPack struct {
	Title   string            `json:"title"`
	Widgets []DashboardWidget `json:"widgets"`
}

type PlaneSLOPack struct {
	Plane      ServicePlane   `json:"plane"`
	Objectives []SLOObjective `json:"objectives"`
	Alerts     []AlertRule    `json:"alerts"`
	Dashboard  DashboardPack  `json:"dashboard"`
}

type SLOPack struct {
	Planes []PlaneSLOPack `json:"planes"`
}

func DefaultSLOPack() SLOPack {
	return SLOPack{
		Planes: []PlaneSLOPack{
			{
				Plane: ServicePlaneControl,
				Objectives: []SLOObjective{
					{Name: "control write latency", Measure: "p95 auth and mutation latency", Target: "<= 300ms", Window: "30d"},
					{Name: "control error budget", Measure: "5xx rate on auth and control APIs", Target: "< 0.5%", Window: "30d"},
				},
				Alerts: []AlertRule{
					{Name: "control api error spike", Severity: AlertSeverityPage, Trigger: "5xx rate > 2% for 10m", Runbook: "check Postgres reachability, auth tables, and recent control-plane migrations"},
					{Name: "control write latency regression", Severity: AlertSeverityWarn, Trigger: "p95 write latency > 500ms for 15m", Runbook: "inspect Postgres saturation, lock waits, and auth or issue mutation pressure"},
				},
				Dashboard: DashboardPack{
					Title: "Control plane",
					Widgets: []DashboardWidget{
						{Title: "auth success and failure rate", Signal: "control auth responses by code"},
						{Title: "mutation latency", Signal: "p50/p95 project, issue, release, and alert mutation latency"},
						{Title: "Postgres control saturation", Signal: "connections, lock waits, and slow queries"},
					},
				},
			},
			{
				Plane: ServicePlaneAsync,
				Objectives: []SLOObjective{
					{Name: "worker backlog age", Measure: "oldest queued job age", Target: "<= 5m", Window: "30d"},
					{Name: "scheduler lease health", Measure: "scheduler lease gaps", Target: "0 unexpected gaps", Window: "30d"},
				},
				Alerts: []AlertRule{
					{Name: "async backlog growth", Severity: AlertSeverityPage, Trigger: "oldest job age > 15m for 10m", Runbook: "inspect JetStream consumer lag, worker concurrency, and backfill contention"},
					{Name: "scheduler lease churn", Severity: AlertSeverityWarn, Trigger: "lease handoff failures > 0 for 5m", Runbook: "check Valkey reachability and scheduler holder churn"},
				},
				Dashboard: DashboardPack{
					Title: "Async plane",
					Widgets: []DashboardWidget{
						{Title: "queue depth and oldest age", Signal: "queue depth by kind plus oldest message age"},
						{Title: "worker success and retry rates", Signal: "worker completions, retries, and dead-letter counts"},
						{Title: "scheduler lease timeline", Signal: "current holder, renewals, and missed renewals"},
					},
				},
			},
			{
				Plane: ServicePlaneCache,
				Objectives: []SLOObjective{
					{Name: "quota decision freshness", Measure: "successful quota and lease operations", Target: ">= 99.9%", Window: "30d"},
					{Name: "cache latency", Measure: "p95 Valkey command latency", Target: "<= 20ms", Window: "30d"},
				},
				Alerts: []AlertRule{
					{Name: "valkey outage", Severity: AlertSeverityPage, Trigger: "quota or lease command failures > 0 for 2m", Runbook: "restore Valkey connectivity, then verify query guards and scheduler leases"},
					{Name: "valkey latency regression", Severity: AlertSeverityWarn, Trigger: "p95 Valkey latency > 50ms for 10m", Runbook: "inspect saturation, failover behavior, and hot-key pressure"},
				},
				Dashboard: DashboardPack{
					Title: "Cache and lease plane",
					Widgets: []DashboardWidget{
						{Title: "quota decision rate", Signal: "query and ingest guard allow or deny counts"},
						{Title: "lease operations", Signal: "scheduler and worker lease acquire, renew, and release counts"},
						{Title: "Valkey latency", Signal: "p50/p95 command latency and error rate"},
					},
				},
			},
			{
				Plane: ServicePlaneBlob,
				Objectives: []SLOObjective{
					{Name: "artifact read success", Measure: "blob fetch success rate", Target: ">= 99.95%", Window: "30d"},
					{Name: "artifact write latency", Measure: "p95 blob write latency", Target: "<= 750ms", Window: "30d"},
				},
				Alerts: []AlertRule{
					{Name: "blob read failures", Severity: AlertSeverityPage, Trigger: "blob read failures > 1% for 10m", Runbook: "inspect MinIO or S3 connectivity, permissions, and object-key drift"},
					{Name: "blob restore lag", Severity: AlertSeverityWarn, Trigger: "restore or archive jobs pending > 15m", Runbook: "inspect blob throughput, retention backlog, and backfill overlap"},
				},
				Dashboard: DashboardPack{
					Title: "Blob plane",
					Widgets: []DashboardWidget{
						{Title: "blob read and write latency", Signal: "artifact and attachment object latency"},
						{Title: "restore and archive backlog", Signal: "pending retention archive or restore work"},
						{Title: "blob failures by surface", Signal: "attachment, replay, profile, and debug-file blob failures"},
					},
				},
			},
			{
				Plane: ServicePlaneTelemetry,
				Objectives: []SLOObjective{
					{Name: "telemetry query latency", Measure: "p95 discover, logs, traces, replay, and profile query latency", Target: "<= 1s", Window: "30d"},
					{Name: "bridge freshness", Measure: "bridge lag behind authoritative stores", Target: "<= 2m", Window: "30d"},
				},
				Alerts: []AlertRule{
					{Name: "bridge lag breach", Severity: AlertSeverityPage, Trigger: "bridge lag > 10m for 10m", Runbook: "inspect projector health, rebuild runs, and Postgres telemetry saturation"},
					{Name: "telemetry query latency regression", Severity: AlertSeverityWarn, Trigger: "p95 query latency > 2s for 15m", Runbook: "inspect query guard budgets, bridge indexes, and rebuild overlap"},
				},
				Dashboard: DashboardPack{
					Title: "Telemetry plane",
					Widgets: []DashboardWidget{
						{Title: "query latency by surface", Signal: "discover, logs, traces, replay, and profile latency"},
						{Title: "bridge lag and rebuild status", Signal: "projector lag, backfill status, and failed families"},
						{Title: "telemetry saturation", Signal: "bridge database load, slow queries, and connection pressure"},
					},
				},
			},
		},
	}
}

func (p SLOPack) Validate() error {
	if len(p.Planes) != len(servicePlaneOrder) {
		return fmt.Errorf("expected %d service planes, got %d", len(servicePlaneOrder), len(p.Planes))
	}
	for _, plane := range servicePlaneOrder {
		item, ok := p.lookupPlane(plane)
		if !ok {
			return fmt.Errorf("missing service plane %q", plane)
		}
		if len(item.Objectives) == 0 {
			return fmt.Errorf("plane %q must define objectives", plane)
		}
		if len(item.Alerts) == 0 {
			return fmt.Errorf("plane %q must define alerts", plane)
		}
		if item.Dashboard.Title == "" {
			return fmt.Errorf("plane %q must define a dashboard title", plane)
		}
		if len(item.Dashboard.Widgets) == 0 {
			return fmt.Errorf("plane %q must define dashboard widgets", plane)
		}
		for _, alert := range item.Alerts {
			if !slices.Contains([]AlertSeverity{AlertSeverityPage, AlertSeverityWarn, AlertSeverityInfo}, alert.Severity) {
				return fmt.Errorf("plane %q has invalid alert severity %q", plane, alert.Severity)
			}
			if alert.Name == "" || alert.Trigger == "" || alert.Runbook == "" {
				return fmt.Errorf("plane %q has incomplete alert definition", plane)
			}
		}
		for _, objective := range item.Objectives {
			if objective.Name == "" || objective.Measure == "" || objective.Target == "" || objective.Window == "" {
				return fmt.Errorf("plane %q has incomplete SLO objective", plane)
			}
		}
		for _, widget := range item.Dashboard.Widgets {
			if widget.Title == "" || widget.Signal == "" {
				return fmt.Errorf("plane %q has incomplete dashboard widget", plane)
			}
		}
	}
	return nil
}

func (p SLOPack) lookupPlane(plane ServicePlane) (PlaneSLOPack, bool) {
	for _, item := range p.Planes {
		if item.Plane == plane {
			return item, true
		}
	}
	return PlaneSLOPack{}, false
}
