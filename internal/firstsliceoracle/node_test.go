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

func TestNodeClassifyScriptBindsConsecutiveIfAndLiteralSemantics(t *testing.T) {
	if ClassifyScriptHash() == ScriptHash() || ClassifyScriptHash() == LoopScriptHash() || !strings.Contains(nodeClassifyScript, "if(value<0)") || !strings.Contains(nodeClassifyScript, "if(value<1)") || !strings.Contains(nodeClassifyScript, "return -1") {
		t.Fatalf("classify Node script does not bind source semantics: %s", ClassifyScriptHash())
	}
}

func TestNodeCoalesceScriptBindsDistinctNullishTags(t *testing.T) {
	if CoalesceScriptHash() == ScriptHash() || CoalesceScriptHash() == ChooseScriptHash() || !strings.Contains(nodeCoalesceScript, `tag!=="number"`) || !strings.Contains(nodeCoalesceScript, `tag!=="undefined"`) || !strings.Contains(nodeCoalesceScript, "tag===\"number\"?fromBits(a):tag===\"null\"?null:undefined") {
		t.Fatalf("coalesce Node script does not preserve tag semantics: %s", CoalesceScriptHash())
	}
}

func TestNodeCoalesceAssignScriptUsesLogicalAssignment(t *testing.T) {
	if CoalesceAssignScriptHash() == CoalesceScriptHash() || !strings.Contains(nodeCoalesceAssignScript, "value??=fromBits(b)") {
		t.Fatalf("coalesce assignment Node script does not bind ??=: %s", CoalesceAssignScriptHash())
	}
}

func TestNodeClassAccessScriptBindsPrivateProtectedFixtureSemantics(t *testing.T) {
	if ClassAccessScriptHash() == DerivedCounterScriptHash() || !strings.Contains(nodeClassAccessScript, "secret=1") || !strings.Contains(nodeClassAccessScript, "value=2") || !strings.Contains(nodeClassAccessScript, "vault.readSecret(vault)+vault.readValue(vault)") {
		t.Fatalf("classaccess Node script does not bind source semantics: %s", ClassAccessScriptHash())
	}
}

func TestNodeObjectViewScriptPreservesIdentityRead(t *testing.T) {
	if ObjectViewScriptHash() == ObjectAliasScriptHash() || !strings.Contains(nodeObjectViewScript, "const view=source") || !strings.Contains(nodeObjectViewScript, "view.value") {
		t.Fatalf("object-view Node script does not preserve identity-read semantics: %s", ObjectViewScriptHash())
	}
}

func TestNodeObjectAccessorViewScriptPreservesReceiverAndTag(t *testing.T) {
	if ObjectAccessorViewScriptHash() == ObjectViewScriptHash() || !strings.Contains(nodeObjectAccessorViewScript, "const view=source") || !strings.Contains(nodeObjectAccessorViewScript, "get result(){return this.backing}") || !strings.Contains(nodeObjectAccessorViewScript, `result===undefined?"2"`) {
		t.Fatalf("accessor-view Node script does not bind receiver/tag semantics: %s", ObjectAccessorViewScriptHash())
	}
}

func TestNodeCheckedObjectCastScriptBindsExactShapeAndIdentity(t *testing.T) {
	if CheckedObjectCastScriptHash() == ObjectViewScriptHash() || !strings.Contains(nodeCheckedObjectCastScript, `hasOwnProperty.call(d,"value")`) || !strings.Contains(nodeCheckedObjectCastScript, `source.value`) {
		t.Fatalf("checked-cast Node script does not bind exact shape and identity: %s", CheckedObjectCastScriptHash())
	}
	oracle := &NodeOracle{path: "must-not-run", version: LockedNodeVersion, scriptHash: ScriptHash()}
	if _, err := oracle.CheckedObjectCast(t.Context(), "assertion", "0000000000000000"); err == nil {
		t.Fatal("invalid checked-cast shape was accepted")
	}
	if _, err := oracle.CheckedObjectCast(t.Context(), "matching", "0"); err == nil {
		t.Fatal("invalid checked-cast bits were accepted")
	}
}

func TestNodeObjectLayoutCopyScriptBindsNewIdentity(t *testing.T) {
	if ObjectLayoutCopyScriptHash() == ObjectViewScriptHash() || !strings.Contains(nodeObjectLayoutCopyScript, "copy!==source") || !strings.Contains(nodeObjectLayoutCopyScript, "source.value=1") {
		t.Fatalf("object-layout-copy Node script does not bind new-identity semantics: %s", ObjectLayoutCopyScriptHash())
	}
	oracle := &NodeOracle{path: "must-not-run", version: LockedNodeVersion, scriptHash: ScriptHash()}
	if _, err := oracle.ObjectLayoutCopy(t.Context(), "0"); err == nil {
		t.Fatal("invalid object-layout-copy bits were accepted")
	}
}
