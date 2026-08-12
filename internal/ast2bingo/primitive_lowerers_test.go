package ast2bingo

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func TestPrimitiveFunctionLowererRegistryIsCanonical(t *testing.T) {
	seen := make(map[string]struct{}, len(primitiveFunctionLowerers))
	for index, lowerer := range primitiveFunctionLowerers {
		if lowerer.name == "" || lowerer.lower == nil {
			t.Fatalf("primitive lowerer %d is incomplete", index)
		}
		if _, duplicate := seen[lowerer.name]; duplicate {
			t.Fatalf("primitive lowerer %q is duplicated", lowerer.name)
		}
		seen[lowerer.name] = struct{}{}
	}
}

func TestPrimitiveFunctionLowererRejectsAmbiguousMatch(t *testing.T) {
	lowerers := []primitiveFunctionLowerer{
		{name: "first", lower: func(primitiveFunctionLoweringInput) (bingo.HIRFunction, []LoweringEvent, bool, error) {
			return bingo.HIRFunction{}, nil, true, nil
		}},
		{name: "second", lower: func(primitiveFunctionLoweringInput) (bingo.HIRFunction, []LoweringEvent, bool, error) {
			return bingo.HIRFunction{}, nil, true, nil
		}},
	}
	if _, _, matched, err := lowerPrimitiveFunction(primitiveFunctionLoweringInput{}, lowerers); err == nil || matched {
		t.Fatalf("lowerPrimitiveFunction matched = %t, error = %v", matched, err)
	}
}
