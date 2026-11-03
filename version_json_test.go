package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

const jsonDev = "{\"version\":\"dev\"}\n"

func TestVersionJSON(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range [][]string{{"--version-json"}, {"foo", "--version-json"}, {"--version-json", "--version-json"}} {
		check(t, bin, a, jsonDev)
	}
	check(t, buildBinary(t, "-X main.version=1.2.3"), []string{"--version-json"}, "{\"version\":\"1.2.3\"}\n")
}

func TestVersionJSONEscaping(t *testing.T) {
	bin := buildBinary(t, `-X 'main.version=a"b\c<&>'`)
	out, _, _ := runBinary(t, bin, "--version-json")
	if want := "{\"version\":\"a\\\"b\\\\c<&>\"}\n"; out != want {
		t.Errorf("stdout=%q want %q", out, want)
	}
	var v struct{ Version string }
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Version != `a"b\c<&>` {
		t.Errorf("decode: %v, version=%q", err, v.Version)
	}
}

func TestVersionJSONPrecedence(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--help", "--version-json"}, wantUsage)
	check(t, bin, []string{"--version", "--version-json"}, "dev\n")
	for _, a := range [][]string{
		{"--version-json", "--help"}, {"--version-json", "--version"},
		{"--version-json", "--quiet"}, {"--quiet", "--version-json"},
		{"--version-json", "--lang=fr"}, {"--lang=fr", "--version-json"},
		{"--version-json", "--config=nope"}, {"--name=Ann", "--shout", "--version-json"},
	} {
		check(t, bin, a, jsonDev)
	}
}

func TestVersionJSONNearMissesGreet(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range []string{"--version-json=1", "--version-json=", "-version-json", "--VERSION-JSON",
		"--versionjson", "--version_json", "version-json", " --version-json"} {
		check(t, bin, []string{a}, "Hello, world\n")
	}
	check(t, bin, []string{"--name=--version-json"}, "Hello, --version-json\n")
}

func TestVersionJSONUnusableStdout(t *testing.T) {
	bin := buildBinary(t, "")
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	cmd := exec.Command("sh", "-c", `"$0" --version-json >/dev/full 2>err.txt; echo $?`, bin)
	cmd.Dir = t.TempDir()
	out, err := cmd.Output()
	if err != nil || string(out) != "0\n" {
		t.Errorf("/dev/full: out=%q err=%v", out, err)
	}
	if b, _ := os.ReadFile(cmd.Dir + "/err.txt"); len(b) != 0 {
		t.Errorf("/dev/full stderr=%q", b)
	}
	cmd = exec.Command("sh", "-c", `"$0" --version-json >&- 2>err.txt; echo $?`, bin)
	cmd.Dir = t.TempDir()
	out, err = cmd.Output()
	if err != nil || string(out) != "0\n" {
		t.Errorf("closed: out=%q err=%v", out, err)
	}
	if b, _ := os.ReadFile(cmd.Dir + "/err.txt"); len(b) != 0 {
		t.Errorf("closed stderr=%q", b)
	}
}
