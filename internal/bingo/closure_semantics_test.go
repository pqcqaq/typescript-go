package bingo

import (
	"bytes"
	"testing"
)

func testClosureContract() ClosureContract {
	return ClosureContract{SchemaVersion: ClosureContractSchemaVersion,
		Environments: []ClosureEnvironmentContract{{ID: 1, HeapOwned: true, FieldCount: 1, TraceCount: 1}},
		Functions: []ClosureFunctionContract{{ID: 1, SymbolKey: "closure:increment", Signature: "cdecl(ptr)->f64", Escapes: true, EnvironmentID: 1,
			Captures: []ClosureCapture{{ID: 1, SymbolKey: "local:count", Type: TypeNumber, Mutable: true, Mode: ClosureCaptureByCell, Storage: ClosureStorageHeap, EnvironmentSlot: 0, Traced: true}}}}}
}

func TestClosureContractCanonicalRoundTrip(t *testing.T) {
	contract := testClosureContract()
	encoded, hash, err := CanonicalClosureContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	decoded, err := DecodeClosureContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, _, err := CanonicalClosureContract(*decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("closure contract is not canonical")
	}
}

func TestClosureContractRejectsTampering(t *testing.T) {
	for name, mutate := range map[string]func(*ClosureContract){
		"nondense capture": func(c *ClosureContract) { c.Functions[0].Captures[0].ID = 2 },
		"value mutable":    func(c *ClosureContract) { c.Functions[0].Captures[0].Mode = ClosureCaptureByValue },
		"stack escape":     func(c *ClosureContract) { c.Functions[0].Captures[0].Storage = ClosureStorageStack },
		"missing trace":    func(c *ClosureContract) { c.Environments[0].TraceCount = 0 },
		"wrong signature":  func(c *ClosureContract) { c.Functions[0].Signature = "cdecl()->f64" },
		"non-escaping":     func(c *ClosureContract) { c.Functions[0].Escapes = false },
		"second capture": func(c *ClosureContract) {
			c.Functions[0].Captures = append(c.Functions[0].Captures, c.Functions[0].Captures[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			contract := testClosureContract()
			mutate(&contract)
			if _, _, err := CanonicalClosureContract(contract); err == nil {
				t.Fatal("tampered closure contract accepted")
			}
		})
	}
}

func TestClosureContractRejectsUnknownMember(t *testing.T) {
	contract := testClosureContract()
	encoded, hash, err := CanonicalClosureContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	encoded, _, err = CanonicalClosureContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	encoded = bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeClosureContract(encoded); err == nil {
		t.Fatal("unknown member accepted")
	}
}
