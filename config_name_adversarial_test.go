package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Repeat tracking must be per key and independent of value emptiness.
func TestNameAdvRepeatTracking(t *testing.T) {
	bin := buildBinary(t, "")
	for _, c := range []struct{ file, line, leak string }{
		{"name=\nname=Bob\n", "line 2", "Bob"},
		{"name=Ann\nname=\n", "line 2", "Ann"},
		{"name=Ann\nname=Ann\n", "line 2", "Ann"},
		{"name=\nname=\n", "line 2", ""},
		{"name=Ann\ngreeting=Hi\nname=Bob\n", "line 3", "Bob"},
		{"\n\nname=Ann\n\nname=Bob\n", "line 5", "Bob"},
		{"greeting=Hi\nname=Ann\ngreeting=Yo\n", "line 3", "Yo"},
		{"name=Ann\r\nname=Bob\r\n", "line 2", "Bob"},
	} {
		f := "--config=" + writeCfg(t, c.file)
		var no []string
		if c.leak != "" {
			no = append(no, c.leak)
		}
		wantFail(t, bin, []string{f}, "test-repo: config ", c.line, no...)
		wantFail(t, bin, []string{"--name=Zed", f}, "test-repo: config ", c.line, append(no, "Zed")...)
	}
}

// Keys are case-sensitive; spaces around key are trimmed; errors never echo content.
func TestNameAdvKeysAndNoEcho(t *testing.T) {
	bin := buildBinary(t, "")
	for _, c := range []struct{ file, line string }{
		{"Name=SECRETVAL\n", "line 1"},
		{"NAME=SECRETVAL\n", "line 1"},
		{"name =Ann\nNAME2=SECRETVAL\n", "line 2"},
		{"nam=SECRETVAL\n", "line 1"},
		{"names=SECRETVAL\n", "line 1"},
		{"name=Ann\nSECRETKEY=SECRETVAL\n", "line 2"},
		{"name=Ann\nSECRETLINE\n", "line 2"},
		{"name=Ann\n=SECRETVAL\n", "line 2"},
		{"name\n", "line 1"},
	} {
		f := "--config=" + writeCfg(t, c.file)
		wantFail(t, bin, []string{f}, "test-repo: config ", c.line, "SECRET", "Ann", "name=")
	}
}

func TestNameAdvValueForms(t *testing.T) {
	bin := buildBinary(t, "")
	long := strings.Repeat("x", 70*1024)
	for _, c := range []struct{ file, want string }{
		{"name=Ann", "Hello, Ann\n"},                     // no trailing newline
		{"name=Ann\r\n", "Hello, Ann\n"},                 // CRLF
		{"greeting=Hi\r\nname=Ann\r\n", "Hi, Ann\n"},     //
		{"  name  =  Ann Lee  \n", "Hello, Ann Lee\n"},   // trims
		{"\tname\t=\tAnn\t\n", "Hello, Ann\n"},           //
		{"name==\n", "Hello, =\n"},                       // value is "="
		{"name=a=b=c\n", "Hello, a=b=c\n"},               //
		{"name=%s%d%v\n", "Hello, %s%d%v\n"},             // no format interpretation
		{"name=José 日本語 🎉\n", "Hello, José 日本語 🎉\n"},     //
		{"name=Hello\n", "Hello, Hello\n"},               // value equal to default greeting
		{"name=Hello, Bob\n", "Hello, Hello, Bob\n"},     //
		{"greeting=Hi\nname=Hello\n", "Hi, Hello\n"},     //
		{"name=--name=Bob\n", "Hello, --name=Bob\n"},     //
		{"name=--quiet\n", "Hello, --quiet\n"},           //
		{"name=world\n", "Hello, world\n"},               //
		{"\n\n  \n\nname=Ann\n\n\n", "Hello, Ann\n"},     // blank lines
		{"name=" + long + "\n", "Hello, " + long + "\n"}, // >64KiB line
		{"greeting=" + long + "\nname=Ann", long + ", Ann\n"},
		{"name=Ann\n" + strings.Repeat("\n", 100000), "Hello, Ann\n"},
	} {
		name := c.file
		if len(name) > 40 {
			name = name[:40]
		}
		t.Run(name, func(t *testing.T) {
			check(t, bin, []string{"--config=" + writeCfg(t, c.file)}, c.want)
		})
	}
}

