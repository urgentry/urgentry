package dsn

import (
	"fmt"
	"net/url"
	"path"
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
