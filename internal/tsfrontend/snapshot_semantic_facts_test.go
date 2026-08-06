package tsfrontend

import (
	"context"
	"strings"
	"testing"
)

func TestSnapshotCarriesPropertyAndSignatureFacts(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"target":"es2022"},"files":["main.ts"]}`,
		"/project/main.ts": `
			export class Box {
				readonly fixed: number = 1;
				optional?: string;
				#secret = 0;
				get value(): string { return "value"; }
				set value(input: string | number) { void input; }
			}
			export function combine(required: number, optional?: string, ...rest: boolean[]): number {
				return required + rest.length + (optional ? 1 : 0);
			}
		`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}

	symbolNames := make(map[SymbolID]string, len(snapshot.Symbols))
	for _, symbol := range snapshot.Symbols {
		symbolNames[symbol.ID] = symbol.Name
	}
	typeDebug := make(map[TypeID]string, len(snapshot.Types))
	for _, typ := range snapshot.Types {
		typeDebug[typ.ID] = typ.DebugText
	}
	properties := make(map[string]PropertySnapshot)
	for _, typ := range snapshot.Types {
		for _, property := range typ.PropertyFacts {
			properties[symbolNames[property.Symbol]] = property
		}
	}
	if !properties["fixed"].Readonly || !properties["optional"].Optional {
		t.Fatalf("property flags = %#v", properties)
	}
	private := properties["#secret"]
	if private.Visibility != "private" || private.PrivateIdentity == "" {
		t.Fatalf("private property fact = %#v", private)
	}
	accessor := properties["value"]
	if !accessor.HasGetter || !accessor.HasSetter || accessor.ReadType == 0 || accessor.WriteType == 0 {
		t.Fatalf("accessor fact = %#v", accessor)
	}
	if typeDebug[accessor.ReadType] == typeDebug[accessor.WriteType] || !strings.Contains(typeDebug[accessor.WriteType], "string | number") {
		t.Fatalf("accessor read/write types = %q / %q", typeDebug[accessor.ReadType], typeDebug[accessor.WriteType])
	}

	var combine SignatureSnapshot
	for _, signature := range snapshot.Signatures {
		if len(signature.ParameterFacts) == 3 {
			combine = signature
			break
		}
	}
	if combine.ID == 0 || len(combine.ParameterFacts) != len(combine.Parameters) {
		t.Fatalf("combine signature = %#v", combine)
	}
	if combine.ParameterFacts[0].Optional || !combine.ParameterFacts[1].Optional || !combine.ParameterFacts[2].Rest {
		t.Fatalf("parameter facts = %#v", combine.ParameterFacts)
	}
	if len(combine.Effects) == 0 {
		t.Fatalf("signature effects = %#v", combine.Effects)
	}
}
