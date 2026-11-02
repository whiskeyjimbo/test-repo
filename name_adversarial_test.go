package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nameOracle is the contract's reading of one argument, written without strings.CutPrefix.
func nameOracle(arg string) string {
	const prefix = "--name="
	if len(arg) > len(prefix) && arg[:len(prefix)] == prefix {
		return arg[len(prefix):]
	}
	return ""
}

func greetingFor(arg string) string {
	if v := nameOracle(arg); v != "" {
		return "Hello, " + v + "\n"
	}
	return "Hello, world\n"
}

// NUL is the one byte argv cannot carry.
func TestNameEveryByteAsValue(t *testing.T) {
	bin := buildBinary(t, "")
	for b := 1; b <= 255; b++ {
		c := string([]byte{byte(b)})
		for _, v := range []string{c, c + c, "x" + c, c + "x", "x" + c + "y"} {
			check(t, bin, []string{"--name=" + v}, "Hello, "+v+"\n")
		}
	}
}

func TestNameAllBytesInOneValue(t *testing.T) {
	bin := buildBinary(t, "")
	var all []byte
	for b := 1; b <= 255; b++ {
		all = append(all, byte(b))
	}
	check(t, bin, []string{"--name=" + string(all)}, "Hello, "+string(all)+"\n")
}

// Every byte in every position of the prefix, and before and after the argument.
func TestNameEveryByteAroundPrefix(t *testing.T) {
	bin := buildBinary(t, "")
	const prefix = "--name="
	for b := 1; b <= 255; b++ {
		c := string([]byte{byte(b)})
		for i := 0; i <= len(prefix); i++ {
			arg := prefix[:i] + c + prefix[i:] + "Ann"
			if arg == "--help" || arg == "--version" {
				t.Fatalf("test bug: %q is a flag", arg)
			}
			check(t, bin, []string{arg}, greetingFor(arg))
		}
		for i := 0; i < len(prefix); i++ {
			arg := prefix[:i] + c + prefix[i+1:] + "Ann"
			check(t, bin, []string{arg}, greetingFor(arg))
		}
		check(t, bin, []string{"--name=Ann" + c}, "Hello, Ann"+c+"\n")
	}
}

func TestNameMultibyteValues(t *testing.T) {
	bin := buildBinary(t, "")
	for _, v := range []string{
		"\xc3\xa9", "e\xcc\x81", "\xe6\x97\xa5\xe6\x9c\xac", "\xf0\x9f\x98\x80",
		"\xef\xbb\xbf", "\xe2\x80\x8b", "\xe2\x80\xae", "\xc2\xa0", "\xef\xbf\xbd", "\xc2\x85",
		"\xc0\xaf", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xc3", "\xe6\x97", "\xf0\x9f\x98", "\x80", "\xfe\xff",
		"\xef\xbc\x8d\xef\xbc\x8dhelp", "\xe2\x80\x94help",
	} {
		check(t, bin, []string{"--name=" + v}, "Hello, "+v+"\n")
	}
	// Lookalikes of the prefix are not the prefix.
	for _, a := range []string{
		"\xef\xbb\xbf--name=Ann", "\xe2\x80\x8b--name=Ann", "\xe2\x80\x94name=Ann",
		"\xef\xbc\x8d\xef\xbc\x8dname=Ann", "--name\xef\xbc\x9dAnn", "--n\xc9\x91me=Ann", "--\xef\xbd\x8eame=Ann",
	} {
		check(t, bin, []string{a}, "Hello, world\n")
	}
}

// Nothing is trimmed, unquoted, expanded or interpreted.
func TestNameValueNotInterpreted(t *testing.T) {
	bin := buildBinary(t, "")
	t.Setenv("HOME", "/expanded")
	for _, v := range []string{
		"\n", "\r", "\t", "\r\n", "Ann\n", "Ann\r\n", "\nAnn", "  Ann  ", "\tAnn\t", "a\n--help", "--help\n", "\x1b[31mred\x1b[0m", "\x7f",
		"%", "%%", "%d", "%v", "%n%n%n", "%!", "%[1]s", "%x%x", "%s%s%s",
		"\\", "\\n", "\\t", "\\x41", "\\\\", "\\u0041", "C:\\tmp",
		"$HOME", "${HOME}", "$(id)", "`id`", "~", "*", "?", "a;b", "a|b", "a&b", ">x", "<x", "!!", "#c", "'", "\"", "\"Ann\"", "'Ann'",
		"world", "world!", "Hello, world", "-", "--", "---", "=", "==", "=Ann", "Ann=", "a=b=c",
		"--name", "--name=", "--name=Bob", "--name=--help", "--help=x", "--version=x", "-h", "dev",
	} {
		check(t, bin, []string{"--name=" + v}, "Hello, "+v+"\n")
	}
}

