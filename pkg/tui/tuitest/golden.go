package tuitest

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateGolden controls whether AssertGolden rewrites the golden file instead
// of comparing against it. Enable with `go test ./... -tuitest.update`.
var updateGolden = flag.Bool("tuitest.update", false, "rewrite tuitest golden frames instead of comparing")

// goldenDir is the subdirectory of the test package where golden frames live.
const goldenDir = "testdata"

// AssertGolden compares the latest frame against the golden file
// testdata/<name>.golden. With -tuitest.update the golden file is (re)written
// from the current frame, which is how you record or refresh a snapshot after
// an intentional UI change.
//
// Goldens are the regression net for "we didn't mean to change the finished
// product": any unintended visual drift in a covered screen turns into a diff.
func (d *Driver) AssertGolden(name string) *Driver {
	d.tb.Helper()
	assertGolden(d.tb, name, d.frames.latest())
	return d
}

// assertGolden is the testing-agnostic core, split out so it can be unit
// tested without a Driver.
func assertGolden(tb testing.TB, name, actual string) {
	tb.Helper()

	path := filepath.Join(goldenDir, name+".golden")

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			tb.Fatalf("tuitest: creating golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(actual), 0o600); err != nil {
			tb.Fatalf("tuitest: writing golden %s: %v", path, err)
		}
		tb.Logf("tuitest: wrote golden %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("tuitest: reading golden %s: %v\n"+
			"run with -tuitest.update to create it", path, err)
	}

	if string(want) != actual {
		tb.Errorf("tuitest: frame does not match golden %s\n"+
			"run with -tuitest.update to accept the change\n"+
			"--- want ---\n%s\n--- got ---\n%s", path, string(want), actual)
	}
}
