package tsfrontend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/internal/checker"
)

func TestWithCheckerForFileReleasesAfterReturnAndError(t *testing.T) {
	t.Parallel()
	build := testProgramBuild(t)
	file := build.program.GetSourceFile("/project/main.ts")
	if file == nil {
		t.Fatal("source file not found")
	}

	for _, capture := range []func(*checker.Checker) (int, error){
		func(*checker.Checker) (int, error) { return 7, nil },
		func(*checker.Checker) (int, error) { return 0, errors.New("stop") },
	} {
		_, _ = withCheckerForFile(context.Background(), build.program, file, capture)
		if _, err := withCheckerForFile(context.Background(), build.program, file, func(*checker.Checker) (int, error) { return 9, nil }); err != nil {
			t.Fatalf("checker was not reacquirable: %v", err)
		}
	}
}

func TestWithCheckerForFileReleasesAfterPanic(t *testing.T) {
	t.Parallel()
	build := testProgramBuild(t)
	file := build.program.GetSourceFile("/project/main.ts")
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("capture panic did not propagate")
			}
		}()
		_, _ = withCheckerForFile(context.Background(), build.program, file, func(*checker.Checker) (int, error) {
			panic("capture failed")
		})
	}()

	done := make(chan error, 1)
	go func() {
		_, err := withCheckerForFile(context.Background(), build.program, file, func(*checker.Checker) (int, error) { return 1, nil })
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("checker remained locked after panic")
	}
}

func TestWithCheckerForFileRejectsCanceledContext(t *testing.T) {
	t.Parallel()
	build := testProgramBuild(t)
	file := build.program.GetSourceFile("/project/main.ts")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := withCheckerForFile(ctx, build.program, file, func(*checker.Checker) (int, error) {
		called = true
		return 0, nil
	})
	if err == nil || called {
		t.Fatalf("err = %v, called = %v", err, called)
	}
}

func TestWithCheckerForFileConcurrentBorrow(t *testing.T) {
	t.Parallel()
	build := testProgramBuild(t)
	file := build.program.GetSourceFile("/project/main.ts")
	const workers = 12
	done := make(chan error, workers)
	for range workers {
		go func() {
			_, err := withCheckerForFile(context.Background(), build.program, file, func(c *checker.Checker) (int, error) {
				if c.GetTypeAtLocation(file.AsNode()) == nil {
					return 0, errors.New("nil source-file type")
				}
				return 1, nil
			})
			done <- err
		}()
	}
	for range workers {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func testProgramBuild(t *testing.T) *programBuild {
	t.Helper()
	build, err := buildProgram(context.Background(), testBuildRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export const value = 1;`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if build.program == nil {
		t.Fatalf("program was not constructed: %#v", build.diagnostics)
	}
	return build
}
