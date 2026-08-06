package tsfrontend

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// RunSubsetGate validates the complete pointer-free ProgramSnapshot before
// passing it to the subset gate. Invalid or unfinalized snapshots never reach
// lowering-oriented gate logic.
func RunSubsetGate(snapshot ProgramSnapshot) []Diagnostic {
	validated, err := newValidatedProgramSnapshot(snapshot)
	if err != nil {
		return []Diagnostic{subsetGlobalDiagnostic(DiagnosticCodeInternalFailure, "subset.snapshot_invalid", err.Error())}
	}
	return runSubsetGate(validated)
}

// runSubsetGate never reaches back into typescript-go: all decisions use
// validated copied node/type facts, the checked-in Kind manifest, and
// normalized Bingo profile settings. The returned diagnostics are detached,
// sorted, and safe to serialize.
func runSubsetGate(validated validatedProgramSnapshot) []Diagnostic {
	snapshot := validated.snapshot
	if snapshot.SchemaVersion != SnapshotSchemaVersion {
		return []Diagnostic{subsetGlobalDiagnostic(DiagnosticCodeInternalFailure, "subset.snapshot_schema_mismatch", fmt.Sprint(snapshot.SchemaVersion), fmt.Sprint(SnapshotSchemaVersion))}
	}
	manifest, err := LoadKindManifest()
	if err != nil {
		return []Diagnostic{subsetGlobalDiagnostic(DiagnosticCodeInternalFailure, "subset.kind_manifest_invalid", err.Error())}
	}
	currentManifestDigest := KindManifestDigest()
	if snapshot.Provenance.KindManifestHash == "" {
		return []Diagnostic{subsetGlobalDiagnostic(DiagnosticCodeInternalFailure, "subset.kind_manifest_hash_missing", currentManifestDigest)}
	}
	if snapshot.Provenance.KindManifestHash != currentManifestDigest {
		return []Diagnostic{subsetGlobalDiagnostic(DiagnosticCodeUnclassifiedASTKind, "snapshot.kind_manifest_hash_mismatch", snapshot.Provenance.KindManifestHash, currentManifestDigest)}
	}

	entries := make(map[string]KindManifestEntry, len(manifest.Kinds))
	for _, entry := range manifest.Kinds {
		entries[entry.Kind] = entry
	}
	types := make(map[TypeID]TypeSnapshot, len(snapshot.Types))
	for _, record := range snapshot.Types {
		types[record.ID] = record
	}
	symbols := make(map[SymbolID]SymbolSnapshot, len(snapshot.Symbols))
	for _, record := range snapshot.Symbols {
		symbols[record.ID] = record
	}
	signatures := make(map[SignatureID]SignatureSnapshot, len(snapshot.Signatures))
	for _, record := range snapshot.Signatures {
		signatures[record.ID] = record
	}
	facts := subsetFacts{types: types, symbols: symbols, signatures: signatures, nodes: make(map[NodeID]NodeSnapshot, len(snapshot.Nodes))}
	filePaths := make(map[FileID]string, len(snapshot.Files))
	for _, file := range snapshot.Files {
		filePaths[file.ID] = file.CanonicalPath
	}
	nodes := slices.Clone(snapshot.Nodes)
	slices.SortStableFunc(nodes, func(left, right NodeSnapshot) int {
		if cmp := strings.Compare(string(left.File), string(right.File)); cmp != 0 {
			return cmp
		}
		if left.Span.Start != right.Span.Start {
			return left.Span.Start - right.Span.Start
		}
		if left.Span.End != right.Span.End {
			return left.Span.End - right.Span.End
		}
		return strings.Compare(string(left.ID), string(right.ID))
	})

	profile := snapshot.Config.Bingo.Profile
	if profile == "" {
		profile = ProfileStatic
	}
	diagnostics := make([]Diagnostic, 0)
	for _, node := range nodes {
		if path := filePaths[node.File]; path != "" {
			node.File = FileID(path)
			node.Span.File = FileID(path)
		}
		facts.nodes[node.ID] = node
		entry, ok := entries[node.Kind]
		if !ok || entry.KindValue != node.KindValue {
			diagnostics = append(diagnostics, subsetNodeDiagnostic(
				DiagnosticCodeUnclassifiedASTKind,
				node,
				profile,
				"snapshot.unclassified_ast_kind",
				node.Kind,
			))
			continue
		}
		if diagnostic := gateKindNode(node, entry, profile, facts); diagnostic != nil {
			diagnostics = append(diagnostics, *diagnostic)
		}
	}
	diagnostics = append(diagnostics, gateExportedGenericSignatures(facts, profile)...)
	for _, edge := range snapshot.ModuleEdges {
		if edge.Kind != "dynamic-import" {
			continue
		}
		diagnostic := NewRegisteredDiagnostic(
			DiagnosticCodeUnsupportedSyntax,
			SourceSpan{File: string(edge.Span.File), Start: edge.Span.Start, End: edge.Span.End},
			"dynamic-import",
		)
		diagnostic.MessageKey = "subset.feature_unavailable"
		diagnostic.NodeKind = ast.KindCallExpression.String()
		diagnostic.EntityID = fmt.Sprintf("module-edge:%s:%s:%s", edge.Importer, edge.Kind, edge.Specifier)
		diagnostic.Profile = profile
		diagnostic.RequiredCapability = "dynamic-module-loader"
		diagnostics = append(diagnostics, diagnostic)
	}
	return SortAndDeduplicateDiagnostics(diagnostics)
}

