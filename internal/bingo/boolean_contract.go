package bingo

import "fmt"

// BooleanContractVersion identifies the primitive boolean representation and
// ABI contract used by Phase 2B control-flow lowering.
const BooleanContractVersion uint32 = 1

type BooleanRepresentation string
type BooleanABIRepresentation string
type BooleanBranchPolicy string
type BooleanCoercionPolicy string

const (
	BooleanRepresentationI1 BooleanRepresentation = "i1"

	// C ABIs do not expose LLVM i1 directly. Public boolean parameters and
	// results use one uint8_t containing exactly zero or one.
	BooleanABIRepresentationUint8 BooleanABIRepresentation = "c-uint8-zero-or-one"

	BooleanBranchDirectI1 BooleanBranchPolicy = "direct-i1-condition"

	BooleanCoercionNoImplicitNumber BooleanCoercionPolicy = "no-implicit-number-coercion"

	BooleanABIFalse uint8 = 0
	BooleanABITrue  uint8 = 1
)

// PrimitiveBooleanContract is the canonical boolean contract consumed by
// representation planning, MIR verification, LLVM lowering, and ABI tests.
type PrimitiveBooleanContract struct {
	Version           uint32                   `json:"version"`
	Representation    BooleanRepresentation    `json:"representation"`
	ABIRepresentation BooleanABIRepresentation `json:"abiRepresentation"`
	FalseValue        uint8                    `json:"falseValue"`
	TrueValue         uint8                    `json:"trueValue"`
	BranchPolicy      BooleanBranchPolicy      `json:"branchPolicy"`
	CoercionPolicy    BooleanCoercionPolicy    `json:"coercionPolicy"`
}

func BooleanContract() PrimitiveBooleanContract {
	return PrimitiveBooleanContract{
		Version:           BooleanContractVersion,
		Representation:    BooleanRepresentationI1,
		ABIRepresentation: BooleanABIRepresentationUint8,
		FalseValue:        BooleanABIFalse,
		TrueValue:         BooleanABITrue,
		BranchPolicy:      BooleanBranchDirectI1,
		CoercionPolicy:    BooleanCoercionNoImplicitNumber,
	}
}

func ValidateBooleanContract(contract PrimitiveBooleanContract) error {
	want := BooleanContract()
	if contract != want {
		return fmt.Errorf("unsupported primitive boolean contract: got %#v, want %#v", contract, want)
	}
	return nil
}

func EncodeBooleanABI(value bool) uint8 {
	if value {
		return BooleanABITrue
	}
	return BooleanABIFalse
}

func DecodeBooleanABI(value uint8) (bool, error) {
	switch value {
	case BooleanABIFalse:
		return false, nil
	case BooleanABITrue:
		return true, nil
	default:
		return false, fmt.Errorf("boolean ABI value %d is not canonical zero or one", value)
	}
}
