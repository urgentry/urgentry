package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	sharedstore "urgentry/internal/store"
)

type Bundle struct {
	Surfaces    SurfacesRegistry    `json:"surfaces"`
	Entities    EntitiesRegistry    `json:"entities"`
	QueryFields QueryFieldsRegistry `json:"query_fields"`
}

type SurfacesRegistry struct {
	SourceFiles       []string         `json:"source_files"`
	OpenAPI           OpenAPIRegistry  `json:"openapi"`
	RouteMatrix       RouteMatrix      `json:"route_matrix"`
	TelemetrySurfaces []string         `json:"telemetry_surfaces"`
	ArtifactFamilies  []ArtifactFamily `json:"artifact_families"`
}

type OpenAPIRegistry struct {
	OperationCount int                `json:"operation_count"`
	TagCounts      []NamedCount       `json:"tag_counts"`
	PrefixCounts   []NamedCount       `json:"prefix_counts"`
	Operations     []OpenAPIOperation `json:"operations"`
}

type OpenAPIOperation struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Tag         string `json:"tag,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	Prefix      string `json:"prefix"`
}

type RouteMatrix struct {
	RouteCount    int               `json:"route_count"`
	SectionCounts []NamedCount      `json:"section_counts"`
	Routes        []DocumentedRoute `json:"routes"`
}

type DocumentedRoute struct {
	Section                string   `json:"section"`
	Path                   string   `json:"path"`
	Methods                []string `json:"methods"`
	AcceptedCredentials    []string `json:"accepted_credentials,omitempty"`
	AcceptedCredentialsRaw string   `json:"accepted_credentials_raw"`
	RequiredScope          string   `json:"required_scope"`
	MountedInRoles         []string `json:"mounted_in_roles,omitempty"`
	MountedInRolesRaw      string   `json:"mounted_in_roles_raw"`
	Notes                  string   `json:"notes,omitempty"`
}

type ArtifactFamily struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Routes      []string `json:"routes,omitempty"`
	Tables      []string `json:"tables,omitempty"`
}

type EntitiesRegistry struct {
	SourceFiles   []string         `json:"source_files"`
	TableCount    int              `json:"table_count"`
	RelationCount int              `json:"relation_count"`
	Families      []EntityFamily   `json:"families"`
	Tables        []EntityTable    `json:"tables"`
	Relations     []EntityRelation `json:"relations"`
}

type EntityFamily struct {
	Name   string   `json:"name"`
	Tables []string `json:"tables"`
}

type EntityTable struct {
	Name          string   `json:"name"`
	Family        string   `json:"family"`
	MigrationFile string   `json:"migration_file"`
	Columns       []string `json:"columns"`
}

type EntityRelation struct {
	FromTable  string `json:"from_table"`
	FromColumn string `json:"from_column"`
	ToTable    string `json:"to_table"`
	Kind       string `json:"kind"`
	InferredBy string `json:"inferred_by"`
}

type QueryFieldsRegistry struct {
	SourceFiles    []string         `json:"source_files"`
	DatasetCount   int              `json:"dataset_count"`
	AggregateCount int              `json:"aggregate_count"`
	Datasets       []QueryDataset   `json:"datasets"`
	Aggregates     []QueryAggregate `json:"aggregates"`
}

type QueryDataset struct {
	Name   string       `json:"name"`
	Fields []QueryField `json:"fields"`
}

type QueryField struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Groupable  bool   `json:"groupable,omitempty"`
	Searchable bool   `json:"searchable,omitempty"`
	Measure    bool   `json:"measure,omitempty"`
}

type QueryAggregate struct {
	Name         string   `json:"name"`
	Datasets     []string `json:"datasets"`
	Args         int      `json:"args"`
	DefaultField string   `json:"default_field,omitempty"`
}

type NamedCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type tableDef struct {
	Name          string
	MigrationFile string
	Columns       []string
}

type openAPIMeta struct {
	Tags        []string `json:"tags"`
	OperationID string   `json:"operationId"`
}

var (
	createTableStartRE = regexp.MustCompile(`CREATE TABLE(?: IF NOT EXISTS)?\s+([a-zA-Z0-9_]+)\s*\(`)
	fieldSpecRE        = regexp.MustCompile(`^"([^"]+)":\s+\{(.+)\},?$`)
	aggregateStartRE   = regexp.MustCompile(`^"([^"]+)":\s+\{$`)
	datasetRefRE       = regexp.MustCompile(`^(Dataset[[:alnum:]]+):`)
	identifierRE       = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

func defaultRepoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

func (b Bundle) FileNames() []string {
	return []string{"surfaces.json", "entities.json", "query-fields.json"}
}

func GenerateBundle(repoRoot string) (Bundle, error) {
	surfaces, err := generateSurfaces(repoRoot)
	if err != nil {
		return Bundle{}, err
	}
	entities, err := generateEntities(repoRoot)
	if err != nil {
		return Bundle{}, err
	}
	queryFields, err := generateQueryFields(repoRoot)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{
		Surfaces:    surfaces,
		Entities:    entities,
		QueryFields: queryFields,
	}, nil
}

func WriteOutputs(outDir string, bundle Bundle) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for name, value := range map[string]any{
		"surfaces.json":     bundle.Surfaces,
		"entities.json":     bundle.Entities,
		"query-fields.json": bundle.QueryFields,
	} {
		if err := writeJSONFile(filepath.Join(outDir, name), value); err != nil {
			return err
		}
	}
	return nil
}

func CheckOutputs(outDir string, bundle Bundle) error {
	for name, value := range map[string]any{
		"surfaces.json":     bundle.Surfaces,
		"entities.json":     bundle.Entities,
		"query-fields.json": bundle.QueryFields,
	} {
		path := filepath.Join(outDir, name)
		expected, err := marshalJSON(value)
		if err != nil {
			return err
		}
		current, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if !bytes.Equal(current, expected) {
			return fmt.Errorf("%s is stale; rerun `cd apps/urgentry && make synthetic-registry`", filepath.ToSlash(path))
		}
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	data, err := marshalJSON(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func marshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	return data, nil
}

func generateSurfaces(repoRoot string) (SurfacesRegistry, error) {
	openapiPath := filepath.Join(repoRoot, "research", "sentry-openapi-schema.json")
	routeMatrixPath := filepath.Join(repoRoot, "docs", "reference", "auth-schema-and-route-matrix.md")

	openapi, err := parseOpenAPI(openapiPath)
	if err != nil {
		return SurfacesRegistry{}, err
	}
	routeMatrix, err := parseRouteMatrix(routeMatrixPath)
	if err != nil {
		return SurfacesRegistry{}, err
	}

	telemetrySurfaces := make([]string, 0, len(sharedstore.TelemetrySurfaces()))
	for _, item := range sharedstore.TelemetrySurfaces() {
		telemetrySurfaces = append(telemetrySurfaces, string(item))
	}
	sort.Strings(telemetrySurfaces)

	return SurfacesRegistry{
		SourceFiles: []string{
			relPath(repoRoot, openapiPath),
			relPath(repoRoot, routeMatrixPath),
			"apps/urgentry/internal/store/catalog.go",
		},
		OpenAPI:           openapi,
		RouteMatrix:       routeMatrix,
		TelemetrySurfaces: telemetrySurfaces,
		ArtifactFamilies:  artifactFamilies(),
	}, nil
}

func parseOpenAPI(path string) (OpenAPIRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return OpenAPIRegistry{}, err
	}
	var raw struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return OpenAPIRegistry{}, err
	}

	ops := make([]OpenAPIOperation, 0, 256)
	tagCounts := make(map[string]int)
	prefixCounts := make(map[string]int)
	for path, methods := range raw.Paths {
		for method, payload := range methods {
			upper := strings.ToUpper(method)
			if !isHTTPMethod(upper) {
				continue
			}
			var meta openAPIMeta
			if err := json.Unmarshal(payload, &meta); err != nil {
				return OpenAPIRegistry{}, fmt.Errorf("parse OpenAPI operation %s %s: %w", upper, path, err)
			}
			tag := ""
			if len(meta.Tags) > 0 {
				tag = meta.Tags[0]
				tagCounts[tag]++
			}
			prefix := openAPIPrefix(path)
			prefixCounts[prefix]++
			ops = append(ops, OpenAPIOperation{
				Method:      upper,
				Path:        path,
				Tag:         tag,
				OperationID: meta.OperationID,
				Prefix:      prefix,
			})
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path == ops[j].Path {
			return ops[i].Method < ops[j].Method
		}
		return ops[i].Path < ops[j].Path
	})

	return OpenAPIRegistry{
		OperationCount: len(ops),
		TagCounts:      namedCountsDescending(tagCounts),
		PrefixCounts:   namedCountsDescending(prefixCounts),
		Operations:     ops,
	}, nil
}

