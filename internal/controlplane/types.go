package controlplane

import (
	"urgentry/internal/notify"
	"urgentry/internal/sqlite"
)

type OrgMemberRecord = sqlite.OrgMemberRecord
type ProjectMemberRecord = sqlite.ProjectMemberRecord
type TeamRecord = sqlite.TeamRecord
type TeamMemberRecord = sqlite.TeamMemberRecord
type InviteRecord = sqlite.InviteRecord
type InviteAcceptanceResult = sqlite.InviteAcceptanceResult
type MonitorSchedule = sqlite.MonitorSchedule
type MonitorConfig = sqlite.MonitorConfig
type Monitor = sqlite.Monitor
type MonitorCheckIn = sqlite.MonitorCheckIn
type Release = sqlite.Release
type NotificationOutbox = notify.EmailNotification
type NotificationDelivery = notify.DeliveryRecord
