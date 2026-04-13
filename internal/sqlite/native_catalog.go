package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/minidump"
	"urgentry/pkg/id"
)

type NativeSymbolSource struct {
	ID           string
	DebugFileID  string
	ProjectID    string
	ReleaseID    string
	Kind         string
	DebugID      string
	CodeID       string
	BuildID      string
	UUID         string
	ModuleName   string
	Architecture string
	Platform     string
	MatchedBy    string
	CreatedAt    time.Time
}

type NativeCrashImage struct {
	ID              string
	ProjectID       string
	EventID         string
	Position        int
	ReleaseID       string
	Platform        string
	ImageName       string
	ModuleName      string
	DebugID         string
	CodeID          string
	BuildID         string
	UUID            string
	Architecture    string
	ImageAddr       string
	ImageSize       string
	InstructionAddr string
	Source          string
	CreatedAt       time.Time
}

type NativeLookupInput struct {
	DebugID      string
	CodeID       string
	BuildID      string
	UUID         string
	ModuleName   string
	Architecture string
	Platform     string
}

func (s *DebugFileStore) ListNativeSymbolSourcesByRelease(ctx context.Context, projectID, releaseVersion string) ([]NativeSymbolSource, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, debug_file_id, project_id, release_version, kind, debug_id, code_id, build_id, uuid, module_name, architecture, platform, created_at
		   FROM native_symbol_sources
		  WHERE project_id = ? AND release_version = ?
		  ORDER BY created_at DESC, id DESC`,
		projectID, releaseVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("list native symbol sources: %w", err)
	}
	defer rows.Close()

	var items []NativeSymbolSource
	for rows.Next() {
		item, err := scanNativeSymbolSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *DebugFileStore) LookupNativeSymbolSource(ctx context.Context, projectID, releaseVersion, kind string, input NativeLookupInput) (*NativeSymbolSource, *DebugFile, []byte, error) {
	source, file, body, err := s.lookupNativeSymbolSource(ctx, projectID, releaseVersion, kind, input)
	if err != nil || source == nil {
		return source, file, body, err
	}
	return source, file, body, nil
}

func (s *DebugFileStore) lookupNativeSymbolSource(ctx context.Context, projectID, releaseVersion, kind string, input NativeLookupInput) (*NativeSymbolSource, *DebugFile, []byte, error) {
	normalized := NativeLookupInput{
		DebugID:      normalizeNativeKey(input.DebugID),
		CodeID:       normalizeNativeKey(input.CodeID),
		BuildID:      normalizeNativeKey(input.BuildID),
		UUID:         normalizeNativeKey(input.UUID),
		ModuleName:   normalizeNativeKey(input.ModuleName),
		Architecture: normalizeNativeKey(input.Architecture),
		Platform:     normalizeNativeKey(input.Platform),
	}
	matchers := []struct {
		column string
		value  string
	}{
		{column: "debug_id", value: normalized.DebugID},
		{column: "code_id", value: normalized.CodeID},
		{column: "build_id", value: normalized.BuildID},
		{column: "uuid", value: normalized.UUID},
	}
	for _, matcher := range matchers {
		if matcher.value == "" {
			continue
		}
		source, file, body, err := s.lookupNativeSymbolSourceByColumn(ctx, projectID, releaseVersion, kind, matcher.column, matcher.value, normalized)
		if err != nil || source != nil {
			return source, file, body, err
		}
	}
	if normalized.ModuleName != "" {
		source, file, body, err := s.lookupNativeSymbolSourceByModule(ctx, projectID, releaseVersion, kind, normalized)
		if err != nil || source != nil {
			return source, file, body, err
		}
	}
	return nil, nil, nil, nil
}

func (s *DebugFileStore) ListNativeCrashImages(ctx context.Context, projectID, eventID string) ([]NativeCrashImage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, event_id, position, release_version, platform, image_name, module_name, debug_id, code_id, build_id, uuid, architecture, image_addr, image_size, instruction_addr, source, created_at
		   FROM native_crash_images
		  WHERE project_id = ? AND event_id = ?
		  ORDER BY position ASC, created_at ASC, id ASC`,
		projectID, eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("list native crash images: %w", err)
	}
	defer rows.Close()

	var items []NativeCrashImage
	for rows.Next() {
		item, err := scanNativeCrashImage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *DebugFileStore) RebuildNativeCrashImages(ctx context.Context, projectID, eventID string, payload []byte) error {
	return rebuildNativeCrashImagesWithQuerier(ctx, s.db, projectID, eventID, payload)
}

