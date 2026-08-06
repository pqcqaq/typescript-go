package bingo

import (
	"fmt"
	"math"
)

// NumberContractVersion identifies the first-slice JavaScript number
// contract. Changing any field represented by this contract requires a new
// version and coordinated backend/runtime conformance tests.
const NumberContractVersion uint32 = 1

// NumberRepresentation names the in-memory primitive representation.
type NumberRepresentation string

// NumberNaNPolicy names the observable NaN payload and sign policy.
type NumberNaNPolicy string

// NumberArithmeticPolicy names the rounding and optimization constraints
// imposed on primitive arithmetic.
type NumberArithmeticPolicy string

// NumberOperator names an operator admitted by the first-slice contract.
type NumberOperator string

// NumberABIObservation names the representation observed by the ABI harness.
type NumberABIObservation string

const (
	// NumberRepresentationF64 maps every JavaScript number to one IEEE-754
	// binary64 value. The first slice has no integer number representation.
	NumberRepresentationF64 NumberRepresentation = "ieee754-binary64"

	// NumberNaNPolicyCanonicalQuiet makes NaN payloads and signs
	// unobservable at the first-slice C ABI boundary. Every observed NaN uses
	// CanonicalNumberNaNBits; all non-NaN bit patterns remain exact.
	NumberNaNPolicyCanonicalQuiet NumberNaNPolicy = "canonical-quiet-nan"

	// NumberArithmeticJavaScript requires binary64 round-to-nearest,
	// ties-to-even arithmetic without reassociation or LLVM fast-math flags.
	NumberArithmeticJavaScript NumberArithmeticPolicy = "round-to-nearest-ties-to-even-no-fast-math"

	// NumberOperatorAdd is the only primitive operator in the first slice.
	NumberOperatorAdd NumberOperator = "+"

	// NumberABIObservationBits observes C double arguments and results as
	// their IEEE-754 binary64 bits, after applying the NaN policy.
	NumberABIObservationBits NumberABIObservation = "ieee754-binary64-bits"

	NumberAddABISymbol    = "add"
	NumberAddABISignature = `extern "C" double add(double,double)`

	CanonicalNumberNaNBits NumberABIBits = 0x7ff8000000000000
	NegativeZeroNumberBits NumberABIBits = 0x8000000000000000
)

// NumberABIBits is the fixed-width observation form used by the C ABI test
// harness. It is not a JavaScript integer value.
type NumberABIBits uint64

// FirstSliceNumberContract is a declarative copy of the number contract for
// consumers that need to bind emitted artifacts or manifests to it.
type FirstSliceNumberContract struct {
	Version          uint32                 `json:"version"`
	Representation   NumberRepresentation   `json:"representation"`
	NaNPolicy        NumberNaNPolicy        `json:"nanPolicy"`
	ArithmeticPolicy NumberArithmeticPolicy `json:"arithmeticPolicy"`
	Operator         NumberOperator         `json:"operator"`
	ABISymbol        string                 `json:"abiSymbol"`
	ABISignature     string                 `json:"abiSignature"`
	ABIObservation   NumberABIObservation   `json:"abiObservation"`
}

// NumberContract returns the only number contract accepted by the first
// vertical slice.
func NumberContract() FirstSliceNumberContract {
	return FirstSliceNumberContract{
		Version:          NumberContractVersion,
		Representation:   NumberRepresentationF64,
		NaNPolicy:        NumberNaNPolicyCanonicalQuiet,
		ArithmeticPolicy: NumberArithmeticJavaScript,
		Operator:         NumberOperatorAdd,
		ABISymbol:        NumberAddABISymbol,
		ABISignature:     NumberAddABISignature,
		ABIObservation:   NumberABIObservationBits,
	}
}

// ValidateNumberContract rejects alternative primitive representations,
// operators, or ABI shapes before they can enter later IR schemas.
func ValidateNumberContract(contract FirstSliceNumberContract) error {
	want := NumberContract()
	if contract != want {
		return fmt.Errorf("unsupported first-slice number contract: got %#v, want %#v", contract, want)
	}
	return nil
}

// ObserveNumberABIBits returns the exact binary64 observation for a C double.
// Signed zero is preserved. NaN sign, signaling state, and payload are
// canonicalized because JavaScript number arithmetic cannot observe them in
// this first slice.
func ObserveNumberABIBits(value float64) NumberABIBits {
	if math.IsNaN(value) {
		return CanonicalNumberNaNBits
	}
	return NumberABIBits(math.Float64bits(value))
}

// NumberFromABIBits decodes a C ABI observation. All NaN encodings enter the
// JavaScript number domain as the canonical quiet NaN; non-NaN encodings are
// preserved bit for bit, including negative zero.
func NumberFromABIBits(bits NumberABIBits) float64 {
	value := math.Float64frombits(uint64(bits))
	if math.IsNaN(value) {
		return math.Float64frombits(uint64(CanonicalNumberNaNBits))
	}
	return value
}

// AddNumbers implements the first-slice number + contract: IEEE-754 binary64
// addition with no integer coercion or fast-math assumptions, followed by the
// canonical NaN policy used at the observation boundary.
func AddNumbers(left, right float64) float64 {
	return NumberFromABIBits(ObserveNumberABIBits(left + right))
}
