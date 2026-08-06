// Package frontendwire owns the checker-free serialized frontend contract.
// It contains only immutable DTOs plus strict decoding and validation.
package frontendwire

import (
	"encoding/json"
	"fmt"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// SnapshotSchemaVersion is the major version of the serialized frontend
// contract. Readers must reject unsupported major versions.
const (
	SnapshotSchemaVersion       uint32 = 2
	LegacySnapshotSchemaVersion uint32 = 1
)

// FileID identifies a source file by canonical project-relative identity.
type FileID string

// NodeID identifies a source node by file, kind, span, and occurrence.
type NodeID string

// OriginID identifies the source provenance attached to a frontend entity and
// later HIR/MIR/backend artifacts.
type OriginID string

// SymbolID identifies a symbol by its declarations, name, and parent.
type SymbolID string

// TypeID is a deterministic dense identifier within one ProgramSnapshot.
// CanonicalHash on TypeSnapshot is the persistent cross-build identity.
type TypeID uint32

// SignatureID is a deterministic dense identifier within one ProgramSnapshot.
type SignatureID uint32

// ModuleID identifies a resolved module and its resolution mode.
type ModuleID string

// Span is a half-open UTF-8 byte range in a source file.
type Span struct {
	File  FileID `json:"file,omitempty"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// ProgramSnapshot is the immutable handoff between typescript-go and later
// ts2bin stages. It contains no AST, checker, type, signature, or symbol
// pointers. Callers must treat all slices as read-only after Build returns.
type ProgramSnapshot struct {
	SchemaVersion     uint32              `json:"schemaVersion"`
	Config            ConfigSnapshot      `json:"config"`
	Provenance        ProvenanceSnapshot  `json:"provenance"`
	Files             []FileSnapshot      `json:"files"`
	Modules           []ModuleSnapshot    `json:"modules"`
	ModuleEdges       []ModuleEdge        `json:"moduleEdges"`
	ModuleSCCs        []ModuleSCCSnapshot `json:"moduleSccs,omitempty"`
	ModuleGraphDigest string              `json:"moduleGraphDigest"`
	Origins           []OriginSnapshot    `json:"origins"`
	Nodes             []NodeSnapshot      `json:"nodes"`
	Types             []TypeSnapshot      `json:"types"`
	Symbols           []SymbolSnapshot    `json:"symbols"`
	Signatures        []SignatureSnapshot `json:"signatures"`
	Diagnostics       []Diagnostic        `json:"diagnostics,omitempty"`
	ContentHash       string              `json:"contentHash"`
}

// ConfigSnapshot records the normalized Bingo and TypeScript options that can
// change frontend semantics.
type ConfigSnapshot struct {
	BingoSchemaVersion   int               `json:"bingoSchemaVersion"`
	Bingo                BingoOptions      `json:"bingo"`
	BingoDigest          string            `json:"bingoDigest"`
	TypeScript           TypeScriptOptions `json:"typescript"`
	TypeScriptDigest     string            `json:"typescriptDigest"`
	CanonicalConfigPath  string            `json:"canonicalConfigPath,omitempty"`
	CanonicalProjectRoot string            `json:"canonicalProjectRoot"`
}

// TypeScriptPathMapping is a canonical paths alias entry. Substitution order
// is significant because TypeScript tries the entries in order.
type TypeScriptPathMapping struct {
	Pattern       string   `json:"pattern"`
	Substitutions []string `json:"substitutions"`
}

// TypeScriptOptions is the stable subset of compiler options that affects
// snapshot semantics. It intentionally excludes output-only and transient
// command-line fields.
type TypeScriptOptions struct {
	Strict                                    bool                    `json:"strict"`
	StrictNullChecks                          bool                    `json:"strictNullChecks"`
	StrictFunctionTypes                       bool                    `json:"strictFunctionTypes"`
	NoImplicitAny                             bool                    `json:"noImplicitAny"`
	NoImplicitThis                            bool                    `json:"noImplicitThis"`
	StrictBindCallApply                       bool                    `json:"strictBindCallApply"`
	StrictBuiltinIteratorReturn               bool                    `json:"strictBuiltinIteratorReturn"`
	StrictPropertyInitialization              bool                    `json:"strictPropertyInitialization"`
	UseUnknownInCatchVariables                bool                    `json:"useUnknownInCatchVariables"`
	ExactOptionalPropertyTypes                bool                    `json:"exactOptionalPropertyTypes"`
	NoUncheckedIndexedAccess                  bool                    `json:"noUncheckedIndexedAccess"`
	NoPropertyAccessFromIndexSig              bool                    `json:"noPropertyAccessFromIndexSignature"`
	NoImplicitReturns                         bool                    `json:"noImplicitReturns"`
	NoFallthroughCasesInSwitch                bool                    `json:"noFallthroughCasesInSwitch"`
	NoUnusedLocals                            bool                    `json:"noUnusedLocals"`
	NoUnusedParameters                        bool                    `json:"noUnusedParameters"`
	AllowUnreachableCode                      bool                    `json:"allowUnreachableCode"`
	AllowUnusedLabels                         bool                    `json:"allowUnusedLabels"`
	AllowJS                                   bool                    `json:"allowJs"`
	CheckJS                                   bool                    `json:"checkJs"`
	AllowArbitraryExtensions                  bool                    `json:"allowArbitraryExtensions"`
	AllowNonTsExtensions                      bool                    `json:"allowNonTsExtensions"`
	AllowImportingTSExtensions                bool                    `json:"allowImportingTsExtensions"`
	RewriteRelativeImportExtensions           bool                    `json:"rewriteRelativeImportExtensions"`
	AllowUmdGlobalAccess                      bool                    `json:"allowUmdGlobalAccess"`
	AlwaysStrict                              bool                    `json:"alwaysStrict"`
	UseDefineForClassFields                   bool                    `json:"useDefineForClassFields"`
	VerbatimModuleSyntax                      bool                    `json:"verbatimModuleSyntax"`
	ExperimentalDecorators                    bool                    `json:"experimentalDecorators"`
	EmitDecoratorMetadata                     bool                    `json:"emitDecoratorMetadata"`
	ESModuleInterop                           bool                    `json:"esModuleInterop"`
	AllowSyntheticDefaultImports              bool                    `json:"allowSyntheticDefaultImports"`
	IsolatedModules                           bool                    `json:"isolatedModules"`
	IsolatedDeclarations                      bool                    `json:"isolatedDeclarations"`
	ErasableSyntaxOnly                        bool                    `json:"erasableSyntaxOnly"`
	NoCheck                                   bool                    `json:"noCheck"`
	DisableSizeLimit                          bool                    `json:"disableSizeLimit"`
	NoErrorTruncation                         bool                    `json:"noErrorTruncation"`
	AssumeChangesOnlyAffectDirectDependencies bool                    `json:"assumeChangesOnlyAffectDirectDependencies"`
	NoImplicitOverride                        bool                    `json:"noImplicitOverride"`
	ForceConsistentCasing                     bool                    `json:"forceConsistentCasingInFileNames"`
	NoResolve                                 bool                    `json:"noResolve"`
	NoDTSResolution                           bool                    `json:"noDtsResolution"`
	NoLib                                     bool                    `json:"noLib"`
	NoUncheckedSideEffectImports              bool                    `json:"noUncheckedSideEffectImports"`
	PreserveSymlinks                          bool                    `json:"preserveSymlinks"`
	SkipLibCheck                              bool                    `json:"skipLibCheck"`
	SkipDefaultLibCheck                       bool                    `json:"skipDefaultLibCheck"`
	DeduplicatePackages                       bool                    `json:"deduplicatePackages"`
	LibReplacement                            bool                    `json:"libReplacement"`
	StableTypeOrdering                        bool                    `json:"stableTypeOrdering"`
	PreserveConstEnums                        bool                    `json:"preserveConstEnums"`
	MaxNodeModuleJSDepth                      int                     `json:"maxNodeModuleJsDepth"`
	Target                                    string                  `json:"target"`
	Module                                    string                  `json:"module"`
	ModuleResolution                          string                  `json:"moduleResolution"`
	ModuleDetection                           string                  `json:"moduleDetection"`
	ModuleSuffixes                            []string                `json:"moduleSuffixes,omitempty"`
	BaseURL                                   string                  `json:"baseUrl,omitempty"`
	Paths                                     []TypeScriptPathMapping `json:"paths,omitempty"`
	RootDirs                                  []string                `json:"rootDirs,omitempty"`
	TypeRoots                                 []string                `json:"typeRoots,omitempty"`
	Types                                     []string                `json:"types,omitempty"`
	JSX                                       string                  `json:"jsx"`
	JSXFactory                                string                  `json:"jsxFactory,omitempty"`
	JSXFragmentFactory                        string                  `json:"jsxFragmentFactory,omitempty"`
	JSXImportSource                           string                  `json:"jsxImportSource,omitempty"`
	ReactNamespace                            string                  `json:"reactNamespace,omitempty"`
	Lib                                       []string                `json:"lib,omitempty"`
	CustomConditions                          []string                `json:"customConditions,omitempty"`
	ResolvePackageJSONExports                 bool                    `json:"resolvePackageJsonExports"`
	ResolvePackageJSONImports                 bool                    `json:"resolvePackageJsonImports"`
	ResolveJSONModule                         bool                    `json:"resolveJsonModule"`
}

// ProvenanceSnapshot locks the external inputs used to produce a snapshot.
type ProvenanceSnapshot struct {
	TypeScriptGoCommit  string `json:"typescriptGoCommit"`
	TypeScriptVersion   string `json:"typescriptVersion"`
	GoVersion           string `json:"goVersion"`
	StandardLibraryHash string `json:"standardLibraryHash"`
	KindManifestHash    string `json:"kindManifestHash"`
}

// FileSnapshot contains stable file metadata and the root node ordering.
type FileSnapshot struct {
	ID                  FileID   `json:"id"`
	CanonicalPath       string   `json:"canonicalPath"`
	ContentHash         string   `json:"contentHash"`
	SourceBlob          string   `json:"sourceBlob"`
	ScriptKind          string   `json:"scriptKind"`
	IsDeclarationFile   bool     `json:"isDeclarationFile"`
	IsExternalModule    bool     `json:"isExternalModule"`
	EmitModuleFormat    string   `json:"emitModuleFormat"`
	ImpliedModuleFormat string   `json:"impliedModuleFormat"`
	RootNodes           []NodeID `json:"rootNodes"`
	References          []string `json:"references,omitempty"`
	TypeReferences      []string `json:"typeReferences,omitempty"`
	LibReferences       []string `json:"libReferences,omitempty"`
}

// NodeSnapshot contains the checker-derived facts needed by subset gating and
// lowering. DeclaredType is the type visible at the declaration, while
// NarrowedType is GetTypeAtLocation and therefore includes flow narrowing.
type NodeSnapshot struct {
	ID                      NodeID                   `json:"id"`
	Origin                  OriginID                 `json:"origin"`
	File                    FileID                   `json:"file"`
	Kind                    string                   `json:"kind"`
	KindValue               int16                    `json:"kindValue"`
	Span                    Span                     `json:"span"`
	Parent                  NodeID                   `json:"parent,omitempty"`
	Children                []NodeID                 `json:"children,omitempty"`
	NamedChildren           []NamedChildSnapshot     `json:"namedChildren,omitempty"`
	SyntaxPayload           SyntaxPayload            `json:"syntaxPayload"`
	DeclaredType            TypeID                   `json:"declaredType,omitempty"`
	NarrowedType            TypeID                   `json:"narrowedType,omitempty"`
	ContextualType          TypeID                   `json:"contextualType,omitempty"`
	Symbol                  SymbolID                 `json:"symbol,omitempty"`
	ResolvedSymbol          SymbolID                 `json:"resolvedSymbol,omitempty"`
	SelectedSignature       SignatureID              `json:"selectedSignature,omitempty"`
	SelectedOverloadOrdinal uint32                   `json:"selectedOverloadOrdinal,omitempty"`
	Constant                ConstantSnapshot         `json:"constant"`
	AssertionTarget         TypeID                   `json:"assertionTarget,omitempty"`
	AssertionAssignable     bool                     `json:"assertionAssignable,omitempty"`
	AssertionChain          []AssertionProofSnapshot `json:"assertionChain,omitempty"`
	NonNullProof            NonNullProofSnapshot     `json:"nonNullProof"`
	ModifierBits            uint32                   `json:"modifierBits,omitempty"`
	NodeFlags               uint32                   `json:"nodeFlags,omitempty"`
	EvaluationFlags         uint32                   `json:"evaluationFlags,omitempty"`
	Flow                    FlowFactSnapshot         `json:"flow"`
	CaptureComplete         bool                     `json:"captureComplete"`
	CaptureSet              []SymbolID               `json:"captureSet,omitempty"`
	CaptureBindings         []CaptureBindingSnapshot `json:"captureBindings,omitempty"`
	Module                  ModuleID                 `json:"module,omitempty"`
}

// NamedChildSnapshot gives every syntax edge a stable role. Repeated roles use an
// explicit zero-based suffix, for example parameter[0] or statement[2].
type NamedChildSnapshot struct {
	Role string `json:"role"`
	Node NodeID `json:"node"`
}

// NamedChild is retained as a short source-level alias for draft v2 callers.
type NamedChild = NamedChildSnapshot

// SyntaxPayload is the tagged, pointer-free syntax variant consumed by
// lowering. Tag is the exact ast.Kind string; Text and Operator retain the
// payload that cannot be reconstructed from child edges alone.
type SyntaxPayload struct {
	Tag      string `json:"tag"`
	Text     string `json:"text,omitempty"`
	Operator string `json:"operator,omitempty"`
}

// OriginSnapshot is the stable first link in the source-to-backend metadata
// chain. Phase 1 creates one origin for each source node.
type OriginSnapshot struct {
	ID   OriginID `json:"id"`
	Node NodeID   `json:"node"`
	Span Span     `json:"span"`
}

// ConstantSnapshot is a pointer-free representation of checker constant
// values. Kind is one of none, string, number, boolean, bigint, or null.
type ConstantSnapshot struct {
	Kind   string  `json:"kind"`
	Text   string  `json:"text,omitempty"`
	Number float64 `json:"number,omitempty"`
	Bool   bool    `json:"bool,omitempty"`
}

// FlowFactSnapshot records the distinction between a declaration type and the
// checker type observed at the node. The checker FlowNode graph is never kept.
type FlowFactSnapshot struct {
	Narrowed           bool   `json:"narrowed"`
	ProofKind          string `json:"proofKind,omitempty"`
	DeclaredTypeHash   string `json:"declaredTypeHash,omitempty"`
	NarrowedTypeHash   string `json:"narrowedTypeHash,omitempty"`
	ContextualTypeHash string `json:"contextualTypeHash,omitempty"`
}

// AssertionProofSnapshot records every step in an assertion chain. OpenType
// identifies any/unknown traversal and RepresentationProof describes whether
// the step is identity, statically assignable, or requires a checked adapter.
type AssertionProofSnapshot struct {
	SourceType          TypeID `json:"sourceType"`
	TargetType          TypeID `json:"targetType"`
	Assignable          bool   `json:"assignable"`
	OpenType            string `json:"openType,omitempty"`
	RepresentationProof string `json:"representationProof"`
}

// NonNullProofSnapshot explains a postfix non-null assertion without relying
// on a generic "hash changed" bit.
type NonNullProofSnapshot struct {
	Present          bool   `json:"present"`
	OperandType      TypeID `json:"operandType,omitempty"`
	ResultType       TypeID `json:"resultType,omitempty"`
	ProofKind        string `json:"proofKind,omitempty"`
	RemovedNull      bool   `json:"removedNull,omitempty"`
	RemovedUndefined bool   `json:"removedUndefined,omitempty"`
}

// CaptureBindingSnapshot is one true runtime free binding. Type-only names and
// property keys never appear here.
type CaptureBindingSnapshot struct {
	Symbol  SymbolID `json:"symbol,omitempty"`
	Kind    string   `json:"kind"`
	Access  string   `json:"access"`
	Mutable bool     `json:"mutable"`
}

// TypeSnapshot is a pointer-free structural description of a checker type.
// DebugText is diagnostic-only and never participates in CanonicalHash.
type TypeSnapshot struct {
	ID                  TypeID              `json:"id"`
	CanonicalHash       string              `json:"canonicalHash"`
	Kind                string              `json:"kind"`
	Flags               uint32              `json:"flags"`
	ObjectFlags         uint32              `json:"objectFlags,omitempty"`
	Symbol              SymbolID            `json:"symbol,omitempty"`
	AliasSymbol         SymbolID            `json:"aliasSymbol,omitempty"`
	ElementTypes        []TypeID            `json:"elementTypes,omitempty"`
	TypeArguments       []TypeID            `json:"typeArguments,omitempty"`
	BaseTypes           []TypeID            `json:"baseTypes,omitempty"`
	Properties          []SymbolID          `json:"properties,omitempty"`
	PropertyFacts       []PropertySnapshot  `json:"propertyFacts,omitempty"`
	CallSignatures      []SignatureID       `json:"callSignatures,omitempty"`
	ConstructSignatures []SignatureID       `json:"constructSignatures,omitempty"`
	IndexInfos          []IndexInfoSnapshot `json:"indexInfos,omitempty"`
	ConstraintType      TypeID              `json:"constraintType,omitempty"`
	DefaultType         TypeID              `json:"defaultType,omitempty"`
	Variance            string              `json:"variance,omitempty"`
	NotLowerableReason  string              `json:"notLowerableReason,omitempty"`
	DebugText           string              `json:"debugText,omitempty"`
	TypePayload         TypePayload         `json:"typePayload"`
}

// PropertySnapshot closes the read/write and visibility contract needed by
// object layout and variance planning.
type PropertySnapshot struct {
	Symbol          SymbolID `json:"symbol"`
	ReadType        TypeID   `json:"readType,omitempty"`
	WriteType       TypeID   `json:"writeType,omitempty"`
	Optional        bool     `json:"optional"`
	Readonly        bool     `json:"readonly"`
	HasGetter       bool     `json:"hasGetter"`
	HasSetter       bool     `json:"hasSetter"`
	Visibility      string   `json:"visibility"`
	PrivateIdentity string   `json:"privateIdentity,omitempty"`
}

// TypePayload is the tagged structural type variant consumed by lowering.
// The reference slices deliberately mirror the normalized TypeSnapshot
// fields so a decoder can dispatch on Tag without checker-specific flags.
type TypePayload struct {
	Tag           string   `json:"tag"`
	Scalar        string   `json:"scalar"`
	Elements      []TypeID `json:"elements,omitempty"`
	TypeArguments []TypeID `json:"typeArguments,omitempty"`
	BaseTypes     []TypeID `json:"baseTypes,omitempty"`
}

// IndexInfoSnapshot records a TypeScript index signature without retaining
// checker data.
type IndexInfoSnapshot struct {
	KeyType     TypeID `json:"keyType"`
	ValueType   TypeID `json:"valueType"`
	Readonly    bool   `json:"readonly"`
	Declaration NodeID `json:"declaration,omitempty"`
}

// SymbolSnapshot records symbol identity and declaration provenance.
type SymbolSnapshot struct {
	ID               SymbolID `json:"id"`
	Name             string   `json:"name"`
	Flags            uint32   `json:"flags"`
	CheckFlags       uint32   `json:"checkFlags,omitempty"`
	Parent           SymbolID `json:"parent,omitempty"`
	ExportSymbol     SymbolID `json:"exportSymbol,omitempty"`
	Declarations     []NodeID `json:"declarations,omitempty"`
	ValueDeclaration NodeID   `json:"valueDeclaration,omitempty"`
	Type             TypeID   `json:"type,omitempty"`
}

// SignatureSnapshot records a resolved or declared call/construct signature.
type SignatureSnapshot struct {
	ID                        SignatureID                  `json:"id"`
	CanonicalHash             string                       `json:"canonicalHash"`
	Declaration               NodeID                       `json:"declaration,omitempty"`
	Flags                     uint32                       `json:"flags"`
	ThisParameter             SymbolID                     `json:"thisParameter,omitempty"`
	Parameters                []SymbolID                   `json:"parameters,omitempty"`
	ParameterFacts            []ParameterSnapshot          `json:"parameterFacts,omitempty"`
	MinArgumentCount          int                          `json:"minArgumentCount"`
	HasRest                   bool                         `json:"hasRest"`
	TypeParameters            []TypeID                     `json:"typeParameters,omitempty"`
	InstantiatedTypeArguments []TypeID                     `json:"instantiatedTypeArguments,omitempty"`
	ReturnType                TypeID                       `json:"returnType,omitempty"`
	Predicate                 TypePredicateSnapshot        `json:"predicate"`
	CallingConventionClass    string                       `json:"callingConventionClass"`
	Effects                   []string                     `json:"effects"`
	EffectProof               SignatureEffectProofSnapshot `json:"effectProof"`
}

// SignatureEffectProofSnapshot is the checker-resolved evidence used to
// recompute a signature's transitive source-effect summary without a Checker.
type SignatureEffectProofSnapshot struct {
	Kind           string                        `json:"kind"`
	Implementation NodeID                        `json:"implementation,omitempty"`
	Complete       bool                          `json:"complete"`
	DirectEffects  []string                      `json:"directEffects,omitempty"`
	Calls          []SignatureEffectCallSnapshot `json:"calls,omitempty"`
}

// SignatureEffectCallSnapshot connects one source call site to the selected
// checker signature. A zero Signature is an explicit unresolved edge.
type SignatureEffectCallSnapshot struct {
	Node      NodeID      `json:"node"`
	Signature SignatureID `json:"signature,omitempty"`
}

// ParameterSnapshot keeps the optional/rest/type contract aligned with the
// ordered signature parameter list.
type ParameterSnapshot struct {
	Symbol   SymbolID `json:"symbol"`
	Type     TypeID   `json:"type"`
	Optional bool     `json:"optional"`
	Rest     bool     `json:"rest"`
}

// TypePredicateSnapshot records a checker-resolved type predicate.
type TypePredicateSnapshot struct {
	Present        bool   `json:"present"`
	Kind           int32  `json:"kind,omitempty"`
	ParameterIndex int32  `json:"parameterIndex,omitempty"`
	ParameterName  string `json:"parameterName,omitempty"`
	Type           TypeID `json:"type,omitempty"`
}

// ModuleSnapshot records one source or resolved external module.
type ModuleSnapshot struct {
	ID            ModuleID `json:"id"`
	CanonicalPath string   `json:"canonicalPath"`
	Package       string   `json:"package,omitempty"`
	Format        string   `json:"format"`
	External      bool     `json:"external"`
	SCC           int      `json:"scc"`
}

// ModuleSCCSnapshot records one strongly connected component of the resolved
// module graph. Components and members are sorted before serialization.
type ModuleSCCSnapshot struct {
	ID      int        `json:"id"`
	Modules []ModuleID `json:"modules"`
}

// ModuleBindingSnapshot records one import or re-export binding on a resolved
// module edge. Names preserve the three namespaces visible in source while
// AliasSymbol and TargetSymbol retain checker resolution.
type ModuleBindingSnapshot struct {
	Node         NodeID   `json:"node"`
	Kind         string   `json:"kind"`
	ImportedName string   `json:"importedName,omitempty"`
	LocalName    string   `json:"localName,omitempty"`
	ExportedName string   `json:"exportedName,omitempty"`
	AliasSymbol  SymbolID `json:"aliasSymbol,omitempty"`
	TargetSymbol SymbolID `json:"targetSymbol,omitempty"`
	TypeOnly     bool     `json:"typeOnly"`
	Value        bool     `json:"value"`
}

// ModuleEdge records the exact Program resolution result for an import or
// re-export. A missing ResolvedModule leaves Resolved empty and is diagnosed by
// typescript-go before subset gating.
type ModuleEdge struct {
	Importer           ModuleID                `json:"importer"`
	Imported           ModuleID                `json:"imported,omitempty"`
	Source             NodeID                  `json:"source,omitempty"`
	SpecifierNode      NodeID                  `json:"specifierNode,omitempty"`
	Specifier          string                  `json:"specifier"`
	Span               Span                    `json:"span"`
	ResolutionMode     string                  `json:"resolutionMode"`
	Resolved           string                  `json:"resolved,omitempty"`
	Package            string                  `json:"package,omitempty"`
	Extension          string                  `json:"extension,omitempty"`
	TypeOnly           bool                    `json:"typeOnly"`
	Value              bool                    `json:"value"`
	SideEffectOnly     bool                    `json:"sideEffectOnly"`
	Kind               string                  `json:"kind"`
	ImportAttributes   []ImportAttribute       `json:"importAttributes,omitempty"`
	Bindings           []ModuleBindingSnapshot `json:"bindings,omitempty"`
	BindingsComplete   bool                    `json:"bindingsComplete"`
	DeferredEvaluation bool                    `json:"deferredEvaluation"`
}

// ImportAttribute is a stable key/value pair from an import attribute clause.
type ImportAttribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CanonicalBytes serializes a snapshot in its stable, human-reviewable form.
// The snapshot must already be normalized and sorted by the builder.
func (s ProgramSnapshot) CanonicalBytes() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// DecodeProgramSnapshot reads both the lowering-ready v2 contract and the
// legacy v1 contract. A v1 value remains schema 1 and is intentionally
// read-only: ValidateProgramSnapshot rejects it so lowering cannot mistake
// missing syntax/type payloads for complete facts.
func DecodeProgramSnapshot(data []byte) (*ProgramSnapshot, error) {
	var header struct {
		SchemaVersion uint32 `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("decode snapshot header: %w", err)
	}
	var snapshot ProgramSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode snapshot schema %d: %w", header.SchemaVersion, err)
	}
	switch header.SchemaVersion {
	case SnapshotSchemaVersion:
		if err := jsonx.Unmarshal(data, &snapshot, jsonx.RejectUnknownMembers(true)); err != nil {
			return nil, fmt.Errorf("decode snapshot schema %d: %w", header.SchemaVersion, err)
		}
		if err := ValidateProgramSnapshot(snapshot); err != nil {
			return nil, err
		}
	case LegacySnapshotSchemaVersion:
		if err := validateLegacyProgramSnapshot(snapshot); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported snapshot schema %d", header.SchemaVersion)
	}
	return &snapshot, nil
}
