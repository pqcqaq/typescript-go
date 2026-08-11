package bingo

import (
	"fmt"
	"strconv"
)

const (
	// UTF16StringABIByteWidth is the SysV C ABI size of BingoUtf16String:
	// { const uint16_t *data; uint64_t length }. It is a borrowed view, not
	// an owning runtime object.
	UTF16StringABIByteWidth uint32 = 128
	UTF16StringABIAlign     uint32 = 8
)

// UTF16StringContract fixes the Phase 2B literal-string boundary. Literal
// storage is immutable and lives for the full executable lifetime. Dynamic
// allocation, ownership transfer, and GC rooting are deliberately excluded.
type UTF16StringContract struct {
	Encoding       string `json:"encoding"`
	Storage        string `json:"storage"`
	Ownership      string `json:"ownership"`
	BitWidth       uint32 `json:"bitWidth"`
	ABIAlign       uint32 `json:"abiAlign"`
	DataOffset     uint32 `json:"dataOffset"`
	LengthOffset   uint32 `json:"lengthOffset"`
	EmptyDataIsNil bool   `json:"emptyDataIsNil"`
}

func CanonicalUTF16StringContract() UTF16StringContract {
	return UTF16StringContract{
		Encoding: "utf-16-code-unit", Storage: "static-literal", Ownership: "borrowed-immutable",
		BitWidth: UTF16StringABIByteWidth, ABIAlign: UTF16StringABIAlign, DataOffset: 0, LengthOffset: 8,
		EmptyDataIsNil: true,
	}
}

func ValidateUTF16StringContract(contract UTF16StringContract) error {
	if contract != CanonicalUTF16StringContract() {
		return fmt.Errorf("unsupported UTF-16 string ABI contract: %#v", contract)
	}
	return nil
}

// ValidateUTF16CodeUnits checks the canonical HIR/MIR literal encoding: four
// lowercase hexadecimal digits for each UTF-16 code unit. Surrogate code
// units are intentionally accepted unchanged because JavaScript strings are
// specified in code units, not scalar values.
func ValidateUTF16CodeUnits(value string) error {
	if len(value)%4 != 0 {
		return fmt.Errorf("UTF-16 literal has incomplete code unit")
	}
	for index := 0; index < len(value); index += 4 {
		if _, err := strconv.ParseUint(value[index:index+4], 16, 16); err != nil {
			return fmt.Errorf("UTF-16 literal code unit %q is invalid", value[index:index+4])
		}
		for _, char := range value[index : index+4] {
			if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
				return fmt.Errorf("UTF-16 literal code unit %q is not lowercase hexadecimal", value[index:index+4])
			}
		}
	}
	return nil
}
