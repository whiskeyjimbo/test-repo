package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Failure paths must print nothing on stdout, one line on stderr, exit 1, and
// never echo any key, value or line of the file. Each canary appears in a key
// or value position of a different malformed file.
func TestConfigAdversarialNoEcho(t *testing.T) {
	bin := buildBinary(t, "")
	cases := []struct{ content, line string }{
		{"ZQKEY1ZQ\n", "line 1"},
		{"ZQKEY2ZQ=ZQVAL2ZQ\n", "line 1"},
		{"=ZQVAL3ZQ\n", "line 1"},
		{"greeting=ZQVAL4ZQ\nZQKEY4ZQ=ZQVAL4bZQ\n", "line 2"},
		{"greeting=ZQVAL5ZQ\ngreeting=ZQVAL5bZQ\n", "line 2"},
		{"greeting=ZQVAL6ZQ\r\n\r\n#ZQKEY6ZQ\r\n", "line 3"},
		{"\xef\xbb\xbfgreeting=ZQVAL7ZQ\n", "line 1"},
		{"greeting=ZQVAL8ZQ\n" + strings.Repeat("\n", 5) + "color=ZQVAL8bZQ", "line 7"},
		{"gree ting=ZQVAL9ZQ\n", "line 1"},
		{"GREETING=ZQVAL10ZQ\n", "line 1"},
		{"greeting=ZQVAL11ZQ\n==ZQVAL11bZQ\n", "line 2"},
	}
	for _, c := range cases {
		f := writeCfg(t, c.content)
		wantFail(t, bin, []string{"--config=" + f}, "test-repo: config "+f+": ", c.line, "ZQ")
		// --name, --shout and a later --config=GOOD must not rescue a bad file.
		good := writeCfg(t, "greeting=Hi\n")
		wantFail(t, bin, []string{"--name=Zed", "--shout", "--config=" + f, "--config=" + good},
			"test-repo: config "+f+": ", c.line, "ZQ", "ZED", "Zed")
	}
}

// First non-empty --config wins, even when it is bad and a later one is good.
func TestConfigAdversarialFirstWins(t *testing.T) {
	bin := buildBinary(t, "")
	good := writeCfg(t, "greeting=Hi\n")
	other := writeCfg(t, "greeting=Yo\n")
	check(t, bin, []string{"--config=" + good, "--config=" + other}, "Hi, world\n")
	check(t, bin, []string{"--config=", "--config=", "--config=" + other, "--config=" + good}, "Yo, world\n")
	missing := filepath.Join(t.TempDir(), "nope")
	wantFail(t, bin, []string{"--config=" + missing, "--config=" + good}, "test-repo: config "+missing+": ", "no such file")
}

// Line numbers count blank lines and CRLF/whitespace-only lines.
func TestConfigAdversarialLineNumbers(t *testing.T) {
	bin := buildBinary(t, "")
	for content, line := range map[string]string{
		"greeting=Hi\r\n\r\n   \r\n\t\r\nnonsense\r\n":               "line 5",
		"\n\n\n\n\n\n\n\n\n\ncolor=red":                              "line 11",
		"\r\n\r\n=x":                                                 "line 3",
		"greeting=Hi\n" + strings.Repeat(" \n", 998) + "greeting=Hi": "line 1000",
	} {
		f := writeCfg(t, content)
		wantFail(t, bin, []string{"--config=" + f}, "test-repo: config "+f+": ", line)
	}
}

// Odd but valid contents: values are literal.
func TestConfigAdversarialValidOddities(t *testing.T) {
	bin := buildBinary(t, "")
	cases := map[string]string{
		"greeting==\n":                           "=, world\n",
		"greeting===x==\n":                       "==x==, world\n",
		"greeting\t=\tHi there\t\n":              "Hi there, world\n",
		"greeting=Hello\n":                       "Hello, world\n",
		"greeting=Hello Hello\n":                 "Hello Hello, world\n",
		"greeting=Hi, there\n":                   "Hi, there, world\n",
		"greeting=Hi\x00x\n":                     "Hi\x00x, world\n",
		"greeting=\xff\xfe\n":                    "\xff\xfe, world\n",
		"greeting=héllo ✓\n":                     "héllo ✓, world\n",
		"greeting=Hi\n\n\n\n":                    "Hi, world\n",
		"\r\n\r\ngreeting=Hi\r\n\r\n":            "Hi, world\n",
		"greeting=Hi\r":                          "Hi, world\n",
		"greeting=#notacomment\n":                "#notacomment, world\n",
		"greeting=Hi # trailing\n":               "Hi # trailing, world\n",
		"greeting=" + strings.Repeat("y", 5<<20): strings.Repeat("y", 5<<20) + ", world\n",
	}
	for content, want := range cases {
		check(t, bin, []string{"--config=" + writeCfg(t, content)}, want)
	}
	// Name that itself starts with "Hello" must not be eaten by the greeting swap.
	f := writeCfg(t, "greeting=Hi\n")
	check(t, bin, []string{"--config=" + f, "--name=Hello"}, "Hi, Hello\n")
	check(t, bin, []string{"--config=" + f, "--name=Hello, Hello"}, "Hi, Hello, Hello\n")
	check(t, bin, []string{"--config=" + f, "--name=%s%d"}, "Hi, %s%d\n")
	check(t, bin, []string{"--config=" + f, "--name=a b", "--shout"}, "HI, A B\n")
}

