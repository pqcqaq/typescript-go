package ast2bingo

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func loadCheckedObjectCastSnapshot(t testing.TB) ProgramSnapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/checkedobjectcast/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program
}

func TestReplayCheckedObjectCastSnapshotBindsAmbientBoundaryAndReadonlyTarget(t *testing.T) {
	snapshot := loadCheckedObjectCastSnapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	first, err := ReplayCheckedObjectCastSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayCheckedObjectCastSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	left, err := first.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("checked object cast replay is not deterministic")
	}
	decoded, err := DecodeCheckedObjectCastReplay(left)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Cast.Boundary.Kind != "ffi-import" || len(decoded.Cast.Properties) != 1 || decoded.Cast.Properties[0] != "value" || !decoded.Cast.ReadonlyResult || !decoded.Cast.PreservesIdentity {
		t.Fatalf("checked object cast replay lost admission proof: %#v", decoded.Cast)
	}
}

func TestCheckedObjectCastReplayStrictDecoderRejectsTamper(t *testing.T) {
	snapshot := loadCheckedObjectCastSnapshot(t)
	result, err := ReplayCheckedObjectCastSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeCheckedObjectCastReplay(unknown); err == nil {
		t.Fatal("checked object cast replay accepted unknown member")
	}
	result.Cast.ReadonlyResult = false
	if _, err := result.CanonicalBytes(); err == nil {
		t.Fatal("checked object cast replay accepted substituted cast")
	}
	if _, err := DecodeCheckedObjectCastReplay(make([]byte, maxCheckedObjectCastReplayBytes+1)); err == nil {
		t.Fatal("checked object cast replay accepted oversized input")
	}
}

func TestCheckedObjectCastReplayRejectsFrontendEvidenceSubstitution(t *testing.T) {
	snapshot := loadCheckedObjectCastSnapshot(t)
	base, err := ReplayCheckedObjectCastSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CheckedObjectCastReplayResult){
		"frontend snapshot": func(result *CheckedObjectCastReplayResult) {
			result.FrontendSnapshotHash = strings.Repeat("d", 64)
		},
		"host symbol": func(result *CheckedObjectCastReplayResult) { result.Evidence.HostSymbolID = "symbol_forged" },
		"compiler identity": func(result *CheckedObjectCastReplayResult) {
			result.Evidence.CompilerBuildIdentity.ForkCommit = strings.Repeat("e", 40)
		},
		"host signature": func(result *CheckedObjectCastReplayResult) {
			result.Evidence.HostSignatureHash = strings.Repeat("a", 64)
		},
		"source type": func(result *CheckedObjectCastReplayResult) {
			result.Evidence.SourceUnknownTypeHash = strings.Repeat("b", 64)
		},
		"target type": func(result *CheckedObjectCastReplayResult) { result.Evidence.TargetTypeHash = strings.Repeat("c", 64) },
		"property":    func(result *CheckedObjectCastReplayResult) { result.Evidence.TargetPropertyKey = "other" },
		"case union": func(result *CheckedObjectCastReplayResult) {
			result.Evidence.CaseUnionTypeHash = strings.Repeat("f", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := base
			mutate(&result)
			result.ContentHash = ""
			_, hash, err := canonicalCheckedObjectCastReplay(result)
			if err == nil {
				t.Fatalf("checked object cast replay accepted substituted %s with hash %s", name, hash)
			}
		})
	}
}

