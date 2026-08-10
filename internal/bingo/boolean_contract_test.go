package bingo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBooleanContractIsCanonicalAndRejectsAlternatives(t *testing.T) {
	contract := BooleanContract()
	if err := ValidateBooleanContract(contract); err != nil {
		t.Fatal(err)
	}
	if contract.Representation != BooleanRepresentationI1 ||
		contract.ABIRepresentation != BooleanABIRepresentationUint8 ||
		contract.FalseValue != 0 || contract.TrueValue != 1 ||
		contract.BranchPolicy != BooleanBranchDirectI1 ||
		contract.CoercionPolicy != BooleanCoercionNoImplicitNumber {
		t.Fatalf("unexpected boolean contract: %#v", contract)
	}

	tests := []struct {
		name   string
		mutate func(*PrimitiveBooleanContract)
	}{
		{name: "version", mutate: func(value *PrimitiveBooleanContract) { value.Version++ }},
		{name: "representation", mutate: func(value *PrimitiveBooleanContract) { value.Representation = "i8" }},
		{name: "ABI representation", mutate: func(value *PrimitiveBooleanContract) { value.ABIRepresentation = "c-bool" }},
		{name: "false value", mutate: func(value *PrimitiveBooleanContract) { value.FalseValue = 2 }},
		{name: "true value", mutate: func(value *PrimitiveBooleanContract) { value.TrueValue = 0xff }},
		{name: "branch policy", mutate: func(value *PrimitiveBooleanContract) { value.BranchPolicy = "truthy" }},
		{name: "coercion policy", mutate: func(value *PrimitiveBooleanContract) { value.CoercionPolicy = "number-coercion" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := contract
			test.mutate(&candidate)
			if err := ValidateBooleanContract(candidate); err == nil || !strings.Contains(err.Error(), "unsupported primitive boolean contract") {
				t.Fatalf("ValidateBooleanContract error = %v", err)
			}
		})
	}
}

func TestBooleanContractJSONIsStableAndExplicit(t *testing.T) {
	encoded, err := json.Marshal(BooleanContract())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":1,"representation":"i1","abiRepresentation":"c-uint8-zero-or-one","falseValue":0,"trueValue":1,"branchPolicy":"direct-i1-condition","coercionPolicy":"no-implicit-number-coercion"}`
	if string(encoded) != want {
		t.Fatalf("boolean contract JSON = %s, want %s", encoded, want)
	}
}

func TestBooleanABIOnlyAcceptsZeroOrOne(t *testing.T) {
	for _, value := range []bool{false, true} {
		encoded := EncodeBooleanABI(value)
		decoded, err := DecodeBooleanABI(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if decoded != value {
			t.Fatalf("boolean ABI round trip = %t, want %t", decoded, value)
		}
	}
	for _, value := range []uint8{2, 0x7f, 0xff} {
		if _, err := DecodeBooleanABI(value); err == nil || !strings.Contains(err.Error(), "not canonical zero or one") {
			t.Fatalf("DecodeBooleanABI(%d) error = %v", value, err)
		}
	}
}
