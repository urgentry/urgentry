package dsn

import (
	"fmt"
	"hash/crc32"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type DSN struct {
	Scheme    string
	Host      string
	ProjectID string
	PublicKey string
	SecretKey string
}

func Parse(raw string) (DSN, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return DSN{}, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return DSN{}, fmt.Errorf("dsn must include scheme and host")
	}
	projectID := path.Base(strings.TrimSuffix(u.Path, "/"))
	if projectID == "." || projectID == "/" || projectID == "" {
		return DSN{}, fmt.Errorf("dsn must include project id in path")
	}
	if u.User == nil || u.User.Username() == "" {
		return DSN{}, fmt.Errorf("dsn must include public key")
	}
	secret, _ := u.User.Password()
	return DSN{
		Scheme:    u.Scheme,
		Host:      u.Host,
		ProjectID: projectID,
		PublicKey: u.User.Username(),
		SecretKey: secret,
	}, nil
}

// PublicProjectID returns the SDK-facing project ID used in DSN URLs.
// Sentry JavaScript SDKs validate the DSN project ID as digits-only, while
// Urgentry keeps human-readable or random string project IDs internally.
func PublicProjectID(projectID string) string {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ""
	}
	if isNumericProjectID(projectID) {
		return projectID
	}
	sum := crc32.ChecksumIEEE([]byte(projectID))
	if sum == 0 {
		sum = 1
	}
	return strconv.FormatUint(uint64(sum), 10)
}

// ProjectIDMatches reports whether requested is either the canonical internal
// project ID or its SDK-facing numeric DSN ID.
func ProjectIDMatches(canonical, requested string) bool {
	canonical = strings.TrimSpace(canonical)
	requested = strings.TrimSpace(requested)
	return canonical != "" && requested != "" && (requested == canonical || requested == PublicProjectID(canonical))
}

func isNumericProjectID(projectID string) bool {
	for _, r := range projectID {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