func TestCheckedObjectCastReplayRejectsRehashedFrontendEvidenceChain(t *testing.T) {
	snapshot := loadCheckedObjectCastSnapshot(t)
	result, err := ReplayCheckedObjectCastSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	result.Evidence.FrontendSnapshotHash = strings.Repeat("d", 64)
	_, evidenceHash, err := canonicalCheckedObjectCastFrontendEvidence(result.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	result.Evidence.ContentHash = evidenceHash
	result.Cast.Boundary.SourceID = "frontend-evidence:" + evidenceHash
	_, boundaryHash, err := bingo.CanonicalDynamicObjectBoundary(result.Cast.Boundary)
	if err != nil {
		t.Fatal(err)
	}
	result.Cast.Boundary.ContentHash = boundaryHash
	_, castHash, err := bingo.CanonicalCheckedObjectCast(result.Cast)
	if err != nil {
		t.Fatal(err)
	}
	result.Cast.ContentHash = castHash
	result.ContentHash = ""
	if _, _, err := canonicalCheckedObjectCastReplay(result); err == nil {
		t.Fatal("checked object cast replay accepted rehashed evidence for another frontend")
	}
	for _, schema := range []uint32{1, 2} {
		result.SchemaVersion = schema
		if _, _, err := canonicalCheckedObjectCastReplay(result); err == nil {
			t.Fatalf("checked object cast replay accepted stale schema v%d", schema)
		}
	}
}

func TestCheckedObjectCastReplayRejectsOuterCompilerIdentitySubstitution(t *testing.T) {
	snapshot := loadCheckedObjectCastSnapshot(t)
	result, err := ReplayCheckedObjectCastSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	result.CompilerBuildIdentity.ForkCommit = strings.Repeat("e", 40)
	result.ContentHash = ""
	if _, _, err := canonicalCheckedObjectCastReplay(result); err == nil {
		t.Fatal("checked object cast replay accepted another valid outer compiler identity")
	}
}

func TestCheckedObjectCastReplayRejectsRehashedEmbeddedSnapshotSubstitution(t *testing.T) {
	snapshot := loadCheckedObjectCastSnapshot(t)
	result, err := ReplayCheckedObjectCastSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	for i := range result.FrontendSnapshot.Types {
		if result.FrontendSnapshot.Types[i].DebugText == "HostValue" {
			result.FrontendSnapshot.Types[i].DebugText = "ForgedHostValue"
			break
		}
	}
	if err := finalizeTestSnapshot(&result.FrontendSnapshot); err != nil {
		t.Fatal(err)
	}
	result.FrontendSnapshotHash = result.FrontendSnapshot.ContentHash
	result.ContentHash = ""
	if _, _, err := canonicalCheckedObjectCastReplay(result); err == nil {
		t.Fatal("checked object cast replay accepted rehashed embedded frontend substitution")
	}
	result.SchemaVersion = 3
	if _, _, err := canonicalCheckedObjectCastReplay(result); err == nil {
		t.Fatal("checked object cast replay accepted stale schema v3")
	}
}

func TestCheckedObjectCastDerivationIgnoresDiagnosticTypeText(t *testing.T) {
	base := loadCheckedObjectCastSnapshot(t)
	identity := testCompilerIdentity(t, base)
	want, err := ReplayCheckedObjectCastSnapshot(base, identity)
	if err != nil {
		t.Fatal(err)
	}
	changed := cloneCheckedObjectCastTestSnapshot(base)
	for i := range changed.Types {
		if changed.Types[i].Symbol != "" && changed.Types[i].DebugText == "HostValue" {
			changed.Types[i].DebugText = "diagnostic-only-renamed"
		}
	}
	if err := finalizeTestSnapshot(&changed); err != nil {
		t.Fatal(err)
	}
	got, err := ReplayCheckedObjectCastSnapshot(changed, testCompilerIdentity(t, changed))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cast.Target.ContentHash != want.Cast.Target.ContentHash || got.Cast.TargetLayout.ContentHash != want.Cast.TargetLayout.ContentHash {
		t.Fatal("diagnostic-only type text changed checked object cast semantics")
	}
}

func TestCheckedObjectCastDerivationRejectsCanonicalTargetIdentityTamper(t *testing.T) {
	base := loadCheckedObjectCastSnapshot(t)
	for name, mutate := range map[string]func(*ProgramSnapshot) bool{
		"interface name": func(snapshot *ProgramSnapshot) bool {
			indexes := indexPrimitiveSnapshot(*snapshot)
			for _, declaration := range snapshot.Nodes {
				if declaration.Kind != "KindInterfaceDeclaration" {
					continue
				}
				nameID := childByRole(declaration, "child[0]")
				for i := range snapshot.Nodes {
					if snapshot.Nodes[i].ID == nameID && indexes.Nodes[nameID].SyntaxPayload.Text == "HostValue" {
						snapshot.Nodes[i].SyntaxPayload.Text = "OtherValue"
						return true
					}
				}
			}
			return false
		},
		"symbol declaration": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Symbols {
				if snapshot.Symbols[i].Name == "HostValue" {
					snapshot.Symbols[i].Declarations = nil
					return true
				}
			}
			return false
		},
		"type symbol": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Types {
				if snapshot.Types[i].DebugText == "HostValue" {
					snapshot.Types[i].Symbol = "symbol_forged"
					return true
				}
			}
			return false
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := cloneCheckedObjectCastTestSnapshot(base)
			if !mutate(&snapshot) {
				t.Fatal("fixture did not expose tamper target")
			}
			if err := finalizeTestSnapshot(&snapshot); err != nil {
				t.Fatalf("rehash target identity tamper: %v", err)
			}
			if _, err := ReplayCheckedObjectCastSnapshot(snapshot, testCompilerIdentity(t, snapshot)); err == nil {
				t.Fatal("checked object cast accepted canonical target identity tamper")
			}
		})
	}
}