func rebuildNativeCrashImagesWithQuerier(ctx context.Context, db execQuerier, projectID, eventID string, payload []byte) error {
	images, err := collectNativeCrashImages(eventID, payload)
	if err != nil {
		return err
	}
	return replaceNativeCrashImagesWithQuerier(ctx, db, projectID, eventID, images)
}

func mergeMinidumpCrashImagesWithQuerier(ctx context.Context, db *sql.DB, projectID, eventID, releaseVersion, platform string, dump *minidump.File) error {
	if dump == nil {
		return nil
	}
	current, err := NewDebugFileStore(db, nil).ListNativeCrashImages(ctx, projectID, eventID)
	if err != nil {
		return err
	}
	merged := mergeDumpImages(current, releaseVersion, platform, dump)
	return replaceNativeCrashImagesWithQuerier(ctx, db, projectID, eventID, merged)
}

func replaceNativeCrashImagesWithQuerier(ctx context.Context, db execQuerier, projectID, eventID string, images []NativeCrashImage) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM native_crash_images WHERE project_id = ? AND event_id = ?`, projectID, eventID); err != nil {
		return fmt.Errorf("clear native crash images: %w", err)
	}
	for i, item := range images {
		if item.ID == "" {
			item.ID = id.New()
		}
		item.ProjectID = projectID
		item.EventID = eventID
		item.Position = i
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now().UTC()
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO native_crash_images
				(id, project_id, event_id, position, release_version, platform, image_name, module_name, debug_id, code_id, build_id, uuid, architecture, image_addr, image_size, instruction_addr, source, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.ProjectID, item.EventID, item.Position, item.ReleaseID, item.Platform, item.ImageName, item.ModuleName, item.DebugID, item.CodeID, item.BuildID, item.UUID, item.Architecture, item.ImageAddr, item.ImageSize, item.InstructionAddr, item.Source, item.CreatedAt.UTC().Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("insert native crash image: %w", err)
		}
	}
	return nil
}

func mergeDumpImages(current []NativeCrashImage, releaseVersion, platform string, dump *minidump.File) []NativeCrashImage {
	items := append([]NativeCrashImage(nil), current...)
	if dump == nil {
		return items
	}

	matchIndex := -1
	for _, module := range dump.Modules {
		if containsAddress(module, dump.Exception.Address) {
			matchIndex = len(items)
		}
		items = upsertDumpModule(items, releaseVersion, platform, module, dump.Exception.Address)
	}
	if matchIndex == -1 {
		for i, item := range items {
			if item.InstructionAddr != "" {
				matchIndex = i
				break
			}
		}
	}
	if matchIndex >= 0 && matchIndex < len(items) && items[matchIndex].InstructionAddr == "" {
		items[matchIndex].InstructionAddr = normalizeNativeKey(fmt.Sprintf("0x%x", dump.Exception.Address))
	}
	return dedupeNativeCrashImages(items)
}

func upsertDumpModule(items []NativeCrashImage, releaseVersion, platform string, module minidump.Module, exceptionAddr uint64) []NativeCrashImage {
	moduleName := normalizeNativeKey(module.Name)
	imageAddr := normalizeNativeKey(fmt.Sprintf("0x%x", module.BaseOfImage))
	imageSize := normalizeNativeKey(fmt.Sprintf("0x%x", module.SizeOfImage))
	instructionAddr := ""
	if containsAddress(module, exceptionAddr) {
		instructionAddr = normalizeNativeKey(fmt.Sprintf("0x%x", exceptionAddr))
	}

	for i := range items {
		if moduleName != "" && moduleName == items[i].ModuleName || imageAddr != "" && imageAddr == items[i].ImageAddr {
			if items[i].ReleaseID == "" {
				items[i].ReleaseID = normalizeNativeKey(releaseVersion)
			}
			if items[i].Platform == "" {
				items[i].Platform = normalizeNativeKey(platform)
			}
			if items[i].ImageName == "" {
				items[i].ImageName = moduleName
			}
			if items[i].ModuleName == "" {
				items[i].ModuleName = moduleName
			}
			if items[i].ImageAddr == "" {
				items[i].ImageAddr = imageAddr
			}
			if items[i].ImageSize == "" {
				items[i].ImageSize = imageSize
			}
			if items[i].InstructionAddr == "" {
				items[i].InstructionAddr = instructionAddr
			}
			if items[i].Source == "" || items[i].Source == "frame" || items[i].Source == "tags" {
				items[i].Source = "minidump"
			}
			return items
		}
	}

	return append(items, NativeCrashImage{
		ReleaseID:       normalizeNativeKey(releaseVersion),
		Platform:        normalizeNativeKey(platform),
		ImageName:       moduleName,
		ModuleName:      moduleName,
		ImageAddr:       imageAddr,
		ImageSize:       imageSize,
		InstructionAddr: instructionAddr,
		Source:          "minidump",
	})
}

func containsAddress(module minidump.Module, addr uint64) bool {
	if module.BaseOfImage == 0 || module.SizeOfImage == 0 || addr == 0 {
		return false
	}
	limit := module.BaseOfImage + uint64(module.SizeOfImage)
	return addr >= module.BaseOfImage && addr < limit
}

func upsertNativeSymbolSources(ctx context.Context, db execQuerier, file *DebugFile, body []byte) error {
	if file == nil || strings.EqualFold(file.Kind, "proguard") {
		return nil
	}
	items := buildNativeSymbolSources(file, body)
	if len(items) == 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM native_symbol_sources WHERE debug_file_id = ?`, file.ID); err != nil {
		return fmt.Errorf("clear native symbol sources: %w", err)
	}
	for _, item := range items {
		if item.ID == "" {
			item.ID = id.New()
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO native_symbol_sources
				(id, debug_file_id, project_id, release_version, kind, debug_id, code_id, build_id, uuid, module_name, architecture, platform, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.DebugFileID, item.ProjectID, item.ReleaseID, item.Kind, item.DebugID, item.CodeID, item.BuildID, item.UUID, item.ModuleName, item.Architecture, item.Platform, item.CreatedAt.UTC().Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("insert native symbol source: %w", err)
		}
	}
	return nil
}