func parseRouteMatrix(path string) (RouteMatrix, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RouteMatrix{}, err
	}
	sectionOrder := []string{"System", "Ingest", "Management REST API", "Web UI"}
	validSections := make(map[string]struct{}, len(sectionOrder))
	for _, item := range sectionOrder {
		validSections[item] = struct{}{}
	}

	lines := strings.Split(string(data), "\n")
	currentSection := ""
	routes := make([]DocumentedRoute, 0, 128)
	counts := make(map[string]int)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			currentSection = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		if _, ok := validSections[currentSection]; !ok {
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := parseMarkdownRow(trimmed)
		if len(cells) < 6 || !strings.HasPrefix(cells[0], "/") {
			continue
		}
		routes = append(routes, DocumentedRoute{
			Section:                currentSection,
			Path:                   cells[0],
			Methods:                splitList(cells[1]),
			AcceptedCredentials:    splitList(cells[2]),
			AcceptedCredentialsRaw: cells[2],
			RequiredScope:          cells[3],
			MountedInRoles:         splitList(cells[4]),
			MountedInRolesRaw:      cells[4],
			Notes:                  cells[5],
		})
		counts[currentSection]++
	}

	return RouteMatrix{
		RouteCount:    len(routes),
		SectionCounts: namedCountsInOrder(sectionOrder, counts),
		Routes:        routes,
	}, nil
}

func generateEntities(repoRoot string) (EntitiesRegistry, error) {
	migrationPaths, err := filepath.Glob(filepath.Join(repoRoot, "apps", "urgentry", "internal", "sqlite", "migrations*.go"))
	if err != nil {
		return EntitiesRegistry{}, err
	}
	sort.Strings(migrationPaths)
	tables, err := parseTableDefinitions(repoRoot, migrationPaths)
	if err != nil {
		return EntitiesRegistry{}, err
	}
	tables = dedupeTables(tables)
	tableNames := make(map[string]struct{}, len(tables))
	for _, table := range tables {
		tableNames[table.Name] = struct{}{}
	}

	entityTables := make([]EntityTable, 0, len(tables))
	familyTables := make(map[string][]string)
	for _, table := range tables {
		family := classifyTableFamily(table.Name)
		entityTables = append(entityTables, EntityTable{
			Name:          table.Name,
			Family:        family,
			MigrationFile: table.MigrationFile,
			Columns:       append([]string(nil), table.Columns...),
		})
		familyTables[family] = append(familyTables[family], table.Name)
	}
	sort.Slice(entityTables, func(i, j int) bool { return entityTables[i].Name < entityTables[j].Name })

	families := make([]EntityFamily, 0, len(familyTables))
	for family, items := range familyTables {
		sort.Strings(items)
		families = append(families, EntityFamily{Name: family, Tables: items})
	}
	sort.Slice(families, func(i, j int) bool { return families[i].Name < families[j].Name })

	relations := inferRelations(tables, tableNames)
	sort.Slice(relations, func(i, j int) bool {
		if relations[i].FromTable == relations[j].FromTable {
			if relations[i].FromColumn == relations[j].FromColumn {
				return relations[i].ToTable < relations[j].ToTable
			}
			return relations[i].FromColumn < relations[j].FromColumn
		}
		return relations[i].FromTable < relations[j].FromTable
	})

	sourceFiles := make([]string, 0, len(migrationPaths))
	for _, path := range migrationPaths {
		sourceFiles = append(sourceFiles, relPath(repoRoot, path))
	}

	return EntitiesRegistry{
		SourceFiles:   sourceFiles,
		TableCount:    len(entityTables),
		RelationCount: len(relations),
		Families:      families,
		Tables:        entityTables,
		Relations:     relations,
	}, nil
}

func parseTableDefinitions(repoRoot string, paths []string) ([]tableDef, error) {
	items := make([]tableDef, 0, 128)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		lines := strings.Split(string(data), "\n")
		var current tableDef
		inTable := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if !inTable {
				match := createTableStartRE.FindStringSubmatch(trimmed)
				if match == nil {
					continue
				}
				inTable = true
				current = tableDef{
					Name:          match[1],
					MigrationFile: relPath(repoRoot, path),
				}
				continue
			}
			if strings.Contains(trimmed, ");") {
				items = append(items, current)
				current = tableDef{}
				inTable = false
				continue
			}
			if column := parseColumnName(trimmed); column != "" {
				current.Columns = append(current.Columns, column)
			}
		}
	}
	return items, nil
}

