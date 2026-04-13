package api

import (
	"encoding/json"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
)

type Dashboard struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Visibility  string            `json:"visibility"`
	Config      json.RawMessage   `json:"config,omitempty"`
	OwnerUserID string            `json:"ownerUserId"`
	DateCreated time.Time         `json:"dateCreated"`
	DateUpdated time.Time         `json:"dateUpdated"`
	Widgets     []DashboardWidget `json:"widgets,omitempty"`
}

type DashboardWidget struct {
	ID            string          `json:"id"`
	DashboardID   string          `json:"dashboardId"`
	Title         string          `json:"title"`
	Description   string          `json:"description,omitempty"`
	Kind          string          `json:"kind"`
	Position      int             `json:"position"`
	Width         int             `json:"width"`
	Height        int             `json:"height"`
	SavedSearchID string          `json:"savedSearchId,omitempty"`
	QueryVersion  int             `json:"queryVersion"`
	Query         discover.Query  `json:"query"`
	Config        json.RawMessage `json:"config,omitempty"`
	DateCreated   time.Time       `json:"dateCreated"`
	DateUpdated   time.Time       `json:"dateUpdated"`
}

func mapDashboard(item sqlite.Dashboard) Dashboard {
	resp := Dashboard{
		ID:          item.ID,
		Title:       item.Title,
		Description: item.Description,
		Visibility:  string(item.Visibility),
		Config:      item.Config,
		OwnerUserID: item.OwnerUserID,
		DateCreated: item.CreatedAt,
		DateUpdated: item.UpdatedAt,
	}
	if len(item.Widgets) > 0 {
		resp.Widgets = make([]DashboardWidget, 0, len(item.Widgets))
		for _, widget := range item.Widgets {
			resp.Widgets = append(resp.Widgets, mapDashboardWidget(widget))
		}
	}
	return resp
}

func mapDashboardWidget(item sqlite.DashboardWidget) DashboardWidget {
	return DashboardWidget{
		ID:            item.ID,
		DashboardID:   item.DashboardID,
		Title:         item.Title,
		Description:   item.Description,
		Kind:          string(item.Kind),
		Position:      item.Position,
		Width:         item.Width,
		Height:        item.Height,
		SavedSearchID: item.SavedSearchID,
		QueryVersion:  item.QueryVersion,
		Query:         item.QueryDoc,
		Config:        item.Config,
		DateCreated:   item.CreatedAt,
		DateUpdated:   item.UpdatedAt,
	}
}
