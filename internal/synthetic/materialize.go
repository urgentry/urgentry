package synthetic

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"urgentry/internal/migration"
	nativefixtures "urgentry/internal/testfixtures/nativecrash"
	profilefixtures "urgentry/internal/testfixtures/profiles"
	replayfixtures "urgentry/internal/testfixtures/replays"
)

type MaterializedDeep struct {
	Case          DeepCase                      `json:"case"`
	EnvelopeBody  []byte                        `json:"envelope_body,omitempty"`
	ReplayPolicy  map[string]any                `json:"replay_policy,omitempty"`
	NativeDump    []byte                        `json:"native_dump,omitempty"`
	NativeEvent   []byte                        `json:"native_event,omitempty"`
	SymbolSources []nativefixtures.SymbolSource `json:"symbol_sources,omitempty"`
}

func WriteArtifactCorpus(root string) error {
	manifest, err := generateArtifactManifest(RepoRoot())
	if err != nil {
		return err
	}
	for _, item := range manifest.Cases {
		caseDir := filepath.Join(root, sanitizeCaseID(item.ID))
		if err := os.MkdirAll(caseDir, 0o755); err != nil {
			return err
		}
		body, name, contentType, err := MaterializeArtifactCase(item)
		if err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(caseDir, "metadata.json"), map[string]any{
			"case":         item,
			"content_type": contentType,
		}); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(caseDir, name), body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func WriteDeepCorpus(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, fixture := range replayfixtures.Corpus() {
		caseDir := filepath.Join(root, "replay-"+fixture.Name)
		if err := os.MkdirAll(caseDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(caseDir, "envelope.envelope"), fixture.EnvelopeBody(), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(caseDir, "recording.json"), fixture.RecordingPayload(), 0o644); err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(caseDir, "metadata.json"), fixture.Spec()); err != nil {
			return err
		}
	}
	for _, fixture := range []profilefixtures.Fixture{
		profilefixtures.SaveRead(),
		profilefixtures.IOHeavy(),
		profilefixtures.CPUHeavy(),
		profilefixtures.MixedLanguage(),
		profilefixtures.DBHeavy(),
		profilefixtures.MalformedEmpty(),
		profilefixtures.InvalidFrames(),
	} {
		caseDir := filepath.Join(root, "profile-"+fixture.Name)
		if err := os.MkdirAll(caseDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(caseDir, "payload.json"), fixture.Spec().Payload(), 0o644); err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(caseDir, "metadata.json"), fixture.Spec()); err != nil {
			return err
		}
	}
	nativeCorpus, err := nativefixtures.CorpusForLibrary()
	if err != nil {
		return err
	}
	for _, fixture := range nativeCorpus {
		caseDir := filepath.Join(root, "native-"+fixture.Name)
		if err := os.MkdirAll(caseDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(caseDir, fixture.DumpFilename), fixture.Dump, 0o644); err != nil {
			return err
		}
		eventBody, err := fixture.EventJSONForLibrary(nativeEventID(fixture.Name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(caseDir, "event.json"), eventBody, 0o644); err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(caseDir, "metadata.json"), fixture); err != nil {
			return err
		}
		for _, symbol := range fixture.Symbols {
			if err := os.WriteFile(filepath.Join(caseDir, symbol.Name), symbol.Body, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func MaterializeDeepCase(id string) (MaterializedDeep, error) {
	manifest, err := generateDeepManifest(RepoRoot())
	if err != nil {
		return MaterializedDeep{}, err
	}
	for _, item := range manifest.Cases {
		if item.ID != id {
			continue
		}
		switch item.Family {
		case "replay":
			var fixture replayfixtures.Fixture
			switch item.Name {
			case "core_journey":
				fixture = replayfixtures.CoreJourney()
			case "scrubbed_journey":
				fixture = replayfixtures.ScrubbedJourney()
			default:
				return MaterializedDeep{}, fmt.Errorf("unknown replay fixture %q", item.Name)
			}
			policy := fixture.Policy()
			return MaterializedDeep{
				Case:         item,
				EnvelopeBody: fixture.EnvelopeBody(),
				ReplayPolicy: map[string]any{"sampleRate": policy.SampleRate, "maxBytes": policy.MaxBytes, "scrubFields": policy.ScrubFields, "scrubSelectors": policy.ScrubSelectors},
			}, nil
		case "profile":
			var fixture profilefixtures.Fixture
			switch item.Name {
			case "save_read":
				fixture = profilefixtures.SaveRead()
			case "io_heavy":
				fixture = profilefixtures.IOHeavy()
			case "cpu_heavy":
				fixture = profilefixtures.CPUHeavy()
			case "mixed_language":
				fixture = profilefixtures.MixedLanguage()
			case "db_heavy":
				fixture = profilefixtures.DBHeavy()
			case "malformed_empty":
				fixture = profilefixtures.MalformedEmpty()
			case "invalid_frames":
				fixture = profilefixtures.InvalidFrames()
			default:
				return MaterializedDeep{}, fmt.Errorf("unknown profile fixture %q", item.Name)
			}
			return MaterializedDeep{Case: item, EnvelopeBody: fixture.Spec().Payload()}, nil
		case "native":
			fixture, err := nativefixtures.ByNameForLibrary(strings.TrimPrefix(item.ID, "native/"))
			if err != nil {
				return MaterializedDeep{}, err
			}
			eventBody, err := fixture.EventJSONForLibrary(nativeEventID(fixture.Name))
			if err != nil {
				return MaterializedDeep{}, err
			}
			return MaterializedDeep{
				Case:          item,
				NativeDump:    append([]byte(nil), fixture.Dump...),
				NativeEvent:   eventBody,
				SymbolSources: append([]nativefixtures.SymbolSource(nil), fixture.Symbols...),
			}, nil
		default:
			return MaterializedDeep{}, fmt.Errorf("unsupported deep family %q", item.Family)
		}
	}
	return MaterializedDeep{}, fmt.Errorf("unknown deep case %q", id)
}

func MaterializeArtifactCase(item ArtifactCase) ([]byte, string, string, error) {
	switch item.Builder {
	case "envelope_attachment_text":
		eventID := "07070707070707070707070707070707"
		eventPayload := mustJSON(map[string]any{
			"event_id": eventID,
			"message":  "Synthetic artifact envelope",
			"level":    "error",
			"platform": "go",
		})
		body := buildEnvelope(map[string]any{"event_id": eventID},
			envelopeItem{typ: "event", payload: eventPayload},
			envelopeItem{typ: "attachment", payload: []byte("synthetic attachment body"), filename: "test.txt", contentType: "text/plain"},
		)
		return body, "request.envelope", item.ContentType, nil
	case "standalone_attachment_text":
		body, contentType, err := BuildMultipart([]multipartPart{{
			FieldName:   "file",
			FileName:    "standalone.log",
			ContentType: "text/plain",
			Body:        []byte("synthetic standalone attachment"),
		}}, item.ExtraFields)
		if err != nil {
			return nil, "", "", err
		}
		return body, "request.multipart", contentType, nil
	case "source_map_basic":
		sourceMap := []byte(`{"version":3,"file":"app.min.js","sources":["app.ts"],"names":[],"mappings":"AAAA"}`)
		body, contentType, err := BuildMultipart([]multipartPart{{
			FieldName:   "file",
			FileName:    "app.min.js.map",
			ContentType: "application/json",
			Body:        sourceMap,
		}}, map[string]string{"name": "app.min.js.map"})
		if err != nil {
			return nil, "", "", err
		}
		return body, "request.multipart", contentType, nil
	case "proguard_basic":
		mapping := []byte("com.example.Foo -> a:")
		body, contentType, err := BuildMultipart([]multipartPart{{
			FieldName:   "file",
			FileName:    "mapping.txt",
			ContentType: "text/plain",
			Body:        mapping,
		}}, map[string]string{"uuid": "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"})
		if err != nil {
			return nil, "", "", err
		}
		return body, "request.multipart", contentType, nil
	case "import_bundle_minimal":
		payload := migration.ImportPayload{
			Projects: []migration.ProjectImport{{
				ID:       "synthetic-project-1",
				Name:     "Synthetic Import Project",
				Slug:     "synthetic-import-project",
				Platform: "go",
			}},
			Artifacts: []migration.ArtifactImport{{
				Kind:        "attachment",
				ID:          "synthetic-artifact-1",
				ProjectSlug: "synthetic-import-project",
				EventID:     "08080808080808080808080808080808",
				Name:        "crash.txt",
				ContentType: "text/plain",
				Checksum:    "sha256:synthetic-attachment",
				Size:        20,
				BodyBase64:  "c3ludGhldGljIGF0dGFjaG1lbnQ=",
			}},
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, "", "", err
		}
		return body, "request.json", "application/json", nil
	default:
		return nil, "", "", fmt.Errorf("unsupported artifact builder %q", item.Builder)
	}
}

type envelopeItem struct {
	typ         string
	payload     []byte
	filename    string
	contentType string
}

func buildEnvelope(header map[string]any, items ...envelopeItem) []byte {
	lines := []string{string(mustJSON(header))}
	for _, item := range items {
		line := fmt.Sprintf(`{"type":"%s","length":%d`, item.typ, len(item.payload))
		if item.filename != "" {
			line += `,"filename":"` + item.filename + `"`
		}
		if item.contentType != "" {
			line += `,"content_type":"` + item.contentType + `"`
		}
		line += `}`
		lines = append(lines, line, string(item.payload))
	}
	return []byte(strings.Join(lines, "\n"))
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func sanitizeCaseID(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	return replacer.Replace(value)
}

func nativeEventID(name string) string {
	switch name {
	case "apple_multimodule":
		return "14141414141414141414141414141414"
	case "linux_elf":
		return "15151515151515151515151515151515"
	case "fallback_module_only":
		return "16161616161616161616161616161616"
	default:
		return "17171717171717171717171717171717"
	}
}
