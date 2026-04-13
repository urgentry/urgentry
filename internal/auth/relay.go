package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/httputil"
)

const (
	relayIDHeader        = "X-Urgentry-Relay-Id"
	relayTimestampHeader = "X-Urgentry-Relay-Timestamp"
	relaySignatureHeader = "X-Urgentry-Relay-Signature"

	relayReceivedAtHeader   = "X-Urgentry-Relay-Received-At"
	relayQueueLatencyHeader = "X-Urgentry-Relay-Queue-Latency-Ms"
	relayReplayWindow       = 5 * time.Minute
	trustedRelayCredential  = "relay"
)

type TrustedRelay struct {
	OrganizationID   string
	OrganizationSlug string
	RelayID          string
	Secret           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type TrustedRelayStore interface {
	GetTrustedRelay(ctx context.Context, relayID string) (*TrustedRelay, error)
}

type TrustedRelayAuditRecord struct {
	OrganizationID string
	RelayID        string
	Action         string
	RequestPath    string
	RequestMethod  string
	IPAddress      string
	UserAgent      string
}

type TrustedRelayAuditWriter interface {
	RecordTrustedRelayDecision(ctx context.Context, record TrustedRelayAuditRecord) error
}

type trustedRelayContextKey struct{}

type TrustedRelayContext struct {
	OrganizationID   string
	OrganizationSlug string
	RelayID          string
	OriginalClientIP string
	OriginalProto    string
	ReceivedAt       string
	QueueLatencyMS   string
}

func WithTrustedRelay(ctx context.Context, relay TrustedRelayContext) context.Context {
	return context.WithValue(ctx, trustedRelayContextKey{}, relay)
}

func TrustedRelayFromContext(ctx context.Context) (TrustedRelayContext, bool) {
	relay, ok := ctx.Value(trustedRelayContextKey{}).(TrustedRelayContext)
	return relay, ok
}

type MemoryTrustedRelayStore struct {
	relays map[string]*TrustedRelay
}

func NewMemoryTrustedRelayStore() *MemoryTrustedRelayStore {
	return &MemoryTrustedRelayStore{relays: make(map[string]*TrustedRelay)}
}

func (s *MemoryTrustedRelayStore) UpsertTrustedRelay(_ context.Context, relay *TrustedRelay) error {
	if s == nil || relay == nil || strings.TrimSpace(relay.RelayID) == "" {
		return fmt.Errorf("relay id is required")
	}
	relayCopy := *relay
	if relayCopy.CreatedAt.IsZero() {
		relayCopy.CreatedAt = time.Now().UTC()
	}
	relayCopy.UpdatedAt = time.Now().UTC()
	s.relays[relayCopy.RelayID] = &relayCopy
	return nil
}

func (s *MemoryTrustedRelayStore) GetTrustedRelay(_ context.Context, relayID string) (*TrustedRelay, error) {
	if s == nil {
		return nil, nil
	}
	relay, ok := s.relays[strings.TrimSpace(relayID)]
	if !ok {
		return nil, nil
	}
	relayCopy := *relay
	return &relayCopy, nil
}

func ComputeTrustedRelaySignature(method, path string, body []byte, relayID, timestamp, secret string) string {
	bodySum := sha256.Sum256(body)
	canonical := strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(method)),
		strings.TrimSpace(path),
		hex.EncodeToString(bodySum[:]),
		strings.TrimSpace(relayID),
		strings.TrimSpace(timestamp),
	}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func RelayMiddleware(store TrustedRelayStore, audits TrustedRelayAuditWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			relayID := strings.TrimSpace(r.Header.Get(relayIDHeader))
			timestamp := strings.TrimSpace(r.Header.Get(relayTimestampHeader))
			signature := strings.TrimSpace(r.Header.Get(relaySignatureHeader))
			if relayID == "" && timestamp == "" && signature == "" {
				next.ServeHTTP(w, r)
				return
			}
			if relayID == "" || timestamp == "" || signature == "" {
				recordTrustedRelayAudit(r, audits, "", relayID, "relay.denied")
				httputil.WriteError(w, http.StatusUnauthorized, "Trusted relay headers are incomplete")
				return
			}
			if store == nil {
				recordTrustedRelayAudit(r, audits, "", relayID, "relay.denied")
				httputil.WriteError(w, http.StatusUnauthorized, "Trusted relay is not configured")
				return
			}

			relay, err := store.GetTrustedRelay(r.Context(), relayID)
			if err != nil || relay == nil {
				recordTrustedRelayAudit(r, audits, "", relayID, "relay.denied")
				httputil.WriteError(w, http.StatusUnauthorized, "Trusted relay is unknown")
				return
			}

			relayTime, err := time.Parse(time.RFC3339, timestamp)
			if err != nil || absDuration(time.Since(relayTime)) > relayReplayWindow {
				recordTrustedRelayAudit(r, audits, relay.OrganizationID, relayID, "relay.denied")
				httputil.WriteError(w, http.StatusUnauthorized, "Trusted relay timestamp is invalid")
				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				recordTrustedRelayAudit(r, audits, relay.OrganizationID, relayID, "relay.denied")
				httputil.WriteError(w, http.StatusBadRequest, "Failed to read trusted relay body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			expected := ComputeTrustedRelaySignature(r.Method, r.URL.Path, body, relayID, timestamp, relay.Secret)
			if !hmac.Equal([]byte(strings.ToLower(signature)), []byte(expected)) {
				recordTrustedRelayAudit(r, audits, relay.OrganizationID, relayID, "relay.denied")
				httputil.WriteError(w, http.StatusUnauthorized, "Trusted relay signature is invalid")
				return
			}

			recordTrustedRelayAudit(r, audits, relay.OrganizationID, relayID, "relay.allowed")
			ctx := WithTrustedRelay(r.Context(), TrustedRelayContext{
				OrganizationID:   relay.OrganizationID,
				OrganizationSlug: relay.OrganizationSlug,
				RelayID:          relay.RelayID,
				OriginalClientIP: strings.TrimSpace(strings.Split(strings.TrimSpace(r.Header.Get("X-Forwarded-For")), ",")[0]),
				OriginalProto:    strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")),
				ReceivedAt:       strings.TrimSpace(r.Header.Get(relayReceivedAtHeader)),
				QueueLatencyMS:   strings.TrimSpace(r.Header.Get(relayQueueLatencyHeader)),
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func recordTrustedRelayAudit(r *http.Request, audits TrustedRelayAuditWriter, orgID, relayID, action string) {
	if audits == nil || strings.TrimSpace(action) == "" {
		return
	}
	_ = audits.RecordTrustedRelayDecision(r.Context(), TrustedRelayAuditRecord{
		OrganizationID: strings.TrimSpace(orgID),
		RelayID:        strings.TrimSpace(relayID),
		Action:         action,
		RequestPath:    r.URL.Path,
		RequestMethod:  r.Method,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	})
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
