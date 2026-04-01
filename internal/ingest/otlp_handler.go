package ingest

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"

	"urgentry/internal/metrics"
	"urgentry/internal/pipeline"
	"urgentry/internal/trace"
)

// gzipReaderPool reuses gzip.Reader instances to avoid repeated allocation
// of internal decompression buffers (~54 KB) on each OTLP request.
var gzipReaderPool sync.Pool

const maxOTLPBodySize = 5 << 20 // 5 MB

const (
	otlpContentTypeJSON     = "application/json"
	otlpContentTypeProtobuf = "application/x-protobuf"
)

type otlpTraceExportResponse struct{}

type otlpStatusResponse struct {
	Message string `json:"message,omitempty"`
}

// OTLPTracesHandler handles OTLP/HTTP JSON traces for a project.
func OTLPTracesHandler(pipe *pipeline.Pipeline, met *metrics.Metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := otlpResponseContentType(r.Header.Get("Content-Type"))
		if r.Method != http.MethodPost {
			writeOTLPStatus(w, contentType, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if contentType != otlpContentTypeJSON && contentType != otlpContentTypeProtobuf {
			writeOTLPStatus(w, otlpContentTypeJSON, http.StatusUnsupportedMediaType, "unsupported otlp content type")
			return
		}
		if contentType == otlpContentTypeProtobuf {
			writeOTLPStatus(w, contentType, http.StatusUnsupportedMediaType, "binary otlp traces are not supported")
			return
		}
		payload, err := readOTLPBody(w, r)
		if err != nil {
			if met != nil {
				met.RecordIngest(0, err)
			}
			writeOTLPStatus(w, contentType, http.StatusBadRequest, err.Error())
			return
		}
		items, err := trace.TranslateOTLPJSON(payload)
		if err != nil {
			if met != nil {
				met.RecordIngest(len(payload), err)
			}
			writeOTLPStatus(w, contentType, http.StatusBadRequest, "invalid otlp trace payload")
			return
		}
		projectID := r.PathValue("project_id")
		if projectID == "" {
			projectID = "1"
		}
		for _, item := range items {
			if pipe == nil || !pipe.EnqueueNonBlocking(pipeline.Item{ProjectID: projectID, RawEvent: item}) {
				if met != nil {
					met.RecordIngest(len(payload), errQueueFull)
				}
				w.Header().Set("Retry-After", "1")
				writeOTLPStatus(w, contentType, http.StatusServiceUnavailable, "ingest queue is full, retry later")
				return
			}
		}
		if met != nil {
			met.RecordIngest(len(payload), nil)
		}
		writeOTLPJSON(w, contentType, http.StatusOK, otlpTraceExportResponse{})
	})
}

func readOTLPBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	reader := io.Reader(http.MaxBytesReader(w, r.Body, maxOTLPBodySize))
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		var gz *gzip.Reader
		if v, ok := gzipReaderPool.Get().(*gzip.Reader); ok {
			if err := v.Reset(reader); err != nil {
				gzipReaderPool.Put(v)
				return nil, err
			}
			gz = v
		} else {
			var err error
			gz, err = gzip.NewReader(reader)
			if err != nil {
				return nil, err
			}
		}
		defer func() {
			gz.Close()
			gzipReaderPool.Put(gz)
		}()
		reader = gz
	default:
		return nil, errors.New("unsupported otlp content encoding")
	}
	return io.ReadAll(reader)
}

func otlpResponseContentType(raw string) string {
	if raw == "" {
		return otlpContentTypeJSON
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return otlpContentTypeJSON
	}
	switch mediaType {
	case otlpContentTypeJSON, otlpContentTypeProtobuf:
		return mediaType
	default:
		return mediaType
	}
}

func writeOTLPStatus(w http.ResponseWriter, contentType string, status int, message string) {
	writeOTLP(w, contentType, status, otlpStatusResponse{Message: message}, message)
}

func writeOTLPJSON(w http.ResponseWriter, contentType string, status int, payload any) {
	writeOTLP(w, contentType, status, payload, "")
}

func writeOTLP(w http.ResponseWriter, contentType string, status int, payload any, message string) {
	if contentType == otlpContentTypeProtobuf {
		w.Header().Set("Content-Type", otlpContentTypeProtobuf)
		w.WriteHeader(status)
		if message != "" {
			_, _ = w.Write(encodeProtoStatus(message))
		}
		return
	}
	w.Header().Set("Content-Type", otlpContentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func encodeProtoStatus(message string) []byte {
	if message == "" {
		return nil
	}
	buf := make([]byte, 0, len(message)+4)
	buf = append(buf, 0x12)
	buf = binary.AppendUvarint(buf, uint64(len(message)))
	buf = append(buf, message...)
	return buf
}
