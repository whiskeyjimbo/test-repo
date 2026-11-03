package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// parseCounts edge cases the PR's tests do not list.
func TestQAParseCountsInvalid(t *testing.T) {
	for _, in := range []string{
		`{"counts":{"a":01}}`, `{"counts":{"a":-0}}`, `{"counts":{"a":1E2}}`, `{"counts":{"a":1e+2}}`,
		`{"counts":{"a":0.0}}`, `{"counts":{"a":+1}}`, `{"counts":{"a":0x10}}`, `{"counts":{"a":true}}`,
		`{"counts":{"a":[1]}}`, `{"counts":{"a":{"b":1}}}`, `{"counts":{"a":1,}}`, `{"counts":{"a":1 "b":2}}`,
		`{"counts":{"a":9223372036854775808}}`, // int64 max + 1
		`{"Counts":{}}`, `{"COUNTS":{}}`, `{"counts ":{}}`, `{"coun\u0074s":{"a":1},"x":1}`,
		`{"counts":{"a":1},"counts":{}}`, `{"counts":{"a":1,"\u0061":2}}`, // duplicate after unescaping
		`{"counts":{"a":1}}}`, `{"counts":{"a":1}} {}`, `{"counts":{"a":1}}` + "\x00", `{"counts":{"a":1}}//c`,
		`[{"counts":{}}]`, `"counts"`, `{"counts":"x"}`, `{"counts":{}`, `{"counts":{"a":`,
		"\xef\xbb\xbf{\"counts\":{}}", "\xff", `{'counts':{}}`,
		"{\"counts\":{}}\u00a0", // NBSP is not JSON whitespace
		"{\"counts\":{}}\v",
	} {
		if got, err := parseCounts([]byte(in)); err != errInvalidData {
			t.Errorf("%q: got %v, %v; want errInvalidData", in, got, err)
		}
	}
}

func TestQAParseCountsValid(t *testing.T) {
	for in, want := range map[string]string{
		"\t\r\n {\r\n\"counts\"\t:\n{ } \r\n}\r\n\t ": "map[]",
		`{"counts":{"a":0}}`:                          "map[a:0]",
		`{"counts":{"":3}}`:                           "map[:3]",
		`{"counts":{"a":9223372036854775807}}`:        "map[a:9223372036854775807]",
	} {
		got, err := parseCounts([]byte(in))
		if err != nil || fmt.Sprint(got) != want {
			t.Errorf("%q: got %v, %v; want %s", in, got, err, want)
		}
	}
}

// Names that need JSON escaping survive /stats, the file and a restart.
func TestQAEscapedNamesRoundTrip(t *testing.T) {
	names := []string{
		`<b>&"x"`, `back\slash`, "tab\tnl\nx", "nul\x00x", "\u2028\u2029", "é日本🙂", "\ufffd", "a/b", "{\"counts\":1}", "world",
	}
	file := filepath.Join(t.TempDir(), "c.json")
	h, _ := dataHandler(t, file)
	for _, n := range names {
		if rec := do(h, "GET", "/greet", "name="+url.QueryEscape(n)); rec.Code != 200 {
			t.Fatalf("%q: %d %s", n, rec.Code, rec.Body)
		}
	}
	b, _ := os.ReadFile(file)
	if strings.ContainsAny(string(b[:len(b)-1]), "\n\t\x00\u2028\u2029") || b[len(b)-1] != '\n' {
		t.Errorf("file not one compact line with raw control chars escaped: %q", b)
	}
	if strings.Contains(string(b), "<") || strings.Contains(string(b), "&") {
		t.Errorf("file has unescaped <>&: %q", b)
	}
	st, err := loadStore(file)
	if err != nil {
		t.Fatalf("restart: %v (file %q)", err, b)
	}
	got := st.snapshot()
	for _, n := range names {
		if got[n] != 1 {
			t.Errorf("after restart %q = %d; want 1 (all %v)", n, got[n], got)
		}
	}
	if len(got) != len(names) {
		t.Errorf("%d keys after restart; want %d", len(got), len(names))
	}
	var m struct{ Counts map[string]int }
	if err := json.Unmarshal([]byte(statsBody(t, h)), &m); err != nil || len(m.Counts) != len(names) {
		t.Errorf("/stats: %v %v", err, m)
	}
}

// Name length boundary at the byte level, through the counting path.
func TestQANameBoundaryCounts(t *testing.T) {
	h, st := dataHandler(t, "")
	ok := strings.Repeat("a", 255)          // 255 bytes
	ok256 := strings.Repeat("a", 254) + "é" // 256 bytes
	bad := strings.Repeat("a", 255) + "é"   // 257 bytes
	for name, code := range map[string]int{ok: 200, ok256: 200, bad: 400} {
		if rec := do(h, "GET", "/greet", "name="+url.QueryEscape(name)); rec.Code != code {
			t.Errorf("len %d: %d; want %d", len(name), rec.Code, code)
		}
	}
	if s := st.snapshot(); len(s) != 2 {
		t.Errorf("counts %v; want only the two accepted names", s)
	}
}

