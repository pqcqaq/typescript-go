// Command kind_manifest_gen emits the checked-in AST Kind support manifest.
// Keep the classification rules explicit here: an upstream Kind addition must
// be reviewed as a source-language decision instead of silently inheriting a
// lowering plan. The reviewed inventory fingerprint makes that review gate
// fail closed when ast.Kind changes.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/internal/ast"
)

type manifest struct {
	SchemaVersion uint32  `json:"schemaVersion"`
	Kinds         []entry `json:"kinds"`
}

type entry struct {
	Kind            string   `json:"kind"`
	KindValue       int16    `json:"kindValue"`
	Domain          string   `json:"domain"`
	SyntaxGroup     string   `json:"syntaxGroup"`
	PlannedLevels   []string `json:"plannedLevels"`
	DecisionPolicy  string   `json:"decisionPolicy"`
	DefaultDecision string   `json:"defaultDecision"`
	Feature         string   `json:"feature"`
	Capability      string   `json:"capability"`
	GateHandler     string   `json:"gateHandler"`
	LoweringPlan    string   `json:"loweringPlan,omitempty"`
}

const (
	reviewedKindCount           = 351
	reviewedKindInventorySHA256 = "fb7d5644ffcb7e5cce991ed3716b9ac8368c93d1c72dfb90ad93ee4806215751"
)

func main() {
	outputPath := flag.String("out", "", "write the manifest to this path instead of stdout")
	flag.Parse()
	output, err := generateManifest()
	if err != nil {
		panic(err)
	}
	if *outputPath == "" {
		if _, err := os.Stdout.Write(output); err != nil {
			panic(err)
		}
		return
	}
	if err := os.WriteFile(*outputPath, output, 0o644); err != nil {
		panic(err)
	}
}