// GateProgramSnapshot is a descriptive alias for callers that prefer the
// stage name in orchestration code.
func GateProgramSnapshot(snapshot ProgramSnapshot) []Diagnostic {
	return RunSubsetGate(snapshot)
}

type subsetFacts struct {
	types      map[TypeID]TypeSnapshot
	symbols    map[SymbolID]SymbolSnapshot
	signatures map[SignatureID]SignatureSnapshot
	nodes      map[NodeID]NodeSnapshot
}

func gateKindNode(node NodeSnapshot, entry KindManifestEntry, profile Profile, facts subsetFacts) *Diagnostic {
	if diagnostic := gateCopiedNodeFlags(node, profile); diagnostic != nil {
		return diagnostic
	}
	handler, ok := lookupKindGateHandler(entry.GateHandler)
	if !ok || handler.Handle == nil {
		diagnostic := subsetNodeDiagnostic(DiagnosticCodeInternalFailure, node, profile, "subset.gate_handler_unbound", entry.GateHandler)
		return &diagnostic
	}
	return handler.Handle(node, entry, profile, facts)
}

func gateAssertionKind(node NodeSnapshot, entry KindManifestEntry, profile Profile, facts subsetFacts) *Diagnostic {
	if diagnostic := gateAssertionFacts(node, entry, profile, facts.types); diagnostic != nil {
		return diagnostic
	}
	return gateTypeFacts(node, entry, profile, facts)
}

func gateCapabilityKind(node NodeSnapshot, entry KindManifestEntry, profile Profile, facts subsetFacts) *Diagnostic {
	if diagnostic := gateTypeFacts(node, entry, profile, facts); diagnostic != nil {
		return diagnostic
	}
	// The source gate has no TargetContext and therefore cannot claim that a
	// selected runtime lacks this capability. Until its HIR lowerer exists, use
	// a target-independent subset diagnostic and preserve the logical capability
	// only as planning metadata for the later target gate.
	diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnsupportedSyntax, node, profile, "subset.lowerer_unavailable", entry.Feature)
	diagnostic.RequiredCapability = entry.Capability
	return &diagnostic
}

func gateEraseKind(NodeSnapshot, KindManifestEntry, Profile, subsetFacts) *Diagnostic {
	return nil
}

func gateFeatureKind(node NodeSnapshot, entry KindManifestEntry, profile Profile, _ subsetFacts) *Diagnostic {
	if (entry.Feature == "namespace-runtime" || entry.Feature == "enum-runtime") && ast.ModifierFlags(node.ModifierBits)&ast.ModifierFlagsAmbient != 0 {
		return nil
	}
	diagnostic := featureDiagnostic(node, entry, profile)
	return &diagnostic
}

func gateRecoveryKind(node NodeSnapshot, entry KindManifestEntry, profile Profile, _ subsetFacts) *Diagnostic {
	diagnostic := featureDiagnostic(node, entry, profile)
	return &diagnostic
}

