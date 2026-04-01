package sqlite

import "urgentry/pkg/id"

// generateID produces a random 32-hex-char identifier.
func generateID() string {
	return id.New()
}