func buildNativeSymbolSources(file *DebugFile, body []byte) []NativeSymbolSource {
	if file == nil {
		return nil
	}
	item := NativeSymbolSource{
		DebugFileID:  file.ID,
		ProjectID:    file.ProjectID,
		ReleaseID:    file.ReleaseID,
		Kind:         normalizeNativeKey(file.Kind),
		DebugID:      normalizeNativeKey(file.UUID),
		CodeID:       normalizeNativeKey(file.CodeID),
		BuildID:      normalizeNativeKey(file.BuildID),
		UUID:         normalizeNativeKey(file.UUID),
		ModuleName:   normalizeNativeKey(file.ModuleName),
		Architecture: normalizeNativeKey(file.Architecture),
		Platform:     normalizeNativeKey(file.Platform),
		CreatedAt:    file.CreatedAt,
	}
	if platform, arch, debugID, module, ok := parseBreakpadModuleHeader(body); ok {
		if item.Platform == "" {
			item.Platform = normalizeNativeKey(platform)
		}
		if item.Architecture == "" {
			item.Architecture = normalizeNativeKey(arch)
		}
		if item.DebugID == "" {
			item.DebugID = normalizeNativeKey(debugID)
		}
		if item.UUID == "" {
			item.UUID = normalizeNativeKey(debugID)
		}
		if item.ModuleName == "" {
			item.ModuleName = normalizeNativeKey(module)
		}
	}
	if item.BuildID == "" && item.Kind == "elf" {
		item.BuildID = item.CodeID
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.DebugID == "" && item.CodeID == "" && item.BuildID == "" && item.UUID == "" && item.ModuleName == "" {
		return nil
	}
	return []NativeSymbolSource{item}
}

func collectNativeCrashImages(eventID string, payload []byte) ([]NativeCrashImage, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("parse native crash payload: %w", err)
	}
	releaseVersion := stringAny(raw["release"])
	platform := stringAny(raw["platform"])
	items := make([]NativeCrashImage, 0, 4)

	if debugMeta, ok := raw["debug_meta"].(map[string]any); ok {
		if images, ok := debugMeta["images"].([]any); ok {
			for _, rawImage := range images {
				image, ok := nativeCrashImageFromMap(rawImage, "debug_meta", releaseVersion, platform)
				if ok {
					items = append(items, image)
				}
			}
		}
	}

	if exception, ok := raw["exception"].(map[string]any); ok {
		if values, ok := exception["values"].([]any); ok {
			for _, rawValue := range values {
				value, ok := rawValue.(map[string]any)
				if !ok {
					continue
				}
				stacktrace, ok := value["stacktrace"].(map[string]any)
				if !ok {
					continue
				}
				frames, ok := stacktrace["frames"].([]any)
				if !ok {
					continue
				}
				for _, rawFrame := range frames {
					image, ok := nativeCrashImageFromMap(rawFrame, "frame", releaseVersion, platform)
					if ok {
						items = append(items, image)
					}
				}
			}
		}
	}

	if tags, ok := raw["tags"].(map[string]any); ok {
		if len(items) == 0 {
			image := NativeCrashImage{
				EventID:   eventID,
				ReleaseID: normalizeNativeKey(releaseVersion),
				Platform:  normalizeNativeKey(platform),
				DebugID:   normalizeNativeKey(stringAny(tags["minidump.debug_id"])),
				CodeID:    normalizeNativeKey(stringAny(tags["minidump.code_id"])),
				Source:    "tags",
			}
			if image.DebugID != "" || image.CodeID != "" {
				items = append(items, image)
			}
		}
	}

	return dedupeNativeCrashImages(items), nil
}

