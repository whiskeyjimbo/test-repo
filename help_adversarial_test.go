package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every argument here is a near miss of a flag, so the greeting must print.
func TestHelpMangledArgsGreet(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range []string{
		"", " ", "--", "-", "---help", "--Help", "--he", "--helpp", "--help-", "--help=",
		" --help", "--help ", "\t--help", "--help\t", "\n--help", "--help\n", "--help\r", "--help\r\n",
		"--help\xff", "\xff--help", "--\xc0\xafhelp", "\xe2\x80\x94help", "\xef\xbc\x8d\xef\xbc\x8dhelp",
		"\xe2\x80\x8b--help", "--help\xe2\x80\x8b", "\xef\xbb\xbf--help", "\"--help\"", "%s", "%n%n",
		" --version", "--version ", "--version\n", "--VERSION", "--version\xff",
	} {
		check(t, bin, []string{a}, "Hello, world\n")
	}
}

// The first exact flag decides the output, wherever it is and whatever precedes it.
func TestHelpScanPosition(t *testing.T) {
	bin := buildBinary(t, "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"a", "b", "--help"}, wantUsage},
		{[]string{"a", "b", "c", "d", "e", "--version", "--help"}, "dev\n"},
		{[]string{"", "--help"}, wantUsage},
		{[]string{"--help", ""}, wantUsage},
		{[]string{"", "--version"}, "dev\n"},
		{[]string{"--", "--help"}, wantUsage},
		{[]string{"--", "--version"}, "dev\n"},
		{[]string{"--help", "--"}, wantUsage},
		{[]string{"--help", "--help"}, wantUsage},
		{[]string{"--help", "--help", "--help", "--version"}, wantUsage},
		{[]string{"\xff", "--help"}, wantUsage},
		{[]string{"--help=1", "--help"}, wantUsage},
		{[]string{"--version=1", "--help"}, wantUsage},
		{[]string{"--HELP", "--version", "--help"}, "dev\n"},
		{[]string{"-h", "--version"}, "dev\n"},
	} {
		check(t, bin, tc.args, tc.want)
	}
}

func TestHelpAfterManyArgs(t *testing.T) {
	bin := buildBinary(t, "")
	filler := make([]string, 50000)
	for i := range filler {
		filler[i] = "x"
	}
	check(t, bin, append(filler[:len(filler):len(filler)], "--help", "--version"), wantUsage)
	check(t, bin, append(filler[:len(filler):len(filler)], "--version", "--help"), "dev\n")
	check(t, bin, filler, "Hello, world\n")
	check(t, bin, append([]string{"--help"}, filler...), wantUsage)
}

func TestHelpVeryLongArgs(t *testing.T) {
	bin := buildBinary(t, "")
	const maxArg = 131071 // Linux MAX_ARG_STRLEN less the NUL.
	long := strings.Repeat("a", maxArg)
	check(t, bin, []string{long}, "Hello, world\n")
	check(t, bin, []string{long, "--help"}, wantUsage)
	check(t, bin, []string{"--help" + long[6:]}, "Hello, world\n")
	check(t, bin, []string{long[6:] + "--help"}, "Hello, world\n")
}

// Runs bin with an exact argv, so argv[0] need not be the file name.
func checkArgv(t *testing.T, bin, dir string, argv []string, wantOut string) {
	t.Helper()
	var o, e strings.Builder
	cmd := &exec.Cmd{Path: bin, Args: argv, Dir: dir, Stdout: &o, Stderr: &e}
	err := cmd.Run()
	if err != nil || o.String() != wantOut || e.String() != "" {
		t.Errorf("argv %q in %q: stdout=%q stderr=%q err=%v; want stdout=%q", argv, dir, o.String(), e.String(), err, wantOut)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

// The usage text always says test-repo, and the binary's own name is never a flag.
func TestHelpIgnoresBinaryName(t *testing.T) {
	bin := buildBinary(t, "")
	dir := t.TempDir()
	for _, name := range []string{"help", "my-help-tool", "--help", "--version", "-h", "%s", "a b"} {
		p := filepath.Join(dir, name)
		copyFile(t, bin, p)
		checkArgv(t, p, "", []string{p, "--help"}, wantUsage)
		checkArgv(t, p, "", []string{p}, "Hello, world\n")
		checkArgv(t, p, dir, []string{"./" + name, "--help"}, wantUsage)
		checkArgv(t, p, dir, []string{"./" + name}, "Hello, world\n")
	}
	link := filepath.Join(dir, "link-help")
	if err := os.Symlink(bin, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	checkArgv(t, link, "", []string{link, "--help"}, wantUsage)
	checkArgv(t, link, "", []string{link}, "Hello, world\n")
}

// argv[0] is the program's name, not an argument, even when it reads like a flag.
func TestHelpIgnoresArgv0(t *testing.T) {
	bin := buildBinary(t, "")
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"--help"}, "Hello, world\n"},
		{[]string{"--version"}, "Hello, world\n"},
		{[]string{"--help", "--version"}, "dev\n"},
		{[]string{"--version", "--help"}, wantUsage},
		{[]string{"other", "--help"}, wantUsage},
		{[]string{"", "--help"}, wantUsage},
		{[]string{"%s %n", "--help"}, wantUsage},
	} {
		checkArgv(t, bin, "", tc.argv, tc.want)
	}
}

