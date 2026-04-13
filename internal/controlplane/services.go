package controlplane

type Services struct {
	Catalog      CatalogStore
	Admin        AdminStore
	Issues       IssueWorkflowStore
	IssueReads   IssueReadStore
	Ownership    OwnershipStore
	Releases     ReleaseStore
	Alerts       AlertStore
	MetricAlerts MetricAlertStore
	Outbox       NotificationOutboxStore
	Deliveries   NotificationDeliveryStore
	Monitors     MonitorStore
}
