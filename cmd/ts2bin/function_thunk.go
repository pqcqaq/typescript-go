package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/microsoft/typescript-go/internal/artifactio"
	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func runEmitFunctionThunkReplay(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-function-thunk-replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "new file for the canonical function-thunk replay")
	flags.StringVar(output, "o", "", "new file for the canonical function-thunk replay")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || *output == "" {
		fmt.Fprintln(stderr, "ts2bin emit-function-thunk-replay: expected --output FILE SNAPSHOT")
		return exitUsage
	}
	snapshotBytes, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-function-thunk-replay: read snapshot: %v\n", err)
		return exitUsage
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-function-thunk-replay: decode snapshot: %v\n", err)
		return exitDiagnostics
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-function-thunk-replay: compiler identity: %v\n", err)
		return exitUsage
	}
	replay, err := ast2bingo.ReplayFunctionThunkSnapshot(frontend.Program, identity)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-function-thunk-replay: %v\n", err)
		return exitDiagnostics
	}
	encoded, err := replay.CanonicalBytes()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-function-thunk-replay: encode replay: %v\n", err)
		return exitDiagnostics
	}
	outputPath, err := filepath.Abs(*output)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-function-thunk-replay: resolve output: %v\n", err)
		return exitUsage
	}
	if err := artifactio.PublishNewFile(outputPath, append(encoded, '\n'), 0o644); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-function-thunk-replay: publish: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "[ok] emit-function-thunk-replay %s %s\n", outputPath, replay.ContentHash)
	return exitSuccess
}
