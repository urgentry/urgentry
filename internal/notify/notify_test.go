package notify

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"urgentry/internal/alert"
)

type memoryOutbox struct {
	records []*EmailNotification
}

func (m *memoryOutbox) RecordEmail(_ context.Context, notification *EmailNotification) error {
	m.records = append(m.records, notification)
	return nil
}

type memoryDeliveries struct {
	records []*DeliveryRecord
	err     error
}

func (m *memoryDeliveries) RecordDelivery(_ context.Context, delivery *DeliveryRecord) error {
	m.records = append(m.records, delivery)
	return m.err
}

func TestNotifyEmailRecordsOutbox(t *testing.T) {
	outbox := &memoryOutbox{}
	deliveries := &memoryDeliveries{}
	n := NewNotifier(outbox, deliveries)

	err := n.NotifyEmail(context.Background(), "proj-1", "dev@example.com", alert.TriggerEvent{
		RuleID:    "rule-1",
		GroupID:   "grp-1",
		EventID:   "evt-1",
		Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyEmail: %v", err)
	}
	if len(outbox.records) != 1 {
		t.Fatalf("expected 1 outbox record, got %d", len(outbox.records))
	}
	got := outbox.records[0]
	if got.ProjectID != "proj-1" || got.Recipient != "dev@example.com" {
		t.Fatalf("unexpected record: %+v", got)
	}
	if got.Status != DeliveryStatusQueued || got.Transport != "tiny-outbox" {
		t.Fatalf("unexpected status/transport: %+v", got)
	}
	if !strings.Contains(got.Body, "Rule: rule-1") {
		t.Fatalf("unexpected email body: %s", got.Body)
	}
	if len(deliveries.records) != 1 || deliveries.records[0].Kind != DeliveryKindEmail {
		t.Fatalf("unexpected delivery records: %+v", deliveries.records)
	}
}

func TestNotifyEmailRequiresOutbox(t *testing.T) {
	n := NewNotifier(nil, nil)
	if err := n.NotifyEmail(context.Background(), "proj-1", "dev@example.com", alert.TriggerEvent{}); err == nil {
		t.Fatal("expected error when no outbox is configured")
	}
}

func TestNotifySlackRecordsDelivery(t *testing.T) {
	deliveries := &memoryDeliveries{}
	n := NewNotifier(nil, deliveries)
	n.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	})}

	err := n.NotifySlack(context.Background(), "proj-1", "https://hooks.slack.test/services/T000/B000/XYZ", alert.TriggerEvent{
		RuleID:      "rule-1",
		EventID:     "evt-1",
		EventType:   alert.EventTypeTransaction,
		Transaction: "GET /checkout",
		DurationMS:  812,
		Timestamp:   time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifySlack: %v", err)
	}
	if len(deliveries.records) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(deliveries.records))
	}
	got := deliveries.records[0]
	if got.Kind != DeliveryKindSlack || got.Status != DeliveryStatusDelivered {
		t.Fatalf("unexpected delivery: %+v", got)
	}
}

func TestNotifyWebhookIncludesProfileContext(t *testing.T) {
	deliveries := &memoryDeliveries{}
	var payload map[string]any
	n := NewNotifier(nil, deliveries)
	n.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       io.NopCloser(strings.NewReader("accepted")),
			Header:     make(http.Header),
		}, nil
	})}

	err := n.NotifyWebhook(context.Background(), "proj-1", "https://hooks.example.test/alerts", alert.TriggerEvent{
		RuleID:      "rule-1",
		EventID:     "evt-1",
		EventType:   alert.EventTypeTransaction,
		Transaction: "GET /checkout",
		TraceID:     "trace-1",
		DurationMS:  812,
		Profile: &alert.ProfileContext{
			ProfileID:   "profile-1",
			URL:         "/profiles/profile-1/",
			TopFunction: "dbQuery",
			SampleCount: 9,
		},
		Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyWebhook: %v", err)
	}
	profile, ok := payload["profile"].(map[string]any)
	if !ok || profile["profileId"] != "profile-1" || profile["topFunction"] != "dbQuery" {
		t.Fatalf("unexpected webhook payload: %+v", payload)
	}
	if len(deliveries.records) != 1 || !strings.Contains(deliveries.records[0].PayloadJSON, "\"profileId\":\"profile-1\"") {
		t.Fatalf("unexpected delivery payload: %+v", deliveries.records)
	}
}

