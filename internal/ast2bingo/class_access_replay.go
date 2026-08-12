package ast2bingo

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

const ClassAccessReplaySchemaVersion uint32 = 2

type ClassAccessReplayResult struct {
	SchemaVersion         uint32                             `json:"schemaVersion"`
	FrontendSnapshotHash  string                             `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity        `json:"compilerBuildIdentity"`
	Contract              bingo.ClassAccessContract          `json:"contract"`
	Execution             bingo.ClassAccessExecutionContract `json:"execution"`
	HIR                   bingo.HIRModule                    `json:"hir"`
	ContentHash           string                             `json:"contentHash"`
}

func (result ClassAccessReplayResult) CanonicalBytes() ([]byte, error) {
	if result.SchemaVersion != ClassAccessReplaySchemaVersion || result.FrontendSnapshotHash == "" || result.ContentHash == "" || bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity) != nil {
		return nil, fmt.Errorf("invalid OBJ-003b access replay identity")
	}
	if err := bingo.VerifyCanonicalClassAccessContract(result.Contract); err != nil {
		return nil, fmt.Errorf("invalid OBJ-003b access contract: %w", err)
	}
	if err := bingo.VerifyCanonicalClassAccessExecution(result.Execution); err != nil || result.Execution.ClassAccessHash != result.Contract.ContentHash {
		return nil, fmt.Errorf("invalid OBJ-003b execution contract binding")
	}
	if err := bingo.VerifyCanonicalClassAccessHIR(result.HIR); err != nil || result.HIR.Provenance.FrontendSnapshotHash != result.FrontendSnapshotHash || result.HIR.Provenance.CompilerBuildIdentity != result.CompilerBuildIdentity || result.HIR.ClassAccess == nil || result.HIR.ClassAccess.ContentHash != result.Contract.ContentHash {
		return nil, fmt.Errorf("invalid OBJ-003b access HIR binding")
	}
	without := result
	without.ContentHash = ""
	encoded, err := json.Marshal(without)
	if err != nil || hashBytes(encoded) != result.ContentHash {
		return nil, fmt.Errorf("OBJ-003b access replay content hash mismatch")
	}
	return json.Marshal(result)
}

func ReplayClassAccessFrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (ClassAccessReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return ClassAccessReplayResult{}, err
	}
	return ReplayClassAccessSnapshot(frontend.Program, identity)
}

func ReplayClassAccessSnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (ClassAccessReplayResult, error) {
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return ClassAccessReplayResult{}, err
	}
	replay, err := LowerClassAccessReplay(snapshot)
	if err != nil {
		return ClassAccessReplayResult{}, err
	}
	requirements := bingo.ClassAccessLogicalCapabilities()
	digest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		return ClassAccessReplayResult{}, err
	}
	hir, err := bingo.NewClassAccessHIR(primitiveHIRProvenance(snapshot, identity, digest), replay.Contract)
	if err != nil {
		return ClassAccessReplayResult{}, err
	}
	result := ClassAccessReplayResult{SchemaVersion: ClassAccessReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, Contract: replay.Contract, Execution: replay.Execution, HIR: hir}
	encoded, err := json.Marshal(result)
	if err != nil {
		return ClassAccessReplayResult{}, err
	}
	result.ContentHash = hashBytes(encoded)
	return result, nil
}
