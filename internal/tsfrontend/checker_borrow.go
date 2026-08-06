package tsfrontend

import (
	"context"
	"errors"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
)

// withCheckerForFile holds one exclusive checker only for the callback scope.
// The release function is deferred immediately, so early returns and panics
// cannot strand the checker-pool mutex. The callback must return pointer-free
// data and must not retain checker-owned Type, Signature, or Symbol values.
func withCheckerForFile[T any](
	ctx context.Context,
	program *compiler.Program,
	file *ast.SourceFile,
	capture func(*checker.Checker) (T, error),
) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if program == nil || file == nil || capture == nil {
		return zero, errors.New("tsfrontend: checker borrow requires program, file, and capture callback")
	}
	typeChecker, done := program.GetTypeCheckerForFileExclusive(ctx, file)
	defer done()
	if typeChecker == nil {
		return zero, errors.New("tsfrontend: checker pool returned a nil checker")
	}
	return capture(typeChecker)
}
