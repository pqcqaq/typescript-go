package irartifact

import (
	"strings"
	"testing"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

func TestRunnableManifestRejectsAmbiguousExecutions(t *testing.T) {
	valid := CaseManifest{
		TimeoutMS: 2000,
		Executions: []CaseExecution{{
			Name: "one-plus-two", LeftBits: "3ff0000000000000", RightBits: "4000000000000000", ExpectedBits: "4008000000000000",
		}},
	}
	if err := ValidateRunnableManifest(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CaseManifest){
		"timeout":        func(value *CaseManifest) { value.TimeoutMS = 0 },
		"duplicate name": func(value *CaseManifest) { value.Executions = append(value.Executions, value.Executions[0]) },
		"uppercase bits": func(value *CaseManifest) { value.Executions[0].ExpectedBits = "400800000000000A" },
		"short bits":     func(value *CaseManifest) { value.Executions[0].LeftBits = "0" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.Executions = append([]CaseExecution(nil), valid.Executions...)
			mutate(&candidate)
			if err := ValidateRunnableManifest(candidate); err == nil {
				t.Fatal("invalid runnable manifest was accepted")
			}
		})
	}
}

func TestLoadCaseRejectsUnknownRunFields(t *testing.T) {
	data := `{"schemaVersion":1,"name":"case","frontendSnapshot":"frontend.json","timeoutMs":2000,"executions":[],"unknownRunField":true}`
	var manifest CaseManifest
	if err := jsonx.Unmarshal([]byte(data), &manifest, jsonx.RejectUnknownMembers(true)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown run field error = %v", err)
	}
}