func gateSyntaxKind(node NodeSnapshot, entry KindManifestEntry, profile Profile, facts subsetFacts) *Diagnostic {
	if entry.Domain != "runtime" {
		return nil
	}
	return gateTypeFacts(node, entry, profile, facts)
}

func gateCopiedNodeFlags(node NodeSnapshot, profile Profile) *Diagnostic {
	if ast.NodeFlags(node.NodeFlags)&ast.NodeFlagsUsing != 0 {
		diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnsupportedSyntax, node, profile, "subset.feature_unavailable", "using")
		diagnostic.RequiredCapability = "disposable"
		return &diagnostic
	}
	modifiers := ast.ModifierFlags(node.ModifierBits)
	if modifiers&ast.ModifierFlagsAsync != 0 {
		diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnsupportedSyntax, node, profile, "subset.feature_unavailable", "async")
		diagnostic.RequiredCapability = "async"
		return &diagnostic
	}
	if modifiers&ast.ModifierFlagsDecorator != 0 {
		diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnsupportedSyntax, node, profile, "subset.feature_unavailable", "decorators")
		diagnostic.RequiredCapability = "decorator"
		return &diagnostic
	}
	return nil
}

func featureDiagnostic(node NodeSnapshot, entry KindManifestEntry, profile Profile) Diagnostic {
	code := DiagnosticCodeUnsupportedSyntax
	messageKey := "subset.feature_unavailable"
	switch entry.Feature {
	case "unsafe-assertion":
		code, messageKey = DiagnosticCodeUnsafeAssertionChain, "assertion.unsafe_chain"
	case "non-null-assertion":
		code, messageKey = DiagnosticCodeUnprovenNonNullAssertion, "assertion.non_null_unproven"
	case "unknown", "conflict-marker-trivia", "non-text-file-marker-trivia", "missing-declaration", "synthetic-expression", "synthetic-reference-expression", "not-emitted-statement", "partially-emitted-expression", "not-emitted-type-element", "syntax-list":
		code, messageKey = DiagnosticCodeParserRecoveryNode, "snapshot.parser_recovery_node"
	}
	diagnostic := subsetNodeDiagnostic(code, node, profile, messageKey, entry.Feature)
	diagnostic.RequiredCapability = entry.Capability
	return diagnostic
}

