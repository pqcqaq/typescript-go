package firstsliceoracle

import (
	"strings"
	"testing"
)

func TestNodeOracleScriptIdentityIsStable(t *testing.T) {
	first := ScriptHash()
	second := ScriptHash()
	if first != second || len(first) != 64 {
		t.Fatalf("script hash = %q / %q", first, second)
	}
	if !strings.Contains(nodeAddScript, "setBigUint64") || !strings.Contains(nodeAddScript, "getFloat64") || !strings.Contains(nodeAddScript, "setFloat64") {
		t.Fatal("Node oracle does not perform explicit binary64 conversion")
	}
}

func TestNodeOracleRejectsNonCanonicalBitsBeforeExecution(t *testing.T) {
	oracle := &NodeOracle{path: "must-not-run", version: LockedNodeVersion, scriptHash: ScriptHash()}
	if _, err := oracle.Add(t.Context(), "0", "0000000000000000"); err == nil {
		t.Fatal("short binary64 input was accepted")
	}
}

func TestNodeChooseScriptIdentityIsDistinctAndBooleanBound(t *testing.T) {
	if ChooseScriptHash() == ScriptHash() || !strings.Contains(nodeChooseScript, "flag===\"true\"") {
		t.Fatalf("choose Node script identity is not distinct or boolean-bound: %s", ChooseScriptHash())
	}
}

func TestNodeComputeScriptBindsLocalAssignmentAndDirectCall(t *testing.T) {
	if ComputeScriptHash() == ScriptHash() || ComputeScriptHash() == ChooseScriptHash() || !strings.Contains(nodeComputeScript, "value=add(left,right)") || !strings.Contains(nodeComputeScript, "value=value+right") {
		t.Fatalf("compute Node script does not bind source semantics: %s", ComputeScriptHash())
	}
}

func TestNodeLoopScriptBindsWhileAndPhiSourceSemantics(t *testing.T) {
	if LoopScriptHash() == ScriptHash() || LoopScriptHash() == ComputeScriptHash() || LoopScriptHash() == ChooseScriptHash() || !strings.Contains(nodeLoopScript, "while(value<limit)") || !strings.Contains(nodeLoopScript, "value=value+step") {
		t.Fatalf("loop Node script does not bind source semantics: %s", LoopScriptHash())
	}
}
