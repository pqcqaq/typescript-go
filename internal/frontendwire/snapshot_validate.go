package frontendwire

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/tspath"
)

// ValidateProgramSnapshot verifies the immutable frontend handoff before it is
// serialized, cached, or consumed by lowering. Zero IDs are allowed only in
// optional reference fields.
func ValidateProgramSnapshot(snapshot ProgramSnapshot) error {
	if snapshot.SchemaVersion != SnapshotSchemaVersion {
		return fmt.Errorf("unsupported snapshot schema %d", snapshot.SchemaVersion)
	}
	return validateProgramSnapshot(snapshot, true)
}

func validateLegacyProgramSnapshot(snapshot ProgramSnapshot) error {
	if snapshot.SchemaVersion != LegacySnapshotSchemaVersion {
		return fmt.Errorf("unsupported legacy snapshot schema %d", snapshot.SchemaVersion)
	}
	return validateProgramSnapshot(snapshot, false)
}

func validateProgramSnapshot(snapshot ProgramSnapshot, loweringReady bool) error {
	if snapshot.Config.BingoSchemaVersion != OptionsSchemaVersion {
		return fmt.Errorf("unsupported Bingo options schema %d", snapshot.Config.BingoSchemaVersion)
	}
	if err := validateSnapshotLogicalPaths(snapshot); err != nil {
		return err
	}
	if err := validateSnapshotConfigAndProvenance(snapshot); err != nil {
		return err
	}

	files, err := indexSnapshotValues(snapshot.Files, func(value FileSnapshot) FileID { return value.ID }, "file")
	if err != nil {
		return err
	}
	nodes, err := indexSnapshotValues(snapshot.Nodes, func(value NodeSnapshot) NodeID { return value.ID }, "node")
	if err != nil {
		return err
	}
	origins, err := indexSnapshotValues(snapshot.Origins, func(value OriginSnapshot) OriginID { return value.ID }, "origin")
	if err != nil {
		return err
	}
	types, err := indexSnapshotValues(snapshot.Types, func(value TypeSnapshot) TypeID { return value.ID }, "type")
	if err != nil {
		return err
	}
	symbols, err := indexSnapshotValues(snapshot.Symbols, func(value SymbolSnapshot) SymbolID { return value.ID }, "symbol")
	if err != nil {
		return err
	}
	signatures, err := indexSnapshotValues(snapshot.Signatures, func(value SignatureSnapshot) SignatureID { return value.ID }, "signature")
	if err != nil {
		return err
	}
	modules, err := indexSnapshotValues(snapshot.Modules, func(value ModuleSnapshot) ModuleID { return value.ID }, "module")
	if err != nil {
		return err
	}
	if loweringReady {
		if err := validateSnapshotKindShapeRegistry(snapshotKindShapeRegistry); err != nil {
			return fmt.Errorf("snapshot Kind shape registry: %w", err)
		}
		if err := validateSnapshotEffectRuleRegistry(); err != nil {
			return fmt.Errorf("snapshot effect rule registry: %w", err)
		}
	}

	for _, file := range snapshot.Files {
		if file.CanonicalPath == "" || !isDigest(file.ContentHash) {
			return fmt.Errorf("file %q has incomplete canonical metadata", file.ID)
		}
		if loweringReady {
			digest := sha256.Sum256([]byte(file.SourceBlob))
			if got := hex.EncodeToString(digest[:]); got != file.ContentHash {
				return fmt.Errorf("file %q source blob hash mismatch: got %s, want %s", file.ID, file.ContentHash, got)
			}
		}
		if len(file.RootNodes) == 0 {
			return fmt.Errorf("file %q has no root nodes", file.ID)
		}
		seenRoots := make(map[NodeID]struct{}, len(file.RootNodes))
		for _, root := range file.RootNodes {
			if _, ok := nodes[root]; !ok {
				return fmt.Errorf("file %q references missing root node %q", file.ID, root)
			}
			if _, duplicate := seenRoots[root]; duplicate {
				return fmt.Errorf("file %q contains duplicate root node %q", file.ID, root)
			}
			seenRoots[root] = struct{}{}
			rootRecord := nodes[root]
			if rootRecord.File != file.ID || rootRecord.Parent != "" {
				return fmt.Errorf("file %q root node %q is not a parentless node in the file", file.ID, root)
			}
		}
	}
	for _, node := range snapshot.Nodes {
		file, ok := files[node.File]
		if !ok {
			return fmt.Errorf("node %q references missing file %q", node.ID, node.File)
		}
		if node.Kind == "" {
			return fmt.Errorf("node %q has no syntax kind", node.ID)
		}
		if loweringReady && !snapshotEffectKindIdentityMatches(node.Kind, node.KindValue) {
			return fmt.Errorf("node %q syntax kind %q does not match value %d", node.ID, node.Kind, node.KindValue)
		}
		if loweringReady && node.SyntaxPayload.Tag != node.Kind {
			return fmt.Errorf("node %q syntax payload tag %q does not match kind %q", node.ID, node.SyntaxPayload.Tag, node.Kind)
		}
		if node.Span.File != node.File {
			return fmt.Errorf("node %q span file %q does not match node file %q", node.ID, node.Span.File, node.File)
		}
		if node.Span.Start < 0 || node.Span.End < node.Span.Start {
			return fmt.Errorf("node %q has invalid span [%d,%d)", node.ID, node.Span.Start, node.Span.End)
		}
		if loweringReady && node.Span.End > len(file.SourceBlob) {
			return fmt.Errorf("node %q span [%d,%d) exceeds source blob length %d", node.ID, node.Span.Start, node.Span.End, len(file.SourceBlob))
		}
		if node.Parent != "" {
			parent, ok := nodes[node.Parent]
			if !ok {
				return fmt.Errorf("node %q references missing parent %q", node.ID, node.Parent)
			}
			if parent.File != node.File {
				return fmt.Errorf("node %q parent %q belongs to a different file", node.ID, node.Parent)
			}
			if countSnapshotNodeID(parent.Children, node.ID) != 1 {
				return fmt.Errorf("node %q parent %q does not contain exactly one reverse child edge", node.ID, node.Parent)
			}
		}
		seenChildren := make(map[NodeID]struct{}, len(node.Children))
		for _, child := range node.Children {
			childRecord, ok := nodes[child]
			if !ok {
				return fmt.Errorf("node %q references missing child %q", node.ID, child)
			}
			if _, duplicate := seenChildren[child]; duplicate {
				return fmt.Errorf("node %q contains duplicate child %q", node.ID, child)
			}
			seenChildren[child] = struct{}{}
			if childRecord.Parent != node.ID {
				return fmt.Errorf("node %q child %q has parent %q", node.ID, child, childRecord.Parent)
			}
		}
		if loweringReady {
			if len(node.NamedChildren) != len(node.Children) {
				return fmt.Errorf("node %q named child count %d does not match child count %d", node.ID, len(node.NamedChildren), len(node.Children))
			}
			seenRoles := make(map[string]struct{}, len(node.NamedChildren))
			for index, named := range node.NamedChildren {
				if strings.TrimSpace(named.Role) == "" {
					return fmt.Errorf("node %q named child %d has an empty role", node.ID, index)
				}
				if _, duplicate := seenRoles[named.Role]; duplicate {
					return fmt.Errorf("node %q contains duplicate named child role %q", node.ID, named.Role)
				}
				seenRoles[named.Role] = struct{}{}
				if named.Node != node.Children[index] {
					return fmt.Errorf("node %q named child %q does not match child ordering", node.ID, named.Role)
				}
			}
			if err := validateSnapshotKindShape(node, nodes); err != nil {
				return err
			}
		}
		origin, ok := origins[node.Origin]
		if !ok || origin.Node != node.ID || origin.Span != node.Span {
			return fmt.Errorf("node %q has inconsistent origin %q", node.ID, node.Origin)
		}
		if loweringReady && !validConstantKind(node.Constant.Kind) {
			return fmt.Errorf("node %q has invalid constant kind %q", node.ID, node.Constant.Kind)
		}
		if err := requireOptionalSnapshotRef(types, node.DeclaredType, "node", node.ID, "declared type"); err != nil {
			return err
		}
		if err := requireOptionalSnapshotRef(types, node.NarrowedType, "node", node.ID, "narrowed type"); err != nil {
			return err
		}
		if err := requireOptionalSnapshotRef(types, node.ContextualType, "node", node.ID, "contextual type"); err != nil {
			return err
		}
		if err := requireOptionalSnapshotRef(types, node.AssertionTarget, "node", node.ID, "assertion target"); err != nil {
			return err
		}
		if node.AssertionAssignable && node.AssertionTarget == 0 {
			return fmt.Errorf("node %q marks assertion assignable without an assertion target", node.ID)
		}
		for index, proof := range node.AssertionChain {
			if proof.SourceType == 0 || proof.TargetType == 0 {
				return fmt.Errorf("node %q assertion proof %d has a zero type reference", node.ID, index)
			}
			if _, ok := types[proof.SourceType]; !ok {
				return fmt.Errorf("node %q assertion proof %d references missing source type %d", node.ID, index, proof.SourceType)
			}
			if _, ok := types[proof.TargetType]; !ok {
				return fmt.Errorf("node %q assertion proof %d references missing target type %d", node.ID, index, proof.TargetType)
			}
			if strings.TrimSpace(proof.RepresentationProof) == "" {
				return fmt.Errorf("node %q assertion proof %d has no representation proof", node.ID, index)
			}
		}
		if node.NonNullProof.Present {
			if node.NonNullProof.OperandType == 0 || node.NonNullProof.ResultType == 0 || strings.TrimSpace(node.NonNullProof.ProofKind) == "" {
				return fmt.Errorf("node %q has an incomplete non-null proof", node.ID)
			}
			if err := requireOptionalSnapshotRef(types, node.NonNullProof.OperandType, "node", node.ID, "non-null operand type"); err != nil {
				return err
			}
			if err := requireOptionalSnapshotRef(types, node.NonNullProof.ResultType, "node", node.ID, "non-null result type"); err != nil {
				return err
			}
		}
		if err := validateFlowFacts(node, types); err != nil {
			return err
		}
		if loweringReady {
			if err := validateAssertionAndNonNullFacts(node, types); err != nil {
				return err
			}
		}
		if err := requireOptionalSnapshotRef(symbols, node.Symbol, "node", node.ID, "symbol"); err != nil {
			return err
		}
		if err := requireOptionalSnapshotRef(symbols, node.ResolvedSymbol, "node", node.ID, "resolved symbol"); err != nil {
			return err
		}
		if err := requireOptionalSnapshotRef(signatures, node.SelectedSignature, "node", node.ID, "selected signature"); err != nil {
			return err
		}
		if node.SelectedOverloadOrdinal != 0 && node.SelectedSignature == 0 {
			return fmt.Errorf("node %q has overload ordinal %d without a selected signature", node.ID, node.SelectedOverloadOrdinal)
		}
		if node.SelectedOverloadOrdinal != 0 && !isSnapshotResolvedSignatureNode(node) {
			return fmt.Errorf("node %q has overload ordinal %d but is not call-like", node.ID, node.SelectedOverloadOrdinal)
		}
		if err := requireOptionalSnapshotRef(modules, node.Module, "node", node.ID, "module"); err != nil {
			return err
		}
		if loweringReady && node.Module == "" {
			return fmt.Errorf("node %q has no owning module", node.ID)
		}
		for _, symbol := range node.CaptureSet {
			if _, ok := symbols[symbol]; !ok {
				return fmt.Errorf("node %q capture set references missing symbol %q", node.ID, symbol)
			}
		}
		for index, binding := range node.CaptureBindings {
			if binding.Symbol != "" {
				if _, ok := symbols[binding.Symbol]; !ok {
					return fmt.Errorf("node %q capture binding %d references missing symbol %q", node.ID, index, binding.Symbol)
				}
			}
			if strings.TrimSpace(binding.Kind) == "" || strings.TrimSpace(binding.Access) == "" {
				return fmt.Errorf("node %q capture binding %d is incomplete", node.ID, index)
			}
		}
		if loweringReady {
			if err := validateCaptureFacts(node); err != nil {
				return err
			}
		}
	}
	for _, origin := range snapshot.Origins {
		if _, ok := nodes[origin.Node]; !ok {
			return fmt.Errorf("origin %q references missing node %q", origin.ID, origin.Node)
		}
		if _, ok := files[origin.Span.File]; !ok {
			return fmt.Errorf("origin %q references missing file %q", origin.ID, origin.Span.File)
		}
	}
	if len(origins) != len(nodes) {
		return fmt.Errorf("origin coverage is incomplete: got %d of %d nodes", len(origins), len(nodes))
	}
	if err := validateSnapshotTree(snapshot.Files, nodes); err != nil {
		return err
	}
	if loweringReady {
		if err := validateSnapshotEffectTypeContexts(snapshot.Nodes, nodes); err != nil {
			return err
		}
	}

	for _, record := range snapshot.Types {
		if !isDigest(record.CanonicalHash) || record.Kind == "" {
			return fmt.Errorf("type %d has incomplete canonical metadata", record.ID)
		}
		if loweringReady {
			if record.TypePayload.Tag != record.Kind || strings.TrimSpace(record.TypePayload.Scalar) == "" {
				return fmt.Errorf("type %d has incomplete or mismatched type payload", record.ID)
			}
			if !slices.Equal(record.TypePayload.Elements, record.ElementTypes) ||
				!slices.Equal(record.TypePayload.TypeArguments, record.TypeArguments) ||
				!slices.Equal(record.TypePayload.BaseTypes, record.BaseTypes) {
				return fmt.Errorf("type %d payload references do not match normalized type references", record.ID)
			}
		}
		for _, reference := range slices.Concat(record.ElementTypes, record.TypeArguments, record.BaseTypes) {
			if _, ok := types[reference]; !ok {
				return fmt.Errorf("type %d references missing type %d", record.ID, reference)
			}
		}
		for _, reference := range []TypeID{record.ConstraintType, record.DefaultType} {
			if err := requireOptionalSnapshotRef(types, reference, "type", record.ID, "type"); err != nil {
				return err
			}
		}
		for _, property := range record.Properties {
			if _, ok := symbols[property]; !ok {
				return fmt.Errorf("type %d references missing property symbol %q", record.ID, property)
			}
		}
		for index, property := range record.PropertyFacts {
			if _, ok := symbols[property.Symbol]; !ok {
				return fmt.Errorf("type %d property fact %d references missing symbol %q", record.ID, index, property.Symbol)
			}
			if err := requireOptionalSnapshotRef(types, property.ReadType, "type", record.ID, "property read type"); err != nil {
				return err
			}
			if err := requireOptionalSnapshotRef(types, property.WriteType, "type", record.ID, "property write type"); err != nil {
				return err
			}
			if strings.TrimSpace(property.Visibility) == "" {
				return fmt.Errorf("type %d property fact %d has no visibility", record.ID, index)
			}
		}
		if loweringReady {
			if err := validatePropertyFacts(record); err != nil {
				return err
			}
		}
		for _, signature := range slices.Concat(record.CallSignatures, record.ConstructSignatures) {
			if _, ok := signatures[signature]; !ok {
				return fmt.Errorf("type %d references missing signature %d", record.ID, signature)
			}
		}
		for _, index := range record.IndexInfos {
			if _, ok := types[index.KeyType]; !ok {
				return fmt.Errorf("type %d index references missing key type %d", record.ID, index.KeyType)
			}
			if _, ok := types[index.ValueType]; !ok {
				return fmt.Errorf("type %d index references missing value type %d", record.ID, index.ValueType)
			}
			if err := requireOptionalSnapshotRef(nodes, index.Declaration, "type", record.ID, "index declaration"); err != nil {
				return err
			}
		}
	}
	for _, symbol := range snapshot.Symbols {
		if err := requireOptionalSnapshotRef(symbols, symbol.Parent, "symbol", symbol.ID, "parent"); err != nil {
			return err
		}
		if err := requireOptionalSnapshotRef(symbols, symbol.ExportSymbol, "symbol", symbol.ID, "export symbol"); err != nil {
			return err
		}
		if err := requireOptionalSnapshotRef(types, symbol.Type, "symbol", symbol.ID, "type"); err != nil {
			return err
		}
		for _, declaration := range symbol.Declarations {
			if _, ok := nodes[declaration]; !ok {
				return fmt.Errorf("symbol %q references missing declaration %q", symbol.ID, declaration)
			}
		}
		if err := requireOptionalSnapshotRef(nodes, symbol.ValueDeclaration, "symbol", symbol.ID, "value declaration"); err != nil {
			return err
		}
	}
	for _, signature := range snapshot.Signatures {
		if !isDigest(signature.CanonicalHash) {
			return fmt.Errorf("signature %d has no canonical hash", signature.ID)
		}
		if err := requireOptionalSnapshotRef(nodes, signature.Declaration, "signature", signature.ID, "declaration"); err != nil {
			return err
		}
		if err := requireOptionalSnapshotRef(symbols, signature.ThisParameter, "signature", signature.ID, "this parameter"); err != nil {
			return err
		}
		for _, parameter := range signature.Parameters {
			if _, ok := symbols[parameter]; !ok {
				return fmt.Errorf("signature %d references missing parameter %q", signature.ID, parameter)
			}
		}
		for index, parameter := range signature.ParameterFacts {
			if _, ok := symbols[parameter.Symbol]; !ok {
				return fmt.Errorf("signature %d parameter fact %d references missing symbol %q", signature.ID, index, parameter.Symbol)
			}
			if _, ok := types[parameter.Type]; !ok {
				return fmt.Errorf("signature %d parameter fact %d references missing type %d", signature.ID, index, parameter.Type)
			}
		}
		for _, reference := range slices.Concat(signature.TypeParameters, signature.InstantiatedTypeArguments) {
			if _, ok := types[reference]; !ok {
				return fmt.Errorf("signature %d references missing type %d", signature.ID, reference)
			}
		}
		if err := requireOptionalSnapshotRef(types, signature.ReturnType, "signature", signature.ID, "return type"); err != nil {
			return err
		}
		if err := requireOptionalSnapshotRef(types, signature.Predicate.Type, "signature", signature.ID, "predicate type"); err != nil {
			return err
		}
		if loweringReady {
			if err := validateSignatureFacts(signature); err != nil {
				return err
			}
			if err := validateSignatureEffectProof(signature, nodes, types, symbols, signatures); err != nil {
				return err
			}
		}
	}
	if loweringReady {
		if err := validateSignatureEffectClosure(snapshot.Signatures, signatures); err != nil {
			return err
		}
	}

	if err := validateSnapshotModuleGraph(snapshot, modules, nodes, symbols, loweringReady); err != nil {
		return err
	}
	if !isDigest(snapshot.ContentHash) {
		return fmt.Errorf("snapshot content hash is empty")
	}
	if loweringReady {
		copy := snapshot
		if err := finalizeSnapshot(&copy); err != nil {
			return err
		}
		if copy.ContentHash != snapshot.ContentHash {
			return fmt.Errorf("snapshot content hash mismatch: got %s, want %s", snapshot.ContentHash, copy.ContentHash)
		}
	}
	return nil
}

