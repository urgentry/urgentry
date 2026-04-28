package dsn

import "testing"

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"http://public@example.test/1",
		"https://public:secret@example.test/api/project-1/store/",
		"://bad",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		parsed, err := Parse(raw)
		if err != nil {
			return
		}
		if parsed.Scheme == "" || parsed.Host == "" || parsed.ProjectID == "" || parsed.PublicKey == "" {
			t.Fatalf("Parse(%q) returned incomplete DSN: %#v", raw, parsed)
		}
	})
}
