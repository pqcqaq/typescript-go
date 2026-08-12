package ast2bingo

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

const VERT013bReplaySchemaVersion uint32 = 1

type VERT013bReplayResult struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	Contract              bingo.VERT013bClassContract `json:"contract"`
	HIR                   bingo.HIRModule             `json:"hir"`
	ContentHash           string                      `json:"contentHash"`
}

func (result VERT013bReplayResult) CanonicalBytes() ([]byte, error) {
	if result.SchemaVersion != VERT013bReplaySchemaVersion || result.FrontendSnapshotHash == "" || result.ContentHash == "" || bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity) != nil {
		return nil, fmt.Errorf("invalid VERT-013b replay identity")
	}
	if err := bingo.VerifyCanonicalVERT013bClassContract(result.Contract); err != nil {
		return nil, fmt.Errorf("invalid VERT-013b contract: %w", err)
	}
	if err := bingo.VerifyCanonicalVERT013bDerivedHIR(result.HIR); err != nil || result.HIR.Provenance.FrontendSnapshotHash != result.FrontendSnapshotHash || result.HIR.Provenance.CompilerBuildIdentity != result.CompilerBuildIdentity || result.HIR.DerivedClasses == nil || result.HIR.DerivedClasses.ContentHash != result.Contract.ContentHash {
		return nil, fmt.Errorf("invalid VERT-013b HIR binding")
	}
	without := result
	without.ContentHash = ""
	encoded, err := json.Marshal(without)
	if err != nil || hashBytes(encoded) != result.ContentHash {
		return nil, fmt.Errorf("VERT-013b replay content hash mismatch")
	}
	return json.Marshal(result)
}

func ReplayVERT013bFrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (VERT013bReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return VERT013bReplayResult{}, err
	}
	return ReplayVERT013bSnapshot(frontend.Program, identity)
}

func ReplayVERT013bSnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (VERT013bReplayResult, error) {
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return VERT013bReplayResult{}, err
	}
	contract, err := LowerVERT013bClassContract(snapshot)
	if err != nil {
		return VERT013bReplayResult{}, err
	}
	requirements := bingo.VERT013bLogicalCapabilities()
	digest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		return VERT013bReplayResult{}, err
	}
	hir, err := bingo.NewVERT013bDerivedHIR(primitiveHIRProvenance(snapshot, identity, digest), contract)
	if err != nil {
		return VERT013bReplayResult{}, err
	}
	result := VERT013bReplayResult{SchemaVersion: VERT013bReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, Contract: contract, HIR: hir}
	encoded, err := json.Marshal(result)
	if err != nil {
		return VERT013bReplayResult{}, err
	}
	result.ContentHash = hashBytes(encoded)
	return result, nil
}
