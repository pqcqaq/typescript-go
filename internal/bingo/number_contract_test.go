package bingo

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestNumberContractIsTheOnlyAcceptedFirstSliceContract(t *testing.T) {
	contract := NumberContract()
	if err := ValidateNumberContract(contract); err != nil {
		t.Fatal(err)
	}
	if contract.Representation != NumberRepresentationF64 ||
		contract.NaNPolicy != NumberNaNPolicyCanonicalQuiet ||
		contract.ArithmeticPolicy != NumberArithmeticJavaScript ||
		contract.Operator != NumberOperatorAdd ||
		contract.ABISymbol != "add" ||
		contract.ABISignature != `extern "C" double add(double,double)` ||
		contract.ABIObservation != NumberABIObservationBits {
		t.Fatalf("unexpected first-slice number contract: %#v", contract)
	}

	tests := []struct {
		name   string
		mutate func(*FirstSliceNumberContract)
	}{
		{name: "version", mutate: func(value *FirstSliceNumberContract) { value.Version++ }},
		{name: "representation", mutate: func(value *FirstSliceNumberContract) { value.Representation = "i32" }},
		{name: "NaN policy", mutate: func(value *FirstSliceNumberContract) { value.NaNPolicy = "preserve-payload" }},
		{name: "arithmetic policy", mutate: func(value *FirstSliceNumberContract) { value.ArithmeticPolicy = "fast-math" }},
		{name: "operator", mutate: func(value *FirstSliceNumberContract) { value.Operator = "-" }},
		{name: "ABI symbol", mutate: func(value *FirstSliceNumberContract) { value.ABISymbol = "number_add" }},
		{name: "ABI signature", mutate: func(value *FirstSliceNumberContract) { value.ABISignature = "double add(float,float)" }},
		{name: "ABI observation", mutate: func(value *FirstSliceNumberContract) { value.ABIObservation = "decimal-text" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := contract
			test.mutate(&mutated)
			if err := ValidateNumberContract(mutated); err == nil || !strings.Contains(err.Error(), "unsupported first-slice number contract") {
				t.Fatalf("ValidateNumberContract error = %v", err)
			}
		})
	}
}

func TestNumberContractJSONIsStableAndExplicit(t *testing.T) {
	encoded, err := json.Marshal(NumberContract())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":1,"representation":"ieee754-binary64","nanPolicy":"canonical-quiet-nan","arithmeticPolicy":"round-to-nearest-ties-to-even-no-fast-math","operator":"+","abiSymbol":"add","abiSignature":"extern \"C\" double add(double,double)","abiObservation":"ieee754-binary64-bits"}`
	if string(encoded) != want {
		t.Fatalf("number contract JSON = %s, want %s", encoded, want)
	}
}

func TestNumberABIObservationPreservesEveryNonNaNBit(t *testing.T) {
	for _, bits := range []NumberABIBits{
		0x0000000000000000,
		NegativeZeroNumberBits,
		0x0000000000000001,
		0x0010000000000000,
		0x3ff0000000000000,
		0x7fefffffffffffff,
		0x7ff0000000000000,
		0xfff0000000000000,
	} {
		value := NumberFromABIBits(bits)
		if got := ObserveNumberABIBits(value); got != bits {
			t.Errorf("ABI bits round trip = %#016x, want %#016x", uint64(got), uint64(bits))
		}
	}
}

func TestNumberABIObservationCanonicalizesEveryNaN(t *testing.T) {
	for _, bits := range []NumberABIBits{
		0x7ff0000000000001,
		0x7ff8000000000000,
		0x7fffffffffffffff,
		0xfff0000000000001,
		0xfff8000000000042,
	} {
		value := NumberFromABIBits(bits)
		if !math.IsNaN(value) {
			t.Fatalf("NumberFromABIBits(%#016x) is not NaN", uint64(bits))
		}
		if got := math.Float64bits(value); got != uint64(CanonicalNumberNaNBits) {
			t.Errorf("decoded NaN bits = %#016x, want %#016x", got, uint64(CanonicalNumberNaNBits))
		}
		if got := ObserveNumberABIBits(math.Float64frombits(uint64(bits))); got != CanonicalNumberNaNBits {
			t.Errorf("observed NaN bits = %#016x, want %#016x", uint64(got), uint64(CanonicalNumberNaNBits))
		}
	}
}

func TestAddNumbersUsesJavaScriptBinary64Semantics(t *testing.T) {
	tests := []struct {
		name    string
		left    NumberABIBits
		right   NumberABIBits
		want    NumberABIBits
		wantNaN bool
	}{
		{name: "ordinary", left: 0x3ff8000000000000, right: 0x4002000000000000, want: 0x400e000000000000}, // 1.5 + 2.25 = 3.75
		{name: "round ties to even", left: 0x3ff0000000000000, right: 0x3ca0000000000000, want: 0x3ff0000000000000},
		{name: "overflow", left: 0x7fefffffffffffff, right: 0x7fefffffffffffff, want: 0x7ff0000000000000},
		{name: "negative zero plus negative zero", left: NegativeZeroNumberBits, right: NegativeZeroNumberBits, want: NegativeZeroNumberBits},
		{name: "mixed zero signs", left: NegativeZeroNumberBits, right: 0, want: 0},
		{name: "exact cancellation is positive zero", left: 0xbff0000000000000, right: 0x3ff0000000000000, want: 0},
		{name: "subnormal addition", left: 0x0000000000000001, right: 0x0000000000000001, want: 0x0000000000000002},
		{name: "infinities produce canonical NaN", left: 0x7ff0000000000000, right: 0xfff0000000000000, want: CanonicalNumberNaNBits, wantNaN: true},
		{name: "input payload produces canonical NaN", left: 0x7ff0000000000042, right: 0x3ff0000000000000, want: CanonicalNumberNaNBits, wantNaN: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotValue := AddNumbers(NumberFromABIBits(test.left), NumberFromABIBits(test.right))
			if test.wantNaN && !math.IsNaN(gotValue) {
				t.Fatalf("AddNumbers result = %v, want NaN", gotValue)
			}
			if got := ObserveNumberABIBits(gotValue); got != test.want {
				t.Fatalf("AddNumbers bits = %#016x, want %#016x", uint64(got), uint64(test.want))
			}
		})
	}
}