func TestReplayCheckedObjectCastSnapshotRejectsRehashedSourceTamper(t *testing.T) {
	base := loadCheckedObjectCastSnapshot(t)
	hostDeclaration := checkedObjectCastHostDeclarationID(base)
	for name, mutate := range map[string]func(*ProgramSnapshot) bool{
		"host return": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Signatures {
				if snapshot.Signatures[i].Declaration == hostDeclaration {
					snapshot.Signatures[i].ReturnType = 3
					return true
				}
			}
			return false
		},
		"host effect": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Signatures {
				if snapshot.Signatures[i].Declaration == hostDeclaration {
					snapshot.Signatures[i].Effects = []string{"read"}
					return true
				}
			}
			return false
		},
		"target writable": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Types {
				if snapshot.Types[i].DebugText == "HostValue" {
					snapshot.Types[i].PropertyFacts[0].Readonly = false
					snapshot.Types[i].PropertyFacts[0].WriteType = snapshot.Types[i].PropertyFacts[0].ReadType
					return true
				}
			}
			return false
		},
		"target setter": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Types {
				if snapshot.Types[i].DebugText == "HostValue" {
					snapshot.Types[i].PropertyFacts[0].HasSetter = true
					return true
				}
			}
			return false
		},
		"target property parent": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Symbols {
				if snapshot.Symbols[i].Name == "value" && snapshot.Symbols[i].Parent != "" {
					snapshot.Symbols[i].Parent = "symbol_forged"
					return true
				}
			}
			return false
		},
		"target property declaration": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Symbols {
				if snapshot.Symbols[i].Name == "value" && snapshot.Symbols[i].Parent != "" {
					snapshot.Symbols[i].Declarations = nil
					snapshot.Symbols[i].ValueDeclaration = ""
					return true
				}
			}
			return false
		},
		"target property type": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Types {
				if snapshot.Types[i].DebugText == "HostValue" {
					snapshot.Types[i].PropertyFacts[0].ReadType = 1
					return true
				}
			}
			return false
		},
		"assertion node": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Nodes {
				if snapshot.Nodes[i].Kind == snapshotKindTypeReference {
					snapshot.Nodes[i].Kind = snapshotKindAsExpression
					snapshot.Nodes[i].SyntaxPayload.Tag = snapshotKindAsExpression
					return true
				}
			}
			return false
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := cloneCheckedObjectCastTestSnapshot(base)
			if !mutate(&snapshot) {
				t.Fatal("fixture did not expose tamper target")
			}
			if err := finalizeTestSnapshot(&snapshot); err != nil {
				t.Fatalf("rehash tampered snapshot: %v", err)
			}
			if _, err := ReplayCheckedObjectCastSnapshot(snapshot, testCompilerIdentity(t, snapshot)); err == nil {
				t.Fatal("checked object cast replay accepted rehashed source tamper")
			}
		})
	}
}

func checkedObjectCastHostDeclarationID(snapshot ProgramSnapshot) NodeID {
	indexes := indexPrimitiveSnapshot(snapshot)
	for _, node := range snapshot.Nodes {
		if node.Kind == snapshotKindFunctionDeclaration && childText(node, "name", indexes.Nodes) == "hostObject" {
			return node.ID
		}
	}
	return ""
}

func cloneCheckedObjectCastTestSnapshot(base ProgramSnapshot) ProgramSnapshot {
	result := base
	result.Nodes = append([]NodeSnapshot(nil), base.Nodes...)
	for i := range result.Nodes {
		result.Nodes[i].Children = append([]NodeID(nil), base.Nodes[i].Children...)
		result.Nodes[i].NamedChildren = append([]frontendwire.NamedChildSnapshot(nil), base.Nodes[i].NamedChildren...)
	}
	result.Types = append([]TypeSnapshot(nil), base.Types...)
	for i := range result.Types {
		result.Types[i].PropertyFacts = append([]frontendwire.PropertySnapshot(nil), base.Types[i].PropertyFacts...)
	}
	result.Signatures = append([]SignatureSnapshot(nil), base.Signatures...)
	for i := range result.Signatures {
		result.Signatures[i].Parameters = append([]SymbolID(nil), base.Signatures[i].Parameters...)
		result.Signatures[i].Effects = append([]string(nil), base.Signatures[i].Effects...)
	}
	result.Symbols = append([]SymbolSnapshot(nil), base.Symbols...)
	for i := range result.Symbols {
		result.Symbols[i].Declarations = append([]NodeID(nil), base.Symbols[i].Declarations...)
	}
	return result
}

func FuzzDecodeCheckedObjectCastReplay(f *testing.F) {
	snapshot := loadCheckedObjectCastSnapshot(f)
	result, err := ReplayCheckedObjectCastSnapshot(snapshot, testCompilerIdentity(f, snapshot))
	if err != nil {
		f.Fatal(err)
	}
	encoded, err := result.CanonicalBytes()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeCheckedObjectCastReplay(data)
	})
}
