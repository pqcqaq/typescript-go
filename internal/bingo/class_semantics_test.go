package bingo

import (
	"bytes"
	"strings"
	"testing"
)

func testClassContract() ClassContract {
	return ClassContract{SchemaVersion: ClassContractSchemaVersion, Classes: []ClassDeclarationContract{{
		ID: 1, SymbolKey: "class:Counter", InstanceTypeKey: typeKeyB,
		Constructor:    ClassConstructorContract{SymbolKey: "constructor:Counter", Signature: "cdecl(ptr,f64)->void", AllocatesReceiver: true, ReturnsOwnReceiver: true},
		Fields:         []ClassFieldContract{{ID: 1, SymbolKey: "field:Counter.value", Name: "value", Type: TypeNumber, Visibility: "public", Mutable: true, Storage: ClassFieldInstanceSlot, SourceOrder: 1}},
		Methods:        []ClassMethodContract{{ID: 1, SymbolKey: "method:Counter.increment", Name: "increment", Signature: "cdecl(ptr)->f64", Visibility: "public", RequiresReceiver: true, SourceOrder: 3}},
		Initialization: []ClassInitializationStep{{ID: 1, Kind: ClassInitAllocate}, {ID: 2, Kind: ClassInitField, FieldID: 1, SourceOrder: 1}, {ID: 3, Kind: ClassInitBody, SourceOrder: 2}},
	}}}
}

func TestClassContractCanonicalRoundTrip(t *testing.T) {
	contract := testClassContract()
	encoded, hash, err := CanonicalClassContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	decoded, err := DecodeClassContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, _, err := CanonicalClassContract(*decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("class contract is not canonical")
	}
}

func TestClassContractRejectsTampering(t *testing.T) {
	for name, mutate := range map[string]func(*ClassContract){
		"second class":            func(c *ClassContract) { c.Classes = append(c.Classes, c.Classes[0]) },
		"base class":              func(c *ClassContract) { c.Classes[0].BaseClassID = 1 },
		"derived constructor":     func(c *ClassContract) { c.Classes[0].Constructor.Derived = true },
		"constructor return":      func(c *ClassContract) { c.Classes[0].Constructor.ReturnsOwnReceiver = false },
		"private field":           func(c *ClassContract) { c.Classes[0].Fields[0].Visibility = "private" },
		"static field":            func(c *ClassContract) { c.Classes[0].Fields[0].Static = true },
		"readonly field":          func(c *ClassContract) { c.Classes[0].Fields[0].Mutable = false },
		"method without receiver": func(c *ClassContract) { c.Classes[0].Methods[0].RequiresReceiver = false },
		"static method":           func(c *ClassContract) { c.Classes[0].Methods[0].Static = true },
		"wrong method ABI":        func(c *ClassContract) { c.Classes[0].Methods[0].Signature = "cdecl()->f64" },
		"field after body": func(c *ClassContract) {
			c.Classes[0].Initialization[1], c.Classes[0].Initialization[2] = c.Classes[0].Initialization[2], c.Classes[0].Initialization[1]
		},
		"wrong field init": func(c *ClassContract) { c.Classes[0].Initialization[1].FieldID = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			contract := testClassContract()
			mutate(&contract)
			if _, _, err := CanonicalClassContract(contract); err == nil {
				t.Fatal("tampered class contract accepted")
			}
		})
	}
}

func TestClassContractStrictDecode(t *testing.T) {
	encoded, _, err := CanonicalClassContract(testClassContract())
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeClassContract(unknown); err == nil {
		t.Fatal("unknown member accepted")
	}
	stale := bytes.Replace(encoded, []byte(`"contentHash":"`), []byte(`"contentHash":"0`), 1)
	if _, err := DecodeClassContract(stale); err == nil {
		t.Fatal("stale hash accepted")
	}
	oversized := []byte(strings.Repeat(" ", maxClassContractBytes+1))
	if _, err := DecodeClassContract(oversized); err == nil {
		t.Fatal("oversized class contract accepted")
	}
}