func TestNameScanTable(t *testing.T) {
	bin := buildBinary(t, "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		// Terminators and empties are ordinary arguments.
		{[]string{"--", "--name=Ann"}, "Hello, Ann\n"},
		{[]string{"--name=Ann", "--"}, "Hello, Ann\n"},
		{[]string{"--", "--name=", "--name=Bob"}, "Hello, Bob\n"},
		{[]string{"--", "--help", "--name=Ann"}, wantUsage},
		{[]string{"", "--name=Ann"}, "Hello, Ann\n"},
		{[]string{"--name=Ann", ""}, "Hello, Ann\n"},
		{[]string{"", "", "--name=", "", "--name=Bob", ""}, "Hello, Bob\n"},
		{[]string{"", "--name=", ""}, "Hello, world\n"},
		// Empty values fall through however many there are.
		{[]string{"--name=", "--name=", "--name=", "--name=Bob"}, "Hello, Bob\n"},
		{[]string{"--name=", "--name=", "--name="}, "Hello, world\n"},
		{[]string{"--name=", "foo", "--name=", "bar", "--name=Bob", "--name=Cy"}, "Hello, Bob\n"},
		// --name takes no separate value.
		{[]string{"--name", "--name=Bob"}, "Hello, Bob\n"},
		{[]string{"--name", "Ann", "--name=Bob"}, "Hello, Bob\n"},
		{[]string{"--name=Bob", "--name", "Ann"}, "Hello, Bob\n"},
		{[]string{"--name", "--help"}, wantUsage},
		{[]string{"--name", "--version"}, "dev\n"},
		// Only an exact flag is a flag.
		{[]string{"--help=x", "--name=Ann"}, "Hello, Ann\n"},
		{[]string{"--name=Ann", "--help=x"}, "Hello, Ann\n"},
		{[]string{"--version=x", "--name=Ann"}, "Hello, Ann\n"},
		{[]string{"--name=Ann", "--version=x"}, "Hello, Ann\n"},
		{[]string{"--help=x", "--version=x"}, "Hello, world\n"},
		{[]string{"--help=x", "--name=--help"}, "Hello, --help\n"},
		{[]string{"--name=--help", "--name=--version"}, "Hello, --help\n"},
		{[]string{"--name=--version", "--name=--help"}, "Hello, --version\n"},
		// The first exact flag decides, past any --name.
		{[]string{"--name=", "--help"}, wantUsage},
		{[]string{"--name=", "--version"}, "dev\n"},
		{[]string{"--name=Ann", "--help", "--version"}, wantUsage},
		{[]string{"--name=Ann", "--version", "--help"}, "dev\n"},
		{[]string{"--help", "--version", "--name=Ann"}, wantUsage},
		{[]string{"--version", "--help", "--name=Ann"}, "dev\n"},
		{[]string{"--name=--version", "--help"}, wantUsage},
		{[]string{"--name=--help", "--name=--version", "--version", "--help"}, "dev\n"},
		{[]string{"--name=Ann", "foo", "--name=Bob", "bar", "--help"}, wantUsage},
		{[]string{"--name=Ann", "--help\n"}, "Hello, Ann\n"},
		{[]string{"--name=Ann", " --version"}, "Hello, Ann\n"},
		// A value is never read as another flag.
		{[]string{"--name=--name=Bob"}, "Hello, --name=Bob\n"},
		{[]string{"--name=--name=Bob", "--name=Cy"}, "Hello, --name=Bob\n"},
		{[]string{"--name=--name="}, "Hello, --name=\n"},
		{[]string{"--name=="}, "Hello, =\n"},
		{[]string{"--name===", "--name=Bob"}, "Hello, ==\n"},
		// "world" is a real value, not the unset marker.
		{[]string{"--name=world", "--name=Bob"}, "Hello, world\n"},
	} {
		check(t, bin, tc.args, tc.want)
	}
}