func TestNotifyWebhookSucceedsWhenDeliveryRecorderFails(t *testing.T) {
	deliveries := &memoryDeliveries{err: errors.New("delivery sink offline")}
	n := NewNotifier(nil, deliveries)
	n.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       io.NopCloser(strings.NewReader("accepted")),
			Header:     make(http.Header),
		}, nil
	})}

	err := n.NotifyWebhook(context.Background(), "proj-1", "https://hooks.example.test/alerts", alert.TriggerEvent{
		RuleID:    "rule-1",
		EventID:   "evt-1",
		Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyWebhook: %v", err)
	}
	if len(deliveries.records) != 1 || deliveries.records[0].Status != DeliveryStatusDelivered {
		t.Fatalf("unexpected delivery records: %+v", deliveries.records)
	}
}

func TestNotifyWebhookRejectsPrivateTarget(t *testing.T) {
	deliveries := &memoryDeliveries{}
	n := NewNotifier(nil, deliveries)
	n.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.String())
		return nil, nil
	})}

	err := n.NotifyWebhook(context.Background(), "proj-1", "http://127.0.0.1/hook", alert.TriggerEvent{
		RuleID:    "rule-1",
		EventID:   "evt-1",
		Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err == nil || !strings.Contains(err.Error(), "refusing private or local target") {
		t.Fatalf("NotifyWebhook error = %v, want private-target rejection", err)
	}
	if len(deliveries.records) != 1 || deliveries.records[0].Status != DeliveryStatusFailed {
		t.Fatalf("unexpected delivery records: %+v", deliveries.records)
	}
}

func TestNotifyEmailSucceedsWhenDeliveryRecorderFails(t *testing.T) {
	outbox := &memoryOutbox{}
	deliveries := &memoryDeliveries{err: errors.New("delivery sink offline")}
	n := NewNotifier(outbox, deliveries)

	err := n.NotifyEmail(context.Background(), "proj-1", "dev@example.com", alert.TriggerEvent{
		RuleID:    "rule-1",
		EventID:   "evt-1",
		Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyEmail: %v", err)
	}
	if len(outbox.records) != 1 {
		t.Fatalf("expected 1 outbox record, got %d", len(outbox.records))
	}
	if len(deliveries.records) != 1 || deliveries.records[0].Kind != DeliveryKindEmail {
		t.Fatalf("unexpected delivery records: %+v", deliveries.records)
	}
}

func TestNotifyEmailSMTPDelivery(t *testing.T) {
	outbox := &memoryOutbox{}
	deliveries := &memoryDeliveries{}
	smtpServer := newSMTPTestServer(t, smtpTestServerOptions{
		advertiseAuth:       true,
		expectedAuthPayload: "\x00smtp-user\x00smtp-pass",
	})

	n := NewNotifier(outbox, deliveries)
	n.SMTP = SMTPConfig{
		Host: smtpServer.host,
		Port: smtpServer.port,
		From: "alerts@example.com",
		User: "smtp-user",
		Pass: "smtp-pass",
	}

	err := n.NotifyEmail(context.Background(), "proj-1", "dev@example.com", alert.TriggerEvent{
		RuleID:      "rule-1",
		EventID:     "evt-1",
		EventType:   alert.EventTypeTransaction,
		Transaction: "Checkout\r\nX-Evil: 1",
		Timestamp:   time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyEmail: %v", err)
	}
	smtpServer.wait(t)

	headers, _, found := strings.Cut(smtpServer.message, "\r\n\r\n")
	if !found {
		t.Fatalf("expected SMTP message headers, got %q", smtpServer.message)
	}
	if !strings.Contains(headers, "From: alerts@example.com") {
		t.Fatalf("missing From header in %q", headers)
	}
	if !strings.Contains(headers, "To: dev@example.com") {
		t.Fatalf("missing To header in %q", headers)
	}
	if !strings.Contains(headers, "Subject: [Urgentry Alert] Slow transaction CheckoutX-Evil: 1") {
		t.Fatalf("missing sanitized Subject header in %q", headers)
	}
	if strings.Contains(headers, "\r\nX-Evil: 1") {
		t.Fatalf("unexpected injected header in %q", headers)
	}
	if len(outbox.records) != 1 || outbox.records[0].Transport != "smtp" || outbox.records[0].Status != DeliveryStatusDelivered {
		t.Fatalf("unexpected outbox records: %+v", outbox.records)
	}
	if len(deliveries.records) != 1 || deliveries.records[0].Status != DeliveryStatusDelivered {
		t.Fatalf("unexpected delivery records: %+v", deliveries.records)
	}
}

func TestNotifyEmailSMTPFailureRecordsError(t *testing.T) {
	outbox := &memoryOutbox{}
	deliveries := &memoryDeliveries{}
	smtpServer := newSMTPTestServer(t, smtpTestServerOptions{failCommand: "DATA"})

	n := NewNotifier(outbox, deliveries)
	n.SMTP = SMTPConfig{
		Host: smtpServer.host,
		Port: smtpServer.port,
		From: "alerts@example.com",
	}

	err := n.NotifyEmail(context.Background(), "proj-1", "dev@example.com", alert.TriggerEvent{
		RuleID:      "rule-1",
		EventID:     "evt-1",
		EventType:   alert.EventTypeMonitor,
		MonitorSlug: "nightly-import",
		Status:      "missed",
		Timestamp:   time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected SMTP failure")
	}
	smtpServer.wait(t)

	if len(outbox.records) != 1 || outbox.records[0].Status != DeliveryStatusFailed || outbox.records[0].Error == "" {
		t.Fatalf("unexpected outbox records: %+v", outbox.records)
	}
	if len(deliveries.records) != 1 || deliveries.records[0].Status != DeliveryStatusFailed || deliveries.records[0].Error == "" {
		t.Fatalf("unexpected delivery records: %+v", deliveries.records)
	}
}

type smtpTestServerOptions struct {
	advertiseAuth       bool
	expectedAuthPayload string
	failCommand         string
}

type smtpTestServer struct {
	listener net.Listener
	host     string
	port     string
	message  string
	err      error
	done     chan struct{}
}

func newSMTPTestServer(t *testing.T, opts smtpTestServerOptions) *smtpTestServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen smtp server: %v", err)
	}
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split smtp address: %v", err)
	}
	server := &smtpTestServer{
		listener: listener,
		host:     host,
		port:     port,
		done:     make(chan struct{}),
	}
	go func() {
		defer close(server.done)
		conn, err := listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				server.err = err
			}
			return
		}
		defer conn.Close()
		server.err = serveSMTPConn(conn, opts, &server.message)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-server.done
	})
	return server
}

