package synthetic

type ArtifactManifest struct {
	SourceFiles []string       `json:"source_files"`
	Cases       []ArtifactCase `json:"cases"`
}

type ArtifactCase struct {
	ID            string            `json:"id"`
	Family        string            `json:"family"`
	Method        string            `json:"method"`
	RouteTemplate string            `json:"route_template"`
	ContentType   string            `json:"content_type"`
	Builder       string            `json:"builder"`
	Files         []ArtifactFile    `json:"files,omitempty"`
	ExtraFields   map[string]string `json:"extra_fields,omitempty"`
	ScenarioHints []string          `json:"scenario_hints,omitempty"`
}

type ArtifactFile struct {
	FieldName   string `json:"field_name"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type,omitempty"`
	Description string `json:"description,omitempty"`
}

func generateArtifactManifest(repoRoot string) (ArtifactManifest, error) {
	_ = repoRoot
	cases := []ArtifactCase{
		{
			ID:            "artifact/envelope_attachment_text",
			Family:        "attachment",
			Method:        "POST",
			RouteTemplate: "/api/{project_id}/envelope/",
			ContentType:   "application/x-sentry-envelope",
			Builder:       "envelope_attachment_text",
			ScenarioHints: []string{"compat", "attachments"},
		},
		{
			ID:            "artifact/standalone_attachment_text",
			Family:        "standalone_attachment",
			Method:        "POST",
			RouteTemplate: "/api/0/projects/{org_slug}/{proj_slug}/attachments/",
			ContentType:   "multipart/form-data",
			Builder:       "standalone_attachment_text",
			Files: []ArtifactFile{{
				FieldName:   "file",
				FileName:    "standalone.log",
				ContentType: "text/plain",
			}},
			ExtraFields:   map[string]string{"event_id": "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1"},
			ScenarioHints: []string{"compat", "attachments"},
		},
		{
			ID:            "artifact/source_map_basic",
			Family:        "source_map",
			Method:        "POST",
			RouteTemplate: "/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/files/",
			ContentType:   "multipart/form-data",
			Builder:       "source_map_basic",
			Files: []ArtifactFile{{
				FieldName:   "file",
				FileName:    "app.min.js.map",
				ContentType: "application/json",
			}},
			ScenarioHints: []string{"compat", "artifacts", "source-maps"},
		},
		{
			ID:            "artifact/proguard_basic",
			Family:        "proguard",
			Method:        "POST",
			RouteTemplate: "/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/proguard/",
			ContentType:   "multipart/form-data",
			Builder:       "proguard_basic",
			Files: []ArtifactFile{{
				FieldName:   "file",
				FileName:    "mapping.txt",
				ContentType: "text/plain",
			}},
			ScenarioHints: []string{"compat", "artifacts", "proguard"},
		},
		{
			ID:            "artifact/import_bundle_minimal",
			Family:        "import_export",
			Method:        "POST",
			RouteTemplate: "/api/0/organizations/{org_slug}/import/",
			ContentType:   "application/json",
			Builder:       "import_bundle_minimal",
			ScenarioHints: []string{"api", "import-export"},
		},
	}
	return ArtifactManifest{
		SourceFiles: []string{
			"apps/urgentry/internal/compat/attachment_conformance_test.go",
			"apps/urgentry/internal/compat/compat_test.go",
			"apps/urgentry/internal/api/import_export_test.go",
			"apps/urgentry/internal/api/proguard_test.go",
			"apps/urgentry/internal/sqlite/import_export_store_test.go",
			"apps/urgentry/internal/issue/sourcemap_test.go",
			"apps/urgentry/internal/issue/proguard_test.go",
		},
		Cases: cases,
	}, nil
}