func gateAssertionFacts(node NodeSnapshot, entry KindManifestEntry, profile Profile, types map[TypeID]TypeSnapshot) *Diagnostic {
	if entry.Feature == "non-null-assertion" {
		proof := node.NonNullProof
		// A changed flow hash is not a proof.  The capture phase must state
		// which nullish constituents were removed and why; open types and an
		// assertion-strip are deliberately rejected at the subset boundary.
		if !proof.Present || proof.OperandType == 0 || proof.ResultType == 0 || strings.TrimSpace(proof.ProofKind) == "" {
			diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnprovenNonNullAssertion, node, profile, "assertion.non_null_unproven", entry.Feature)
			return &diagnostic
		}
		operand, operandOK := types[proof.OperandType]
		result, resultOK := types[proof.ResultType]
		if !operandOK || !resultOK || isOpenType(operand) || isOpenType(result) ||
			typeClosureContains(proof.OperandType, subsetFacts{types: types}, isAnyType) ||
			typeClosureContains(proof.OperandType, subsetFacts{types: types}, isUnknownType) ||
			typeClosureContains(proof.ResultType, subsetFacts{types: types}, isAnyType) ||
			typeClosureContains(proof.ResultType, subsetFacts{types: types}, isUnknownType) {
			diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnprovenNonNullAssertion, node, profile, "assertion.non_null_unproven", entry.Feature)
			return &diagnostic
		}
		switch proof.ProofKind {
		case "redundant-non-null":
			// These are the only proof kinds that establish a representation-safe
			// result without an unchecked cast.
			if !sameTypeRecord(operand, result) || typeClosureContains(proof.OperandType, subsetFacts{types: types}, isNullishType) {
				diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnprovenNonNullAssertion, node, profile, "assertion.non_null_unproven", entry.Feature)
				return &diagnostic
			}
		case "proven-non-null":
			if !proof.RemovedNull && !proof.RemovedUndefined {
				diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnprovenNonNullAssertion, node, profile, "assertion.non_null_unproven", entry.Feature)
				return &diagnostic
			}
		case "assertion-strip", "open-any", "open-unknown", "unproven":
			fallthrough
		default:
			diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnprovenNonNullAssertion, node, profile, "assertion.non_null_unproven", entry.Feature)
			return &diagnostic
		}
		if !proof.RemovedNull && !proof.RemovedUndefined {
			// A redundant proof is valid only when the operand is already known to
			// be non-nullish; a claimed stripping operation without a removed
			// constituent is malformed.
			if proof.ProofKind != "redundant-non-null" && !sameTypeRecord(operand, result) {
				diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnprovenNonNullAssertion, node, profile, "assertion.non_null_unproven", entry.Feature)
				return &diagnostic
			}
		}
		if typeClosureContains(proof.ResultType, subsetFacts{types: types}, isNullishType) {
			diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnprovenNonNullAssertion, node, profile, "assertion.non_null_unproven", entry.Feature)
			return &diagnostic
		}
		return nil
	}

	chain := node.AssertionChain
	if len(chain) == 0 {
		diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnsafeAssertionChain, node, profile, "assertion.unsafe_chain", entry.Feature)
		return &diagnostic
	}
	previous := TypeID(0)
	for index, proof := range chain {
		source, sourceOK := types[proof.SourceType]
		target, targetOK := types[proof.TargetType]
		if !sourceOK || !targetOK || proof.SourceType == 0 || proof.TargetType == 0 {
			return assertionUnsafeDiagnostic(node, entry, profile, "", "")
		}
		if index == 0 {
			previous = proof.SourceType
			if node.DeclaredType != 0 && proof.SourceType != node.DeclaredType {
				return assertionUnsafeDiagnostic(node, entry, profile, source.DebugText, target.DebugText)
			}
		} else if proof.SourceType != previous {
			return assertionUnsafeDiagnostic(node, entry, profile, source.DebugText, target.DebugText)
		}
		if proof.OpenType != "" || !validAssertionRepresentationProof(proof) ||
			isOpenType(source) || isOpenType(target) ||
			typeClosureContains(proof.SourceType, subsetFacts{types: types}, isAnyType) ||
			typeClosureContains(proof.SourceType, subsetFacts{types: types}, isUnknownType) ||
			typeClosureContains(proof.TargetType, subsetFacts{types: types}, isAnyType) ||
			typeClosureContains(proof.TargetType, subsetFacts{types: types}, isUnknownType) {
			return assertionUnsafeDiagnostic(node, entry, profile, source.DebugText, target.DebugText)
		}
		if proof.RepresentationProof == "identity" && source.CanonicalHash != target.CanonicalHash {
			return assertionUnsafeDiagnostic(node, entry, profile, source.DebugText, target.DebugText)
		}
		previous = proof.TargetType
	}
	finalTarget := chain[len(chain)-1].TargetType
	if node.AssertionTarget != 0 && finalTarget != node.AssertionTarget {
		return assertionUnsafeDiagnostic(node, entry, profile, "", "")
	}
	if node.AssertionTarget == 0 && node.NarrowedType != 0 && finalTarget != node.NarrowedType {
		return assertionUnsafeDiagnostic(node, entry, profile, "", "")
	}
	return nil
}

func assertionUnsafeDiagnostic(node NodeSnapshot, entry KindManifestEntry, profile Profile, source, target string) *Diagnostic {
	diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnsafeAssertionChain, node, profile, "assertion.unsafe_chain", entry.Feature)
	diagnostic.SourceType = source
	diagnostic.TargetType = target
	return &diagnostic
}

func sameTypeRecord(left, right TypeSnapshot) bool {
	return left.ID == right.ID || left.CanonicalHash != "" && left.CanonicalHash == right.CanonicalHash
}

func isNullishType(record TypeSnapshot) bool {
	if checker.TypeFlags(record.Flags)&(checker.TypeFlagsNull|checker.TypeFlagsUndefined) != 0 {
		return true
	}
	kind := strings.ToLower(strings.TrimSpace(record.Kind))
	return kind == "null" || kind == "undefined" || kind == "intrinsic:null" || kind == "intrinsic:undefined"
}

