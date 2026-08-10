package irartifact

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
)

// RenderHIRText renders verified HIR in a stable, line-oriented form. It is
// intentionally derived from the typed artifact, so it cannot hide malformed
// fields or depend on map iteration order.
func RenderHIRText(module bingo.HIRModule) (string, error) {
	if err := bingo.VerifyCanonicalHIR(module); err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "hir schema=%d hash=%s\n", module.SchemaVersion, module.ContentHash)
	fmt.Fprintf(&out, "provenance frontend=%s source=%s stdlib=%s kind-manifest=%s requirements=%s\n",
		module.Provenance.FrontendSnapshotHash,
		module.Provenance.SourceContentHash,
		module.Provenance.StandardLibraryHash,
		module.Provenance.KindManifestHash,
		module.Provenance.LogicalCapabilityRequirementsDigest,
	)
	fmt.Fprintf(&out, "compiler upstream=%s fork=%s lowering=%s lowering-hash=%s\n",
		module.Provenance.CompilerBuildIdentity.UpstreamCommit,
		module.Provenance.CompilerBuildIdentity.ForkCommit,
		module.Provenance.CompilerBuildIdentity.LoweringSchema,
		module.Provenance.CompilerBuildIdentity.LoweringHash,
	)
	fmt.Fprintf(&out, "capabilities requirements=%s\n", joinCapabilities(module.LogicalCapabilityRequirements))
	for _, function := range module.Functions {
		fmt.Fprintf(&out, "function %d %q -> %s origin=%s:%d-%d\n", function.ID, function.Name, function.ReturnType, function.Origin.File, function.Origin.Start, function.Origin.End)
		for _, parameter := range function.Parameters {
			fmt.Fprintf(&out, "  parameter %d %q %s origin=%s:%d-%d\n", parameter.Value, parameter.Name, parameter.Type, parameter.Origin.File, parameter.Origin.Start, parameter.Origin.End)
		}
		for _, block := range function.Blocks {
			fmt.Fprintf(&out, "  block %d\n", block.ID)
			for _, operation := range block.Operations {
				fmt.Fprintf(&out, "    value %d %s %s operator=%q operands=%s effect=%s requirements=%s origin=%s:%d-%d\n",
					operation.ID, operation.Kind, operation.Type, operation.Operator, joinValueIDs(operation.Operands), operation.Effect,
					joinCapabilities(operation.LogicalCapabilityRequirements),
					operation.Origin.File, operation.Origin.Start, operation.Origin.End)
			}
			fmt.Fprintf(&out, "    terminator %s value=%d successors=%s origin=%s:%d-%d\n",
				block.Terminator.Kind, block.Terminator.Value, joinBlockIDs(block.Terminator.Successors),
				block.Terminator.Origin.File, block.Terminator.Origin.Start, block.Terminator.Origin.End)
		}
	}
	return out.String(), nil
}

// RenderMIRText renders verified final first-slice MIR in a stable form.
func RenderMIRText(module bingo.FirstSliceMIRArtifact) (string, error) {
	if err := bingo.VerifyBoundFirstSliceMIR(module); err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "mir schema=%d hash=%s\n", module.SchemaVersion, module.ContentHash)
	fmt.Fprintf(&out, "provenance hir=%s frontend=%s build-plan=%s target-context=%s data-layout=%s\n",
		module.Provenance.HIRHash,
		module.Provenance.FrontendSnapshotHash,
		module.Provenance.BuildPlanHash,
		module.Provenance.TargetContextHash,
		module.Provenance.DataLayoutHash,
	)
	fmt.Fprintf(&out, "provenance representation=%s toolchain=%s runtime=%s catalog=%s requirements=%s\n",
		module.Provenance.RepresentationPlanHash,
		module.Provenance.ToolchainManifestHash,
		module.Provenance.RuntimeManifestHash,
		module.Provenance.AvailableCapabilityCatalogHash,
		module.Provenance.LogicalCapabilityRequirementsDigest,
	)
	fmt.Fprintf(&out, "capabilities catalog=%s closure=%s bindings=%d\n",
		module.BoundCapabilityClosure.AvailableCapabilityCatalogHash,
		module.BoundCapabilityClosure.ContentHash,
		len(module.BoundCapabilityClosure.Bindings))
	for _, function := range module.Functions {
		fmt.Fprintf(&out, "function %d %q -> %s origin=%s:%d-%d\n", function.ID, function.Name, function.ReturnType, function.Origin.File, function.Origin.Start, function.Origin.End)
		for _, parameter := range function.Parameters {
			fmt.Fprintf(&out, "  parameter %d %q %s origin=%s:%d-%d\n", parameter.Value, parameter.Name, parameter.Type, parameter.Origin.File, parameter.Origin.Start, parameter.Origin.End)
		}
		for _, block := range function.Blocks {
			fmt.Fprintf(&out, "  block %d\n", block.ID)
			for _, instruction := range block.Instructions {
				fmt.Fprintf(&out, "    value %d %s %s operands=%s effect=%s requirements=%s origin=%s:%d-%d\n",
					instruction.ID, instruction.Kind, instruction.Type, joinValueIDs(instruction.Operands), instruction.Effect,
					joinCapabilities(instruction.LogicalCapabilityRequirements),
					instruction.Origin.File, instruction.Origin.Start, instruction.Origin.End)
			}
			fmt.Fprintf(&out, "    terminator %s value=%d origin=%s:%d-%d\n",
				block.Terminator.Kind, block.Terminator.Value, block.Terminator.Origin.File, block.Terminator.Origin.Start, block.Terminator.Origin.End)
		}
	}
	return out.String(), nil
}

func joinValueIDs(values []bingo.ValueID) string {
	if len(values) == 0 {
		return "[]"
	}
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = fmt.Sprintf("%%%d", value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func joinBlockIDs(values []bingo.BlockID) string {
	if len(values) == 0 {
		return "[]"
	}
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = fmt.Sprintf("bb%d", value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func joinCapabilities(values []bingo.RuntimeCapabilityID) string {
	if len(values) == 0 {
		return "[]"
	}
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = string(value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