func nativeCrashImageFromMap(raw any, source, releaseVersion, platform string) (NativeCrashImage, bool) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return NativeCrashImage{}, false
	}
	item := NativeCrashImage{
		ReleaseID:       normalizeNativeKey(releaseVersion),
		Platform:        normalizeNativeKey(firstNonEmptyAny(obj["platform"], platform)),
		ImageName:       normalizeNativeKey(firstNonEmptyAny(obj["code_file"], obj["image_name"], obj["debug_file"], obj["name"], obj["module"])),
		ModuleName:      normalizeNativeKey(firstNonEmptyAny(obj["module"], obj["code_file"], obj["name"])),
		DebugID:         normalizeNativeKey(firstNonEmptyAny(obj["debug_id"], obj["debugId"])),
		CodeID:          normalizeNativeKey(firstNonEmptyAny(obj["code_id"], obj["codeId"], obj["package"])),
		BuildID:         normalizeNativeKey(firstNonEmptyAny(obj["build_id"], obj["buildId"])),
		UUID:            normalizeNativeKey(firstNonEmptyAny(obj["uuid"], obj["debug_id"], obj["debugId"])),
		Architecture:    normalizeNativeKey(firstNonEmptyAny(obj["arch"], obj["architecture"])),
		ImageAddr:       normalizeNativeKey(firstNonEmptyAny(obj["image_addr"], obj["imageAddr"])),
		ImageSize:       normalizeNativeKey(firstNonEmptyAny(obj["image_size"], obj["imageSize"])),
		InstructionAddr: normalizeNativeKey(firstNonEmptyAny(obj["instruction_addr"], obj["instructionAddr"])),
		Source:          source,
	}
	if item.Platform == "" {
		item.Platform = normalizeNativeKey(platform)
	}
	if item.DebugID == "" && item.CodeID == "" && item.BuildID == "" && item.UUID == "" && item.ModuleName == "" && item.ImageAddr == "" {
		return NativeCrashImage{}, false
	}
	return item, true
}

func dedupeNativeCrashImages(items []NativeCrashImage) []NativeCrashImage {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]NativeCrashImage{}
	order := make([]string, 0, len(items))
	for _, item := range items {
		key := strings.Join([]string{
			item.ReleaseID,
			item.Platform,
			item.DebugID,
			item.CodeID,
			item.BuildID,
			item.UUID,
			item.ModuleName,
			item.ImageAddr,
			item.InstructionAddr,
		}, "|")
		if key == strings.Repeat("|", 8) {
			continue
		}
		if existing, ok := seen[key]; ok {
			if existing.ImageName == "" && item.ImageName != "" {
				existing.ImageName = item.ImageName
			}
			if existing.ImageSize == "" && item.ImageSize != "" {
				existing.ImageSize = item.ImageSize
			}
			if existing.Source == "frame" && item.Source == "debug_meta" {
				existing.Source = item.Source
			}
			seen[key] = existing
			continue
		}
		seen[key] = item
		order = append(order, key)
	}
	result := make([]NativeCrashImage, 0, len(order))
	for _, key := range order {
		result = append(result, seen[key])
	}
	return result
}

