package store

type EventProcessingStatus string

const (
	EventProcessingStatusPending    EventProcessingStatus = "pending"
	EventProcessingStatusProcessing EventProcessingStatus = "processing"
	EventProcessingStatusCompleted  EventProcessingStatus = "completed"
	EventProcessingStatusFailed     EventProcessingStatus = "failed"
)
