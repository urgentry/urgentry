package search

import (
	"strings"
	"unicode"
)

// Lex tokenizes a raw search query string into a slice of Tokens.
// It handles:
//   - key:value pairs (level:error, environment:production)
//   - negated key:value (!level:error)
//   - has:/!has: presence checks (has:assignee, !has:assignee)
//   - is:/!is: status predicates (is:unresolved, !is:resolved)
//   - "quoted strings" as single text tokens
//   - bare words as text tokens
func Lex(raw string) []Token {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var tokens []Token
	i := 0
	n := len(raw)

	for i < n {
		// Skip whitespace.
		if unicode.IsSpace(rune(raw[i])) {
			i++
			continue
		}

		// Quoted string — consume everything between quotes as a single TEXT token.
		if raw[i] == '"' {
			end := strings.IndexByte(raw[i+1:], '"')
			if end >= 0 {
				val := raw[i+1 : i+1+end]
				if val != "" {
					tokens = append(tokens, Token{Type: TEXT, Value: val})
				}
				i = i + 1 + end + 1
				continue
			}
			// Unterminated quote — treat rest as text.
			val := raw[i+1:]
			if val != "" {
				tokens = append(tokens, Token{Type: TEXT, Value: val})
			}
			break
		}

		// Read a whitespace-delimited word.
		start := i
		for i < n && !unicode.IsSpace(rune(raw[i])) {
			// If we hit a quote mid-word and there's a colon before it,
			// handle key:"quoted value" syntax.
			if raw[i] == '"' {
				break
			}
			i++
		}

		word := raw[start:i]

		// Check for key:"quoted value" pattern.
		if i < n && raw[i] == '"' {
			colonIdx := strings.IndexByte(word, ':')
			if colonIdx >= 0 {
				prefix := word[:colonIdx+1] // includes the colon
				end := strings.IndexByte(raw[i+1:], '"')
				if end >= 0 {
					quotedVal := raw[i+1 : i+1+end]
					word = prefix + quotedVal
					i = i + 1 + end + 1
				} else {
					// Unterminated quote.
					quotedVal := raw[i+1:]
					word = prefix + quotedVal
					i = n
				}
			}
		}

		if word == "" {
			continue
		}

		tokens = append(tokens, classifyWord(word))
	}

	return tokens
}

// classifyWord determines the token type from a single word.
func classifyWord(word string) Token {
	negated := false
	w := word
	if strings.HasPrefix(w, "!") {
		negated = true
		w = w[1:]
	}

	lower := strings.ToLower(w)

	// is: / !is:
	if strings.HasPrefix(lower, "is:") {
		val := w[3:]
		if negated {
			return Token{Type: NOT_IS, Value: val}
		}
		return Token{Type: IS, Value: val}
	}

	// has: / !has:
	if strings.HasPrefix(lower, "has:") {
		val := w[4:]
		if negated {
			return Token{Type: NOT_HAS, Key: val, Value: val}
		}
		return Token{Type: HAS, Key: val, Value: val}
	}

	// key:value / !key:value
	colonIdx := strings.IndexByte(w, ':')
	if colonIdx > 0 && colonIdx < len(w)-1 {
		key := w[:colonIdx]
		val := w[colonIdx+1:]
		if negated {
			return Token{Type: NEGATED_KEY_VALUE, Key: key, Value: val}
		}
		return Token{Type: KEY_VALUE, Key: key, Value: val}
	}

	// Bare text (restore the ! if it was there but didn't match a pattern).
	return Token{Type: TEXT, Value: word}
}