// A 100k-line file must not be slow or fail.
func TestConfigAdversarialManyLines(t *testing.T) {
	bin := buildBinary(t, "")
	f := writeCfg(t, strings.Repeat("\n", 100000)+"greeting=Hi\n"+strings.Repeat("\r\n", 100000))
	check(t, bin, []string{"--config=" + f}, "Hi, world\n")
}

// Path-shaped errors: not a directory, symlink loop, long name, unreadable
// parent, /dev/null, no tilde expansion, "-" is just a missing file.
func TestConfigAdversarialPaths(t *testing.T) {
	bin := buildBinary(t, "")
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	os.WriteFile(file, []byte("greeting=Hi\n"), 0o644)
	loop := filepath.Join(dir, "loop")
	os.Symlink(loop, loop)
	for _, p := range []string{
		file + "/sub", loop, filepath.Join(dir, strings.Repeat("a", 300)),
		"~/nope.cfg", "-", "no such dir/x", dir + "/",
	} {
		wantFail(t, bin, []string{"--config=" + p}, "test-repo: config "+p+": ", "")
	}
	check(t, bin, []string{"--config=/dev/null"}, "Hello, world\n")

	// Symlink to a valid file works; relative path with ./ and ../
	link := filepath.Join(dir, "link")
	os.Symlink(file, link)
	check(t, bin, []string{"--config=" + link}, "Hi, world\n")
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o755)
	out, errOut, code := runIn(t, bin, sub, "--config=../f")
	if out != "Hi, world\n" || errOut != "" || code != 0 {
		t.Errorf("../f: %q %q %d", out, errOut, code)
	}
	// Directory without search permission around a readable file.
	if os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked")
		os.Mkdir(locked, 0o755)
		in := filepath.Join(locked, "c")
		os.WriteFile(in, []byte("greeting=Hi\n"), 0o644)
		os.Chmod(locked, 0)
		defer os.Chmod(locked, 0o755)
		wantFail(t, bin, []string{"--config=" + in}, "test-repo: config "+in+": ", "permission denied")
	}
}

// Error output on a read failure carries the OS reason once, not the Go-wrapped
// "open <path>: " prefix twice.
func TestConfigAdversarialNoDoublePath(t *testing.T) {
	bin := buildBinary(t, "")
	p := filepath.Join(t.TempDir(), "missing")
	_, errOut, _ := runBinary(t, bin, "--config="+p)
	if strings.Count(errOut, p) != 1 {
		t.Errorf("path appears %d times in %q", strings.Count(errOut, p), errOut)
	}
}

// --help/--version/--quiet never open the file: a FIFO with no writer would
// block forever if it were opened, so a hang here is a failure (go test -timeout).
func TestConfigAdversarialNeverOpened(t *testing.T) {
	bin := buildBinary(t, "")
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := mkfifo(fifo); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--config=" + fifo, "--help"}, wantUsage},
		{[]string{"--config=" + fifo, "--version"}, "dev\n"},
		{[]string{"--config=" + fifo, "--quiet"}, ""},
		{[]string{"--quiet", "--config=" + fifo, "--shout", "--name=x"}, ""},
		{[]string{"--config=" + fifo, "--quiet", "--help"}, wantUsage},
		{[]string{"--config=" + fifo, "--quiet", "--version"}, "dev\n"},
		{[]string{"--version", "--config=" + fifo, "--help"}, "dev\n"},
	} {
		check(t, bin, c.args, c.want)
	}
}
