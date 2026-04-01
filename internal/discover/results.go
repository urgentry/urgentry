package discover

import "time"

type Column struct {
	Name string `json:"name"`
}

type TableRow map[string]any

type TableResult struct {
	Columns    []Column     `json:"columns"`
	Rows       []TableRow   `json:"rows"`
	ResultSize int          `json:"resultSize"`
	Cost       CostEstimate `json:"cost"`
}

type SeriesPoint struct {
	Bucket time.Time      `json:"bucket"`
	Values map[string]any `json:"values"`
}

type SeriesResult struct {
	Columns []Column      `json:"columns"`
	Points  []SeriesPoint `json:"points"`
	Cost    CostEstimate  `json:"cost"`
}

type ExplainPlan struct {
	Dataset     Dataset      `json:"dataset"`
	Mode        string       `json:"mode"`
	SQL         string       `json:"sql"`
	Args        []any        `json:"args"`
	ResultLimit int          `json:"resultLimit"`
	Cost        CostEstimate `json:"cost"`
}
