package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cfg")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigGreeting(t *testing.T) {
	bin := buildBinary(t, "")
	cases := map[string]string{
		"greeting=Hi\n":                          "Hi, world\n",
		"greeting=Hi":                            "Hi, world\n",
		"greeting=Hi\r\n":                        "Hi, world\n",
		"\n  greeting = Hi  \n\n":                "Hi, world\n",
		"greeting=Good morning\n":                "Good morning, world\n",
		"greeting=a=b\n":                         "a=b, world\n",
		"greeting=%s\n":                          "%s, world\n",
		"greeting=" + strings.Repeat("x", 70000): strings.Repeat("x", 70000) + ", world\n",
		"":                                       "Hello, world\n",
		"\n\n  \n":                               "Hello, world\n",
		"greeting=\n":                            "Hello, world\n",
		"greeting=   \n":                         "Hello, world\n",
	}
	for content, want := range cases {
		check(t, bin, []string{"--config=" + writeCfg(t, content)}, want)
	}
}

func TestConfigCombined(t *testing.T) {
	bin := buildBinary(t, "")
	f := writeCfg(t, "greeting=Hi\n")
	check(t, bin, []string{"--config=" + f, "--name=Ann"}, "Hi, Ann\n")
	check(t, bin, []string{"--name=Ann", "--config=" + f}, "Hi, Ann\n")
	check(t, bin, []string{"--config=" + f, "--name=--help"}, "Hi, --help\n")
	check(t, bin, []string{"--config=" + f, "--shout"}, "HI, WORLD\n")
	check(t, bin, []string{"--config=" + f, "--config=missing"}, "Hi, world\n")
	check(t, bin, []string{"--config=", "--config=" + f}, "Hi, world\n")

	out, errOut, code := runRel(t, bin, f)
	if out != "Hi, world\n" || errOut != "" || code != 0 {
		t.Errorf("relative: %q %q %d", out, errOut, code)
	}
}

func runRel(t *testing.T, bin, f string) (string, string, int) {
	t.Helper()
	wd, _ := os.Getwd()
	if err := os.Chdir(filepath.Dir(f)); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	return runBinary(t, bin, "--config="+filepath.Base(f))
}

func TestConfigNearMisses(t *testing.T) {
	bin := buildBinary(t, "")
	f := writeCfg(t, "greeting=Hi\n")
	for _, args := range [][]string{
		{"--config"}, {"--config", f}, {"--config="}, {"-config=" + f},
		{"--CONFIG=" + f}, {"--configs=" + f}, {" --config=" + f},
	} {
		check(t, bin, args, "Hello, world\n")
	}
}

func wantFail(t *testing.T, bin string, args []string, prefix string, mustHave string, mustNot ...string) {
	t.Helper()
	out, errOut, code := runBinary(t, bin, args...)
	if out != "" || code != 1 {
		t.Errorf("args %q: stdout=%q exit=%d; want empty stdout, exit 1", args, out, code)
	}
	if !strings.HasPrefix(errOut, prefix) || strings.Count(errOut, "\n") != 1 || !strings.HasSuffix(errOut, "\n") {
		t.Errorf("args %q: stderr=%q; want one line starting %q", args, errOut, prefix)
	}
	if !strings.Contains(errOut, mustHave) {
		t.Errorf("args %q: stderr=%q; want it to contain %q", args, errOut, mustHave)
	}
	for _, s := range mustNot {
		if strings.Contains(errOut, s) {
			t.Errorf("args %q: stderr=%q echoes %q", args, errOut, s)
		}
	}
}

func TestConfigUnusable(t *testing.T) {
	bin := buildBinary(t, "")
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "missing"), dir}
	if os.Geteuid() != 0 {
		p := writeCfg(t, "greeting=Hi\n")
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	for _, p := range paths {
		for _, extra := range [][]string{nil, {"--name=Ann"}, {"--shout"}} {
			wantFail(t, bin, append([]string{"--config=" + p}, extra...), "test-repo: config "+p+": ", "")
		}
	}
}

func TestConfigMalformed(t *testing.T) {
	bin := buildBinary(t, "")
	cases := []struct {
		content, line string
		secrets       []string
	}{
		{"nonsense\n", "line 1", []string{"nonsense"}},
		{"=Hi\n", "line 1", []string{"Hi"}},
		{"color=red\n", "line 1", []string{"color", "red"}},
		{"Greeting=Hi\n", "line 1", []string{"Greeting"}},
		{"# note\n", "line 1", []string{"note"}},
		{"#greeting=Hi\n", "line 1", []string{"#greeting"}},
		{"greeting=Hi\ngreeting=Yo\n", "line 2", []string{"Yo"}},
		{"greeting=Hi\n\nnonsense\n", "line 3", []string{"nonsense"}},
		{"\xef\xbb\xbfgreeting=Hi\n", "line 1", nil},
		{"greeting=Hi\ngreeting=Hi\n", "line 2", nil},
		{"greeting=Hi\ngreeting=\n", "line 2", nil},
	}
	for _, c := range cases {
		f := writeCfg(t, c.content)
		wantFail(t, bin, []string{"--config=" + f}, "test-repo: config "+f+": ", c.line, c.secrets...)
	}
}

func TestConfigNotOpened(t *testing.T) {
	bin := buildBinary(t, "")
	bad := writeCfg(t, "nonsense\n")
	missing := filepath.Join(t.TempDir(), "missing")
	check(t, bin, []string{"--config=" + missing, "--help"}, wantUsage)
	check(t, bin, []string{"--help", "--config=" + bad}, wantUsage)
	check(t, bin, []string{"--config=" + bad, "--version"}, "dev\n")
	check(t, bin, []string{"--quiet", "--config=" + missing}, "")
	check(t, bin, []string{"--config=" + bad, "--quiet"}, "")
}