func validateSnapshotConfigAndProvenance(snapshot ProgramSnapshot) error {
	if snapshot.Config.CanonicalProjectRoot == "" {
		return fmt.Errorf("canonical project root is empty")
	}
	if snapshot.Config.CanonicalConfigPath == "" {
		return fmt.Errorf("canonical config path is empty")
	}
	if err := validateFrontendBingoOptions(snapshot.Config.Bingo); err != nil {
		return err
	}
	wantBingo, err := hashCanonical(struct {
		Schema int          `json:"schema"`
		Bingo  BingoOptions `json:"bingo"`
	}{OptionsSchemaVersion, snapshot.Config.Bingo})
	if err != nil {
		return fmt.Errorf("hash Bingo options: %w", err)
	}
	if snapshot.Config.BingoDigest != wantBingo {
		return fmt.Errorf("Bingo options digest mismatch: got %s, want %s", snapshot.Config.BingoDigest, wantBingo)
	}
	wantTypeScript, err := hashCanonical(snapshot.Config.TypeScript)
	if err != nil {
		return fmt.Errorf("hash TypeScript options: %w", err)
	}
	if snapshot.Config.TypeScriptDigest != wantTypeScript {
		return fmt.Errorf("TypeScript options digest mismatch: got %s, want %s", snapshot.Config.TypeScriptDigest, wantTypeScript)
	}
	provenance := snapshot.Provenance
	if len(provenance.TypeScriptGoCommit) != 40 || !isLowerHex(provenance.TypeScriptGoCommit) {
		return fmt.Errorf("invalid TypeScript-go commit provenance %q", provenance.TypeScriptGoCommit)
	}
	if provenance.TypeScriptVersion == "" || provenance.GoVersion == "" {
		return fmt.Errorf("incomplete compiler provenance")
	}
	if !isDigest(provenance.StandardLibraryHash) || !isDigest(provenance.KindManifestHash) {
		return fmt.Errorf("invalid standard-library or kind-manifest provenance digest")
	}
	return nil
}

