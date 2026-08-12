package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const HIRVarianceGateSchemaVersion uint32 = 1
const maxHIRVarianceGateBytes = 2 << 20

type HIRVarianceGate struct {
	SchemaVersion uint32                     `json:"schemaVersion"`
	FunctionID    FunctionID                 `json:"functionId"`
	HIR           HIRModule                  `json:"hir"`
	Conversion    HIRVarianceConversionProof `json:"conversion"`
	ContentHash   string                     `json:"contentHash"`
}

func BuildHIRVarianceGate(hir HIRModule, functionID FunctionID, conversion HIRVarianceConversionProof) (HIRVarianceGate, error) {
	gate := HIRVarianceGate{SchemaVersion: HIRVarianceGateSchemaVersion, FunctionID: functionID, HIR: hir, Conversion: conversion}
	_, hash, err := CanonicalHIRVarianceGate(gate)
	gate.ContentHash = hash
	return gate, err
}

func CanonicalHIRVarianceGate(gate HIRVarianceGate) ([]byte, string, error) {
	gate.ContentHash = ""
	if gate.SchemaVersion != HIRVarianceGateSchemaVersion || gate.FunctionID == 0 {
		return nil, "", fmt.Errorf("invalid HIR variance gate header")
	}
	if err := verifyCanonicalKnownHIR(gate.HIR); err != nil {
		return nil, "", err
	}
	if err := VerifyCanonicalHIRVarianceConversionProof(gate.Conversion); err != nil {
		return nil, "", err
	}
	functionFound, valueFound := false, false
	for _, function := range gate.HIR.Functions {
		if function.ID != gate.FunctionID {
			continue
		}
		functionFound = true
		for _, block := range function.Blocks {
			for _, operation := range block.Operations {
				if operation.ID == ValueID(gate.Conversion.HIRValueID) {
					if valueFound {
						return nil, "", fmt.Errorf("HIR variance value is duplicated")
					}
					valueFound = true
					if operation.Type != TypeObject || operation.ObjectTypeKey != gate.Conversion.SourceTypeKey {
						return nil, "", fmt.Errorf("HIR variance source value/type binding mismatch")
					}
				}
			}
		}
	}
	if !functionFound || !valueFound {
		return nil, "", fmt.Errorf("HIR variance function/value binding is missing")
	}
	encoded, err := jsonx.Marshal(gate)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	gate.ContentHash = hash
	encoded, err = jsonx.Marshal(gate)
	return encoded, hash, err
}

func VerifyCanonicalHIRVarianceGate(gate HIRVarianceGate) error {
	claimed := gate.ContentHash
	_, want, err := CanonicalHIRVarianceGate(gate)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("HIR variance gate content hash mismatch")
	}
	return nil
}

func DecodeHIRVarianceGate(data []byte) (*HIRVarianceGate, error) {
	if len(data) > maxHIRVarianceGateBytes {
		return nil, fmt.Errorf("HIR variance gate exceeds %d bytes", maxHIRVarianceGateBytes)
	}
	var gate HIRVarianceGate
	if err := jsonx.Unmarshal(data, &gate, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode HIR variance gate: %w", err)
	}
	if err := VerifyCanonicalHIRVarianceGate(gate); err != nil {
		return nil, err
	}
	return &gate, nil
}

func verifyCanonicalKnownHIR(module HIRModule) error {
	switch module.SchemaVersion {
	case HIRSchemaVersion:
		return VerifyCanonicalPhase2HIR(module)
	case VERT010HIRSchemaVersion:
		return VerifyCanonicalVERT010ObjectHIR(module)
	case VERT011HIRSchemaVersion:
		return VerifyCanonicalVERT011PlaceHIR(module)
	case VERT012HIRSchemaVersion:
		return VerifyCanonicalVERT012ClosureHIR(module)
	case VERT013aHIRSchemaVersion:
		return VerifyCanonicalVERT013aClassHIR(module)
	case VERT013bHIRSchemaVersion:
		return VerifyCanonicalVERT013bDerivedHIR(module)
	case ClassAccessHIRSchemaVersion:
		return VerifyCanonicalClassAccessHIR(module)
	default:
		return fmt.Errorf("unsupported HIR variance gate reader %d", module.SchemaVersion)
	}
}
