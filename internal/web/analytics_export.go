package web

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type analyticsExportPayload struct {
	ViewType string              `json:"viewType"`
	Columns  []string            `json:"columns"`
	Rows     []map[string]string `json:"rows"`
}

var exportNamePattern = regexp.MustCompile(`[^a-z0-9._-]+`)

func normalizedExportFormat(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "csv", "json":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func exportURL(currentURL, format string) string {
	format = normalizedExportFormat(format)
	if format == "" {
		return ""
	}
	u, err := url.Parse(currentURL)
	if err != nil {
		return ""
	}
	values := u.Query()
	values.Set("export", format)
	u.RawQuery = values.Encode()
	return u.String()
}

func exportRows(result discoverResultView) ([]string, []map[string]string) {
	switch result.Type {
	case "stat":
		label := result.StatLabel
		if label == "" {
			label = "result"
		}
		return []string{"label", "value"}, []map[string]string{{"label": label, "value": result.StatValue}}
	default:
		rows := make([]map[string]string, 0, len(result.Rows))
		for _, row := range result.Rows {
			item := map[string]string{}
			for i, cell := range row {
				if i >= len(result.Columns) {
					continue
				}
				item[result.Columns[i]] = cell.Text
			}
			rows = append(rows, item)
		}
		return append([]string(nil), result.Columns...), rows
	}
}

func writeAnalyticsExport(w http.ResponseWriter, filename, format string, result discoverResultView) {
	format = normalizedExportFormat(format)
	if format == "" {
		http.Error(w, "unsupported export format", http.StatusBadRequest)
		return
	}
	filename = sanitizeExportName(filename)
	if filename == "" {
		filename = "analytics-export"
	}
	columns, rows := exportRows(result)
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.json"`)
		_ = json.NewEncoder(w).Encode(analyticsExportPayload{
			ViewType: result.Type,
			Columns:  columns,
			Rows:     rows,
		})
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.csv"`)
		writer := csv.NewWriter(w)
		_ = writer.Write(columns)
		for _, row := range rows {
			record := make([]string, 0, len(columns))
			for _, column := range columns {
				record = append(record, row[column])
			}
			_ = writer.Write(record)
		}
		writer.Flush()
	}
}

func sanitizeExportName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = strings.ReplaceAll(name, " ", "-")
	name = exportNamePattern.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	return name
}