func validateSnapshotLogicalPaths(snapshot ProgramSnapshot) error {
	paths := []struct {
		field string
		path  string
	}{
		{field: "canonical project root", path: snapshot.Config.CanonicalProjectRoot},
		{field: "canonical config path", path: snapshot.Config.CanonicalConfigPath},
	}
	for _, entry := range paths {
		if err := validateFrontendLogicalPath(entry.field, entry.path); err != nil {
			return err
		}
	}
	if err := validateFrontendTypeScriptOptionPaths(snapshot.Config.TypeScript); err != nil {
		return err
	}
	for _, file := range snapshot.Files {
		if err := validateFrontendLogicalPath(fmt.Sprintf("file %q canonical path", file.ID), file.CanonicalPath); err != nil {
			return err
		}
	}
	for _, module := range snapshot.Modules {
		if err := validateFrontendLogicalPath(fmt.Sprintf("module %q canonical path", module.ID), module.CanonicalPath); err != nil {
			return err
		}
	}
	for diagnosticIndex, diagnostic := range snapshot.Diagnostics {
		if err := validateFrontendLogicalPath(fmt.Sprintf("diagnostic[%d] primary span file", diagnosticIndex), diagnostic.PrimarySpan.File); err != nil {
			return err
		}
		for relatedIndex, related := range diagnostic.RelatedSpans {
			field := fmt.Sprintf("diagnostic[%d] relatedSpans[%d] file", diagnosticIndex, relatedIndex)
			if err := validateFrontendLogicalPath(field, related.Span.File); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFrontendTypeScriptOptionPaths(options TypeScriptOptions) error {
	if err := validateFrontendLogicalPath("TypeScript option baseUrl", options.BaseURL); err != nil {
		return err
	}
	for index, path := range options.RootDirs {
		if err := validateFrontendLogicalPath(fmt.Sprintf("TypeScript option rootDirs[%d]", index), path); err != nil {
			return err
		}
	}
	for index, path := range options.TypeRoots {
		if err := validateFrontendLogicalPath(fmt.Sprintf("TypeScript option typeRoots[%d]", index), path); err != nil {
			return err
		}
	}
	for mappingIndex, mapping := range options.Paths {
		for substitutionIndex, path := range mapping.Substitutions {
			field := fmt.Sprintf("TypeScript option paths[%d].substitutions[%d]", mappingIndex, substitutionIndex)
			if err := validateFrontendLogicalPath(field, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFrontendLogicalPath(field, path string) error {
	if tspath.IsRootedDiskPath(path) {
		return fmt.Errorf("frontend snapshot %s contains rooted disk path %q", field, path)
	}
	return nil
}

func validateFrontendBingoOptions(options BingoOptions) error {
	switch options.Profile {
	case ProfileStatic, ProfileInterop, ProfileUnsafe:
	default:
		return fmt.Errorf("frontend snapshot profile %q is invalid", options.Profile)
	}
	if options.Runtime != "" || options.LLVMMajor != 0 || options.TargetTriple != "" || options.CPU != "" ||
		len(options.Features) != 0 || options.GC != "" || options.Exceptions != "" || options.Overflow != "" ||
		options.BoundsCheck != "" || len(options.Emit) != 0 {
		return fmt.Errorf("frontend snapshot contains backend-only Bingo options")
	}
	return nil
}

func isDigest(value string) bool {
	return len(value) == sha256.Size*2 && isLowerHex(value)
}

// IsCanonicalDigest reports whether value is the lowercase SHA-256 form used
// by the wire contract and adjacent serialized envelopes.
func IsCanonicalDigest(value string) bool {
	return isDigest(value)
}

func isLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func validConstantKind(kind string) bool {
	switch kind {
	case "none", "string", "number", "boolean", "bigint", "null":
		return true
	default:
		return false
	}
}

func countSnapshotNodeID(values []NodeID, wanted NodeID) int {
	count := 0
	for _, value := range values {
		if value == wanted {
			count++
		}
	}
	return count
}

func validateFlowFacts(node NodeSnapshot, types map[TypeID]TypeSnapshot) error {
	checks := []struct {
		id    TypeID
		hash  string
		field string
	}{
		{node.DeclaredType, node.Flow.DeclaredTypeHash, "declared"},
		{node.NarrowedType, node.Flow.NarrowedTypeHash, "narrowed"},
		{node.ContextualType, node.Flow.ContextualTypeHash, "contextual"},
	}
	for _, check := range checks {
		if check.id == 0 {
			if check.hash != "" {
				return fmt.Errorf("node %q flow %s hash has no TypeID", node.ID, check.field)
			}
			continue
		}
		typ, ok := types[check.id]
		if !ok {
			return fmt.Errorf("node %q flow %s type %d is missing", node.ID, check.field, check.id)
		}
		if check.hash == "" || check.hash != typ.CanonicalHash {
			return fmt.Errorf("node %q flow %s hash does not match type %d", node.ID, check.field, check.id)
		}
	}
	declared, narrowed := node.Flow.DeclaredTypeHash, node.Flow.NarrowedTypeHash
	wantNarrowed := declared != "" && narrowed != "" && declared != narrowed
	if node.Flow.Narrowed != wantNarrowed {
		return fmt.Errorf("node %q flow narrowed flag is inconsistent with type hashes", node.ID)
	}
	return nil
}

func validateAssertionAndNonNullFacts(node NodeSnapshot, types map[TypeID]TypeSnapshot) error {
	chain := node.AssertionChain
	if len(chain) == 0 {
		if node.AssertionTarget != 0 || node.AssertionAssignable {
			return fmt.Errorf("node %q has assertion summary fields without an assertion chain", node.ID)
		}
	} else {
		if node.DeclaredType == 0 || chain[0].SourceType != node.DeclaredType {
			return fmt.Errorf("node %q assertion chain does not start at its declared type", node.ID)
		}
		previousTarget := TypeID(0)
		for index, proof := range chain {
			if index != 0 && proof.SourceType != previousTarget {
				return fmt.Errorf("node %q assertion chain is discontinuous at proof %d", node.ID, index)
			}
			switch proof.OpenType {
			case "", "any", "unknown":
			default:
				return fmt.Errorf("node %q assertion proof %d has invalid open type %q", node.ID, index, proof.OpenType)
			}
			source := types[proof.SourceType]
			target := types[proof.TargetType]
			switch proof.RepresentationProof {
			case "identity":
				if source.CanonicalHash != target.CanonicalHash {
					return fmt.Errorf("node %q assertion proof %d claims identity for different types", node.ID, index)
				}
			case "open-type":
				if proof.OpenType == "" {
					return fmt.Errorf("node %q assertion proof %d has open-type representation without an open type", node.ID, index)
				}
			case "source-assignable":
				if !proof.Assignable || proof.OpenType != "" {
					return fmt.Errorf("node %q assertion proof %d has inconsistent source-assignable representation", node.ID, index)
				}
			case "checked-adapter-required":
				if proof.Assignable || proof.OpenType != "" {
					return fmt.Errorf("node %q assertion proof %d has inconsistent checked-adapter representation", node.ID, index)
				}
			default:
				return fmt.Errorf("node %q assertion proof %d has invalid representation proof %q", node.ID, index, proof.RepresentationProof)
			}
			previousTarget = proof.TargetType
		}
		last := chain[len(chain)-1]
		if node.AssertionTarget == 0 || node.AssertionTarget != last.TargetType {
			return fmt.Errorf("node %q assertion target does not match the final assertion proof", node.ID)
		}
		if node.AssertionAssignable != last.Assignable {
			return fmt.Errorf("node %q assertion assignability does not match the final assertion proof", node.ID)
		}
	}

	proof := node.NonNullProof
	if !proof.Present {
		if proof.OperandType != 0 || proof.ResultType != 0 || proof.ProofKind != "" || proof.RemovedNull || proof.RemovedUndefined {
			return fmt.Errorf("node %q has non-null proof fields without a present proof", node.ID)
		}
	} else {
		if len(chain) != 0 {
			return fmt.Errorf("node %q cannot carry assertion-chain and non-null proofs together", node.ID)
		}
		switch proof.ProofKind {
		case "open-any", "open-unknown", "redundant-non-null", "assertion-strip", "proven-non-null", "unproven":
		default:
			return fmt.Errorf("node %q has invalid non-null proof kind %q", node.ID, proof.ProofKind)
		}
		if node.DeclaredType != proof.OperandType {
			return fmt.Errorf("node %q non-null operand type does not match its declared type", node.ID)
		}
		if node.NarrowedType != proof.ResultType {
			return fmt.Errorf("node %q non-null result type does not match its narrowed type", node.ID)
		}
		if node.Flow.ProofKind != proof.ProofKind {
			return fmt.Errorf("node %q non-null proof kind does not match its flow proof", node.ID)
		}
		operand, result := types[proof.OperandType], types[proof.ResultType]
		changed := operand.CanonicalHash != result.CanonicalHash
		switch proof.ProofKind {
		case "redundant-non-null":
			if changed || proof.RemovedNull || proof.RemovedUndefined {
				return fmt.Errorf("node %q has inconsistent redundant non-null proof", node.ID)
			}
		case "assertion-strip", "proven-non-null":
			if !changed || !proof.RemovedNull && !proof.RemovedUndefined {
				return fmt.Errorf("node %q has inconsistent nullish-removal proof", node.ID)
			}
		case "open-any", "open-unknown", "unproven":
			if proof.RemovedNull || proof.RemovedUndefined {
				return fmt.Errorf("node %q unproven non-null proof claims a removed constituent", node.ID)
			}
		}
	}

	wantFlowProof := ""
	switch {
	case proof.Present:
		wantFlowProof = proof.ProofKind
	case len(chain) != 0:
		wantFlowProof = "assertion"
	case node.Flow.Narrowed:
		wantFlowProof = "checker-flow"
	}
	if node.Flow.ProofKind != wantFlowProof {
		return fmt.Errorf("node %q flow proof kind %q does not match semantic facts", node.ID, node.Flow.ProofKind)
	}
	return nil
}

func validateCaptureFacts(node NodeSnapshot) error {
	functionLike := isSnapshotFunctionLikeKind(node.Kind)
	if !functionLike {
		if node.CaptureComplete || len(node.CaptureSet) != 0 || len(node.CaptureBindings) != 0 {
			return fmt.Errorf("node %q carries capture facts but is not function-like", node.ID)
		}
		return nil
	}
	if !node.CaptureComplete {
		return fmt.Errorf("function-like node %q has no complete capture proof", node.ID)
	}
	lexicalSpecials := node.Kind == "KindArrowFunction"
	wantSet := make([]SymbolID, 0, len(node.CaptureBindings))
	seen := make(map[string]struct{}, len(node.CaptureBindings))
	previousKey := ""
	for index, binding := range node.CaptureBindings {
		key := string(binding.Symbol) + "\x00" + binding.Kind
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("node %q contains duplicate capture binding %d", node.ID, index)
		}
		seen[key] = struct{}{}
		if index != 0 && key <= previousKey {
			return fmt.Errorf("node %q capture bindings are not in canonical order", node.ID)
		}
		previousKey = key
		switch binding.Access {
		case "read", "write", "readwrite":
		default:
			return fmt.Errorf("node %q capture binding %d has invalid access %q", node.ID, index, binding.Access)
		}
		switch binding.Kind {
		case "binding":
			if binding.Symbol == "" {
				return fmt.Errorf("node %q capture binding %d has no symbol", node.ID, index)
			}
			wantSet = append(wantSet, binding.Symbol)
		case "this", "super", "arguments", "new.target":
			if binding.Symbol != "" || binding.Access != "read" || binding.Mutable {
				return fmt.Errorf("node %q special capture binding %d is inconsistent", node.ID, index)
			}
			if !lexicalSpecials {
				return fmt.Errorf("ordinary function-like node %q captures lexical %s", node.ID, binding.Kind)
			}
		default:
			return fmt.Errorf("node %q capture binding %d has invalid kind %q", node.ID, index, binding.Kind)
		}
	}
	if !slices.Equal(node.CaptureSet, wantSet) {
		return fmt.Errorf("node %q capture set does not match capture bindings", node.ID)
	}
	return nil
}

func isSnapshotFunctionLikeKind(kind string) bool {
	switch kind {
	case "KindFunctionDeclaration", "KindMethodDeclaration", "KindConstructor", "KindGetAccessor", "KindSetAccessor",
		"KindFunctionExpression", "KindArrowFunction", "KindMethodSignature", "KindCallSignature", "KindJSDocSignature",
		"KindConstructSignature", "KindIndexSignature", "KindFunctionType", "KindConstructorType":
		return true
	default:
		return false
	}
}

func validatePropertyFacts(record TypeSnapshot) error {
	if len(record.PropertyFacts) != len(record.Properties) {
		return fmt.Errorf("type %d property fact count %d does not match property count %d", record.ID, len(record.PropertyFacts), len(record.Properties))
	}
	for index, property := range record.PropertyFacts {
		if property.Symbol != record.Properties[index] {
			return fmt.Errorf("type %d property fact %d does not match property ordering", record.ID, index)
		}
		switch property.Visibility {
		case "public", "protected", "private":
		default:
			return fmt.Errorf("type %d property fact %d has invalid visibility %q", record.ID, index, property.Visibility)
		}
		if !property.HasGetter && property.ReadType != 0 {
			return fmt.Errorf("type %d property fact %d has a read type without a getter", record.ID, index)
		}
		if (!property.HasSetter || property.Readonly) && property.WriteType != 0 {
			return fmt.Errorf("type %d property fact %d has an invalid write type", record.ID, index)
		}
		if !property.HasGetter && !property.HasSetter {
			return fmt.Errorf("type %d property fact %d has no readable or writable contract", record.ID, index)
		}
		if property.Visibility == "private" {
			if strings.TrimSpace(property.PrivateIdentity) == "" {
				return fmt.Errorf("type %d private property fact %d has no identity", record.ID, index)
			}
		} else if property.PrivateIdentity != "" {
			return fmt.Errorf("type %d non-private property fact %d has a private identity", record.ID, index)
		}
	}
	return nil
}

func validateSignatureFacts(signature SignatureSnapshot) error {
	if len(signature.ParameterFacts) != len(signature.Parameters) {
		return fmt.Errorf("signature %d parameter fact count %d does not match parameter count %d", signature.ID, len(signature.ParameterFacts), len(signature.Parameters))
	}
	if signature.MinArgumentCount < 0 || signature.MinArgumentCount > len(signature.Parameters) {
		return fmt.Errorf("signature %d has invalid minimum argument count %d", signature.ID, signature.MinArgumentCount)
	}
	for index, parameter := range signature.ParameterFacts {
		if parameter.Symbol != signature.Parameters[index] {
			return fmt.Errorf("signature %d parameter fact %d does not match parameter ordering", signature.ID, index)
		}
		if parameter.Rest && index != len(signature.ParameterFacts)-1 {
			return fmt.Errorf("signature %d parameter fact %d is a non-final rest parameter", signature.ID, index)
		}
		wantOptional := index >= signature.MinArgumentCount
		if parameter.Optional != wantOptional {
			return fmt.Errorf("signature %d parameter fact %d optional flag is inconsistent with minimum argument count", signature.ID, index)
		}
	}
	wantRest := len(signature.ParameterFacts) != 0 && signature.ParameterFacts[len(signature.ParameterFacts)-1].Rest
	if signature.HasRest != wantRest {
		return fmt.Errorf("signature %d rest flag does not match parameter facts", signature.ID)
	}
	if len(signature.Effects) == 0 {
		return fmt.Errorf("signature %d has no effect classification", signature.ID)
	}
	seenEffects := make(map[string]struct{}, len(signature.Effects))
	for index, effect := range signature.Effects {
		switch effect {
		case "unknown", "pure", "call", "throw", "suspend", "read", "write", "alloc", "dynamic", "ffi", "block", "nondeterministic":
		default:
			return fmt.Errorf("signature %d has invalid effect %q", signature.ID, effect)
		}
		if _, duplicate := seenEffects[effect]; duplicate {
			return fmt.Errorf("signature %d contains duplicate effect %q", signature.ID, effect)
		}
		seenEffects[effect] = struct{}{}
		if index != 0 && signature.Effects[index-1] >= effect {
			return fmt.Errorf("signature %d effects are not in canonical order", signature.ID)
		}
	}
	if len(signature.Effects) != 1 && (slices.Contains(signature.Effects, "unknown") || slices.Contains(signature.Effects, "pure")) {
		return fmt.Errorf("signature %d mixes exclusive and concrete effects", signature.ID)
	}
	return nil
}

func validateSignatureEffectProof(signature SignatureSnapshot, nodes map[NodeID]NodeSnapshot, types map[TypeID]TypeSnapshot, symbols map[SymbolID]SymbolSnapshot, signatures map[SignatureID]SignatureSnapshot) error {
	proof := signature.EffectProof
	switch proof.Kind {
	case "declaration-only":
		if proof.Implementation != "" || proof.Complete || len(proof.DirectEffects) != 0 || len(proof.Calls) != 0 {
			return fmt.Errorf("signature %d has inconsistent declaration-only effect proof", signature.ID)
		}
		if !slices.Equal(signature.Effects, []string{"unknown"}) {
			return fmt.Errorf("signature %d declaration-only effect proof is not unknown", signature.ID)
		}
		return nil
	case "body-resolved":
	default:
		return fmt.Errorf("signature %d has invalid effect proof kind %q", signature.ID, proof.Kind)
	}

	implementation, ok := nodes[proof.Implementation]
	if proof.Implementation == "" || !ok {
		return fmt.Errorf("signature %d effect proof references missing implementation %q", signature.ID, proof.Implementation)
	}
	if !isSnapshotFunctionLikeKind(implementation.Kind) || snapshotEffectChildByRole(implementation, "body") == "" {
		return fmt.Errorf("signature %d effect proof implementation %q is not a function body", signature.ID, proof.Implementation)
	}
	if signature.Declaration != "" && signature.Declaration != proof.Implementation {
		if !snapshotDeclarationsShareOwner(signature.Declaration, proof.Implementation, symbols) {
			return fmt.Errorf("signature %d effect proof implementation %q does not match declaration %q", signature.ID, proof.Implementation, signature.Declaration)
		}
	}
	previousEffect := ""
	for index, effect := range proof.DirectEffects {
		switch effect {
		case "throw", "suspend", "read", "write", "alloc", "dynamic", "ffi", "block", "nondeterministic":
		default:
			return fmt.Errorf("signature %d direct effect %d has invalid effect %q", signature.ID, index, effect)
		}
		if index != 0 && previousEffect >= effect {
			return fmt.Errorf("signature %d direct effects are not in canonical order", signature.ID)
		}
		previousEffect = effect
	}
	wantEffects, syntaxComplete := snapshotImplementationDirectEffects(implementation, nodes, types, symbols)
	if !slices.Equal(proof.DirectEffects, wantEffects) {
		return fmt.Errorf("signature %d direct effect proof mismatch: got %v, want %v", signature.ID, proof.DirectEffects, wantEffects)
	}

	wantCalls := snapshotImplementationCallSites(implementation, nodes)
	if len(wantCalls) != len(proof.Calls) {
		return fmt.Errorf("signature %d effect call proof count %d does not match implementation call count %d", signature.ID, len(proof.Calls), len(wantCalls))
	}
	allCallsResolved := true
	for index, call := range proof.Calls {
		if call.Node != wantCalls[index] {
			return fmt.Errorf("signature %d effect call proof %d does not match implementation call ordering", signature.ID, index)
		}
		callNode := nodes[call.Node]
		if call.Signature != callNode.SelectedSignature {
			return fmt.Errorf("signature %d effect call proof %d target %d does not match call node signature %d", signature.ID, index, call.Signature, callNode.SelectedSignature)
		}
		if call.Signature == 0 {
			allCallsResolved = false
			continue
		}
		if _, ok := signatures[call.Signature]; !ok {
			return fmt.Errorf("signature %d effect call proof %d references missing signature %d", signature.ID, index, call.Signature)
		}
	}
	wantComplete := syntaxComplete && allCallsResolved
	if proof.Complete != wantComplete {
		return fmt.Errorf("signature %d effect proof completeness mismatch: got %t, want %t", signature.ID, proof.Complete, wantComplete)
	}
	return nil
}

func snapshotDeclarationsShareOwner(left, right NodeID, symbols map[SymbolID]SymbolSnapshot) bool {
	for _, symbol := range symbols {
		if slices.Contains(symbol.Declarations, left) && slices.Contains(symbol.Declarations, right) {
			return true
		}
	}
	return false
}

func snapshotImplementationDirectEffects(implementation NodeSnapshot, nodes map[NodeID]NodeSnapshot, types map[TypeID]TypeSnapshot, symbols map[SymbolID]SymbolSnapshot) ([]string, bool) {
	effects := make(map[string]struct{})
	complete := true
	if implementation.ModifierBits&snapshotEffectAsyncModifier != 0 || snapshotEffectChildByRole(implementation, "asteriskToken") != "" {
		appendSnapshotEffect(effects, "alloc")
	}
	visitSnapshotImplementation(implementation, nodes, func(node NodeSnapshot) {
		mode, registered := snapshotEffectRuleForKind(node.Kind)
		if !registered {
			complete = false
		}
		switch mode {
		case snapshotEffectAccess:
			read, write := snapshotEffectNodeAccess(node, nodes)
			if read {
				appendSnapshotEffect(effects, "read")
			}
			if write {
				appendSnapshotEffect(effects, "write")
			}
			if node.Kind == "KindElementAccessExpression" || snapshotEffectAccessHasAccessor(node, nodes, symbols) {
				complete = false
			}
		case snapshotEffectBinary:
			left := nodes[snapshotEffectChildByRole(node, "left")]
			right := nodes[snapshotEffectChildByRole(node, "right")]
			direct, modeled := snapshotBinaryEffects(node.SyntaxPayload.Operator, serializedEffectPrimitiveKind(left, types), serializedEffectPrimitiveKind(right, types))
			for _, effect := range direct {
				appendSnapshotEffect(effects, effect)
			}
			if !modeled {
				complete = false
			}
		case snapshotEffectPrefix:
			operand := nodes[snapshotEffectChildByRole(node, "operand")]
			direct, modeled := snapshotPrefixEffects(node.SyntaxPayload.Operator, serializedEffectPrimitiveKind(operand, types))
			for _, effect := range direct {
				appendSnapshotEffect(effects, effect)
			}
			if !modeled {
				complete = false
			}
		case snapshotEffectCallAlloc:
			appendSnapshotEffect(effects, "alloc")
			if snapshotEffectIsDynamicImport(node, nodes) {
				complete = false
			}
		case snapshotEffectCall:
			if snapshotEffectIsDynamicImport(node, nodes) {
				complete = false
			}
		case snapshotEffectIncompleteCall:
			complete = false
		case snapshotEffectLiteralAlloc:
			if snapshotEffectIsDestructuringLiteral(node, nodes) {
				complete = false
			} else {
				appendSnapshotEffect(effects, "alloc")
			}
		case snapshotEffectAlloc:
			appendSnapshotEffect(effects, "alloc")
		case snapshotEffectAllocIncomplete:
			appendSnapshotEffect(effects, "alloc")
			complete = false
		case snapshotEffectThrow:
			appendSnapshotEffect(effects, "throw")
		case snapshotEffectSuspend:
			appendSnapshotEffect(effects, "suspend")
		case snapshotEffectNondeterministic:
			appendSnapshotEffect(effects, "nondeterministic")
		case snapshotEffectIncomplete:
			if node.Kind == "KindDeleteExpression" {
				appendSnapshotEffect(effects, "write")
			}
			complete = false
		}
		if snapshotEffectRuntimeBindingName(node, nodes) && !snapshotEffectIsDirectInvokedBinding(node, nodes) {
			symbol := node.ResolvedSymbol
			if symbol == "" {
				symbol = node.Symbol
			}
			if symbol != "" && !snapshotEffectSymbolDeclaredWithin(symbols[symbol], implementation.ID, nodes) {
				read, write := snapshotEffectNodeAccess(node, nodes)
				if read {
					appendSnapshotEffect(effects, "read")
				}
				if write {
					appendSnapshotEffect(effects, "write")
				}
			}
		}
	})
	result := make([]string, 0, len(effects))
	for effect := range effects {
		result = append(result, effect)
	}
	slices.Sort(result)
	return result, complete
}

func snapshotImplementationCallSites(implementation NodeSnapshot, nodes map[NodeID]NodeSnapshot) []NodeID {
	result := make([]NodeID, 0)
	visitSnapshotImplementation(implementation, nodes, func(node NodeSnapshot) {
		if isSnapshotResolvedSignatureNode(node) {
			result = append(result, node.ID)
		}
	})
	return result
}

func visitSnapshotImplementation(implementation NodeSnapshot, nodes map[NodeID]NodeSnapshot, consume func(NodeSnapshot)) {
	var visit func(NodeID)
	visit = func(id NodeID) {
		node, ok := nodes[id]
		if !ok {
			return
		}
		if node.EvaluationFlags&snapshotEvaluationFlagTypeContext != 0 {
			return
		}
		consume(node)
		if id != implementation.ID && isSnapshotFunctionLikeKind(node.Kind) {
			return
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	for _, parameterID := range snapshotEffectChildrenByPrefix(implementation, "parameter[") {
		parameter, ok := nodes[parameterID]
		if !ok {
			continue
		}
		if name := snapshotEffectChildByRole(parameter, "name"); name != "" {
			visit(name)
		}
		if initializer := snapshotEffectChildByRole(parameter, "initializer"); initializer != "" {
			visit(initializer)
		}
	}
	if body := snapshotEffectChildByRole(implementation, "body"); body != "" {
		visit(body)
	}
}

func isSnapshotResolvedSignatureNode(node NodeSnapshot) bool {
	switch node.Kind {
	case "KindCallExpression", "KindNewExpression", "KindTaggedTemplateExpression", "KindJsxOpeningElement", "KindJsxSelfClosingElement", "KindJsxOpeningFragment", "KindDecorator":
		return true
	case "KindBinaryExpression":
		return node.SyntaxPayload.Operator == "KindInstanceOfKeyword"
	default:
		return false
	}
}

func validateSignatureEffectClosure(values []SignatureSnapshot, signatures map[SignatureID]SignatureSnapshot) error {
	computed := make(map[SignatureID][]string, len(values))
	ids := make([]SignatureID, 0, len(values))
	for _, signature := range values {
		ids = append(ids, signature.ID)
		proof := signature.EffectProof
		computed[signature.ID] = effectSummary(proof.DirectEffects, proof.Kind != "body-resolved" || !proof.Complete)
	}
	slices.Sort(ids)
	for changed := true; changed; {
		changed = false
		for _, id := range ids {
			signature := signatures[id]
			proof := signature.EffectProof
			unknown := proof.Kind != "body-resolved" || !proof.Complete
			effects := slices.Clone(proof.DirectEffects)
			for _, call := range proof.Calls {
				calleeEffects, ok := computed[call.Signature]
				if call.Signature == 0 || !ok || slices.Equal(calleeEffects, []string{"unknown"}) {
					unknown = true
					continue
				}
				for _, effect := range calleeEffects {
					if effect != "pure" {
						effects = append(effects, effect)
					}
				}
			}
			next := effectSummary(effects, unknown)
			if !slices.Equal(computed[id], next) {
				computed[id] = next
				changed = true
			}
		}
	}
	for _, signature := range values {
		if !slices.Equal(signature.Effects, computed[signature.ID]) {
			return fmt.Errorf("signature %d effect closure mismatch: got %v, want %v", signature.ID, signature.Effects, computed[signature.ID])
		}
	}
	return nil
}

func validateSnapshotTree(files []FileSnapshot, nodes map[NodeID]NodeSnapshot) error {
	state := make(map[NodeID]uint8, len(nodes))
	reached := make(map[NodeID]struct{}, len(nodes))
	var visit func(NodeID) error
	visit = func(id NodeID) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("snapshot node graph contains a cycle at %q", id)
		case 2:
			return nil
		}
		node, ok := nodes[id]
		if !ok {
			return fmt.Errorf("snapshot tree references missing node %q", id)
		}
		state[id] = 1
		reached[id] = struct{}{}
		for _, child := range node.Children {
			if err := visit(child); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, file := range files {
		for _, root := range file.RootNodes {
			if err := visit(root); err != nil {
				return err
			}
		}
	}
	if len(reached) != len(nodes) {
		return fmt.Errorf("snapshot node graph has %d unreachable nodes out of %d", len(nodes)-len(reached), len(nodes))
	}
	return nil
}

func validateSnapshotModuleGraph(snapshot ProgramSnapshot, modules map[ModuleID]ModuleSnapshot, nodes map[NodeID]NodeSnapshot, symbols map[SymbolID]SymbolSnapshot, loweringReady bool) error {
	for edgeIndex, edge := range snapshot.ModuleEdges {
		if loweringReady && edgeIndex > 0 && compareModuleEdges(snapshot.ModuleEdges[edgeIndex-1], edge) >= 0 {
			return fmt.Errorf("module edges are not in canonical order at %q", edge.Specifier)
		}
		if _, ok := modules[edge.Importer]; !ok {
			return fmt.Errorf("module edge references missing importer %q", edge.Importer)
		}
		if err := requireOptionalSnapshotRef(modules, edge.Imported, "module edge", edge.Specifier, "imported module"); err != nil {
			return err
		}
		if loweringReady && !edge.BindingsComplete {
			return fmt.Errorf("module edge %q has no complete binding proof", edge.Specifier)
		}
		if loweringReady && (edge.TypeOnly && edge.Value || edge.SideEffectOnly && (edge.TypeOnly || edge.Value)) {
			return fmt.Errorf("module edge %q has inconsistent aggregate phase flags", edge.Specifier)
		}
		if loweringReady {
			if err := validateModuleEdgeSource(edge, nodes); err != nil {
				return err
			}
			if err := validateModuleEdgeAttributes(edge); err != nil {
				return err
			}
			if err := validateModuleBindings(edge, nodes, symbols); err != nil {
				return err
			}
		}
	}
	seen := make(map[ModuleID]int, len(modules))
	for _, component := range snapshot.ModuleSCCs {
		for _, moduleID := range component.Modules {
			module, ok := modules[moduleID]
			if !ok {
				return fmt.Errorf("module SCC %d references missing module %q", component.ID, moduleID)
			}
			if previous, duplicate := seen[moduleID]; duplicate {
				return fmt.Errorf("module %q appears in SCC %d and %d", moduleID, previous, component.ID)
			}
			seen[moduleID] = component.ID
			if module.SCC != component.ID {
				return fmt.Errorf("module %q records SCC %d, component says %d", moduleID, module.SCC, component.ID)
			}
		}
	}
	if len(seen) != len(modules) {
		return fmt.Errorf("module SCC coverage is incomplete: got %d of %d modules", len(seen), len(modules))
	}
	if !isDigest(snapshot.ModuleGraphDigest) {
		return fmt.Errorf("module graph digest is invalid")
	}
	if want := digestModuleGraph(snapshot.Modules, snapshot.ModuleEdges, snapshot.ModuleSCCs); snapshot.ModuleGraphDigest != want {
		// A schema-1 reader may encounter a graph produced before binding facts
		// were added. Keep that read-only compatibility path while all lowering
		// validation remains on the schema-2 digest.
		if loweringReady || snapshot.ModuleGraphDigest != digestModuleGraphSchema(snapshot.Modules, snapshot.ModuleEdges, snapshot.ModuleSCCs, 1) {
			return fmt.Errorf("module graph digest mismatch: got %s, want %s", snapshot.ModuleGraphDigest, want)
		}
	}
	return nil
}

func validateModuleEdgeSource(edge ModuleEdge, nodes map[NodeID]NodeSnapshot) error {
	if edge.Source == "" {
		if edge.SpecifierNode != "" || len(edge.Bindings) != 0 || edge.Span.Start != 0 || edge.Span.End != 0 {
			return fmt.Errorf("synthetic module edge %q carries source-owned facts", edge.Specifier)
		}
		return nil
	}
	source, ok := nodes[edge.Source]
	if !ok {
		return fmt.Errorf("module edge %q references missing source node %q", edge.Specifier, edge.Source)
	}
	if source.Module != edge.Importer {
		return fmt.Errorf("module edge %q source node belongs to module %q, want %q", edge.Specifier, source.Module, edge.Importer)
	}
	specifier, ok := nodes[edge.SpecifierNode]
	if !ok {
		return fmt.Errorf("module edge %q references missing specifier node %q", edge.Specifier, edge.SpecifierNode)
	}
	if specifier.File != source.File || specifier.Module != edge.Importer || specifier.Span != edge.Span {
		return fmt.Errorf("module edge %q specifier node is inconsistent with its source", edge.Specifier)
	}
	if specifier.SyntaxPayload.Text != edge.Specifier {
		return fmt.Errorf("module edge %q does not match specifier node text %q", edge.Specifier, specifier.SyntaxPayload.Text)
	}
	if !snapshotNodeDescendsFrom(specifier.ID, source.ID, nodes) {
		return fmt.Errorf("module edge %q specifier node is not below source node %q", edge.Specifier, source.ID)
	}
	wantSourceKind := ""
	switch edge.Kind {
	case "import":
		if source.Kind != "KindImportDeclaration" && source.Kind != "KindJSImportDeclaration" {
			return fmt.Errorf("module edge %q import source has Kind %q", edge.Specifier, source.Kind)
		}
	case "export":
		wantSourceKind = "KindExportDeclaration"
	case "import-equals":
		wantSourceKind = "KindImportEqualsDeclaration"
	case "dynamic-import", "require":
		wantSourceKind = "KindCallExpression"
	case "import-type":
		wantSourceKind = "KindImportType"
	}
	if wantSourceKind != "" && source.Kind != wantSourceKind {
		return fmt.Errorf("module edge %q source has Kind %q, want %q", edge.Specifier, source.Kind, wantSourceKind)
	}
	return nil
}

func validateModuleEdgeAttributes(edge ModuleEdge) error {
	for index, attribute := range edge.ImportAttributes {
		if strings.TrimSpace(attribute.Name) == "" {
			return fmt.Errorf("module edge %q import attribute %d has an empty name", edge.Specifier, index)
		}
		if index > 0 {
			previous := edge.ImportAttributes[index-1]
			if previous.Name > attribute.Name || previous.Name == attribute.Name && previous.Value >= attribute.Value {
				return fmt.Errorf("module edge %q import attributes are not canonical", edge.Specifier)
			}
		}
	}
	return nil
}

func validateModuleBindings(edge ModuleEdge, nodes map[NodeID]NodeSnapshot, symbols map[SymbolID]SymbolSnapshot) error {
	hasType, hasValue := false, false
	seen := make(map[NodeID]struct{}, len(edge.Bindings))
	previousStart, previousEnd := -1, -1
	for index, binding := range edge.Bindings {
		node, ok := nodes[binding.Node]
		if !ok {
			return fmt.Errorf("module edge %q binding %d references missing node %q", edge.Specifier, index, binding.Node)
		}
		if _, duplicate := seen[binding.Node]; duplicate {
			return fmt.Errorf("module edge %q contains duplicate binding node %q", edge.Specifier, binding.Node)
		}
		seen[binding.Node] = struct{}{}
		if edge.Source == "" || node.Module != edge.Importer || !snapshotNodeDescendsFrom(node.ID, edge.Source, nodes) {
			return fmt.Errorf("module edge %q binding %d is outside its source declaration", edge.Specifier, index)
		}
		if index > 0 && (node.Span.Start < previousStart || node.Span.Start == previousStart && node.Span.End <= previousEnd) {
			return fmt.Errorf("module edge %q bindings are not in canonical source order", edge.Specifier)
		}
		previousStart, previousEnd = node.Span.Start, node.Span.End
		if binding.TypeOnly == binding.Value {
			return fmt.Errorf("module edge %q binding %d has invalid phase flags", edge.Specifier, index)
		}
		hasType = hasType || binding.TypeOnly
		hasValue = hasValue || binding.Value
		if err := validateModuleBindingShape(binding, node, nodes); err != nil {
			return fmt.Errorf("module edge %q binding %d: %w", edge.Specifier, index, err)
		}
		if binding.Kind != "export-star" {
			if binding.AliasSymbol == "" || binding.TargetSymbol == "" {
				return fmt.Errorf("module edge %q binding %d has incomplete alias resolution", edge.Specifier, index)
			}
			if _, ok := symbols[binding.AliasSymbol]; !ok {
				return fmt.Errorf("module edge %q binding %d references missing alias symbol %q", edge.Specifier, index, binding.AliasSymbol)
			}
			if _, ok := symbols[binding.TargetSymbol]; !ok {
				return fmt.Errorf("module edge %q binding %d references missing target symbol %q", edge.Specifier, index, binding.TargetSymbol)
			}
		} else if binding.AliasSymbol != "" {
			return fmt.Errorf("module edge %q export-star binding has an alias symbol", edge.Specifier)
		}
	}
	if edge.SideEffectOnly && len(edge.Bindings) != 0 {
		return fmt.Errorf("module edge %q is side-effect-only but has bindings", edge.Specifier)
	}
	if len(edge.Bindings) != 0 {
		wantTypeOnly := hasType && !hasValue
		if edge.TypeOnly != wantTypeOnly || edge.Value != hasValue || edge.SideEffectOnly {
			return fmt.Errorf("module edge %q aggregate flags do not match binding facts", edge.Specifier)
		}
	}
	return nil
}

func validateModuleBindingShape(binding ModuleBindingSnapshot, node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	childName := func(role string) string { return snapshotEffectChildText(node, role, nodes) }
	switch binding.Kind {
	case "default-import":
		if node.Kind != "KindImportClause" || binding.ImportedName != "default" || binding.LocalName == "" || binding.LocalName != childName("defaultBinding") || binding.ExportedName != "" {
			return fmt.Errorf("default-import names or node Kind are inconsistent")
		}
	case "named-import":
		name, property := childName("name"), childName("propertyName")
		if property == "" {
			property = name
		}
		if node.Kind != "KindImportSpecifier" || binding.ImportedName != property || binding.LocalName != name || binding.ExportedName != "" {
			return fmt.Errorf("named-import names or node Kind are inconsistent")
		}
	case "namespace-import":
		if node.Kind != "KindNamespaceImport" || binding.ImportedName != "*" || binding.LocalName != childName("name") || binding.ExportedName != "" {
			return fmt.Errorf("namespace-import names or node Kind are inconsistent")
		}
	case "import-equals":
		if node.Kind != "KindImportEqualsDeclaration" || binding.ImportedName != "*" || binding.LocalName != childName("name") || binding.ExportedName != "" {
			return fmt.Errorf("import-equals names or node Kind are inconsistent")
		}
	case "named-reexport":
		name, property := childName("name"), childName("propertyName")
		if property == "" {
			property = name
		}
		if node.Kind != "KindExportSpecifier" || binding.ImportedName != property || binding.LocalName != "" || binding.ExportedName != name {
			return fmt.Errorf("named-reexport names or node Kind are inconsistent")
		}
	case "namespace-reexport":
		if node.Kind != "KindNamespaceExport" || binding.ImportedName != "*" || binding.LocalName != "" || binding.ExportedName != childName("name") {
			return fmt.Errorf("namespace-reexport names or node Kind are inconsistent")
		}
	case "export-star":
		if node.Kind != "KindExportDeclaration" || binding.ImportedName != "*" || binding.LocalName != "" || binding.ExportedName != "*" {
			return fmt.Errorf("export-star names or node Kind are inconsistent")
		}
	default:
		return fmt.Errorf("unknown binding Kind %q", binding.Kind)
	}
	return nil
}

func snapshotNodeDescendsFrom(nodeID, ancestorID NodeID, nodes map[NodeID]NodeSnapshot) bool {
	for current := nodeID; current != ""; {
		if current == ancestorID {
			return true
		}
		node, ok := nodes[current]
		if !ok {
			return false
		}
		current = node.Parent
	}
	return false
}

func indexSnapshotValues[S ~[]E, E any, K comparable](values S, key func(E) K, name string) (map[K]E, error) {
	result := make(map[K]E, len(values))
	var zero K
	for _, value := range values {
		id := key(value)
		if id == zero {
			return nil, fmt.Errorf("%s table contains a zero ID", name)
		}
		if _, duplicate := result[id]; duplicate {
			return nil, fmt.Errorf("%s table contains duplicate ID %v", name, id)
		}
		result[id] = value
	}
	return result, nil
}

func requireOptionalSnapshotRef[K comparable, V any, O any](table map[K]V, reference K, ownerKind string, owner O, referenceKind string) error {
	var zero K
	if reference == zero {
		return nil
	}
	if _, ok := table[reference]; !ok {
		return fmt.Errorf("%s %v references missing %s %v", ownerKind, owner, referenceKind, reference)
	}
	return nil
}