func (s *smtpTestServer) wait(t *testing.T) {
	t.Helper()
	_ = s.listener.Close()
	<-s.done
	if s.err != nil {
		t.Fatalf("smtp server: %v", s.err)
	}
}

func serveSMTPConn(conn net.Conn, opts smtpTestServerOptions, message *string) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	write := func(line string) error {
		if _, err := writer.WriteString(line); err != nil {
			return err
		}
		return writer.Flush()
	}
	readLine := func() (string, error) {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	if err := write("220 smtp.test ESMTP\r\n"); err != nil {
		return err
	}
	line, err := readLine()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "EHLO ") && !strings.HasPrefix(line, "HELO ") {
		return fmt.Errorf("unexpected greeting %q", line)
	}
	if opts.advertiseAuth {
		if err := write("250-smtp.test\r\n250-AUTH PLAIN\r\n250 OK\r\n"); err != nil {
			return err
		}
		line, err = readLine()
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, "AUTH PLAIN ") {
			return fmt.Errorf("unexpected auth command %q", line)
		}
		payload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(line, "AUTH PLAIN ")))
		if err != nil {
			return fmt.Errorf("decode auth payload: %w", err)
		}
		if string(payload) != opts.expectedAuthPayload {
			return fmt.Errorf("auth payload = %q, want %q", payload, opts.expectedAuthPayload)
		}
		if err := write("235 2.7.0 Authentication successful\r\n"); err != nil {
			return err
		}
	} else if err := write("250 OK\r\n"); err != nil {
		return err
	}

	line, err = readLine()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "MAIL FROM:") {
		return fmt.Errorf("unexpected MAIL command %q", line)
	}
	if opts.failCommand == "MAIL" {
		return write("550 sender rejected\r\n")
	}
	if err := write("250 OK\r\n"); err != nil {
		return err
	}

	line, err = readLine()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "RCPT TO:") {
		return fmt.Errorf("unexpected RCPT command %q", line)
	}
	if opts.failCommand == "RCPT" {
		return write("550 mailbox unavailable\r\n")
	}
	if err := write("250 OK\r\n"); err != nil {
		return err
	}

	line, err = readLine()
	if err != nil {
		return err
	}
	if line != "DATA" {
		return fmt.Errorf("unexpected DATA command %q", line)
	}
	if opts.failCommand == "DATA" {
		return write("554 transaction failed\r\n")
	}
	if err := write("354 End data with <CR><LF>.<CR><LF>\r\n"); err != nil {
		return err
	}

	var data strings.Builder
	for {
		part, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if part == ".\r\n" {
			break
		}
		data.WriteString(part)
	}
	*message = data.String()
	if err := write("250 OK\r\n"); err != nil {
		return err
	}

	line, err = readLine()
	if err != nil {
		return err
	}
	if line != "QUIT" {
		return fmt.Errorf("unexpected quit command %q", line)
	}
	return write("221 Bye\r\n")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