// What does not count: every non-200 path, with the file bytes pinned.
func TestQANonCountingRequests(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.json")
	h, _ := dataHandler(t, file)
	do(h, "GET", "/greet", "name=Ann")
	for _, tc := range []struct {
		method, path, q string
		code            int
	}{
		{"GET", "/greet", "name=%zz", 400}, {"GET", "/greet", "name=%ff", 400}, {"GET", "/greet", "name=a;b", 400},
		{"GET", "/greet", "name=" + strings.Repeat("a", 257), 400},
		{"GET", "/greet", "name=Bob&name=%ff", 200}, // first value wins: counts Bob, not an error
		{"POST", "/greet", "name=Bob", 405}, {"PUT", "/greet", "name=Bob", 405}, {"DELETE", "/greet", "name=Bob", 405},
		{"OPTIONS", "/greet", "name=Bob", 405}, {"PATCH", "/greet", "name=Bob", 405},
		{"POST", "/stats", "", 405}, {"POST", "/healthz", "", 405},
		{"GET", "/greet/", "name=Bob", 404}, {"GET", "/nope", "name=Bob", 404}, {"GET", "/GREET", "name=Bob", 404},
	} {
		rec := do(h, tc.method, tc.path, tc.q)
		if rec.Code != tc.code {
			t.Errorf("%s %s?%s: %d; want %d", tc.method, tc.path, tc.q, rec.Code, tc.code)
		}
	}
	got := statsBody(t, h)
	if got != `{"counts":{"Ann":1,"Bob":1}}`+"\n" {
		t.Errorf("/stats = %q; want only Ann and the one valid Bob", got)
	}
}

// Rejected requests leave the file byte-identical (and uncreated).
func TestQARejectedLeaveFileUntouched(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.json")
	h, _ := dataHandler(t, file)
	for _, r := range [][3]string{{"GET", "/greet", "name=%ff"}, {"POST", "/greet", "name=a"}, {"GET", "/nope", ""}, {"GET", "/stats", ""}, {"GET", "/healthz", ""}} {
		do(h, r[0], r[1], r[2])
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("file exists after non-counting requests: %v", err)
	}
	do(h, "GET", "/greet", "name=Ann")
	before, _ := os.ReadFile(file)
	for _, r := range [][3]string{{"GET", "/greet", "name=%ff"}, {"POST", "/greet", "name=a"}, {"GET", "/nope", ""}, {"GET", "/stats", ""}} {
		do(h, r[0], r[1], r[2])
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Errorf("file changed: %q -> %q", before, after)
	}
}

// HEAD: /greet counts (carried-over decision), /stats and /healthz do not.
func TestQAHeadRequests(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.json")
	h, _ := dataHandler(t, file)
	for _, p := range []string{"/stats", "/healthz"} {
		if rec := do(h, "HEAD", p, ""); rec.Code != 200 {
			t.Errorf("HEAD %s: %d", p, rec.Code)
		}
	}
	if rec := do(h, "HEAD", "/greet", "name=Ann"); rec.Code != 200 {
		t.Errorf("HEAD /greet: %d", rec.Code)
	}
	if rec := do(h, "HEAD", "/greet", "name=%ff"); rec.Code != 400 {
		t.Errorf("HEAD /greet bad name: %d", rec.Code)
	}
	if got := readCounts(t, file); len(got) != 1 || got["Ann"] != 1 {
		t.Errorf("file after HEADs: %v; want Ann:1 only", got)
	}
}

// Rollback of an EXISTING name, then recovery once the directory is back.
func TestQARollbackExistingNameAndRecover(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0o755)
	file := filepath.Join(sub, "c.json")
	h, _ := dataHandler(t, file)
	for i := 0; i < 3; i++ {
		do(h, "GET", "/greet", "name=Ann")
	}
	os.RemoveAll(sub)
	for i := 0; i < 3; i++ {
		if rec := do(h, "GET", "/greet", "name=Ann"); rec.Code != 500 {
			t.Fatalf("greet %d: %d", i, rec.Code)
		}
	}
	if got := statsBody(t, h); got != `{"counts":{"Ann":3}}`+"\n" {
		t.Errorf("/stats after failed writes = %q; want Ann:3", got)
	}
	os.Mkdir(sub, 0o755)
	if rec := do(h, "GET", "/greet", "name=Ann"); rec.Code != 200 {
		t.Fatalf("after recovery: %d", rec.Code)
	}
	if got := readCounts(t, file); got["Ann"] != 4 || len(got) != 1 {
		t.Errorf("file after recovery: %v; want Ann:4 (no phantom increments from failed writes)", got)
	}
	ents, _ := os.ReadDir(sub)
	if len(ents) != 1 {
		t.Errorf("leftovers in dir: %v", ents)
	}
}

