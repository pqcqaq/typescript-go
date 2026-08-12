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

func runEmitCheckedCastReplay(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("emit-checked-cast-replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "new file for the canonical checked-cast replay")
	flags.StringVar(output, "o", "", "new file for the canonical checked-cast replay")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || *output == "" {
		fmt.Fprintln(stderr, "ts2bin emit-checked-cast-replay: expected --output FILE SNAPSHOT")
		return exitUsage
	}

	snapshotBytes, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-checked-cast-replay: read snapshot: %v\n", err)
		return exitUsage
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-checked-cast-replay: decode snapshot: %v\n", err)
		return exitDiagnostics
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-checked-cast-replay: compiler identity: %v\n", err)
		return exitUsage
	}
	replay, err := ast2bingo.ReplayCheckedObjectCastSnapshot(frontend.Program, identity)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-checked-cast-replay: %v\n", err)
		return exitDiagnostics
	}
	encoded, err := replay.CanonicalBytes()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-checked-cast-replay: encode replay: %v\n", err)
		return exitDiagnostics
	}
	outputPath, err := filepath.Abs(*output)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-checked-cast-replay: resolve output: %v\n", err)
		return exitUsage
	}
	if err := artifactio.PublishNewFile(outputPath, append(encoded, '\n'), 0o644); err != nil {
		fmt.Fprintf(stderr, "ts2bin emit-checked-cast-replay: publish: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "[ok] emit-checked-cast-replay %s %s\n", outputPath, replay.ContentHash)
	return exitSuccess
}