func generateManifest() ([]byte, error) {
	names := currentKindNames()
	if err := validateReviewedKindInventory(names); err != nil {
		return nil, err
	}
	result := manifest{SchemaVersion: 2, Kinds: make([]entry, 0, len(names))}
	for value := ast.Kind(0); value < ast.KindCount; value++ {
		result.Kinds = append(result.Kinds, classify(value))
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func currentKindNames() []string {
	names := make([]string, 0, int(ast.KindCount))
	for value := ast.Kind(0); value < ast.KindCount; value++ {
		names = append(names, value.String())
	}
	return names
}

func validateReviewedKindInventory(names []string) error {
	if len(names) != reviewedKindCount {
		return fmt.Errorf("unreviewed AST Kind count %d; review and update the manifest inventory", len(names))
	}
	digest := sha256.Sum256([]byte(strings.Join(names, "\n")))
	if got := fmt.Sprintf("%x", digest); got != reviewedKindInventorySHA256 {
		return fmt.Errorf("unreviewed AST Kind inventory digest %s; review and update the manifest inventory", got)
	}
	return nil
}

func classify(value ast.Kind) entry {
	kind := value.String()
	short := strings.TrimPrefix(kind, "Kind")
	domain, group := classifyGroup(short)
	level := "S1"
	decision := "accept-desugar"
	policy := "desugar"
	feature := slug(short)
	capability := "frontend"
	gateHandler := "gateSyntax"
	loweringPlan := "lowerDesugar"

	if domain == "lexical" || domain == "type" || domain == "jsdoc" {
		level, decision, policy, capability, gateHandler, loweringPlan = "C", "accept-erase", "erase-type", "compile-time", "gateErase", "eraseType"
	}
	if short == "SourceFile" || short == "Identifier" || short == "PrivateIdentifier" || short == "NumericLiteral" || short == "StringLiteral" || short == "NullKeyword" || short == "TrueKeyword" || short == "FalseKeyword" || short == "ThisKeyword" || short == "SuperKeyword" {
		level, decision, policy, capability, gateHandler, loweringPlan = "S0", "accept-direct", "direct", "frontend", "gateSyntax", "lowerDirect"
	}

	// Explicitly rejected or deferred constructs are listed here rather than
	// inferred from their spelling, so an upstream rename cannot change policy.
	switch short {
	case "EndOfFile", "InterfaceDeclaration", "TypeAliasDeclaration", "JSTypeAliasDeclaration", "JSImportDeclaration", "QualifiedName":
		level, decision, policy, capability, gateHandler, loweringPlan = "C", "accept-erase", "erase-type", "compile-time", "gateErase", "eraseType"
	case "Parameter", "ExpressionWithTypeArguments", "TypeOfExpression":
		level, decision, policy, capability, gateHandler, loweringPlan = "S1", "accept-desugar", "desugar", "frontend", "gateSyntax", "lowerDesugar"
	case "Unknown":
		level, decision, policy, capability, gateHandler, loweringPlan = "R", "reject", "reject-static", "parser-recovery", "gateRecovery", ""
	case "ConflictMarkerTrivia", "NonTextFileMarkerTrivia", "MissingDeclaration", "SyntaxList", "NotEmittedStatement", "PartiallyEmittedExpression", "SyntheticReferenceExpression", "NotEmittedTypeElement", "SyntheticExpression":
		level, decision, policy, capability, gateHandler, loweringPlan = "R", "reject", "reject-static", "parser-recovery", "gateRecovery", ""
	case "AsExpression", "TypeAssertionExpression", "SatisfiesExpression":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "S1", "accept-desugar", "checked-boundary", "checked-cast", "gateAssertion", "lowerCheckedCast", "unsafe-assertion"
	case "NonNullExpression":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "S1", "accept-desugar", "checked-boundary", "checked-cast", "gateAssertion", "lowerCheckedCast", "non-null-assertion"
	case "AwaitExpression", "AwaitKeyword", "AsyncKeyword":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "R", "reject", "reject-static", "async", "gateFeature", "", "async"
	case "YieldExpression", "YieldKeyword":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "R", "reject", "reject-static", "generator", "gateFeature", "", "generator"
	case "UsingKeyword":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "R", "reject", "reject-static", "disposable", "gateFeature", "", "using"
	case "Decorator":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "P", "defer-feature", "planned-feature", "decorator", "gateFeature", "", "decorators"
	case "ClassStaticBlockDeclaration":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "P", "defer-feature", "planned-feature", "class-static-block", "gateFeature", "", "class-static-block"
	case "TryStatement", "ThrowStatement", "CatchClause":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "R", "reject", "reject-static", "exceptions", "gateFeature", "", "exceptions"
	case "WithStatement":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "R", "reject", "reject-static", "dynamic-object", "gateFeature", "", "with"
	case "ForInStatement":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "R", "reject", "reject-static", "dynamic-object", "gateFeature", "", "dynamic-enumeration"
	case "DeleteExpression":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "R", "reject", "reject-static", "dynamic-object", "gateFeature", "", "delete"
	case "JsxElement", "JsxSelfClosingElement", "JsxOpeningElement", "JsxClosingElement", "JsxFragment", "JsxOpeningFragment", "JsxClosingFragment", "JsxAttribute", "JsxAttributes", "JsxSpreadAttribute", "JsxExpression", "JsxNamespacedName", "JsxText", "JsxTextAllWhiteSpaces":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "P", "defer-feature", "planned-feature", "jsx", "gateFeature", "", "jsx"
	case "ImportAttributes", "ImportAttribute":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "P", "defer-feature", "planned-feature", "import-attributes", "gateFeature", "", "import-attributes"
	case "DeferKeyword":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "P", "defer-feature", "planned-feature", "module-defer", "gateFeature", "", "import-defer"
	case "EnumDeclaration":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "P", "defer-feature", "planned-feature", "enum-runtime", "gateFeature", "", "enum-runtime"
	case "ModuleDeclaration":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "P", "defer-feature", "planned-feature", "namespace-runtime", "gateFeature", "", "namespace-runtime"
	case "ImportEqualsDeclaration", "ExternalModuleReference":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "R", "reject", "reject-static", "cjs-interop", "gateFeature", "", "cjs-interop"
	case "BigIntLiteral":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "S2", "accept-runtime", "runtime-capability", "runtime:bigint", "gateCapability", "lowerRuntime", "bigint"
	case "RegularExpressionLiteral":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "S2", "accept-runtime", "runtime-capability", "runtime:regexp", "gateCapability", "lowerRuntime", "regexp"
	case "DebuggerStatement":
		level, decision, policy, capability, gateHandler, loweringPlan, feature = "S1", "accept-desugar", "erase-debug", "debugger", "gateSyntax", "lowerErase", "debugger"
	}

	return entry{
		Kind:            kind,
		KindValue:       int16(value),
		Domain:          domain,
		SyntaxGroup:     group,
		PlannedLevels:   []string{level},
		DecisionPolicy:  policy,
		DefaultDecision: decision,
		Feature:         feature,
		Capability:      capability,
		GateHandler:     gateHandler,
		LoweringPlan:    loweringPlan,
	}
}

func classifyGroup(short string) (domain, group string) {
	if strings.Contains(short, "JSDoc") {
		return "type", "jsdoc"
	}
	if strings.HasSuffix(short, "Trivia") || strings.HasSuffix(short, "Token") || strings.HasSuffix(short, "Keyword") || short == "Identifier" || short == "PrivateIdentifier" {
		return "lexical", "token"
	}
	if strings.HasPrefix(short, "Jsx") {
		return "runtime", "jsx"
	}
	if strings.Contains(short, "Import") || strings.Contains(short, "Export") || strings.Contains(short, "Module") || strings.Contains(short, "Namespace") {
		return "module", "module"
	}
	if strings.HasSuffix(short, "Statement") || strings.HasSuffix(short, "Clause") {
		return "runtime", "statement"
	}
	if strings.Contains(short, "Expression") || strings.HasSuffix(short, "Literal") {
		return "runtime", "expression"
	}
	if strings.Contains(short, "Type") || strings.HasSuffix(short, "Signature") || strings.HasSuffix(short, "Parameter") || short == "ThisType" {
		return "type", "type"
	}
	if strings.HasSuffix(short, "Declaration") || strings.HasSuffix(short, "Member") || strings.Contains(short, "Property") || strings.Contains(short, "Accessor") || strings.Contains(short, "Constructor") {
		return "runtime", "declaration"
	}
	if strings.Contains(short, "Binding") {
		return "runtime", "binding"
	}
	return "syntax", "misc"
}

func slug(value string) string {
	var result strings.Builder
	for index, character := range value {
		if unicode.IsUpper(character) && index > 0 {
			result.WriteByte('-')
		}
		result.WriteRune(unicode.ToLower(character))
	}
	return result.String()
}
