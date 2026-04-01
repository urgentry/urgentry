package config

import (
	"fmt"
	"runtime"
)

// Build-time variables injected via -ldflags.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// VersionInfo holds structured build metadata.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
}

// GetVersionInfo returns the current build metadata.
func GetVersionInfo() VersionInfo {
	return VersionInfo{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
	}
}

// String returns a human-readable multi-line version block.
func (v VersionInfo) String() string {
	return fmt.Sprintf("urgentry %s\ncommit:     %s\nbuilt:      %s\ngo:         %s",
		v.Version, v.Commit, v.BuildDate, v.GoVersion)
}
