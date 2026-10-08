package pdf

import (
	"strings"
	"testing"
)

// A delimiter no token starts with, and nesting deep enough to exhaust the
// stack, are errors: the parser used to loop on the one without consuming it.
func TestMalformedObjectsAreErrors(t *testing.T) {
	for _, src := range []string{
		"[1 ) 2]",
		"[1 { 2]",
		"[1 } 2]",
		"<< /W [1 } 2] >>",
		strings.Repeat("[", 1_000_000),
		strings.Repeat("<< /A ", 200_000),
	} {
		if _, err := NewParser([]byte(src)).ParseObject(); err == nil {
			t.Errorf("%.20q: no error", src)
		}
	}
	nested := strings.Repeat("[", maxNesting) + strings.Repeat("]", maxNesting)
	if _, err := NewParser([]byte(nested)).ParseObject(); err != nil {
		t.Errorf("%d levels: %v", maxNesting, err)
	}
}

func TestMalformedContentEnds(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"stray brace in an array", "[(Hello) } ] TJ", "Hello"},
		{"stray brace between operators", "(Hello) Tj } (lost) Tj", "Hello"},
		{"deeply nested arrays", strings.Repeat("[", 1_000_000) + strings.Repeat("]", 1_000_000) + " (Hello) Tj", "Hello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := contentPDF(t, "BT /F1 12 Tf 72 700 Td "+tc.content+" ET")
			if got := soleSpan(t, data).Text; got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
