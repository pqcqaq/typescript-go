package bingo

import "fmt"

// NullableNumberTag is the stable tag stored before a binary64 payload. It
// deliberately keeps null and undefined distinct even when an operation such
// as ?? treats both as nullish.
type NullableNumberTag uint8

const (
	NullableNumberTagNumber    NullableNumberTag = 0
	NullableNumberTagNull      NullableNumberTag = 1
	NullableNumberTagUndefined NullableNumberTag = 2

	NullableNumberABIByteWidth  uint32 = 128
	NullableNumberABIAlign      uint32 = 8
	NullableNumberPayloadOffset uint32 = 8
)

// NullableNumberContract is the immutable Phase 2B ABI: C and LLVM use
// { i8 tag, [7 x i8] padding, double payload }. Nullish payload bytes are
// canonical zero at the external boundary.
type NullableNumberContract struct {
	NumberTag     NullableNumberTag `json:"numberTag"`
	NullTag       NullableNumberTag `json:"nullTag"`
	UndefinedTag  NullableNumberTag `json:"undefinedTag"`
	BitWidth      uint32            `json:"bitWidth"`
	ABIAlign      uint32            `json:"abiAlign"`
	PayloadOffset uint32            `json:"payloadOffset"`
}

func CanonicalNullableNumberContract() NullableNumberContract {
	return NullableNumberContract{
		NumberTag: NullableNumberTagNumber, NullTag: NullableNumberTagNull, UndefinedTag: NullableNumberTagUndefined,
		BitWidth: NullableNumberABIByteWidth, ABIAlign: NullableNumberABIAlign, PayloadOffset: NullableNumberPayloadOffset,
	}
}

func ValidateNullableNumberContract(contract NullableNumberContract) error {
	want := CanonicalNullableNumberContract()
	if contract != want {
		return fmt.Errorf("unsupported nullable-number ABI contract: %#v", contract)
	}
	return nil
}
