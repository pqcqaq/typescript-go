package ast2bingo

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

const VERT012ReplaySchemaVersion uint32 = 1

// VERT012ReplayResult binds the snapshot capture proof to the closure contract.
type VERT012ReplayResult struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	Contract              bingo.ClosureContract       `json:"contract"`
	HIR                   bingo.HIRModule             `json:"hir"`
	ContentHash           string                      `json:"contentHash"`
}

func (result VERT012ReplayResult) CanonicalBytes() ([]byte, error) {
	if result.SchemaVersion != VERT012ReplaySchemaVersion || result.FrontendSnapshotHash == "" || result.ContentHash == "" || bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity) != nil {
		return nil, fmt.Errorf("invalid VERT-012 replay identity")
	}
	if _, hash, err := bingo.CanonicalClosureContract(result.Contract); err != nil || result.Contract.ContentHash != hash {
		return nil, fmt.Errorf("invalid VERT-012 closure contract")
	}
	if err := bingo.VerifyCanonicalVERT012ClosureHIR(result.HIR); err != nil || result.HIR.Provenance.FrontendSnapshotHash != result.FrontendSnapshotHash || result.HIR.Provenance.CompilerBuildIdentity != result.CompilerBuildIdentity || result.HIR.Closures == nil || result.HIR.Closures.ContentHash != result.Contract.ContentHash {
		return nil, fmt.Errorf("invalid VERT-012 closure HIR binding")
	}
	withoutHash := result
	withoutHash.ContentHash = ""
	encoded, err := json.Marshal(withoutHash)
	if err != nil || hashBytes(encoded) != result.ContentHash {
		return nil, fmt.Errorf("VERT-012 replay content hash mismatch")
	}
	return json.Marshal(result)
}

func ReplayVERT012FrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (VERT012ReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return VERT012ReplayResult{}, err
	}
	return ReplayVERT012Snapshot(frontend.Program, identity)
}

// ReplayVERT012Snapshot accepts only closurecounter's single mutable capture.
func ReplayVERT012Snapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (VERT012ReplayResult, error) {
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return VERT012ReplayResult{}, err
	}
	indexes := indexPrimitiveSnapshot(snapshot)
	var arrow NodeSnapshot
	for _, node := range snapshot.Nodes {
		if node.Kind == "KindArrowFunction" {
			if arrow.ID != "" {
				return VERT012ReplayResult{}, fmt.Errorf("VERT-012 requires exactly one arrow function")
			}
			arrow = node
		}
	}
	if arrow.ID == "" || !arrow.CaptureComplete || len(arrow.CaptureSet) != 1 || len(arrow.CaptureBindings) != 1 {
		return VERT012ReplayResult{}, fmt.Errorf("VERT-012 requires one complete capture")
	}
	binding := arrow.CaptureBindings[0]
	if binding.Kind != "binding" || binding.Symbol != arrow.CaptureSet[0] || binding.Access != "readwrite" || !binding.Mutable {
		return VERT012ReplayResult{}, fmt.Errorf("VERT-012 capture must be mutable readwrite binding")
	}
	symbol, ok := indexes.Symbols[binding.Symbol]
	if !ok || symbol.Name != "count" || symbol.ValueDeclaration == "" {
		return VERT012ReplayResult{}, fmt.Errorf("VERT-012 count symbol is invalid")
	}
	declaration, ok := indexes.Nodes[symbol.ValueDeclaration]
	if !ok || declaration.Kind != snapshotKindVariableDeclaration || declaration.Module != arrow.Module {
		return VERT012ReplayResult{}, fmt.Errorf("VERT-012 count declaration is invalid")
	}
	if typ, err := bingoType(symbol.Type, indexes.Types); err != nil || typ != bingo.TypeNumber {
		return VERT012ReplayResult{}, fmt.Errorf("VERT-012 count must be number")
	}
	var signature SignatureSnapshot
	for _, candidate := range indexes.Signatures {
		if candidate.Declaration == arrow.ID {
			if signature.ID != 0 {
				return VERT012ReplayResult{}, fmt.Errorf("VERT-012 arrow signature is ambiguous")
			}
			signature = candidate
		}
	}
	if signature.ID == 0 || len(signature.Parameters) != 0 || signature.MinArgumentCount != 0 || signature.HasRest || signature.ThisParameter != "" {
		return VERT012ReplayResult{}, fmt.Errorf("VERT-012 arrow signature is invalid")
	}
	if typ, err := bingoType(signature.ReturnType, indexes.Types); err != nil || typ != bingo.TypeNumber {
		return VERT012ReplayResult{}, fmt.Errorf("VERT-012 arrow return must be number")
	}
	contract := bingo.ClosureContract{SchemaVersion: bingo.ClosureContractSchemaVersion,
		Environments: []bingo.ClosureEnvironmentContract{{ID: 1, HeapOwned: true, FieldCount: 1, TraceCount: 1}},
		Functions: []bingo.ClosureFunctionContract{{ID: 1, SymbolKey: string(arrow.ID), Signature: "cdecl(ptr)->f64", Escapes: true, EnvironmentID: 1,
			Captures: []bingo.ClosureCapture{{ID: 1, SymbolKey: string(binding.Symbol), Type: bingo.TypeNumber, Mutable: true, Mode: bingo.ClosureCaptureByCell, Storage: bingo.ClosureStorageHeap, EnvironmentSlot: 0, Traced: true}}}}}
	_, hash, err := bingo.CanonicalClosureContract(contract)
	if err != nil {
		return VERT012ReplayResult{}, err
	}
	contract.ContentHash = hash
	requirements := bingo.VERT012LogicalCapabilities()
	digest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		return VERT012ReplayResult{}, err
	}
	hir, err := bingo.NewVERT012ClosureHIR(primitiveHIRProvenance(snapshot, identity, digest), contract)
	if err != nil {
		return VERT012ReplayResult{}, err
	}
	result := VERT012ReplayResult{SchemaVersion: VERT012ReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, Contract: contract, HIR: hir}
	encoded, err := json.Marshal(result)
	if err != nil {
		return VERT012ReplayResult{}, err
	}
	result.ContentHash = hashBytes(encoded)
	return result, nil
}
