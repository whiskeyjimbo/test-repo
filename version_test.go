package main

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildBinary(t *testing.T, ldflags string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "app")
	args := []string{"build", "-o", bin}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

func runBinary(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run failed: %v", err)
		}
		code = ee.ExitCode()
	}
	return o.String(), e.String(), code
}

func check(t *testing.T, bin string, args []string, wantOut string) {
	t.Helper()
	out, errOut, code := runBinary(t, bin, args...)
	if out != wantOut || errOut != "" || code != 0 {
		t.Errorf("args %q: stdout=%q stderr=%q exit=%d; want stdout=%q stderr=\"\" exit=0",
			args, out, errOut, code, wantOut)
	}
}

func TestVersionDefault(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--version"}, "dev\n")
	check(t, bin, []string{"foo", "--version"}, "dev\n")
}

func TestVersionBuildTime(t *testing.T) {
	bin := buildBinary(t, "-X main.version=1.2.3")
	check(t, bin, []string{"--version"}, "1.2.3\n")
}

func TestNoArgsGreets(t *testing.T) {
	check(t, buildBinary(t, ""), nil, "Hello, world\n")
	check(t, buildBinary(t, "-X main.version=1.2.3"), nil, "Hello, world\n")
}

func TestNearMissArgsGreet(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range []string{"--foo", "foo", "-h", "-version", "-v", "--version=1"} {
		check(t, bin, []string{a}, "Hello, world\n")
	}
}