func validAssertionRepresentationProof(proof AssertionProofSnapshot) bool {
	switch proof.RepresentationProof {
	case "identity":
		return true
	case "source-assignable":
		return proof.Assignable
	default:
		return false
	}
}

func gateTypeFacts(node NodeSnapshot, entry KindManifestEntry, profile Profile, facts subsetFacts) *Diagnostic {
	ids := []TypeID{node.NarrowedType, node.DeclaredType, node.ContextualType}
	seen := make(map[TypeID]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		typeRecord, ok := facts.types[id]
		if !ok {
			continue
		}
		if typeRecord.NotLowerableReason == "checker-error-type" {
			continue
		}
		if typeClosureContains(id, facts, isAnyType) {
			diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnsupportedSyntax, node, profile, "subset.any_type", entry.Feature)
			return &diagnostic
		}
		if unknownOperation(node.Kind) && typeClosureContains(id, facts, isUnknownType) {
			diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnsupportedSyntax, node, profile, "subset.unknown_unchecked", entry.Feature)
			return &diagnostic
		}
		if strings.TrimSpace(typeRecord.NotLowerableReason) != "" {
			diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnresolvedGeneric, node, profile, "type.not_lowerable", typeRecord.NotLowerableReason)
			return &diagnostic
		}
	}
	return nil
}

func subsetNodeDiagnostic(code string, node NodeSnapshot, profile Profile, messageKey string, arguments ...string) Diagnostic {
	file := string(node.Span.File)
	if file == "" {
		file = string(node.File)
	}
	diagnostic := NewRegisteredDiagnostic(code, SourceSpan{File: file, Start: node.Span.Start, End: node.Span.End}, arguments...)
	diagnostic.MessageKey = messageKey
	diagnostic.NodeKind = node.Kind
	diagnostic.EntityID = string(node.ID)
	diagnostic.Profile = profile
	return diagnostic
}

func subsetGlobalDiagnostic(code, messageKey string, arguments ...string) Diagnostic {
	diagnostic := NewRegisteredDiagnostic(code, SourceSpan{}, arguments...)
	diagnostic.MessageKey = messageKey
	diagnostic.EntityID = "subset-gate"
	return diagnostic
}

func isAnyType(record TypeSnapshot) bool {
	if record.NotLowerableReason == "checker-error-type" {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(record.Kind))
	return kind == "any" || kind == "dynamic" || kind == "intrinsic:any"
}

func isUnknownType(record TypeSnapshot) bool {
	kind := strings.ToLower(strings.TrimSpace(record.Kind))
	return kind == "unknown" || kind == "intrinsic:unknown"
}

func isOpenType(record TypeSnapshot) bool {
	return isAnyType(record) || isUnknownType(record) || strings.EqualFold(strings.TrimSpace(record.Kind), "object") && strings.TrimSpace(record.CanonicalHash) == ""
}

func typeClosureContains(id TypeID, facts subsetFacts, predicate func(TypeSnapshot) bool) bool {
	types := facts.types
	visited := make(map[TypeID]struct{})
	var visit func(TypeID) bool
	visit = func(current TypeID) bool {
		if _, seen := visited[current]; seen {
			return false
		}
		visited[current] = struct{}{}
		record, ok := types[current]
		if !ok {
			return false
		}
		if predicate(record) {
			return true
		}
		children := make([]TypeID, 0, len(record.ElementTypes)+len(record.TypeArguments)+len(record.BaseTypes)+len(record.IndexInfos)*2)
		children = append(children, record.ElementTypes...)
		children = append(children, record.TypeArguments...)
		children = append(children, record.BaseTypes...)
		for _, info := range record.IndexInfos {
			children = append(children, info.KeyType, info.ValueType)
		}
		for _, propertyID := range record.Properties {
			property, ok := facts.symbols[propertyID]
			if ok && len(property.Declarations) != 0 {
				children = append(children, property.Type)
			}
		}
		for _, signatureID := range slices.Concat(record.CallSignatures, record.ConstructSignatures) {
			signature, ok := facts.signatures[signatureID]
			if !ok || signature.Declaration == "" {
				continue
			}
			children = append(children, signature.ReturnType, signature.Predicate.Type)
			children = append(children, signature.InstantiatedTypeArguments...)
			for _, parameterID := range signature.Parameters {
				if parameter, ok := facts.symbols[parameterID]; ok {
					children = append(children, parameter.Type)
				}
			}
		}
		for _, child := range children {
			if child != 0 && visit(child) {
				return true
			}
		}
		return false
	}
	return visit(id)
}

