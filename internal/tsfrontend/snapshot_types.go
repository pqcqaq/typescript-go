// Package tsfrontend owns live TypeScript Program and Checker capture. The
// serialized DTO contract itself lives in frontendwire so downstream replay
// cannot acquire checker dependencies transitively.
package tsfrontend

import "github.com/microsoft/typescript-go/internal/frontendwire"

const (
	SnapshotSchemaVersion       = frontendwire.SnapshotSchemaVersion
	LegacySnapshotSchemaVersion = frontendwire.LegacySnapshotSchemaVersion
)

type (
	FileID                       = frontendwire.FileID
	NodeID                       = frontendwire.NodeID
	OriginID                     = frontendwire.OriginID
	SymbolID                     = frontendwire.SymbolID
	TypeID                       = frontendwire.TypeID
	SignatureID                  = frontendwire.SignatureID
	ModuleID                     = frontendwire.ModuleID
	Span                         = frontendwire.Span
	ProgramSnapshot              = frontendwire.ProgramSnapshot
	ConfigSnapshot               = frontendwire.ConfigSnapshot
	TypeScriptPathMapping        = frontendwire.TypeScriptPathMapping
	TypeScriptOptions            = frontendwire.TypeScriptOptions
	ProvenanceSnapshot           = frontendwire.ProvenanceSnapshot
	FileSnapshot                 = frontendwire.FileSnapshot
	NodeSnapshot                 = frontendwire.NodeSnapshot
	NamedChildSnapshot           = frontendwire.NamedChildSnapshot
	NamedChild                   = frontendwire.NamedChild
	SyntaxPayload                = frontendwire.SyntaxPayload
	OriginSnapshot               = frontendwire.OriginSnapshot
	ConstantSnapshot             = frontendwire.ConstantSnapshot
	FlowFactSnapshot             = frontendwire.FlowFactSnapshot
	AssertionProofSnapshot       = frontendwire.AssertionProofSnapshot
	NonNullProofSnapshot         = frontendwire.NonNullProofSnapshot
	CaptureBindingSnapshot       = frontendwire.CaptureBindingSnapshot
	TypeSnapshot                 = frontendwire.TypeSnapshot
	PropertySnapshot             = frontendwire.PropertySnapshot
	TypePayload                  = frontendwire.TypePayload
	IndexInfoSnapshot            = frontendwire.IndexInfoSnapshot
	SymbolSnapshot               = frontendwire.SymbolSnapshot
	SignatureSnapshot            = frontendwire.SignatureSnapshot
	SignatureEffectProofSnapshot = frontendwire.SignatureEffectProofSnapshot
	SignatureEffectCallSnapshot  = frontendwire.SignatureEffectCallSnapshot
	ParameterSnapshot            = frontendwire.ParameterSnapshot
	TypePredicateSnapshot        = frontendwire.TypePredicateSnapshot
	ModuleSnapshot               = frontendwire.ModuleSnapshot
	ModuleSCCSnapshot            = frontendwire.ModuleSCCSnapshot
	ModuleBindingSnapshot        = frontendwire.ModuleBindingSnapshot
	ModuleEdge                   = frontendwire.ModuleEdge
	ImportAttribute              = frontendwire.ImportAttribute
)

func DecodeProgramSnapshot(data []byte) (*ProgramSnapshot, error) {
	return frontendwire.DecodeProgramSnapshot(data)
}
