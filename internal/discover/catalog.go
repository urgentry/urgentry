package discover

import "strings"

type valueType string

const (
	valueTypeString    valueType = "string"
	valueTypeEnum      valueType = "enum"
	valueTypeNumber    valueType = "number"
	valueTypeTimestamp valueType = "timestamp"
)

type fieldSpec struct {
	Type       valueType
	Groupable  bool
	Searchable bool
	Measure    bool
}

var datasetCatalog = map[Dataset]map[string]fieldSpec{
	DatasetIssues: {
		"project":        {Type: valueTypeString, Groupable: true, Searchable: true},
		"project.id":     {Type: valueTypeString, Groupable: true},
		"release":        {Type: valueTypeString, Groupable: true, Searchable: true},
		"environment":    {Type: valueTypeString, Groupable: true, Searchable: true},
		"event.type":     {Type: valueTypeEnum, Groupable: true},
		"timestamp":      {Type: valueTypeTimestamp, Groupable: true},
		"issue.id":       {Type: valueTypeString, Groupable: true},
		"issue.short_id": {Type: valueTypeString, Groupable: true},
		"title":          {Type: valueTypeString, Groupable: true, Searchable: true},
		"culprit":        {Type: valueTypeString, Groupable: true, Searchable: true},
		"level":          {Type: valueTypeEnum, Groupable: true, Searchable: true},
		"status":         {Type: valueTypeEnum, Groupable: true, Searchable: true},
		"assignee":       {Type: valueTypeString, Groupable: true},
		"first_seen":     {Type: valueTypeTimestamp, Groupable: true},
		"last_seen":      {Type: valueTypeTimestamp, Groupable: true},
		"count":          {Type: valueTypeNumber, Measure: true},
	},
	DatasetLogs: {
		"project":     {Type: valueTypeString, Groupable: true, Searchable: true},
		"project.id":  {Type: valueTypeString, Groupable: true},
		"release":     {Type: valueTypeString, Groupable: true, Searchable: true},
		"environment": {Type: valueTypeString, Groupable: true, Searchable: true},
		"platform":    {Type: valueTypeString, Groupable: true, Searchable: true},
		"event.type":  {Type: valueTypeEnum, Groupable: true},
		"timestamp":   {Type: valueTypeTimestamp, Groupable: true},
		"event.id":    {Type: valueTypeString, Groupable: true},
		"title":       {Type: valueTypeString, Groupable: true, Searchable: true},
		"message":     {Type: valueTypeString, Groupable: true, Searchable: true},
		"logger":      {Type: valueTypeString, Groupable: true, Searchable: true},
		"level":       {Type: valueTypeEnum, Groupable: true, Searchable: true},
		"culprit":     {Type: valueTypeString, Groupable: true, Searchable: true},
		"trace.id":    {Type: valueTypeString, Groupable: true, Searchable: true},
		"span.id":     {Type: valueTypeString, Groupable: true},
		"count":       {Type: valueTypeNumber, Measure: true},
	},
	DatasetTransactions: {
		"project":     {Type: valueTypeString, Groupable: true, Searchable: true},
		"project.id":  {Type: valueTypeString, Groupable: true},
		"release":     {Type: valueTypeString, Groupable: true, Searchable: true},
		"environment": {Type: valueTypeString, Groupable: true, Searchable: true},
		"platform":    {Type: valueTypeString, Groupable: true},
		"event.type":  {Type: valueTypeEnum, Groupable: true},
		"timestamp":   {Type: valueTypeTimestamp, Groupable: true},
		"event.id":    {Type: valueTypeString, Groupable: true},
		"transaction": {Type: valueTypeString, Groupable: true, Searchable: true},
		"op":          {Type: valueTypeString, Groupable: true, Searchable: true},
		"status":      {Type: valueTypeEnum, Groupable: true, Searchable: true},
		"trace.id":    {Type: valueTypeString, Groupable: true, Searchable: true},
		"span.id":     {Type: valueTypeString, Groupable: true},
		"duration.ms": {Type: valueTypeNumber, Measure: true},
		"count":       {Type: valueTypeNumber, Measure: true},
	},
}

var aggregateCatalog = map[string]struct {
	Datasets map[Dataset]struct{}
	Args     int
	Field    string
}{
	"count": {
		Datasets: map[Dataset]struct{}{
			DatasetIssues:       {},
			DatasetLogs:         {},
			DatasetTransactions: {},
		},
		Args: 0,
	},
	"avg": {
		Datasets: map[Dataset]struct{}{
			DatasetTransactions: {},
		},
		Args:  1,
		Field: "duration.ms",
	},
	"p50": {
		Datasets: map[Dataset]struct{}{
			DatasetTransactions: {},
		},
		Args:  1,
		Field: "duration.ms",
	},
	"p95": {
		Datasets: map[Dataset]struct{}{
			DatasetTransactions: {},
		},
		Args:  1,
		Field: "duration.ms",
	},
	"max": {
		Datasets: map[Dataset]struct{}{
			DatasetTransactions: {},
		},
		Args:  1,
		Field: "duration.ms",
	},
	"min": {
		Datasets: map[Dataset]struct{}{
			DatasetTransactions: {},
		},
		Args:  1,
		Field: "duration.ms",
	},
	"p75": {
		Datasets: map[Dataset]struct{}{
			DatasetTransactions: {},
		},
		Args:  1,
		Field: "duration.ms",
	},
	"p99": {
		Datasets: map[Dataset]struct{}{
			DatasetTransactions: {},
		},
		Args:  1,
		Field: "duration.ms",
	},
	"sum": {
		Datasets: map[Dataset]struct{}{
			DatasetTransactions: {},
		},
		Args:  1,
		Field: "duration.ms",
	},
	"count_unique": {
		Datasets: map[Dataset]struct{}{
			DatasetIssues:       {},
			DatasetLogs:         {},
			DatasetTransactions: {},
		},
		Args:  1,
		Field: "",
	},
	"failure_rate": {
		Datasets: map[Dataset]struct{}{
			DatasetTransactions: {},
		},
		Args: 0,
	},
	"apdex": {
		Datasets: map[Dataset]struct{}{
			DatasetTransactions: {},
		},
		Args:  1,
		Field: "duration.ms",
	},
}

func datasetFields(dataset Dataset) map[string]fieldSpec {
	return datasetCatalog[dataset]
}

func lookupField(dataset Dataset, field string) (fieldSpec, bool) {
	spec, ok := datasetFields(dataset)[strings.ToLower(strings.TrimSpace(field))]
	return spec, ok
}

func SupportsField(dataset Dataset, field string) bool {
	_, ok := lookupField(dataset, field)
	return ok
}