func TestNameManyArgs(t *testing.T) {
	bin := buildBinary(t, "")
	const n = 50000
	rep := func(s string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = s
		}
		return out
	}
	join := func(a []string, b ...string) []string { return append(a[:len(a):len(a)], b...) }

	check(t, bin, join(rep("x"), "--name=Ann"), "Hello, Ann\n")
	check(t, bin, append([]string{"--name=Ann"}, rep("x")...), "Hello, Ann\n")
	check(t, bin, join(rep(""), "--name=Ann"), "Hello, Ann\n")
	check(t, bin, join(rep("--name="), "--name=Bob"), "Hello, Bob\n")
	check(t, bin, rep("--name="), "Hello, world\n")
	check(t, bin, rep("--name=a"), "Hello, a\n")
	check(t, bin, join(rep("--name=a"), "--help"), wantUsage)
	check(t, bin, join(rep("--name=a"), "--version"), "dev\n")
	check(t, bin, join(rep("--name="), "--version", "--help"), "dev\n")
	check(t, bin, join(rep("x"), "--name=Ann", "--help"), wantUsage)
	check(t, bin, append([]string{"--help"}, rep("--name=a")...), wantUsage)
}

func TestNameVeryLongValues(t *testing.T) {
	bin := buildBinary(t, "")
	const maxValue = 131071 - len("--name=") // Linux MAX_ARG_STRLEN less the NUL.
	for _, n := range []int{255, 256, 1024, 4095, 4096, 4097, 65535, 65536, 65537, maxValue} {
		for _, c := range []string{"a", "\xff", " ", "%", "\n"} {
			v := strings.Repeat(c, n)
			check(t, bin, []string{"--name=" + v}, "Hello, "+v+"\n")
		}
	}
	long := strings.Repeat("a", maxValue)
	check(t, bin, []string{"--name=" + long, "--help"}, wantUsage)
	check(t, bin, []string{"--name=" + long, "--version"}, "dev\n")
	check(t, bin, []string{"--name=" + long, "--name=Bob"}, "Hello, "+long+"\n")
	check(t, bin, []string{"--name=", "--name=" + long}, "Hello, "+long+"\n")
	check(t, bin, []string{"--nam" + long}, "Hello, world\n")
}

// The binary's own name is never a source of VALUE.
func TestNameIgnoresBinaryName(t *testing.T) {
	bin := buildBinary(t, "")
	dir := t.TempDir()
	for _, name := range []string{"app", "--name=Ann", "--name=", "--help", "%s", "a b"} {
		p := filepath.Join(dir, name)
		copyFile(t, bin, p)
		checkArgv(t, p, "", []string{p}, "Hello, world\n")
		checkArgv(t, p, "", []string{p, "--name=Bob"}, "Hello, Bob\n")
		checkArgv(t, p, dir, []string{"./" + name}, "Hello, world\n")
		checkArgv(t, p, dir, []string{"./" + name, "--name=Bob"}, "Hello, Bob\n")
	}
	link := filepath.Join(dir, "link-name")
	if err := os.Symlink(bin, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	checkArgv(t, link, "", []string{link}, "Hello, world\n")
	checkArgv(t, link, "", []string{link, "--name=Bob"}, "Hello, Bob\n")
}

func TestNameIgnoresArgv0(t *testing.T) {
	bin := buildBinary(t, "")
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"--name=Ann"}, "Hello, world\n"},
		{[]string{"--name=Ann", "--name=Bob"}, "Hello, Bob\n"},
		{[]string{"--name=Ann", "--name="}, "Hello, world\n"},
		{[]string{"--name=Ann", "foo"}, "Hello, world\n"},
		{[]string{"--name=Ann", "--help"}, wantUsage},
		{[]string{"--name=Ann", "--version"}, "dev\n"},
		{[]string{"--help", "--name=Ann"}, "Hello, Ann\n"},
		{[]string{"--version", "--name=Ann"}, "Hello, Ann\n"},
		{[]string{"", "--name=Ann"}, "Hello, Ann\n"},
	} {
		checkArgv(t, bin, "", tc.argv, tc.want)
	}
}