// Rename fails (FILE became a directory): 500, rollback, temp file removed.
func TestQARenameFailureCleansTemp(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "c.json")
	h, _ := dataHandler(t, file)
	do(h, "GET", "/greet", "name=Ann")
	os.Remove(file)
	if err := os.Mkdir(file, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(file, "keep"), []byte("x"), 0o600) // non-empty dir: rename must fail
	if rec := do(h, "GET", "/greet", "name=Bob"); rec.Code != 500 {
		t.Fatalf("greet: %d", rec.Code)
	}
	if got := statsBody(t, h); got != `{"counts":{"Ann":1}}`+"\n" {
		t.Errorf("/stats = %q", got)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || ents[0].Name() != "c.json" {
		t.Errorf("leftover temp files: %v", ents)
	}
}

// Read-only directory at runtime: 500 and rollback (skipped as root).
func TestQAReadOnlyDirWriteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "c.json")
	h, _ := dataHandler(t, file)
	do(h, "GET", "/greet", "name=Ann")
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	if rec := do(h, "GET", "/greet", "name=Ann"); rec.Code != 500 {
		t.Errorf("greet in read-only dir: %d", rec.Code)
	}
	if got := statsBody(t, h); got != `{"counts":{"Ann":1}}`+"\n" {
		t.Errorf("/stats = %q", got)
	}
}

// A new file must not be world-writable, whatever the umask.
func TestQANewFileMode(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.json")
	h, _ := dataHandler(t, file)
	do(h, "GET", "/greet", "name=Ann")
	fi, _ := os.Stat(file)
	if fi.Mode().Perm()&0o022 != 0 || !fi.Mode().IsRegular() {
		t.Errorf("new file mode %v", fi.Mode())
	}
}