func parseBreakpadModuleHeader(body []byte) (platform, arch, debugID, module string, ok bool) {
	line := strings.TrimSpace(string(bytes.SplitN(body, []byte("\n"), 2)[0]))
	if !strings.HasPrefix(line, "MODULE ") {
		return "", "", "", "", false
	}
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return "", "", "", "", false
	}
	return fields[1], fields[2], fields[3], strings.Join(fields[4:], " "), true
}

func normalizeNativeKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func firstNonEmptyAny(values ...any) string {
	for _, value := range values {
		if text := stringAny(value); text != "" {
			return text
		}
	}
	return ""
}

func stringAny(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.RawMessage:
		return strings.TrimSpace(string(typed))
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func (s *DebugFileStore) lookupNativeSymbolSourceByColumn(ctx context.Context, projectID, releaseVersion, kind, column, value string, _ NativeLookupInput) (*NativeSymbolSource, *DebugFile, []byte, error) {
	query := `SELECT id, debug_file_id, project_id, release_version, kind, debug_id, code_id, build_id, uuid, module_name, architecture, platform, created_at
		FROM native_symbol_sources
		WHERE project_id = ? AND release_version = ?`
	args := []any{projectID, releaseVersion}
	if normalizedKind := normalizeNativeKey(kind); normalizedKind != "" {
		query += ` AND kind = ?`
		args = append(args, normalizedKind)
	}
	query += ` AND ` + column + ` = ? ORDER BY created_at DESC, id DESC LIMIT 1`
	args = append(args, value)

	row := s.db.QueryRowContext(ctx, query, args...)
	source, err := scanNativeSymbolSource(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, err
	}
	source.MatchedBy = column
	file, body, err := s.Get(ctx, source.DebugFileID)
	if err != nil {
		return nil, nil, nil, err
	}
	return &source, file, body, nil
}

func (s *DebugFileStore) lookupNativeSymbolSourceByModule(ctx context.Context, projectID, releaseVersion, kind string, input NativeLookupInput) (*NativeSymbolSource, *DebugFile, []byte, error) {
	query := `SELECT id, debug_file_id, project_id, release_version, kind, debug_id, code_id, build_id, uuid, module_name, architecture, platform, created_at
		FROM native_symbol_sources
		WHERE project_id = ? AND release_version = ? AND module_name = ?`
	args := []any{projectID, releaseVersion, input.ModuleName}
	if normalizedKind := normalizeNativeKey(kind); normalizedKind != "" {
		query += ` AND kind = ?`
		args = append(args, normalizedKind)
	}
	if input.Architecture != "" {
		query += ` AND (architecture = '' OR architecture = ?)`
		args = append(args, input.Architecture)
	}
	if input.Platform != "" {
		query += ` AND (platform = '' OR platform = ?)`
		args = append(args, input.Platform)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT 1`

	row := s.db.QueryRowContext(ctx, query, args...)
	source, err := scanNativeSymbolSource(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, err
	}
	source.MatchedBy = "module_name"
	file, body, err := s.Get(ctx, source.DebugFileID)
	if err != nil {
		return nil, nil, nil, err
	}
	return &source, file, body, nil
}

func scanNativeSymbolSource(scanner interface{ Scan(dest ...any) error }) (NativeSymbolSource, error) {
	var item NativeSymbolSource
	var createdAt string
	if err := scanner.Scan(&item.ID, &item.DebugFileID, &item.ProjectID, &item.ReleaseID, &item.Kind, &item.DebugID, &item.CodeID, &item.BuildID, &item.UUID, &item.ModuleName, &item.Architecture, &item.Platform, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return NativeSymbolSource{}, err
		}
		return NativeSymbolSource{}, fmt.Errorf("scan native symbol source: %w", err)
	}
	item.CreatedAt = parseTime(createdAt)
	return item, nil
}

func scanNativeCrashImage(scanner interface{ Scan(dest ...any) error }) (NativeCrashImage, error) {
	var item NativeCrashImage
	var createdAt string
	if err := scanner.Scan(&item.ID, &item.ProjectID, &item.EventID, &item.Position, &item.ReleaseID, &item.Platform, &item.ImageName, &item.ModuleName, &item.DebugID, &item.CodeID, &item.BuildID, &item.UUID, &item.Architecture, &item.ImageAddr, &item.ImageSize, &item.InstructionAddr, &item.Source, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return NativeCrashImage{}, err
		}
		return NativeCrashImage{}, fmt.Errorf("scan native crash image: %w", err)
	}
	item.CreatedAt = parseTime(createdAt)
	return item, nil
}