// -X values reach --version as literal text and never become VALUE.
func TestNameVersionValueIsInert(t *testing.T) {
	for _, tc := range []struct{ ldflags, want string }{
		{"-X main.version=--name=Evil", "--name=Evil\n"},
		{"-X main.version=%s", "%s\n"},
		{"-X 'main.version=--name=Evil --help'", "--name=Evil --help\n"},
		{"-X main.version=", "\n"},
	} {
		bin := buildBinary(t, tc.ldflags)
		check(t, bin, []string{"--version"}, tc.want)
		check(t, bin, []string{"--name=Ann", "--version"}, tc.want)
		check(t, bin, nil, "Hello, world\n")
		check(t, bin, []string{"--name="}, "Hello, world\n")
		check(t, bin, []string{"--name=Ann"}, "Hello, Ann\n")
		check(t, bin, []string{"--name=%s"}, "Hello, %s\n")
		check(t, bin, []string{"--name=Ann", "--help"}, wantUsage)
	}
}

// The environment never feeds VALUE, set or empty, at any argument count.
func TestNameIgnoresEnvironment(t *testing.T) {
	bin := buildBinary(t, "")
	envs := [][]string{
		{},
		{"NAME=Evil", "name=Evil", "USER=Evil", "LOGNAME=Evil", "GREETING=Evil", "HOSTNAME=Evil", "TEST_REPO_NAME=Evil"},
		{"--name=Evil", "--name=Evil2=x", "ARGS=--name=Evil", "GOFLAGS=--name=Evil", "--help=1", "--version=1"},
		{"LC_ALL=tr_TR.UTF-8", "LANG=C", "NAME="},
	}
	for _, env := range envs {
		for _, tc := range []struct {
			args []string
			want string
		}{
			{nil, "Hello, world\n"},
			{[]string{"--name="}, "Hello, world\n"},
			{[]string{"--name"}, "Hello, world\n"},
			{[]string{"--NAME=Ann"}, "Hello, world\n"},
			{[]string{"--name=Ann"}, "Hello, Ann\n"},
			{[]string{"--name=$NAME"}, "Hello, $NAME\n"},
		} {
			var o, e strings.Builder
			cmd := exec.Command(bin, tc.args...)
			cmd.Env = append([]string{}, env...)
			cmd.Stdout, cmd.Stderr = &o, &e
			if err := cmd.Run(); err != nil || o.String() != tc.want || e.String() != "" {
				t.Errorf("env %q args %q: stdout=%q stderr=%q err=%v; want %q", env, tc.args, o.String(), e.String(), err, tc.want)
			}
		}
	}
}

// Whatever a bad stdout does to the other paths, it must do to the --name path.
func TestNameBadStdoutMatchesOtherPaths(t *testing.T) {
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
			base := run(t, nil, f)
			for _, args := range [][]string{{"--help"}, {"--version"}, {"--name=Ann"}, {"--name=" + strings.Repeat("a", 100000)}} {
				if got := run(t, args, f); got != base {
					t.Errorf("no args gave %+v but %.20q gave %+v", base, args, got)
				}
			}
		})
	}
}

// Closed descriptors are set up by the shell, because exec.Cmd always opens all three.
func TestNameClosedDescriptors(t *testing.T) {
	bin := buildBinary(t, "")
	run := func(redir string, args ...string) (string, string, int) {
		cmd := exec.Command("sh", append([]string{"-c", fmt.Sprintf(`exec "$0" "$@" %s`, redir), bin}, args...)...)
		var o, e strings.Builder
		cmd.Stdout, cmd.Stderr = &o, &e
		err := cmd.Run()
		if _, isExit := err.(*exec.ExitError); err != nil && !isExit {
			t.Fatal(err)
		}
		return o.String(), e.String(), cmd.ProcessState.ExitCode()
	}
	if o, e, c := run("<&-", "--name=Ann"); o != "Hello, Ann\n" || e != "" || c != 0 {
		t.Errorf("stdin closed: stdout=%q stderr=%q exit=%d", o, e, c)
	}
	if o, e, c := run("2>&-", "--name=Ann"); o != "Hello, Ann\n" || e != "" || c != 0 {
		t.Errorf("stderr closed: stdout=%q stderr=%q exit=%d", o, e, c)
	}
	_, baseErr, baseCode := run(">&-")
	for _, args := range [][]string{{"--help"}, {"--version"}, {"--name=Ann"}} {
		if _, e, c := run(">&-", args...); e != baseErr || c != baseCode {
			t.Errorf("stdout closed, args %q: stderr=%q exit=%d; no args gave stderr=%q exit=%d", args, e, c, baseErr, baseCode)
		}
	}
}