// Valid file + startup + clean shutdown with no greetings leaves it byte
// for byte; an absent file is not created.
func TestQAStartupDoesNotTouchFiles(t *testing.T) {
	bin := buildBinary(t, "")
	dir := t.TempDir()
	valid := filepath.Join(dir, "v.json")
	content := " \n{ \"counts\" : { \"b\" : 2 , \"a\" : 1 } }\n\n"
	os.WriteFile(valid, []byte(content), 0o640)
	absent := filepath.Join(dir, "absent.json")
	for _, f := range []string{valid, absent} {
		p := startServe(t, bin, "--data="+f)
		request(t, p.addr, "GET", "/stats", "")
		request(t, p.addr, "GET", "/greet", "name=%ff")
		if code, out := stopServe(t, p, syscall.SIGTERM); code != 0 || out != "" || p.stderr.Len() != 0 {
			t.Errorf("%s stop: %d %q %q", f, code, out, p.stderr)
		}
	}
	if b, _ := os.ReadFile(valid); string(b) != content {
		t.Errorf("valid file rewritten to %q", b)
	}
	if fi, _ := os.Stat(valid); fi.Mode().Perm() != 0o640 {
		t.Errorf("valid file mode now %o", fi.Mode().Perm())
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Errorf("absent file created at startup: %v", err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("dir holds %v", ents)
	}
}

// Restart round trip of a file produced by an older server: counts add up
// across several restarts, and /stats equals the file after each.
func TestQAMultiRestartAccumulates(t *testing.T) {
	bin := buildBinary(t, "")
	file := filepath.Join(t.TempDir(), "c.json")
	for round := 1; round <= 3; round++ {
		p := startServe(t, bin, "--data="+file)
		request(t, p.addr, "GET", "/greet", "name="+url.QueryEscape("Zoë <&>"))
		request(t, p.addr, "HEAD", "/greet", "")
		_, _, stats := request(t, p.addr, "GET", "/stats", "")
		want := fmt.Sprintf(`{"counts":{"Zo\u00eb \u003c\u0026\u003e":%d,"world":%d}}`, round, round)
		want = strings.Replace(want, `\u00eb`, "ë", 1) + "\n"
		if stats != want {
			t.Fatalf("round %d /stats = %q; want %q", round, stats, want)
		}
		if code, _ := stopServe(t, p, syscall.SIGTERM); code != 0 {
			t.Fatalf("round %d exit %d", round, code)
		}
		if got := readCounts(t, file); got["world"] != round {
			t.Fatalf("round %d file %v", round, got)
		}
	}
}

// Error line shape for a few OS errors: one line, FILE echoed, no content.
func TestQAErrorLineShape(t *testing.T) {
	bin := buildBinary(t, "")
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.json")
	os.WriteFile(secret, []byte(`{"counts":{"TOPSECRET":-1}}`), 0o600)
	loop := filepath.Join(dir, "loop")
	os.Symlink("loop", loop)
	for _, f := range []string{secret, dir, loop, filepath.Join(secret, "x")} {
		out, errOut, code := runBinary(t, bin, "serve", "--port=0", "--data="+f)
		if code != 1 || out != "" || strings.Count(errOut, "\n") != 1 || !strings.HasSuffix(errOut, "\n") ||
			!strings.HasPrefix(errOut, "test-repo: serve: data "+f+": ") || strings.Contains(errOut, "TOPSECRET") {
			t.Errorf("%s: exit=%d stdout=%q stderr=%q", f, code, out, errOut)
		}
	}
}

// Unreadable file (skipped as root): OS error, not "invalid", not a crash.
func TestQAUnreadableDataFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	bin := buildBinary(t, "")
	f := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(f, []byte(`{"counts":{}}`), 0o000)
	out, errOut, code := runBinary(t, bin, "serve", "--port=0", "--data="+f)
	if code != 1 || out != "" || errOut != "test-repo: serve: data "+f+": permission denied\n" {
		t.Errorf("exit=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

// Argument order: the first problem wins and nothing is echoed.
func TestQAServeArgOrderAndEcho(t *testing.T) {
	bin := buildBinary(t, "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--bogus=SECRETVAL", "--data="}, "unknown argument"},
		{[]string{"--data=", "--bogus"}, "--data: empty value"},
		{[]string{"--data=x", "--data=SECRETVAL"}, "repeated flag"},
		{[]string{"--port=0", "--data=a", "--port=1"}, "repeated flag"},
		{[]string{"--data=x"}, "--port is required"},
		{[]string{"--port=0", "--Data=x"}, "unknown argument"},
		{[]string{"--port=0", "-data=x"}, "unknown argument"},
		{[]string{"--port=0", "--data"}, "unknown argument"},
		{[]string{"--port=0", "--data=" + "x", "--data"}, "unknown argument"},
	} {
		out, errOut, code := runBinary(t, bin, append([]string{"serve"}, tc.args...)...)
		want := "test-repo: serve: " + tc.want + "\n"
		if out != "" || errOut != want || code != 1 || strings.Contains(errOut, "SECRETVAL") {
			t.Errorf("%q: stdout=%q stderr=%q exit=%d; want %q", tc.args, out, errOut, code, want)
		}
	}
}

// 20 x 50 concurrent greetings plus concurrent /stats readers and a raw
// file reader: every observed file is valid, /stats totals never decrease
// and never exceed the number of 200s so far, and the end state sums to 1000.
func TestQAConcurrentReadersDuringWrites(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.json")
	h, _ := dataHandler(t, file)
	names := []string{"Ann", "Bob", "world"}
	done := make(chan struct{})
	var readers sync.WaitGroup
	var mu sync.Mutex
	var problems []string
	note := func(s string) { mu.Lock(); problems = append(problems, s); mu.Unlock() }
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func(r int) {
			defer readers.Done()
			last := int64(0)
			for {
				select {
				case <-done:
					return
				default:
				}
				if r%2 == 0 {
					var m struct{ Counts map[string]int64 }
					rec := do(h, "GET", "/stats", "")
					if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m.Counts == nil {
						note("stats: " + rec.Body.String())
						return
					}
					var sum int64
					for _, v := range m.Counts {
						sum += v
					}
					if sum < last || sum > 1000 {
						note(fmt.Sprintf("stats sum %d after %d", sum, last))
						return
					}
					last = sum
				} else if b, err := os.ReadFile(file); err == nil {
					if _, err := parseCounts(b); err != nil {
						note("torn file: " + string(b))
						return
					}
				}
			}
		}(r)
	}
	var writers sync.WaitGroup
	for g := 0; g < 20; g++ {
		writers.Add(1)
		go func(g int) {
			defer writers.Done()
			for i := 0; i < 50; i++ {
				q := "name=" + names[(g+i)%3]
				if names[(g+i)%3] == "world" {
					q = ""
				}
				if rec := do(h, "GET", "/greet", q); rec.Code != 200 {
					note(fmt.Sprint("greet ", rec.Code))
				}
			}
		}(g)
	}
	writers.Wait()
	close(done)
	readers.Wait()
	for _, p := range problems {
		t.Error(p)
	}
	var total int
	fileCounts := readCounts(t, file)
	for _, n := range names {
		total += fileCounts[n]
	}
	if total != 1000 {
		t.Errorf("file total %d; want 1000 (%v)", total, fileCounts)
	}
	var m struct{ Counts map[string]int }
	json.Unmarshal([]byte(statsBody(t, h)), &m)
	for _, n := range names {
		if m.Counts[n] != fileCounts[n] {
			t.Errorf("%s: stats %d, file %d", n, m.Counts[n], fileCounts[n])
		}
	}
}
