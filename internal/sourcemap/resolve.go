package sourcemap

import (
	"encoding/json"
	"fmt"
)

// ResolvedFrame holds the result of resolving a minified source location.
type ResolvedFrame struct {
	OrigFile string
	OrigLine int
	OrigFunc string
}

// sourceMap is a minimal representation of the Source Map V3 JSON.
type sourceMap struct {
	Version  int      `json:"version"`
	Sources  []string `json:"sources"`
	Names    []string `json:"names"`
	Mappings string   `json:"mappings"`
}

// mapping represents a single decoded VLQ segment.
type mapping struct {
	genLine    int
	genCol     int
	srcIndex   int
	srcLine    int
	srcCol     int
	nameIndex  int
	hasSource  bool
	hasName    bool
}

// Resolve takes raw source map JSON and a generated line:col, returning the
// original file, line, and function name. Line and col are 1-based.
// This is a minimal implementation — it decodes VLQ mappings and finds the
// closest match. It does not handle all edge cases of the spec.
func Resolve(sourceMapData []byte, line, col int) (origFile string, origLine int, origFunc string, err error) {
	var sm sourceMap
	if err := json.Unmarshal(sourceMapData, &sm); err != nil {
		return "", 0, "", fmt.Errorf("parse source map: %w", err)
	}
	if sm.Version != 3 {
		return "", 0, "", fmt.Errorf("unsupported source map version: %d", sm.Version)
	}

	mappings, err := decodeMappings(sm.Mappings)
	if err != nil {
		return "", 0, "", fmt.Errorf("decode mappings: %w", err)
	}

	// Find the best matching mapping for the given line:col (1-based input).
	// Convert to 0-based for matching.
	targetLine := line - 1
	targetCol := col - 1
	if targetLine < 0 {
		targetLine = 0
	}
	if targetCol < 0 {
		targetCol = 0
	}

	var best *mapping
	for i := range mappings {
		m := &mappings[i]
		if !m.hasSource {
			continue
		}
		if m.genLine == targetLine {
			if best == nil || m.genCol <= targetCol {
				// Pick the segment whose genCol is closest to (but not exceeding) targetCol.
				if best == nil || best.genLine != targetLine || m.genCol > best.genCol {
					best = m
				}
			}
		} else if m.genLine < targetLine {
			// If no exact line match, take the last segment before the target line.
			if best == nil || m.genLine > best.genLine || (m.genLine == best.genLine && m.genCol > best.genCol) {
				best = m
			}
		}
	}

	if best == nil {
		return "", 0, "", fmt.Errorf("no mapping found for %d:%d", line, col)
	}

	if best.srcIndex >= 0 && best.srcIndex < len(sm.Sources) {
		origFile = sm.Sources[best.srcIndex]
	}
	origLine = best.srcLine + 1 // convert back to 1-based

	if best.hasName && best.nameIndex >= 0 && best.nameIndex < len(sm.Names) {
		origFunc = sm.Names[best.nameIndex]
	}

	return origFile, origLine, origFunc, nil
}

// decodeMappings parses a VLQ-encoded mappings string into a flat list.
func decodeMappings(encoded string) ([]mapping, error) {
	var result []mapping
	line := 0
	col := 0
	srcIndex := 0
	srcLine := 0
	srcCol := 0
	nameIndex := 0

	i := 0
	for i < len(encoded) {
		if encoded[i] == ';' {
			line++
			col = 0
			i++
			continue
		}
		if encoded[i] == ',' {
			i++
			continue
		}

		// Decode a segment (1, 4, or 5 VLQ values).
		var fields []int
		for i < len(encoded) && encoded[i] != ',' && encoded[i] != ';' {
			val, bytesRead, err := decodeVLQ(encoded[i:])
			if err != nil {
				return nil, err
			}
			fields = append(fields, val)
			i += bytesRead
		}

		if len(fields) == 0 {
			continue
		}

		m := mapping{genLine: line}
		col += fields[0]
		m.genCol = col

		if len(fields) >= 4 {
			m.hasSource = true
			srcIndex += fields[1]
			m.srcIndex = srcIndex
			srcLine += fields[2]
			m.srcLine = srcLine
			srcCol += fields[3]
			m.srcCol = srcCol
		}

		if len(fields) >= 5 {
			m.hasName = true
			nameIndex += fields[4]
			m.nameIndex = nameIndex
		}

		result = append(result, m)
	}

	return result, nil
}

// base64VLQ decoding table.
var vlqTable [128]int

func init() {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for i := range vlqTable {
		vlqTable[i] = -1
	}
	for i, c := range chars {
		vlqTable[c] = i
	}
}

// decodeVLQ decodes a single VLQ value from the string, returning the value
// and the number of characters consumed.
func decodeVLQ(s string) (int, int, error) {
	result := 0
	shift := 0
	i := 0

	for i < len(s) {
		c := s[i]
		if c >= 128 || vlqTable[c] < 0 {
			return 0, 0, fmt.Errorf("invalid VLQ character: %c", c)
		}
		digit := vlqTable[c]
		i++

		result |= (digit & 0x1F) << shift
		shift += 5

		if digit&0x20 == 0 {
			// Continuation bit not set — done.
			// Convert from VLQ signed: LSB is sign bit.
			if result&1 == 1 {
				return -(result >> 1), i, nil
			}
			return result >> 1, i, nil
		}
	}

	return 0, 0, fmt.Errorf("unterminated VLQ sequence")
}
