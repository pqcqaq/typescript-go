package bingo

import (
	"strings"
	"testing"
)

func testClassAccessExecution(t testing.TB) ClassAccessExecutionContract {
	t.Helper()
	access := testClassAccessContract()
	access.Classes = access.Classes[:2]
	_, hash, err := CanonicalClassAccessContract(access)
	if err != nil {
		t.Fatal(err)
	}
	access.ContentHash = hash
	execution, err := NewClassAccessExecutionContract(access)
	if err != nil {
		t.Fatal(err)
	}
	return execution
}

func TestClassAccessExecutionCanonicalRoundTrip(t *testing.T) {
	execution := testClassAccessExecution(t)
	encoded, hash, err := CanonicalClassAccessExecution(execution)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeClassAccessExecution(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != hash || len(decoded.Functions) != 5 || decoded.Functions[4].Name != "classAccess" || !decoded.Functions[4].Exported {
		t.Fatalf("unexpected OBJ-003b execution round trip: %#v", decoded)
	}
}

func TestClassAccessExecutionRejectsTampering(t *testing.T) {
	execution := testClassAccessExecution(t)
	encoded, hash, err := CanonicalClassAccessExecution(execution)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"unknown": []byte(strings.Replace(string(encoded), `"contentHash":`, `"unknown":true,"contentHash":`, 1)),
		"schema":  []byte(strings.Replace(string(encoded), `"schemaVersion":1`, `"schemaVersion":0`, 1)),
		"stale":   []byte(strings.Replace(string(encoded), hash, typeKeyA, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeClassAccessExecution(data); err == nil {
				t.Fatal("tampered execution contract accepted")
			}
		})
	}
	mutations := map[string]func(*ClassAccessExecutionContract){
		"initializer": func(c *ClassAccessExecutionContract) { c.Functions[0].FieldInitBits[0] = "4022000000000000" },
		"allocation":  func(c *ClassAccessExecutionContract) { c.AllocatedClassID = 1 },
		"super":       func(c *ClassAccessExecutionContract) { c.Functions[1].Calls = nil },
		"entry calls": func(c *ClassAccessExecutionContract) {
			c.Functions[4].Calls[1], c.Functions[4].Calls[2] = c.Functions[4].Calls[2], c.Functions[4].Calls[1]
		},
		"export": func(c *ClassAccessExecutionContract) { c.Functions[4].Exported = false },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := testClassAccessExecution(t)
			mutate(&candidate)
			if _, _, err := CanonicalClassAccessExecution(candidate); err == nil {
				t.Fatal("malformed execution contract accepted")
			}
		})
	}
}
