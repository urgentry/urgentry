package search

import (
	"testing"
)

func TestLex(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   []Token
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "bare text",
			input: "hello world",
			want: []Token{
				{Type: TEXT, Value: "hello"},
				{Type: TEXT, Value: "world"},
			},
		},
		{
			name:  "is:unresolved",
			input: "is:unresolved",
			want: []Token{
				{Type: IS, Value: "unresolved"},
			},
		},
		{
			name:  "!is:resolved",
			input: "!is:resolved",
			want: []Token{
				{Type: NOT_IS, Value: "resolved"},
			},
		},
		{
			name:  "has:assignee",
			input: "has:assignee",
			want: []Token{
				{Type: HAS, Key: "assignee", Value: "assignee"},
			},
		},
		{
			name:  "!has:assignee",
			input: "!has:assignee",
			want: []Token{
				{Type: NOT_HAS, Key: "assignee", Value: "assignee"},
			},
		},
		{
			name:  "key:value",
			input: "level:error",
			want: []Token{
				{Type: KEY_VALUE, Key: "level", Value: "error"},
			},
		},
		{
			name:  "negated key:value",
			input: "!level:error",
			want: []Token{
				{Type: NEGATED_KEY_VALUE, Key: "level", Value: "error"},
			},
		},
		{
			name:  "quoted string",
			input: `"hello world"`,
			want: []Token{
				{Type: TEXT, Value: "hello world"},
			},
		},
		{
			name:  "mixed tokens",
			input: `is:unresolved level:error "connection refused" has:assignee browser.name:Chrome`,
			want: []Token{
				{Type: IS, Value: "unresolved"},
				{Type: KEY_VALUE, Key: "level", Value: "error"},
				{Type: TEXT, Value: "connection refused"},
				{Type: HAS, Key: "assignee", Value: "assignee"},
				{Type: KEY_VALUE, Key: "browser.name", Value: "Chrome"},
			},
		},
		{
			name:  "key with quoted value",
			input: `environment:"staging env"`,
			want: []Token{
				{Type: KEY_VALUE, Key: "environment", Value: "staging env"},
			},
		},
		{
			name:  "negated has and is together",
			input: "!has:assignee !is:ignored",
			want: []Token{
				{Type: NOT_HAS, Key: "assignee", Value: "assignee"},
				{Type: NOT_IS, Value: "ignored"},
			},
		},
		{
			name:  "env alias",
			input: "env:production",
			want: []Token{
				{Type: KEY_VALUE, Key: "env", Value: "production"},
			},
		},
		{
			name:  "event.type with dot",
			input: "event.type:error",
			want: []Token{
				{Type: KEY_VALUE, Key: "event.type", Value: "error"},
			},
		},
		{
			name:  "assigned operator",
			input: "assigned:user@example.com",
			want: []Token{
				{Type: KEY_VALUE, Key: "assigned", Value: "user@example.com"},
			},
		},
		{
			name:  "release filter",
			input: "release:1.2.3",
			want: []Token{
				{Type: KEY_VALUE, Key: "release", Value: "1.2.3"},
			},
		},
		{
			name:  "unterminated quote",
			input: `"hello world`,
			want: []Token{
				{Type: TEXT, Value: "hello world"},
			},
		},
		{
			name:  "exclamation in bare text",
			input: "!broken",
			want: []Token{
				{Type: TEXT, Value: "!broken"},
			},
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Lex(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("Lex(%q) returned %d tokens, want %d\ngot:  %+v\nwant: %+v", tt.input, len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i].Type != tt.want[i].Type || got[i].Key != tt.want[i].Key || got[i].Value != tt.want[i].Value {
					t.Errorf("token[%d]: got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