func TestNameAdvPrecedence(t *testing.T) {
	bin := buildBinary(t, "")
	f := "--config=" + writeCfg(t, "greeting=Hi\nname=Ann\n")
	empty := "--config=" + writeCfg(t, "name=\n")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--name=", "--name=", f}, "Hi, Ann\n"},
		{[]string{"--name=Bob", "--name=Cy", f}, "Hi, Bob\n"},
		{[]string{"--name=", "--name=Bob", "--name=Cy", f}, "Hi, Bob\n"},
		{[]string{f, "--name=Bob", "--shout"}, "HI, BOB\n"},
		{[]string{"--name= ", f}, "Hi,  \n"}, // whitespace --name is non-empty today (flag unchanged)
		{[]string{"--name=", empty}, "Hello, world\n"},
		{[]string{"--name=Bob", empty}, "Hello, Bob\n"},
		{[]string{"--Name=Bob", f}, "Hi, Ann\n"},
		{[]string{"--name", "Bob", f}, "Hi, Ann\n"},
		{[]string{f, "--config=" + writeCfg(t, "name=Other\n")}, "Hi, Ann\n"}, // first config wins
		{[]string{"--config=", f}, "Hi, Ann\n"},
	} {
		check(t, bin, c.args, c.want)
	}
}

// --name must not skip validation, for every kind of bad file.
func TestNameAdvNameDoesNotSkipValidation(t *testing.T) {
	bin := buildBinary(t, "")
	dir := t.TempDir()
	for _, c := range []struct {
		cfg, must string
	}{
		{"--config=" + filepath.Join(dir, "missing"), "no such file"},
		{"--config=" + dir, "directory"},
		{"--config=" + writeCfg(t, "name=Ann\nbogus=1\n"), "line 2"},
		{"--config=" + writeCfg(t, "name=Ann\nnoequals\n"), "line 2"},
		{"--config=" + writeCfg(t, "name=Ann\n=x\n"), "line 2"},
	} {
		wantFail(t, bin, []string{"--name=Zed", c.cfg}, "test-repo: config ", c.must, "Zed")
		wantFail(t, bin, []string{c.cfg, "--name=Zed", "--shout"}, "test-repo: config ", c.must, "Zed", "ZED")
	}
}

// File never opened for --help/--version/--quiet, even when bad, and a bad
// file with a valid name still fails.
func TestNameAdvNotOpened(t *testing.T) {
	bin := buildBinary(t, "")
	bad := "--config=" + filepath.Join(t.TempDir(), "missing")
	malformed := "--config=" + writeCfg(t, "name=a\nname=b\n")
	for _, f := range []string{bad, malformed} {
		check(t, bin, []string{f, "--name=Zed", "--quiet"}, "")
		check(t, bin, []string{"--name=Zed", "--quiet", f}, "")
		check(t, bin, []string{f, "--name=Zed", "--help"}, wantUsage)
		check(t, bin, []string{f, "--name=Zed", "--version"}, "dev\n")
		check(t, bin, []string{"--version", f, "--quiet"}, "dev\n")
	}
}

// Story 1 behaviour still holds with a name key present.
func TestNameAdvStory1Regression(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--config=" + writeCfg(t, "greeting=Hi\n")}, "Hi, world\n")
	check(t, bin, []string{"--config=" + writeCfg(t, "greeting=Hi\n"), "--name=Ann", "--shout"}, "HI, ANN\n")
	check(t, bin, []string{"--config=" + writeCfg(t, "")}, "Hello, world\n")
	check(t, bin, []string{"--config=" + writeCfg(t, "\n  \n")}, "Hello, world\n")
	wantFail(t, bin, []string{"--config=" + writeCfg(t, "greeting=A\ngreeting=B\n")}, "test-repo: config ", "line 2", "B")
	wantFail(t, bin, []string{"--config=" + writeCfg(t, "greeting=\ngreeting=B\n")}, "test-repo: config ", "line 2", "B")
}
