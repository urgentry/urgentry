//go:build go1.18

package search

import "testing"

// FuzzParse tests the parser with arbitrary input to find panics/crashes.
func FuzzParse(f *testing.F) {
	// Seed corpus with representative inputs from the existing tests.
	seeds := []string{
		"",
		"hello",
		"is:unresolved",
		"has:user",
		"level:error",
		`message:"hello world"`,
		"!is:resolved",
		"is:unresolved level:error has:user",
		"browser.name:Chrome",
		`title:"crash in main"`,
		"assigned:me",
		"firstSeen:>2024-01-01",
		// Edge cases
		`"unterminated`,
		"::::",
		"is:",
		":value",
		`key:"value with \"escapes\""`,
		string([]byte{0, 1, 2, 3}),
		"a:b c:d e:f g:h i:j k:l m:n",
		"!has:assignee",
		"!level:warning",
		`environment:"staging one"`,
		"release:v1.2.3",
		"event.type:error",
		"!!!",
		`""`,
		`" "`,
		"  \t\n  ",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, input string) {
		// The parser should never panic on any input.
		_ = Parse(input)
	})
}
