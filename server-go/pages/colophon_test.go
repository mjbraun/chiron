package pages

import (
	"strings"
	"testing"
)

// On paper the colophon is set the same way as on the screen: small, grey,
// at the end, with the sources above it.
func TestThePrintShellStylesTheColophon(t *testing.T) {
	css := baseCSS()
	if !strings.Contains(css, ".colophon {") || !strings.Contains(css, ".sources {") {
		t.Fatalf("print css lacks the colophon or sources rules:\n%s", css)
	}
	if !strings.Contains(css[strings.Index(css, ".colophon {"):], "#888") {
		t.Fatal("the colophon is not grey")
	}
}
