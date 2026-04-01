package sqlite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"

	"urgentry/internal/migration"
	"urgentry/internal/store"
)

func (export *organizationPayloadExport) writeJSON(ctx context.Context, w io.Writer, blobs store.BlobStore, policy artifactExportPolicy) error {
	writer := jsonObjectWriter{w: w}
	if err := writer.begin(); err != nil {
		return err
	}
	if err := writer.sliceField("projects", export.projects); err != nil {
		return err
	}
	if err := writer.sliceField("releases", export.releases); err != nil {
		return err
	}
	if err := writer.sliceField("issues", export.issues); err != nil {
		return err
	}
	if err := writer.sliceField("events", export.events); err != nil {
		return err
	}
	if err := writer.sliceField("projectKeys", export.projectKeys); err != nil {
		return err
	}
	if err := writer.sliceField("alertRules", export.alertRules); err != nil {
		return err
	}
	if err := writer.sliceField("members", export.members); err != nil {
		return err
	}
	if len(export.artifacts) > 0 {
		if err := writer.field("artifacts", func(w io.Writer) error {
			return writeArtifactJSONArray(ctx, w, blobs, export.artifacts, policy)
		}); err != nil {
			return err
		}
	}
	return writer.end()
}

func writeArtifactJSONArray(ctx context.Context, w io.Writer, blobs store.BlobStore, artifacts []migration.ArtifactImport, policy artifactExportPolicy) error {
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	for i, item := range artifacts {
		if i > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if err := writeArtifactJSON(ctx, w, blobs, item, policy); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "]")
	return err
}

func writeArtifactJSON(ctx context.Context, w io.Writer, blobs store.BlobStore, item migration.ArtifactImport, policy artifactExportPolicy) error {
	item, body, err := materializeArtifactExport(ctx, blobs, item, policy)
	if err != nil {
		return err
	}
	writer := jsonObjectWriter{w: w}
	if err := writer.begin(); err != nil {
		return err
	}
	if err := writer.stringField("kind", item.Kind); err != nil {
		return err
	}
	if err := writer.stringField("id", item.ID); err != nil {
		return err
	}
	if err := writer.stringField("projectSlug", item.ProjectSlug); err != nil {
		return err
	}
	if err := writer.stringField("releaseVersion", item.ReleaseVersion); err != nil {
		return err
	}
	if err := writer.stringField("eventId", item.EventID); err != nil {
		return err
	}
	if err := writer.stringField("name", item.Name); err != nil {
		return err
	}
	if err := writer.stringField("contentType", item.ContentType); err != nil {
		return err
	}
	if err := writer.stringField("uuid", item.UUID); err != nil {
		return err
	}
	if err := writer.stringField("codeId", item.CodeID); err != nil {
		return err
	}
	if err := writer.stringField("objectKey", item.ObjectKey); err != nil {
		return err
	}
	if err := writer.stringField("checksum", item.Checksum); err != nil {
		return err
	}
	if err := writer.int64Field("size", item.Size); err != nil {
		return err
	}
	if len(body) > 0 {
		if err := writer.base64Field("bodyBase64", body); err != nil {
			return err
		}
	}
	if err := writer.stringField("createdAt", item.CreatedAt); err != nil {
		return err
	}
	return writer.end()
}

type jsonObjectWriter struct {
	w          io.Writer
	wroteField bool
}

func (w *jsonObjectWriter) begin() error {
	_, err := io.WriteString(w.w, "{")
	return err
}

func (w *jsonObjectWriter) end() error {
	_, err := io.WriteString(w.w, "}")
	return err
}

func (w *jsonObjectWriter) sliceField(name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if string(raw) == "null" || string(raw) == "[]" {
		return nil
	}
	return w.field(name, func(out io.Writer) error {
		_, err := out.Write(raw)
		return err
	})
}

func (w *jsonObjectWriter) stringField(name, value string) error {
	if value == "" {
		return nil
	}
	return w.valueField(name, value)
}

func (w *jsonObjectWriter) int64Field(name string, value int64) error {
	if value == 0 {
		return nil
	}
	return w.valueField(name, value)
}

func (w *jsonObjectWriter) base64Field(name string, body []byte) error {
	if len(body) == 0 {
		return nil
	}
	return w.field(name, func(out io.Writer) error {
		if _, err := io.WriteString(out, `"`); err != nil {
			return err
		}
		enc := base64.NewEncoder(base64.StdEncoding, out)
		if _, err := enc.Write(body); err != nil {
			_ = enc.Close()
			return err
		}
		if err := enc.Close(); err != nil {
			return err
		}
		_, err := io.WriteString(out, `"`)
		return err
	})
}

func (w *jsonObjectWriter) valueField(name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return w.field(name, func(out io.Writer) error {
		_, err := out.Write(raw)
		return err
	})
}

func (w *jsonObjectWriter) field(name string, writeValue func(io.Writer) error) error {
	if w.wroteField {
		if _, err := io.WriteString(w.w, ","); err != nil {
			return err
		}
	}
	key, err := json.Marshal(name)
	if err != nil {
		return err
	}
	if _, err := w.w.Write(key); err != nil {
		return err
	}
	if _, err := io.WriteString(w.w, ":"); err != nil {
		return err
	}
	if err := writeValue(w.w); err != nil {
		return err
	}
	w.wroteField = true
	return nil
}
