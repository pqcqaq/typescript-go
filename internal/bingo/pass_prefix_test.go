package bingo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPassExecutorRunsExplicitCanonicalPrefix(t *testing.T) {
	handlers := testPassHandlers(0, nil)
	for _, spec := range PassSpecs()[2:] {
		delete(handlers, spec.ID)
	}
	executor, err := NewPassExecutorThrough(handlers, PassTypedHIR, 0)
	if err != nil {
		t.Fatal(err)
	}
	initial := PassState{
		Schema:   "snapshot-v2",
		Facts:    []string{"syntax", "types", "symbols", "signatures", "module-graph"},
		Artifact: json.RawMessage(`{"snapshot":true}`),
	}
	execution, err := executor.ExecuteThrough(context.Background(), initial, PassTypedHIR)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(execution.Dumps), 2; got != want {
		t.Fatalf("prefix dump count = %d, want %d", got, want)
	}
	if execution.Dumps[0].Pass != PassValidateSnapshot || execution.Dumps[1].Pass != PassTypedHIR {
		t.Fatalf("prefix pass order = %#v", execution.Dumps)
	}
	if execution.State.Schema != "hir-v7" {
		t.Fatalf("prefix final schema = %q, want hir-v7", execution.State.Schema)
	}
	if execution.State.Artifacts != nil {
		t.Fatalf("typed-HIR prefix unexpectedly changed its legacy PassState API: %#v", execution.State.Artifacts)
	}
	if _, err := executor.Execute(context.Background(), initial); err == nil || !strings.Contains(err.Error(), "use ExecuteThrough") {
		t.Fatalf("implicit partial execution error = %v", err)
	}
	if _, err := executor.ExecuteThrough(context.Background(), initial, PassEvaluationOrder); err == nil || !strings.Contains(err.Error(), "after registered terminal") {
		t.Fatalf("unregistered terminal error = %v", err)
	}
	if _, err := executor.ExecuteThrough(context.Background(), initial, PassID("invented")); err == nil || !strings.Contains(err.Error(), "unknown terminal") {
		t.Fatalf("unknown terminal error = %v", err)
	}
}

func TestPassPrefixRegistryCannotSkipOrHideLaterHandlers(t *testing.T) {
	prefix := testPassHandlers(0, nil)
	for _, spec := range PassSpecs()[2:] {
		delete(prefix, spec.ID)
	}

	missing := make(map[PassID]PassHandler, len(prefix)-1)
	for id, handler := range prefix {
		if id != PassValidateSnapshot {
			missing[id] = handler
		}
	}
	if _, err := NewPassExecutorThrough(missing, PassTypedHIR, 0); err == nil || !strings.Contains(err.Error(), "no registered handler") {
		t.Fatalf("skipped handler error = %v", err)
	}

	withLater := make(map[PassID]PassHandler, len(prefix)+1)
	for id, handler := range prefix {
		withLater[id] = handler
	}
	withLater[PassEvaluationOrder] = testPassHandlers(0, nil)[PassEvaluationOrder]
	if _, err := NewPassExecutorThrough(withLater, PassTypedHIR, 0); err == nil || !strings.Contains(err.Error(), "after terminal") {
		t.Fatalf("hidden later handler error = %v", err)
	}
}

func TestValidatePassPrefixRejectsTruncationReorderingAndUnknownTerminal(t *testing.T) {
	if err := ValidatePassPrefix([]PassID{PassValidateSnapshot, PassTypedHIR}, PassTypedHIR); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		sequence []PassID
		terminal PassID
	}{
		{name: "truncated", sequence: []PassID{PassValidateSnapshot}, terminal: PassTypedHIR},
		{name: "reordered", sequence: []PassID{PassTypedHIR, PassValidateSnapshot}, terminal: PassTypedHIR},
		{name: "skipped", sequence: []PassID{PassTypedHIR}, terminal: PassTypedHIR},
		{name: "unknown terminal", sequence: nil, terminal: PassID("invented")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidatePassPrefix(test.sequence, test.terminal); err == nil {
				t.Fatalf("ValidatePassPrefix(%v, %q) succeeded", test.sequence, test.terminal)
			}
		})
	}
}
