package web

import (
	"encoding/json"
	"fmt"
	"strings"

	"urgentry/internal/store"
)

// ---------------------------------------------------------------------------
// Rich stack trace view models
// ---------------------------------------------------------------------------

// exceptionGroup represents one exception in a chain with its stack trace.
type exceptionGroup struct {
	Type       string
	Value      string
	Module     string
	Mechanism  string // e.g. "generic", "onerror", "onunhandledrejection"
	Handled    string // "true", "false", or "" if unknown
	Frames     []richFrame
	HasFrames  bool
	InAppCount int
	LibCount   int
}

// richFrame extends stackFrame with richer metadata for the detail page.
type richFrame struct {
	File        string
	AbsPath     string
	Function    string
	Module      string
	LineNo      int
	ColNo       int
	InApp       bool
	MappedFrom  string
	SourceURL   string // clickable link to source code (populated by code mappings)
	ContextLine string
	PreContext   []contextLine
	PostContext  []contextLine
	HasContext   bool
	Collapsed   bool // true = collapsed by default (library frames)
}

// contextLine is a numbered line of source context.
type contextLine struct {
	Number  int
	Content string
}

// ---------------------------------------------------------------------------
// JSON wire types for parsing the normalized payload
// ---------------------------------------------------------------------------

type payloadEnvelope struct {
	Exception *struct {
		Values []payloadException `json:"values"`
	} `json:"exception"`
}

type payloadException struct {
	Type       string            `json:"type"`
	Value      string            `json:"value"`
	Module     string            `json:"module"`
	Mechanism  *payloadMechanism `json:"mechanism"`
	Stacktrace *struct {
		Frames []payloadFrame `json:"frames"`
	} `json:"stacktrace"`
}

type payloadMechanism struct {
	Type    string `json:"type"`
	Handled *bool  `json:"handled"`
}

type payloadFrame struct {
	Filename    string   `json:"filename"`
	Function    string   `json:"function"`
	Module      string   `json:"module"`
	Package     string   `json:"package"`
	Lineno      int      `json:"lineno"`
	Colno       int      `json:"colno"`
	AbsPath     string   `json:"abs_path"`
	ContextLine string   `json:"context_line"`
	PreContext  []string `json:"pre_context"`
	PostContext []string `json:"post_context"`
	InApp      *bool    `json:"in_app"`
}

// stackTraceFromPayload parses the event payload JSON and returns a list of
// exception groups, each containing its frames in most-recent-call-first order.
// Library frames are marked as collapsed by default; in-app frames are expanded.
func stackTraceFromPayload(payloadJSON []byte) []exceptionGroup {
	if len(payloadJSON) == 0 {
		return nil
	}
	var env payloadEnvelope
	if err := json.Unmarshal(payloadJSON, &env); err != nil || env.Exception == nil {
		return nil
	}

	groups := make([]exceptionGroup, 0, len(env.Exception.Values))
	for _, exc := range env.Exception.Values {
		eg := exceptionGroup{
			Type:   exc.Type,
			Value:  exc.Value,
			Module: exc.Module,
		}
		if exc.Mechanism != nil {
			eg.Mechanism = exc.Mechanism.Type
			if exc.Mechanism.Handled != nil {
				eg.Handled = fmt.Sprintf("%t", *exc.Mechanism.Handled)
			}
		}

		if exc.Stacktrace != nil && len(exc.Stacktrace.Frames) > 0 {
			// Reverse iterate: Sentry stores frames bottom-up, we display top-down.
			for i := len(exc.Stacktrace.Frames) - 1; i >= 0; i-- {
				pf := exc.Stacktrace.Frames[i]
				if pf.Filename == "" && pf.Function == "" {
					continue
				}

				inApp := pf.InApp != nil && *pf.InApp

				rf := richFrame{
					File:        pf.Filename,
					AbsPath:     pf.AbsPath,
					Function:    pf.Function,
					Module:      pf.Module,
					LineNo:      pf.Lineno,
					ColNo:       pf.Colno,
					InApp:       inApp,
					ContextLine: pf.ContextLine,
					Collapsed:   !inApp, // library frames collapsed by default
				}

				// Source-mapped annotation
				if pf.AbsPath != "" && pf.AbsPath != pf.Filename {
					rf.MappedFrom = fmt.Sprintf("mapped from %s", pf.AbsPath)
				}

				// Build context lines
				if pf.ContextLine != "" || len(pf.PreContext) > 0 || len(pf.PostContext) > 0 {
					rf.HasContext = true
					startLine := pf.Lineno - len(pf.PreContext)
					for pi, line := range pf.PreContext {
						rf.PreContext = append(rf.PreContext, contextLine{
							Number:  startLine + pi,
							Content: line,
						})
					}
					for pi, line := range pf.PostContext {
						rf.PostContext = append(rf.PostContext, contextLine{
							Number:  pf.Lineno + 1 + pi,
							Content: line,
						})
					}
				}

				if inApp {
					eg.InAppCount++
				} else {
					eg.LibCount++
				}
				eg.Frames = append(eg.Frames, rf)
			}
			eg.HasFrames = len(eg.Frames) > 0
		}

		groups = append(groups, eg)
	}
	return groups
}

// applyCodeMappings enriches each frame with a SourceURL when a code mapping
// matches the frame's filename. The mapping replaces the StackRoot prefix with
// the SourceRoot prefix and builds a full URL like:
//
//	{RepoURL}/blob/{DefaultBranch}/{SourceRoot}{rest}#L{lineNo}
func applyCodeMappings(groups []exceptionGroup, mappings []*store.CodeMapping) {
	if len(mappings) == 0 {
		return
	}
	for gi := range groups {
		for fi := range groups[gi].Frames {
			frame := &groups[gi].Frames[fi]
			filename := frame.File
			if filename == "" {
				continue
			}
			for _, m := range mappings {
				if !strings.HasPrefix(filename, m.StackRoot) {
					continue
				}
				rest := strings.TrimPrefix(filename, m.StackRoot)
				repoPath := m.SourceRoot + rest
				// Normalize double slashes
				repoPath = strings.ReplaceAll(repoPath, "//", "/")
				repoPath = strings.TrimPrefix(repoPath, "/")

				repoURL := strings.TrimSuffix(m.RepoURL, "/")
				branch := m.DefaultBranch
				if branch == "" {
					branch = "main"
				}

				url := fmt.Sprintf("%s/blob/%s/%s", repoURL, branch, repoPath)
				if frame.LineNo > 0 {
					url += fmt.Sprintf("#L%d", frame.LineNo)
				}
				frame.SourceURL = url
				break // first matching mapping wins
			}
		}
	}
}