func dedupeTables(input []tableDef) []tableDef {
	if len(input) == 0 {
		return nil
	}
	byName := make(map[string]tableDef, len(input))
	order := make([]string, 0, len(input))
	for _, item := range input {
		existing, ok := byName[item.Name]
		if !ok {
			byName[item.Name] = item
			order = append(order, item.Name)
			continue
		}
		existing.Columns = mergeUniqueStrings(existing.Columns, item.Columns)
		byName[item.Name] = existing
	}
	out := make([]tableDef, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out
}

func parseColumnName(line string) string {
	trimmed := strings.TrimSpace(strings.TrimSuffix(line, ","))
	if trimmed == "" || strings.HasPrefix(trimmed, "--") {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	for _, prefix := range []string{"PRIMARY KEY", "UNIQUE", "FOREIGN KEY", "CONSTRAINT", "CHECK"} {
		if strings.HasPrefix(upper, prefix) {
			return ""
		}
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	name := strings.Trim(fields[0], "`\"")
	if !identifierRE.MatchString(name) {
		return ""
	}
	return name
}

func inferRelations(tables []tableDef, tableNames map[string]struct{}) []EntityRelation {
	seen := make(map[string]struct{})
	relations := make([]EntityRelation, 0, 256)
	for _, table := range tables {
		for _, column := range table.Columns {
			target, kind, inferredBy := inferRelationTarget(table.Name, column, tableNames)
			if target == "" {
				continue
			}
			key := table.Name + "|" + column + "|" + target
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			relations = append(relations, EntityRelation{
				FromTable:  table.Name,
				FromColumn: column,
				ToTable:    target,
				Kind:       kind,
				InferredBy: inferredBy,
			})
		}
	}
	return relations
}

func inferRelationTarget(tableName, column string, tableNames map[string]struct{}) (string, string, string) {
	if target, ok := explicitRelationTargets[tableName+"."+column]; ok {
		return target, relationKind(column), "explicit_override"
	}
	base := ""
	kind := "reference"
	switch {
	case strings.HasSuffix(column, "_row_id"):
		base = strings.TrimSuffix(column, "_row_id")
		kind = "row_reference"
	case strings.HasSuffix(column, "_id"):
		base = strings.TrimSuffix(column, "_id")
	default:
		return "", "", ""
	}
	target := guessTargetTable(tableName, base, tableNames)
	if target == "" {
		return "", "", ""
	}
	return target, kind, "column_suffix"
}

func guessTargetTable(tableName, base string, tableNames map[string]struct{}) string {
	switch base {
	case "organization":
		return "organizations"
	case "repository":
		return "repositories"
	case "saved_search":
		return "saved_searches"
	case "dashboard":
		return "dashboards"
	case "widget":
		return "dashboard_widgets"
	case "suite":
		return "prevent_repository_test_suites"
	case "manifest":
		if strings.HasPrefix(tableName, "profile_") {
			return "profile_manifests"
		}
		if strings.HasPrefix(tableName, "replay_") {
			return "replay_manifests"
		}
	case "frame":
		if strings.HasPrefix(tableName, "profile_") {
			return "profile_frames"
		}
	case "stack":
		if strings.HasPrefix(tableName, "profile_") {
			return "profile_stacks"
		}
	case "thread":
		if strings.HasPrefix(tableName, "profile_") {
			return "profile_threads"
		}
	case "monitor":
		if strings.HasPrefix(tableName, "uptime_") {
			return "uptime_monitors"
		}
		return "monitors"
	case "project":
		return "projects"
	case "group":
		return "groups"
	case "user":
		return "users"
	case "team":
		return "teams"
	case "event":
		return "events"
	case "release":
		return "releases"
	case "debug_file":
		return "debug_files"
	case "token":
		return ""
	}
	candidate := pluralize(base)
	if _, ok := tableNames[candidate]; ok {
		return candidate
	}
	return ""
}

func pluralize(value string) string {
	if value == "" {
		return ""
	}
	if strings.HasSuffix(value, "y") && !strings.HasSuffix(value, "ay") && !strings.HasSuffix(value, "ey") && !strings.HasSuffix(value, "iy") && !strings.HasSuffix(value, "oy") && !strings.HasSuffix(value, "uy") {
		return value[:len(value)-1] + "ies"
	}
	return value + "s"
}

func relationKind(column string) string {
	if strings.HasSuffix(column, "_row_id") {
		return "row_reference"
	}
	return "reference"
}

func generateQueryFields(repoRoot string) (QueryFieldsRegistry, error) {
	catalogPath := filepath.Join(repoRoot, "apps", "urgentry", "internal", "discover", "catalog.go")
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		return QueryFieldsRegistry{}, err
	}
	datasets, aggregates, err := parseDiscoverCatalog(string(data))
	if err != nil {
		return QueryFieldsRegistry{}, err
	}
	return QueryFieldsRegistry{
		SourceFiles:    []string{relPath(repoRoot, catalogPath)},
		DatasetCount:   len(datasets),
		AggregateCount: len(aggregates),
		Datasets:       datasets,
		Aggregates:     aggregates,
	}, nil
}

func parseDiscoverCatalog(data string) ([]QueryDataset, []QueryAggregate, error) {
	lines := strings.Split(data, "\n")
	datasets := make([]QueryDataset, 0, 8)
	currentDataset := -1
	inDatasetCatalog := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "var datasetCatalog =") {
			inDatasetCatalog = true
			continue
		}
		if !inDatasetCatalog {
			continue
		}
		if currentDataset == -1 {
			if strings.HasPrefix(trimmed, "Dataset") && strings.HasSuffix(trimmed, "{") {
				raw := strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(trimmed, "{")), ":")
				datasets = append(datasets, QueryDataset{Name: normalizeDatasetConst(raw)})
				currentDataset = len(datasets) - 1
				continue
			}
			if trimmed == "}" {
				break
			}
			continue
		}
		if trimmed == "}," {
			currentDataset = -1
			continue
		}
		match := fieldSpecRE.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		spec := match[2]
		datasets[currentDataset].Fields = append(datasets[currentDataset].Fields, QueryField{
			Name:       match[1],
			Type:       normalizeValueType(specValue(spec, "Type")),
			Groupable:  strings.Contains(spec, "Groupable: true"),
			Searchable: strings.Contains(spec, "Searchable: true"),
			Measure:    strings.Contains(spec, "Measure: true"),
		})
	}
	for i := range datasets {
		sort.Slice(datasets[i].Fields, func(a, b int) bool { return datasets[i].Fields[a].Name < datasets[i].Fields[b].Name })
	}

	aggregates := make([]QueryAggregate, 0, 16)
	inAggregateCatalog := false
	currentAggregate := -1
	inAggregateDatasets := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "var aggregateCatalog =") {
			inAggregateCatalog = true
			continue
		}
		if !inAggregateCatalog {
			continue
		}
		if currentAggregate == -1 {
			match := aggregateStartRE.FindStringSubmatch(trimmed)
			if match != nil {
				aggregates = append(aggregates, QueryAggregate{Name: match[1]})
				currentAggregate = len(aggregates) - 1
				continue
			}
			if trimmed == "}" {
				break
			}
			continue
		}
		if inAggregateDatasets {
			if trimmed == "}," {
				inAggregateDatasets = false
				continue
			}
			match := datasetRefRE.FindStringSubmatch(trimmed)
			if match != nil {
				aggregates[currentAggregate].Datasets = append(aggregates[currentAggregate].Datasets, normalizeDatasetConst(match[1]))
			}
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "Datasets: map[Dataset]struct{}{"):
			inAggregateDatasets = true
		case strings.HasPrefix(trimmed, "Args:"):
			value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "Args:"), ","))
			args, err := strconv.Atoi(value)
			if err != nil {
				return nil, nil, fmt.Errorf("parse aggregate args from %q: %w", trimmed, err)
			}
			aggregates[currentAggregate].Args = args
		case strings.HasPrefix(trimmed, "Field:"):
			aggregates[currentAggregate].DefaultField = strings.Trim(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "Field:"), ",")), `"`)
		case trimmed == "},":
			sort.Strings(aggregates[currentAggregate].Datasets)
			currentAggregate = -1
		}
	}
	sort.Slice(aggregates, func(i, j int) bool { return aggregates[i].Name < aggregates[j].Name })

	return datasets, aggregates, nil
}

