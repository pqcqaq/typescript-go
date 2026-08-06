package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
)

const (
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2
)

var loadCompilerBuildIdentity = ast2bingo.InjectedCompilerBuildIdentity

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ts2bin-replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "frontend snapshot JSON file")
	flags.StringVar(input, "i", "", "frontend snapshot JSON file")
	output := flags.String("output", "", "write replay JSON to this file instead of stdout")
	flags.StringVar(output, "o", "", "write replay JSON to this file instead of stdout")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 1 || (*input != "" && flags.NArg() != 0) {
		fmt.Fprintln(stderr, "ts2bin-replay: provide exactly one frontend snapshot")
		return exitUsage
	}
	if *input == "" && flags.NArg() == 1 {
		*input = flags.Arg(0)
	}
	if *input == "" {
		fmt.Fprintln(stderr, "ts2bin-replay: missing frontend snapshot path")
		return exitUsage
	}

	data, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin-replay: read snapshot: %v\n", err)
		return exitFailure
	}
	identity, err := loadCompilerBuildIdentity()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin-replay: compiler identity: %v\n", err)
		return exitFailure
	}
	result, err := ast2bingo.ReplayFrontendSnapshot(data, identity)
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin-replay: replay snapshot: %v\n", err)
		return exitFailure
	}
	encoded, err := result.CanonicalBytes()
	if err != nil {
		fmt.Fprintf(stderr, "ts2bin-replay: encode result: %v\n", err)
		return exitFailure
	}
	if err := writeOutput(*output, encoded, stdout); err != nil {
		fmt.Fprintf(stderr, "ts2bin-replay: write result: %v\n", err)
		return exitFailure
	}
	return exitSuccess
}

func writeOutput(path string, data []byte, stdout io.Writer) error {
	if path == "" || path == "-" {
		if _, err := stdout.Write(data); err != nil {
			return err
		}
		_, err := io.WriteString(stdout, "\n")
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
