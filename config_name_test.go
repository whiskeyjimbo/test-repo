package main

import (
	"path/filepath"
	"testing"
)

func TestConfigNameHappy(t *testing.T) {
	bin := buildBinary(t, "")
	for _, c := range []struct{ file, want string }{
		{"name=Ann\n", "Hello, Ann\n"},
		{"greeting=Hi\nname=Ann\n", "Hi, Ann\n"},
		{"name=Ann\ngreeting=Hi\n", "Hi, Ann\n"},
		{"name=Ann Lee\n", "Hello, Ann Lee\n"},
		{"name=a=b\n", "Hello, a=b\n"},
		{"name=--help\n", "Hello, --help\n"},
	} {
		check(t, bin, []string{"--config=" + writeCfg(t, c.file)}, c.want)
	}
}

func TestConfigNamePrecedence(t *testing.T) {
	bin := buildBinary(t, "")
	f := "--config=" + writeCfg(t, "greeting=Hi\nname=Ann\n")
	check(t, bin, []string{f, "--name=Bob"}, "Hi, Bob\n")
	check(t, bin, []string{"--name=Bob", f}, "Hi, Bob\n")
	check(t, bin, []string{"--name=", "--name=Bob", f}, "Hi, Bob\n")
	check(t, bin, []string{"--name=", f}, "Hi, Ann\n")
	check(t, bin, []string{"--name", "Bob", f}, "Hi, Ann\n")
	check(t, bin, []string{f, "--shout"}, "HI, ANN\n")
}

func TestConfigNameEmpty(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--config=" + writeCfg(t, "name=\n")}, "Hello, world\n")
	check(t, bin, []string{"--config=" + writeCfg(t, "greeting=Hi\nname=   \n")}, "Hi, world\n")
}

func TestConfigNameMalformed(t *testing.T) {
	bin := buildBinary(t, "")
	for _, c := range []struct{ file, line, secret string }{
		{"name=Ann\nname=Bob\n", "line 2", "Bob"},
		{"name=\nname=Bob\n", "line 2", "Bob"},
		{"Name=Ann\n", "line 1", "Ann"},
	} {
		f := "--config=" + writeCfg(t, c.file)
		wantFail(t, bin, []string{f}, "test-repo: config ", c.line, c.secret)
		wantFail(t, bin, []string{f, "--name=Zed"}, "test-repo: config ", c.line, c.secret, "Zed")
	}
}

func TestConfigNameMissingFileWithName(t *testing.T) {
	bin := buildBinary(t, "")
	wantFail(t, bin, []string{"--name=Zed", "--config=missing"}, "test-repo: config missing: ", "no such file", "Zed")
}

func TestConfigNameNotOpened(t *testing.T) {
	bin := buildBinary(t, "")
	f := "--config=" + writeCfg(t, "name=Ann\n")
	check(t, bin, []string{f, "--quiet"}, "")
	check(t, bin, []string{f, "--help"}, wantUsage)
	check(t, bin, []string{f, "--version"}, "dev\n")
	bad := "--config=" + filepath.Join(t.TempDir(), "missing")
	check(t, bin, []string{bad, "--quiet"}, "")
}