// -X values reach --version as literal text and never touch --help or the greeting.
func TestVersionValueIsInert(t *testing.T) {
	for _, tc := range []struct{ ldflags, want string }{
		{"-X main.version=--help", "--help\n"},
		{"-X main.version=%s", "%s\n"},
		{"-X main.version=%d%n%!", "%d%n%!\n"},
		{"-X main.version=", "\n"},
		{"-X 'main.version=--help --version'", "--help --version\n"},
	} {
		bin := buildBinary(t, tc.ldflags)
		check(t, bin, []string{"--version"}, tc.want)
		check(t, bin, []string{"--help"}, wantUsage)
		check(t, bin, []string{"--help", "--version"}, wantUsage)
		check(t, bin, nil, "Hello, world\n")
	}
}

func TestUsageNotOverridable(t *testing.T) {
	check(t, buildBinary(t, "-X main.usage=PWNED"), []string{"--help"}, wantUsage)
}

// Whatever a bad stdout does to one path, it must do to all three.
func TestHelpBadStdoutMatchesOtherPaths(t *testing.T) {
	bin := buildBinary(t, "")
	type result struct {
		code   int
		stderr string
	}
	run := func(t *testing.T, args []string, stdout *os.File) result {
		t.Helper()
		var e strings.Builder
		cmd := exec.Command(bin, args...)
		cmd.Stdout, cmd.Stderr = stdout, &e
		err := cmd.Run()
		if _, isExit := err.(*exec.ExitError); err != nil && !isExit {
			t.Fatal(err)
		}
		return result{cmd.ProcessState.ExitCode(), e.String()}
	}
	stdouts := map[string]func(*testing.T) *os.File{
		"full": func(t *testing.T) *os.File {
			f, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
			if err != nil {
				t.Skipf("no /dev/full: %v", err)
			}
			return f
		},
		"readonly": func(t *testing.T) *os.File {
			f, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			return f
		},
		"broken pipe": func(t *testing.T) *os.File {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			r.Close()
			return w
		},
	}
	for name, open := range stdouts {
		t.Run(name, func(t *testing.T) {
			f := open(t)
			defer f.Close()
			help := run(t, []string{"--help"}, f)
			for _, args := range [][]string{{"--version"}, nil} {
				if got := run(t, args, f); got != help {
					t.Errorf("--help gave %+v but args %q gave %+v", help, args, got)
				}
			}
		})
	}
}

// Nothing reads stdin: a pipe that never reaches EOF must not hold up any path.
func TestHelpDoesNotReadStdin(t *testing.T) {
	bin := buildBinary(t, "")
	for _, args := range [][]string{{"--help"}, {"--version"}, nil} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stdin = r
		if err := cmd.Run(); err != nil {
			t.Errorf("args %q with an open stdin: %v", args, err)
		}
		cancel()
		r.Close()
		w.Close()
	}
}

// The environment plays no part in --help.
func TestHelpIgnoresEnvironment(t *testing.T) {
	bin := buildBinary(t, "")
	var o strings.Builder
	cmd := exec.Command(bin, "--help")
	cmd.Env = []string{"HELP=1", "COLUMNS=5", "LC_ALL=tr_TR.UTF-8", "PAGER=false", "NO_COLOR=1"}
	cmd.Stdout = &o
	if err := cmd.Run(); err != nil || o.String() != wantUsage {
		t.Errorf("stdout=%q err=%v", o.String(), err)
	}
}
