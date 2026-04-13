package proguard

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
)

// Resolver applies uploaded ProGuard mappings to obfuscated Java/Android frames.
type Resolver struct {
	Store Store

	mu    sync.Mutex
	cache map[string]*parsedMapping
}

type parsedMapping struct {
	classes map[string]*classMapping
}

type classMapping struct {
	original string
	obf      string
	methods  map[string][]methodMapping
}

type methodMapping struct {
	originalName string
	obfName      string
	lineStart    int
	lineEnd      int
}

// Resolve returns deobfuscated module/file/function data for a frame when a mapping exists.
func (r *Resolver) Resolve(ctx context.Context, projectID, release, module, function string, line int) (origModule, origFile, origFunc string, origLine int, err error) {
	if r == nil || r.Store == nil || projectID == "" || release == "" {
		return "", "", "", 0, nil
	}

	mappings, err := r.Store.ListByRelease(ctx, projectID, release)
	if err != nil || len(mappings) == 0 {
		return "", "", "", 0, err
	}

	for _, mapping := range mappings {
		parsed, err := r.loadParsedMapping(ctx, mapping.ID)
		if err != nil || parsed == nil {
			if err != nil {
				return "", "", "", 0, err
			}
			continue
		}

		class := parsed.classes[module]
		if class == nil {
			continue
		}

		origModule = class.original
		origFile = classFileName(class.original)
		origFunc = function
		origLine = line

		candidates := class.methods[function]
		if len(candidates) == 0 {
			return origModule, origFile, origFunc, origLine, nil
		}

		match := pickMethodMapping(candidates, line)
		if match == nil {
			return origModule, origFile, origFunc, origLine, nil
		}

		if match.originalName != "" {
			origFunc = match.originalName
		}
		return origModule, origFile, origFunc, origLine, nil
	}

	return "", "", "", 0, nil
}

func (r *Resolver) loadParsedMapping(ctx context.Context, mappingID string) (*parsedMapping, error) {
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]*parsedMapping{}
	}
	if parsed, ok := r.cache[mappingID]; ok {
		r.mu.Unlock()
		return parsed, nil
	}
	r.mu.Unlock()

	mapping, data, err := r.Store.GetMapping(ctx, mappingID)
	if err != nil || mapping == nil || len(data) == 0 {
		return nil, err
	}

	parsed := parseMapping(data)

	r.mu.Lock()
	r.cache[mappingID] = parsed
	r.mu.Unlock()

	return parsed, nil
}

func parseMapping(data []byte) *parsedMapping {
	parsed := &parsedMapping{classes: map[string]*classMapping{}}
	scanner := bufio.NewScanner(bytes.NewReader(data))

	var current *classMapping
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasSuffix(line, ":") && strings.Contains(line, " -> ") {
			original, obfuscated, ok := splitMappingLine(strings.TrimSuffix(line, ":"))
			if !ok {
				current = nil
				continue
			}
			current = &classMapping{
				original: original,
				obf:      obfuscated,
				methods:  map[string][]methodMapping{},
			}
			parsed.classes[obfuscated] = current
			continue
		}

		if current == nil || !strings.Contains(line, " -> ") || !strings.Contains(line, "(") {
			continue
		}

		mapping, ok := parseMethodLine(line)
		if !ok {
			continue
		}
		current.methods[mapping.obfName] = append(current.methods[mapping.obfName], mapping)
	}

	return parsed
}

func splitMappingLine(line string) (original, obfuscated string, ok bool) {
	left, right, ok := strings.Cut(line, " -> ")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(left), strings.TrimSpace(right), true
}

func parseMethodLine(line string) (methodMapping, bool) {
	var mapping methodMapping
	left, right, ok := strings.Cut(line, " -> ")
	if !ok {
		return mapping, false
	}
	mapping.obfName = strings.TrimSpace(right)

	signature := strings.TrimSpace(left)
	if parts := strings.SplitN(signature, ":", 3); len(parts) == 3 {
		mapping.lineStart = parsePositiveInt(parts[0])
		mapping.lineEnd = parsePositiveInt(parts[1])
		signature = parts[2]
	}

	open := strings.Index(signature, "(")
	if open < 0 {
		return mapping, false
	}
	head := strings.TrimSpace(signature[:open])
	fields := strings.Fields(head)
	if len(fields) == 0 {
		return mapping, false
	}
	mapping.originalName = fields[len(fields)-1]
	if mapping.obfName == "" {
		return mapping, false
	}
	return mapping, true
}

func pickMethodMapping(candidates []methodMapping, line int) *methodMapping {
	if len(candidates) == 0 {
		return nil
	}
	if line > 0 {
		for i := range candidates {
			candidate := &candidates[i]
			if candidate.lineStart == 0 && candidate.lineEnd == 0 {
				continue
			}
			if line >= candidate.lineStart && line <= candidate.lineEnd {
				return candidate
			}
		}
	}
	return &candidates[0]
}

func classFileName(module string) string {
	if module == "" {
		return ""
	}
	parts := strings.Split(module, ".")
	name := parts[len(parts)-1]
	if idx := strings.Index(name, "$"); idx >= 0 {
		name = name[:idx]
	}
	if name == "" {
		name = "Unknown"
	}
	return name + ".java"
}

func parsePositiveInt(value string) int {
	n := 0
	for _, ch := range strings.TrimSpace(value) {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = (n * 10) + int(ch-'0')
	}
	return n
}

func (r *Resolver) String() string {
	return fmt.Sprintf("proguard.Resolver(cache=%d)", len(r.cache))
}
