package bingo

import "testing"

func TestFirstSliceMIRFunctionSetVerifierRegistryIsCanonical(t *testing.T) {
	seen := make(map[string]struct{}, len(firstSliceMIRFunctionSetVerifiers))
	for index, verifier := range firstSliceMIRFunctionSetVerifiers {
		if verifier.name == "" || verifier.matches == nil || verifier.verify == nil {
			t.Fatalf("first-slice MIR verifier %d is incomplete", index)
		}
		if _, duplicate := seen[verifier.name]; duplicate {
			t.Fatalf("first-slice MIR verifier %q is duplicated", verifier.name)
		}
		seen[verifier.name] = struct{}{}
	}
}