func gateExportedGenericSignatures(facts subsetFacts, profile Profile) []Diagnostic {
	ids := make([]SymbolID, 0, len(facts.symbols))
	for id := range facts.symbols {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	seen := make(map[SymbolID]struct{})
	diagnostics := make([]Diagnostic, 0)
	for _, id := range ids {
		local := facts.symbols[id]
		if ast.SymbolFlags(local.Flags)&ast.SymbolFlagsExportValue == 0 {
			continue
		}
		targetID := local.ExportSymbol
		if targetID == "" {
			targetID = id
		}
		if _, duplicate := seen[targetID]; duplicate {
			continue
		}
		seen[targetID] = struct{}{}
		target, ok := facts.symbols[targetID]
		if !ok || target.Type == 0 || !exportedTypeHasUnresolvedSignature(target.Type, facts) {
			continue
		}
		node, ok := symbolDiagnosticNode(local, target, facts.nodes)
		if !ok {
			diagnostic := subsetGlobalDiagnostic(DiagnosticCodeUnresolvedGeneric, "type.not_lowerable", "unresolved-type-parameter")
			diagnostic.EntityID = string(targetID)
			diagnostic.Profile = profile
			diagnostics = append(diagnostics, diagnostic)
			continue
		}
		diagnostic := subsetNodeDiagnostic(DiagnosticCodeUnresolvedGeneric, node, profile, "type.not_lowerable", "unresolved-type-parameter")
		if typ, ok := facts.types[target.Type]; ok {
			diagnostic.SourceType = typ.DebugText
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics
}

func exportedTypeHasUnresolvedSignature(typeID TypeID, facts subsetFacts) bool {
	typ, ok := facts.types[typeID]
	if !ok {
		return false
	}
	for _, signatureID := range slices.Concat(typ.CallSignatures, typ.ConstructSignatures) {
		signature, ok := facts.signatures[signatureID]
		if !ok {
			continue
		}
		if len(signature.TypeParameters) != 0 {
			return true
		}
		ids := []TypeID{signature.ReturnType, signature.Predicate.Type}
		ids = append(ids, signature.InstantiatedTypeArguments...)
		for _, parameterID := range signature.Parameters {
			if parameter, ok := facts.symbols[parameterID]; ok {
				ids = append(ids, parameter.Type)
			}
		}
		for _, id := range ids {
			if typeClosureContains(id, facts, func(record TypeSnapshot) bool {
				return record.NotLowerableReason == "unresolved-type-parameter"
			}) {
				return true
			}
		}
	}
	return false
}

func symbolDiagnosticNode(local, target SymbolSnapshot, nodes map[NodeID]NodeSnapshot) (NodeSnapshot, bool) {
	for _, symbol := range []SymbolSnapshot{local, target} {
		ids := make([]NodeID, 0, len(symbol.Declarations)+1)
		if symbol.ValueDeclaration != "" {
			ids = append(ids, symbol.ValueDeclaration)
		}
		ids = append(ids, symbol.Declarations...)
		for _, id := range ids {
			if node, ok := nodes[id]; ok {
				return node, true
			}
		}
	}
	return NodeSnapshot{}, false
}

func unknownOperation(kind string) bool {
	if kind == "" {
		return false
	}
	if strings.HasSuffix(kind, "Expression") {
		return true
	}
	switch kind {
	case ast.KindThrowStatement.String(), ast.KindCallExpression.String(), ast.KindNewExpression.String(), ast.KindPropertyAccessExpression.String(), ast.KindElementAccessExpression.String(), ast.KindSpreadElement.String():
		return true
	default:
		return false
	}
}
