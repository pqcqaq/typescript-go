package ast2bingo

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

const VERT013aReplaySchemaVersion uint32 = 1

type VERT013aReplayResult struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	Contract              bingo.ClassContract         `json:"contract"`
	HIR                   bingo.HIRModule             `json:"hir"`
	ContentHash           string                      `json:"contentHash"`
}

func (result VERT013aReplayResult) CanonicalBytes() ([]byte, error) {
	if result.SchemaVersion != VERT013aReplaySchemaVersion || result.FrontendSnapshotHash == "" || result.ContentHash == "" || bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity) != nil {
		return nil, fmt.Errorf("invalid VERT-013a replay identity")
	}
	if _, hash, err := bingo.CanonicalClassContract(result.Contract); err != nil || result.Contract.ContentHash != hash {
		return nil, fmt.Errorf("invalid VERT-013a class contract")
	}
	if err := bingo.VerifyCanonicalVERT013aClassHIR(result.HIR); err != nil || result.HIR.Provenance.FrontendSnapshotHash != result.FrontendSnapshotHash || result.HIR.Provenance.CompilerBuildIdentity != result.CompilerBuildIdentity || result.HIR.Classes == nil || result.HIR.Classes.ContentHash != result.Contract.ContentHash {
		return nil, fmt.Errorf("invalid VERT-013a class HIR binding")
	}
	withoutHash := result
	withoutHash.ContentHash = ""
	encoded, err := json.Marshal(withoutHash)
	if err != nil || hashBytes(encoded) != result.ContentHash {
		return nil, fmt.Errorf("VERT-013a replay content hash mismatch")
	}
	return json.Marshal(result)
}

func ReplayVERT013aFrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (VERT013aReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return VERT013aReplayResult{}, err
	}
	return ReplayVERT013aSnapshot(frontend.Program, identity)
}

func ReplayVERT013aSnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (VERT013aReplayResult, error) {
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return VERT013aReplayResult{}, err
	}
	contract, err := LowerVERT013aClassContract(snapshot)
	if err != nil {
		return VERT013aReplayResult{}, err
	}
	requirements := bingo.VERT013aLogicalCapabilities()
	digest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		return VERT013aReplayResult{}, err
	}
	hir, err := bingo.NewVERT013aClassHIR(primitiveHIRProvenance(snapshot, identity, digest), contract)
	if err != nil {
		return VERT013aReplayResult{}, err
	}
	result := VERT013aReplayResult{SchemaVersion: VERT013aReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, Contract: contract, HIR: hir}
	encoded, err := json.Marshal(result)
	if err != nil {
		return VERT013aReplayResult{}, err
	}
	result.ContentHash = hashBytes(encoded)
	return result, nil
}
