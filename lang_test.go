package main

import (
	"path/filepath"
	"strings"
	"testing"
)

const langErr = "test-repo: lang: unknown code, want en or es\n"

func wantLangErr(t *testing.T, bin string, args ...string) {
	t.Helper()
	out, errOut, code := runBinary(t, bin, args...)
	if out != "" || code != 1 || errOut != langErr {
		t.Errorf("args %q: stdout=%q stderr=%q exit=%d; want empty stdout, %q, exit 1",
			args, out, errOut, code, langErr)
	}
}

func TestLangHappy(t *testing.T) {
	bin := buildBinary(t, "")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--lang=en"}, "Hello, world\n"},
		{[]string{"--lang=es"}, "Hola, world\n"},
		{[]string{"--lang=es", "--name=Ana"}, "Hola, Ana\n"},
		{[]string{"--name=Ana", "--lang=es"}, "Hola, Ana\n"},
		{[]string{"--lang=es", "--shout"}, "HOLA, WORLD\n"},
		{[]string{"--lang=en", "--shout", "--name=Ann"}, "HELLO, ANN\n"},
	} {
		check(t, bin, c.args, c.want)
	}
}

func TestLangFlagForms(t *testing.T) {
	bin := buildBinary(t, "")
	for _, args := range [][]string{
		{"--lang="}, {"--lang"}, {"--lang", "es"}, {"-lang=es"}, {"--LANG=es"}, {"--langs=es"},
	} {
		check(t, bin, args, "Hello, world\n")
	}
	for _, args := range [][]string{
		{"--lang=", "--lang=es"}, {"--lang=es", "--lang=fr"}, {"--lang=es", "--lang=EN"},
	} {
		check(t, bin, args, "Hola, world\n")
	}
}

func TestLangWithConfig(t *testing.T) {
	bin := buildBinary(t, "")
	f := writeCfg(t, "greeting=Hi\n")
	check(t, bin, []string{"--config=" + f}, "Hi, world\n")
	check(t, bin, []string{"--config=" + f, "--lang=es"}, "Hola, world\n")
	check(t, bin, []string{"--lang=es", "--config=" + f}, "Hola, world\n")
	check(t, bin, []string{"--config=" + f, "--lang=en"}, "Hello, world\n")
	check(t, bin, []string{"--config=" + f, "--lang="}, "Hi, world\n")
}

func TestLangUnknownCode(t *testing.T) {
	bin := buildBinary(t, "")
	f := writeCfg(t, "greeting=Hi\n")
	for _, x := range []string{"fr", "EN", "Es", " es", "es ", "es-ES", "english", "e", "x\ny"} {
		base := "--lang=" + x
		for _, extra := range [][]string{nil, {"--name=Ann"}, {"--shout"}, {"--config=" + f}} {
			wantLangErr(t, bin, append([]string{base}, extra...)...)
		}
	}
	wantLangErr(t, bin, "--lang=fr", "--lang=es")
}

func TestLangCheckedBeforeFile(t *testing.T) {
	bin := buildBinary(t, "")
	missing := filepath.Join(t.TempDir(), "missing")
	bad := writeCfg(t, "nonsense\n")
	wantLangErr(t, bin, "--lang=fr", "--config="+missing)
	wantLangErr(t, bin, "--config="+bad, "--lang=fr")
	for _, p := range []string{missing, bad} {
		wantFail(t, bin, []string{"--lang=es", "--config=" + p}, "test-repo: config "+p+": ", "")
	}
	// the lang error is not a config error and vice versa
	if _, e, _ := runBinary(t, bin, "--lang=fr", "--config="+missing); strings.Contains(e, "config") {
		t.Errorf("stderr=%q mentions config", e)
	}
}

func TestLangNotCheckedWithoutGreeting(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--lang=fr", "--help"}, wantUsage)
	check(t, bin, []string{"--help", "--lang=fr"}, wantUsage)
	check(t, bin, []string{"--lang=fr", "--version"}, "dev\n")
	check(t, bin, []string{"--lang=fr", "--quiet"}, "")
	check(t, bin, []string{"--quiet", "--lang=es"}, "")
}
