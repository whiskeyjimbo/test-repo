package main

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestQuietSilent(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range [][]string{
		{"--quiet"}, {"foo", "--quiet"}, {"--quiet", "foo"}, {"--quiet", "--quiet"}, {"--", "--quiet"},
		{"--name=Ann", "--quiet"}, {"--quiet", "--name=Ann"}, {"--quiet", "--name="},
		{"--name=--quiet", "--quiet"}, {"--quiet", "--shout"}, {"--shout", "--quiet"},
	} {
		check(t, bin, a, "")
	}
}

func TestQuietNameValueIsOnlyAValue(t *testing.T) {
	check(t, buildBinary(t, ""), []string{"--name=--quiet"}, "Hello, --quiet\n")
}

func TestQuietHelpVersionStillWin(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--quiet", "--help"}, usage)
	check(t, bin, []string{"--help", "--quiet"}, usage)
	check(t, bin, []string{"--quiet", "--version"}, "dev\n")
	check(t, bin, []string{"--version", "--quiet"}, "dev\n")
	check(t, bin, []string{"--name=Ann", "--quiet", "--version"}, "dev\n")
	check(t, bin, []string{"--quiet", "--quiet", "--help"}, usage)
}

func TestQuietNearMissesGreet(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range []string{"--quiet=1", "--quiet=", "-quiet", "-q", "--QUIET", "--quiets", "quiet", " --quiet"} {
		check(t, bin, []string{a}, "Hello, world\n")
	}
}

func TestQuietUnusableStdout(t *testing.T) {
	bin := buildBinary(t, "")

	run := func(t *testing.T, setup func(cmd *exec.Cmd) func()) {
		t.Helper()
		cmd := exec.Command(bin, "--quiet")
		var errBuf stderrBuf
		cmd.Stderr = &errBuf
		cleanup := setup(cmd)
		err := cmd.Run()
		if cleanup != nil {
			cleanup()
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Errorf("want exit 0, got %v", err)
		} else if err != nil {
			t.Fatalf("run failed: %v", err)
		}
		if errBuf.n != 0 {
			t.Errorf("stderr not empty: %d bytes", errBuf.n)
		}
	}

	t.Run("dev-full", func(t *testing.T) {
		f, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
		if err != nil {
			t.Skip("no /dev/full")
		}
		defer f.Close()
		run(t, func(c *exec.Cmd) func() { c.Stdout = f; return nil })
	})
	t.Run("closed", func(t *testing.T) {
		run(t, func(c *exec.Cmd) func() {
			c.Path, c.Args = "/bin/sh", []string{"sh", "-c", `exec "$0" --quiet >&-`, bin}
			return nil
		})
	})
	t.Run("pipe-no-reader", func(t *testing.T) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		r.Close()
		defer w.Close()
		run(t, func(c *exec.Cmd) func() { c.Stdout = w; return nil })
	})
}

type stderrBuf struct{ n int }

func (b *stderrBuf) Write(p []byte) (int, error) { b.n += len(p); return len(p), nil }