func normalizeDatasetConst(value string) string {
	value = strings.TrimPrefix(value, "Dataset")
	return strings.ToLower(value)
}

func normalizeValueType(value string) string {
	value = strings.TrimPrefix(value, "valueType")
	return strings.ToLower(strings.TrimSpace(value))
}

func specValue(spec, key string) string {
	needle := key + ":"
	index := strings.Index(spec, needle)
	if index < 0 {
		return ""
	}
	value := strings.TrimSpace(spec[index+len(needle):])
	if next := strings.IndexByte(value, ','); next >= 0 {
		value = value[:next]
	}
	return strings.TrimSpace(value)
}

func parseMarkdownRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		value = strings.Trim(value, "`")
		cells = append(cells, value)
	}
	return cells
}

func splitList(value string) []string {
	value = strings.TrimSpace(strings.Trim(value, "`"))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, "`"))
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func mergeUniqueStrings(left, right []string) []string {
	if len(right) == 0 {
		return append([]string(nil), left...)
	}
	seen := make(map[string]struct{}, len(left)+len(right))
	out := make([]string, 0, len(left)+len(right))
	for _, item := range left {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	for _, item := range right {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func namedCountsDescending(items map[string]int) []NamedCount {
	out := make([]NamedCount, 0, len(items))
	for name, count := range items {
		out = append(out, NamedCount{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Name < out[j].Name
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func namedCountsInOrder(order []string, items map[string]int) []NamedCount {
	out := make([]NamedCount, 0, len(order))
	for _, name := range order {
		if count := items[name]; count > 0 {
			out = append(out, NamedCount{Name: name, Count: count})
		}
	}
	return out
}

func relPath(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func openAPIPrefix(path string) string {
	parts := splitPath(path)
	if len(parts) == 0 {
		return "root"
	}
	if parts[0] == "api" && len(parts) >= 2 && parts[1] == "0" {
		if len(parts) >= 3 {
			return parts[2]
		}
		return "root"
	}
	if parts[0] == "api" && len(parts) >= 2 {
		return parts[1]
	}
	return parts[0]
}

func splitPath(path string) []string {
	raw := strings.Split(path, "/")
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func isHTTPMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func classifyTableFamily(name string) string {
	switch name {
	case "organizations", "teams", "users", "user_password_credentials", "organization_members", "user_sessions", "personal_access_tokens", "project_automation_tokens", "auth_audit_logs", "team_members", "member_invites", "project_memberships", "totp_recovery_codes":
		return "identity_auth"
	case "projects", "project_keys", "project_environments", "project_teams", "sampling_rules", "quota_rate_limits", "inbound_filters", "telemetry_retention_policies", "query_guard_policies", "query_guard_usage", "schema_metadata":
		return "project_catalog"
	case "saved_searches", "dashboards", "dashboard_widgets", "saved_search_favorites", "saved_search_tags", "analytics_snapshots", "analytics_report_schedules", "metric_alert_rules":
		return "query_analytics"
	case "events", "groups", "transactions", "spans", "outcomes", "release_sessions", "monitors", "monitor_checkins", "replay_manifests", "replay_assets", "replay_timeline_items", "profile_manifests", "profile_threads", "profile_frames", "profile_stacks", "profile_stack_frames", "profile_samples", "uptime_monitors", "uptime_check_results", "anomaly_events", "metric_buckets", "user_feedback":
		return "ingest_telemetry"
	case "artifacts", "event_attachments", "debug_files", "native_symbol_sources", "native_crash_images", "native_crashes", "project_symbol_sources", "preprod_artifacts", "project_replay_configs", "issue_autofix_runs":
		return "artifacts_symbolication"
	case "issue_comments", "issue_activity", "issue_bookmarks", "issue_subscriptions", "ownership_rules":
		return "issue_workflow"
	case "releases", "release_deploys", "release_commits", "alert_rules", "alert_history", "notification_outbox", "notification_deliveries", "notification_actions", "notification_routing_rules", "project_hooks", "workflows", "detectors", "replay_deletion_jobs":
		return "releases_alerting_ops"
	case "backfill_runs", "install_state", "operator_audit_logs", "jobs", "runtime_leases", "telemetry_archives", "_migrations":
		return "control_ops"
	case "external_users", "external_teams", "integration_configs", "code_mappings", "group_external_issues", "org_data_forwarders", "data_forwarding_configs", "sentry_apps":
		return "integrations_forwarding"
	case "repositories", "prevent_repository_branches", "prevent_repository_tokens", "prevent_repository_test_suites", "prevent_repository_test_results", "prevent_repository_test_result_aggregates":
		return "prevent"
	default:
		return "misc"
	}
}

func artifactFamilies() []ArtifactFamily {
	return []ArtifactFamily{
		{
			Name:        "attachments",
			Description: "Standalone and envelope-backed event attachments.",
			Routes: []string{
				"/api/{project_id}/envelope/",
				"/api/0/projects/{org_slug}/{proj_slug}/attachments/",
			},
			Tables: []string{"artifacts", "event_attachments"},
		},
		{
			Name:        "release_files",
			Description: "Release-scoped generic artifact uploads such as source maps, ProGuard mappings, and artifact bundles.",
			Routes: []string{
				"/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/files/",
			},
			Tables: []string{"artifacts"},
		},
		{
			Name:        "debug_files",
			Description: "Release-scoped native debug files, symbol sources, and native reprocess inputs.",
			Routes: []string{
				"/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/",
				"/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/{debug_file_id}/",
				"/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/{debug_file_id}/reprocess/",
			},
			Tables: []string{"debug_files", "native_symbol_sources", "native_crashes", "native_crash_images", "project_symbol_sources"},
		},
		{
			Name:        "replay_assets",
			Description: "Replay manifests, playback assets, and pane-aware timeline items.",
			Routes: []string{
				"/api/{project_id}/envelope/",
				"/api/0/projects/{org_slug}/{proj_slug}/replays/",
				"/api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/",
			},
			Tables: []string{"replay_manifests", "replay_assets", "replay_timeline_items", "project_replay_configs"},
		},
		{
			Name:        "profile_payloads",
			Description: "Canonical profile manifests plus normalized thread, frame, stack, and sample graphs.",
			Routes: []string{
				"/api/{project_id}/envelope/",
				"/api/0/projects/{org_slug}/{proj_slug}/profiles/",
				"/api/0/projects/{org_slug}/{proj_slug}/profiles/{profile_id}/",
			},
			Tables: []string{"profile_manifests", "profile_threads", "profile_frames", "profile_stacks", "profile_stack_frames", "profile_samples"},
		},
		{
			Name:        "preprod_artifacts",
			Description: "Pre-production mobile build artifacts and install-detail metadata.",
			Tables:      []string{"preprod_artifacts"},
		},
		{
			Name:        "import_export_blobs",
			Description: "Organization import and export bundles with artifact bodies and checksum manifests.",
			Routes: []string{
				"/api/0/organizations/{org_slug}/import/",
				"/api/0/organizations/{org_slug}/export/",
			},
			Tables: []string{"artifacts"},
		},
	}
}

var explicitRelationTargets = map[string]string{
	"saved_search_favorites.saved_search_id":                  "saved_searches",
	"saved_search_tags.saved_search_id":                       "saved_searches",
	"analytics_snapshots.saved_search_id":                     "saved_searches",
	"analytics_snapshots.dashboard_id":                        "dashboards",
	"analytics_snapshots.widget_id":                           "dashboard_widgets",
	"analytics_report_schedules.saved_search_id":              "saved_searches",
	"analytics_report_schedules.dashboard_id":                 "dashboards",
	"analytics_report_schedules.widget_id":                    "dashboard_widgets",
	"dashboard_widgets.dashboard_id":                          "dashboards",
	"organization_members.organization_id":                    "organizations",
	"organization_members.user_id":                            "users",
	"user_sessions.user_id":                                   "users",
	"personal_access_tokens.user_id":                          "users",
	"project_automation_tokens.project_id":                    "projects",
	"auth_audit_logs.user_id":                                 "users",
	"team_members.team_id":                                    "teams",
	"team_members.user_id":                                    "users",
	"member_invites.organization_id":                          "organizations",
	"notification_deliveries.project_id":                      "projects",
	"project_memberships.project_id":                          "projects",
	"project_memberships.user_id":                             "users",
	"totp_recovery_codes.user_id":                             "users",
	"anomaly_events.project_id":                               "projects",
	"project_environments.project_id":                         "projects",
	"project_teams.project_id":                                "projects",
	"project_teams.team_id":                                   "teams",
	"repositories.organization_id":                            "organizations",
	"prevent_repository_branches.repository_id":               "repositories",
	"prevent_repository_tokens.repository_id":                 "repositories",
	"prevent_repository_test_suites.repository_id":            "repositories",
	"prevent_repository_test_results.repository_id":           "repositories",
	"prevent_repository_test_results.suite_id":                "prevent_repository_test_suites",
	"prevent_repository_test_result_aggregates.repository_id": "repositories",
	"events.project_id":                                       "projects",
	"events.group_id":                                         "groups",
	"event_attachments.project_id":                            "projects",
	"event_attachments.event_id":                              "events",
	"release_sessions.project_id":                             "projects",
	"outcomes.project_id":                                     "projects",
	"monitors.project_id":                                     "projects",
	"monitor_checkins.monitor_id":                             "monitors",
	"monitor_checkins.project_id":                             "projects",
	"transactions.project_id":                                 "projects",
	"spans.project_id":                                        "projects",
	"issue_comments.group_id":                                 "groups",
	"issue_comments.project_id":                               "projects",
	"issue_activity.group_id":                                 "groups",
	"issue_activity.project_id":                               "projects",
	"issue_bookmarks.group_id":                                "groups",
	"issue_subscriptions.group_id":                            "groups",
	"ownership_rules.project_id":                              "projects",
	"release_deploys.release_id":                              "releases",
	"release_commits.release_id":                              "releases",
	"profile_manifests.event_row_id":                          "events",
	"profile_manifests.project_id":                            "projects",
	"profile_threads.manifest_id":                             "profile_manifests",
	"profile_frames.manifest_id":                              "profile_manifests",
	"profile_stacks.manifest_id":                              "profile_manifests",
	"profile_stack_frames.manifest_id":                        "profile_manifests",
	"profile_stack_frames.stack_id":                           "profile_stacks",
	"profile_stack_frames.frame_id":                           "profile_frames",
	"profile_samples.manifest_id":                             "profile_manifests",
	"profile_samples.thread_row_id":                           "profile_threads",
	"profile_samples.stack_id":                                "profile_stacks",
	"native_symbol_sources.debug_file_id":                     "debug_files",
	"native_symbol_sources.project_id":                        "projects",
	"native_crash_images.project_id":                          "projects",
	"native_crash_images.event_id":                            "events",
	"native_crashes.project_id":                               "projects",
	"native_crashes.event_id":                                 "events",
	"replay_manifests.event_row_id":                           "events",
	"replay_manifests.project_id":                             "projects",
	"replay_assets.manifest_id":                               "replay_manifests",
	"replay_timeline_items.manifest_id":                       "replay_manifests",
	"project_replay_configs.project_id":                       "projects",
	"preprod_artifacts.project_id":                            "projects",
	"issue_autofix_runs.project_id":                           "projects",
	"uptime_monitors.project_id":                              "projects",
	"uptime_check_results.monitor_id":                         "uptime_monitors",
	"uptime_check_results.project_id":                         "projects",
	"sampling_rules.project_id":                               "projects",
	"quota_rate_limits.project_id":                            "projects",
	"metric_buckets.project_id":                               "projects",
	"notification_routing_rules.organization_id":              "organizations",
	"inbound_filters.project_id":                              "projects",
	"project_symbol_sources.project_id":                       "projects",
	"group_external_issues.group_id":                          "groups",
	"project_hooks.project_id":                                "projects",
	"notification_actions.organization_id":                    "organizations",
	"replay_deletion_jobs.project_id":                         "projects",
	"detectors.organization_id":                               "organizations",
	"workflows.organization_id":                               "organizations",
	"external_users.organization_id":                          "organizations",
	"org_data_forwarders.organization_id":                     "organizations",
	"external_teams.organization_id":                          "organizations",
	"integration_configs.organization_id":                     "organizations",
	"code_mappings.project_id":                                "projects",
	"data_forwarding_configs.project_id":                      "projects",
	"telemetry_retention_policies.project_id":                 "projects",
	"telemetry_archives.project_id":                           "projects",
	"backfill_runs.organization_id":                           "organizations",
	"operator_audit_logs.organization_id":                     "organizations",
	"operator_audit_logs.project_id":                          "projects",
}

func init() {
	if len(artifactFamilies()) == 0 {
		panic(errors.New("artifact families must not be empty"))
	}
}
