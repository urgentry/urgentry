package integration

import (
	"bufio"
	"strings"
)

// CodeownersEntry represents a single line from a CODEOWNERS file:
// a glob pattern and the owners (users or teams) responsible for matching
// paths.
type CodeownersEntry struct {
	Pattern string   `json:"pattern"`
	Owners  []string `json:"owners"` // e.g. ["@backend-team", "user@example.com"]
}

// ParseCodeowners parses the contents of a GitHub/GitLab CODEOWNERS file
// and returns the list of entries in file order. Blank lines and comments
// (lines starting with #) are skipped.
func ParseCodeowners(content string) []CodeownersEntry {
	var entries []CodeownersEntry
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue // pattern with no owners
		}
		entries = append(entries, CodeownersEntry{
			Pattern: fields[0],
			Owners:  fields[1:],
		})
	}
	return entries
}

// OwnershipRule is the normalized form consumed by the ownership store.
// Each entry maps a glob pattern to an assignee string.
type OwnershipRule struct {
	Pattern  string `json:"pattern"`
	Assignee string `json:"assignee"`
}

// CodeownersToOwnershipRules converts parsed CODEOWNERS entries into
// ownership rules suitable for the ownership store. Each owner in an
// entry produces a separate rule so that assignment can be evaluated
// per-owner.
func CodeownersToOwnershipRules(entries []CodeownersEntry) []OwnershipRule {
	var rules []OwnershipRule
	for _, entry := range entries {
		pattern := codeownersGlobToOwnershipPattern(entry.Pattern)
		for _, owner := range entry.Owners {
			rules = append(rules, OwnershipRule{
				Pattern:  pattern,
				Assignee: normalizeOwner(owner),
			})
		}
	}
	return rules
}

// codeownersGlobToOwnershipPattern converts a CODEOWNERS glob pattern to
// the "path:" prefix format used by the ownership store.
//
// CODEOWNERS patterns:
//   - *.js           -> matches any .js file anywhere
//   - /docs/         -> matches the docs directory at root
//   - apps/          -> matches the apps directory anywhere
//   - src/**/*.go    -> matches .go files recursively under src/
//
// The ownership store uses "path:<pattern>" for file-path matching.
func codeownersGlobToOwnershipPattern(pattern string) string {
	// Strip leading slash — CODEOWNERS uses it to anchor to repo root,
	// but the ownership store matches against in-app paths which are
	// already repo-relative.
	pattern = strings.TrimPrefix(pattern, "/")

	// Remove trailing slash (directory markers).
	pattern = strings.TrimSuffix(pattern, "/")

	return "path:" + pattern
}

// normalizeOwner strips a leading '@' if present and returns the owner
// identifier as-is otherwise. GitHub CODEOWNERS uses @user and @org/team
// syntax; the ownership store stores plain identifiers.
func normalizeOwner(owner string) string {
	owner = strings.TrimSpace(owner)
	if strings.HasPrefix(owner, "@") {
		return owner[1:]
	}
	return owner
}

// SyncCodeownersToStore is a helper that the GitHub integration sync flow
// can call to import a CODEOWNERS file into the ownership store. It parses
// the file content, converts entries to ownership rules, and returns them
// for the caller to persist.
func SyncCodeownersToStore(content string) []OwnershipRule {
	entries := ParseCodeowners(content)
	return CodeownersToOwnershipRules(entries)
}

// MatchCodeownersPath checks whether a file path matches a CODEOWNERS glob
// pattern. This uses simplified matching rules:
//   - "*" matches any sequence of non-separator characters
//   - "**" matches everything including separators
//   - Direct prefix/suffix comparison for directory patterns
func MatchCodeownersPath(pattern, path string) bool {
	pattern = strings.TrimPrefix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")
	path = strings.TrimPrefix(path, "/")

	// Exact match.
	if pattern == path {
		return true
	}

	// Directory pattern: "docs" matches "docs/anything".
	if !strings.Contains(pattern, "*") {
		return strings.HasPrefix(path, pattern+"/") || path == pattern
	}

	// ** glob: recursive match.
	if strings.Contains(pattern, "**") {
		return matchDoubleGlob(pattern, path)
	}

	// Single * glob: match within a single directory level.
	return matchSingleGlob(pattern, path)
}

// matchDoubleGlob handles "**" recursive glob matching.
func matchDoubleGlob(pattern, path string) bool {
	// Split on "**" and match prefix + suffix.
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimSuffix(parts[0], "/")
	suffix := strings.TrimPrefix(parts[1], "/")

	if prefix != "" && !strings.HasPrefix(path, prefix+"/") && path != prefix {
		return false
	}

	if suffix == "" {
		return true
	}

	// The suffix may itself contain a simple glob.
	remaining := path
	if prefix != "" {
		remaining = strings.TrimPrefix(path, prefix+"/")
	}

	// Check if any suffix of remaining matches the suffix pattern.
	segments := strings.Split(remaining, "/")
	for i := range segments {
		candidate := strings.Join(segments[i:], "/")
		if matchSingleGlob(suffix, candidate) {
			return true
		}
	}
	return false
}

// matchSingleGlob handles "*" wildcard matching (non-recursive).
func matchSingleGlob(pattern, path string) bool {
	// Simple case: "*.ext" matches any file ending with .ext.
	if strings.HasPrefix(pattern, "*.") {
		ext := pattern[1:] // ".ext"
		return strings.HasSuffix(path, ext)
	}

	// Split pattern by "*" and check if parts appear in order.
	parts := strings.Split(pattern, "*")
	remaining := path
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(remaining, part)
		if idx < 0 {
			return false
		}
		if i == 0 && idx != 0 {
			// First part must match at start.
			return false
		}
		remaining = remaining[idx+len(part):]
	}
	// If the last part is non-empty, remaining must be exhausted.
	if last := parts[len(parts)-1]; last != "" {
		return remaining == ""
	}
	return true
}
