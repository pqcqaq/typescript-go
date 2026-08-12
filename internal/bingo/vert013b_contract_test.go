package bingo

import (
	"bytes"
	"strings"
	"testing"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

func testVERT013bContract(t testing.TB) VERT013bClassContract {
	t.Helper()
	key := strings.Repeat("a", 64)
	return VERT013bClassContract{SchemaVersion: VERT013bClassContractSchemaVersion, Classes: []VERT013bClass{
		{ID: 1, SymbolKey: "base", InstanceTypeKey: key, Constructor: ClassConstructorContract{SymbolKey: "base.ctor", Signature: "cdecl(ptr,f64)->void", AllocatesReceiver: true, ReturnsOwnReceiver: true}, Fields: []ClassFieldContract{{ID: 1, SymbolKey: "base.value", Name: "value", Type: TypeNumber, Visibility: "public", Mutable: true, Storage: ClassFieldInstanceSlot, SourceOrder: 1}}, Methods: []ClassMethodContract{{ID: 1, SymbolKey: "base.increment", Name: "increment", Signature: "cdecl(ptr)->f64", Visibility: "public", RequiresReceiver: true, SourceOrder: 3}}, Initialization: []ClassInitializationStep{{ID: 1, Kind: ClassInitAllocate}, {ID: 2, Kind: ClassInitField, FieldID: 1, SourceOrder: 1}, {ID: 3, Kind: ClassInitBody, SourceOrder: 2}}},
		{ID: 2, SymbolKey: "derived", InstanceTypeKey: strings.Repeat("b", 64), BaseClassID: 1, Constructor: ClassConstructorContract{SymbolKey: "derived.ctor", Signature: "cdecl(ptr,f64,f64)->void", Derived: true, ReturnsOwnReceiver: true}, Fields: []ClassFieldContract{{ID: 1, SymbolKey: "derived.step", Name: "step", Type: TypeNumber, Visibility: "public", Mutable: true, Storage: ClassFieldInstanceSlot, SourceOrder: 1}}, Methods: []ClassMethodContract{{ID: 1, SymbolKey: "derived.increment", Name: "increment", Signature: "cdecl(ptr)->f64", Visibility: "public", RequiresReceiver: true, SourceOrder: 3}}, Super: &VERT013bSuperCall{BaseClassID: 1, Callee: "base.ctor", Arguments: []string{"start"}, SourceOrder: 1}, Initialization: []ClassInitializationStep{{ID: 1, Kind: ClassInitAllocate}, {ID: 2, Kind: ClassInitBody, SourceOrder: 1}, {ID: 3, Kind: ClassInitField, FieldID: 1, SourceOrder: 2}, {ID: 4, Kind: ClassInitBody, SourceOrder: 3}}},
	}}
}

func TestVERT013bClassContractRejectsSubstitutions(t *testing.T) {
	tests := map[string]func(*VERT013bClassContract){
		"base identity":     func(c *VERT013bClassContract) { c.Classes[1].BaseClassID = 2 },
		"same type key":     func(c *VERT013bClassContract) { c.Classes[1].InstanceTypeKey = c.Classes[0].InstanceTypeKey },
		"super callee":      func(c *VERT013bClassContract) { c.Classes[1].Super.Callee = "other" },
		"super argument":    func(c *VERT013bClassContract) { c.Classes[1].Super.Arguments[0] = "step" },
		"derived allocates": func(c *VERT013bClassContract) { c.Classes[1].Constructor.AllocatesReceiver = true },
		"field readonly":    func(c *VERT013bClassContract) { c.Classes[1].Fields[0].Mutable = false },
		"method ABI":        func(c *VERT013bClassContract) { c.Classes[1].Methods[0].Signature = "cdecl(ptr,f64)->f64" },
		"late super": func(c *VERT013bClassContract) {
			c.Classes[1].Initialization[1], c.Classes[1].Initialization[2] = c.Classes[1].Initialization[2], c.Classes[1].Initialization[1]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			contract := testVERT013bContract(t)
			mutate(&contract)
			if _, _, err := CanonicalVERT013bClassContract(contract); err == nil {
				t.Fatal("substitution accepted")
			}
		})
	}
}

func TestVERT013bClassContractStrictReader(t *testing.T) {
	contract := testVERT013bContract(t)
	canonical, _, err := CanonicalVERT013bClassContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(canonical, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":2,"unknown":true`), 1)
	if _, err := DecodeVERT013bClassContract(unknown); err == nil {
		t.Fatal("unknown member accepted")
	}
	if _, err := DecodeVERT013bClassContract(bytes.Repeat([]byte{' '}, maxVERT013bClassContractBytes+1)); err == nil {
		t.Fatal("oversized contract accepted")
	}
	if _, err := DecodeClassContract(canonical); err == nil {
		t.Fatal("v1 reader accepted v2 contract")
	}
}

func TestVERT013bClassContractCanonicalAndTamper(t *testing.T) {
	contract := testVERT013bContract(t)
	canonical, hash, err := CanonicalVERT013bClassContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	decoded, err := DecodeVERT013bClassContract(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, mustJSON(t, *decoded)) {
		t.Fatal("contract round trip changed bytes")
	}
	decoded.Classes[1].Super.SourceOrder = 2
	if err := VerifyCanonicalVERT013bClassContract(*decoded); err == nil {
		t.Fatal("late super call accepted")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := jsonx.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
