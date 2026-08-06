package tsfrontend

import (
	"context"
	"testing"
)

func buildReplayAddSnapshot(t *testing.T) *ProgramSnapshot {
	t.Helper()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function add(left: number, right: number): number { return left + right; }`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	return snapshot
}
