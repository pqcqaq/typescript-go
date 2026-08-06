package frontendwire_test

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func TestSyntheticSymbolNamesRoundTripAsCanonicalUTF8(t *testing.T) {
	frontend := buildFrontendSnapshot(t, `
const offset: number = 1;
export const add = (value: number): number => value + offset;
`)

	foundSyntheticName := false
	for _, symbol := range frontend.Program.Symbols {
		if !utf8.ValidString(symbol.Name) {
			t.Fatalf("symbol %q contains invalid UTF-8", symbol.ID)
		}
		if strings.Contains(symbol.Name, "\uFFFD") {
			foundSyntheticName = true
		}
	}
	if !foundSyntheticName {
		t.Fatal("capture did not expose the checker synthetic symbol-name sentinel")
	}

	first, err := frontend.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := frontendwire.DecodeFrontendSnapshot(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := decoded.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("canonical frontend snapshot changed after UTF-8 wire round-trip")
	}
}
