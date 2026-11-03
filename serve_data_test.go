package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// dataHandler returns a handler counting into the data file at path.
func dataHandler(t *testing.T, path string) (http.Handler, *store) {
	t.Helper()
	st, err := loadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return newHandlerWith(st), st
}

// statsBody returns the /stats body from h.
func statsBody(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := do(h, "GET", "/stats", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("/stats: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	return rec.Body.String()
}

// readCounts decodes the data file at path.
func readCounts(t *testing.T, path string) map[string]int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Counts map[string]int }
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil || f.Counts == nil {
		t.Fatalf("data file %q is not valid: %v", b, err)
	}
	return f.Counts
}

// Scenario 2.1
func TestDataSurvivesRestart(t *testing.T) {
	bin := buildBinary(t, "")
	file := filepath.Join(t.TempDir(), "c.json")
	const want = `{"counts":{"Ann":2,"world":1}}` + "\n"

	p := startServe(t, bin, "--data="+file)
	if code, _, body := request(t, p.addr, "GET", "/stats", ""); code != 200 || body != `{"counts":{}}`+"\n" {
		t.Errorf("fresh /stats: %d %q", code, body)
	}
	for _, q := range []string{"name=Ann", "name=Ann", ""} {
		if code, _, _ := request(t, p.addr, "GET", "/greet", q); code != 200 {
			t.Fatalf("greet %q: %d", q, code)
		}
	}
	if code, ct, body := request(t, p.addr, "GET", "/stats", ""); code != 200 || ct != "application/json" || body != want {
		t.Errorf("/stats: %d %q %q", code, ct, body)
	}
	if got := readCounts(t, file); got["Ann"] != 2 || got["world"] != 1 || len(got) != 2 {
		t.Errorf("file counts %v", got)
	}
	if code, out := stopServe(t, p, syscall.SIGTERM); code != 0 || out != "" || p.stderr.Len() != 0 {
		t.Errorf("stop: exit=%d stdout=%q stderr=%q", code, out, p.stderr.String())
	}

	p = startServe(t, bin, "--data="+file)
	if _, _, body := request(t, p.addr, "GET", "/stats", ""); body != want {
		t.Errorf("after restart /stats: %q", body)
	}
	stopServe(t, p, syscall.SIGTERM)

	// A server without --data starts empty and does not touch the file.
	p = startServe(t, bin)
	if _, _, body := request(t, p.addr, "GET", "/stats", ""); body != `{"counts":{}}`+"\n" {
		t.Errorf("no --data /stats: %q", body)
	}
	stopServe(t, p, syscall.SIGTERM)
}

// Scenario 2.2: run with -race to also catch unsynchronised access.
func TestConcurrentGreetings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.json")
	h, _ := dataHandler(t, file)
	names := []string{"Ann", "Bob", "Cy"}
	var wg sync.WaitGroup
	codes := make(chan int, 1000)
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				codes <- do(h, "GET", "/greet", "name="+names[(g+i)%3]).Code
			}
		}(g)
	}
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != 200 {
			t.Fatalf("got status %d", c)
		}
	}
	var stats struct{ Counts map[string]int }
	if err := json.Unmarshal([]byte(statsBody(t, h)), &stats); err != nil {
		t.Fatal(err)
	}
	fileCounts := readCounts(t, file)
	sum := 0
	for _, n := range names {
		if stats.Counts[n] != fileCounts[n] {
			t.Errorf("%s: stats %d, file %d", n, stats.Counts[n], fileCounts[n])
		}
		sum += stats.Counts[n]
	}
	if sum != 1000 || len(stats.Counts) != 3 || len(fileCounts) != 3 {
		t.Errorf("sum %d, stats %v, file %v; want 1000 over 3 names", sum, stats.Counts, fileCounts)
	}
}

// Scenario 2.3
func TestOnlyOKGreetingsCount(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.json")
	h, _ := dataHandler(t, file)
	do(h, "GET", "/greet", "name=Ann")
	before, _ := os.ReadFile(file)
	for _, tc := range []struct {
		method, path, query string
		code                int
	}{
		{"GET", "/greet", "name=%zz", 400},
		{"GET", "/greet", "name=%FF", 400},
		{"GET", "/greet", "name=" + strings.Repeat("a", 257), 400},
		{"GET", "/nope", "name=Ann", 404},
		{"POST", "/greet", "name=Ann", 405},
		{"DELETE", "/greet", "name=Ann", 405},
		{"POST", "/stats", "", 405},
	} {
		if rec := do(h, tc.method, tc.path, tc.query); rec.Code != tc.code {
			t.Errorf("%s %s: got %d; want %d", tc.method, tc.path, rec.Code, tc.code)
		}
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(before) || statsBody(t, h) != `{"counts":{"Ann":1}}`+"\n" {
		t.Errorf("counts changed: file %q, stats %q", after, statsBody(t, h))
	}
}

// A HEAD request that gets 200 from /greet counts like GET.
func TestHeadGreetCounts(t *testing.T) {
	h := newHandler()
	if rec := do(h, "HEAD", "/greet", "name=Ann"); rec.Code != 200 {
		t.Fatalf("HEAD /greet: %d", rec.Code)
	}
	if got := statsBody(t, h); got != `{"counts":{"Ann":1}}`+"\n" {
		t.Errorf("/stats after HEAD: %q", got)
	}
}

// Without --data, counts live in memory and /stats keys are sorted.
func TestStatsInMemory(t *testing.T) {
	h := newHandler()
	if got := statsBody(t, h); got != `{"counts":{}}`+"\n" {
		t.Errorf("empty /stats: %q", got)
	}
	for _, q := range []string{"name=world", "name=Zed", "name=Ann", "", "name=Ann"} {
		do(h, "GET", "/greet", q)
	}
	if got := statsBody(t, h); got != `{"counts":{"Ann":2,"Zed":1,"world":2}}`+"\n" {
		t.Errorf("/stats: %q", got)
	}
}

// Scenario 2.4: every bad file is refused and left as it was.
func TestBadDataFiles(t *testing.T) {
	bin := buildBinary(t, "")
	for _, content := range []string{
		``, `null`, `[]`, `{}`, `{"counts":null}`, `{"counts":[]}`,
		`{"counts":{"a":-1}}`, `{"counts":{"a":1.5}}`, `{"counts":{"a":1e2}}`, `{"counts":{"a":"1"}}`,
		`{"counts":{"a":null}}`, `{"counts":{"a":1,"a":2}}`, `{"counts":{"a":99999999999999999999}}`,
		`{"counts":{},"x":1}`, `{"counts":{},"counts":{}}`, `{"x":{}}`, `{"counts":{}} x`, `{"counts":{}}{}`,
		`{"counts":{}`, "\xef\xbb\xbf" + `{"counts":{}}`,
	} {
		file := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		out, errOut, code := runBinary(t, bin, "serve", "--port=0", "--data="+file)
		want := "test-repo: serve: data " + file + ": invalid\n"
		if out != "" || errOut != want || code != 1 {
			t.Errorf("%q: stdout=%q stderr=%q exit=%d; want \"\", %q, 1", content, out, errOut, code, want)
		}
		if after, _ := os.ReadFile(file); string(after) != content {
			t.Errorf("%q: file changed to %q", content, after)
		}
	}
}

func TestDataFileOSErrors(t *testing.T) {
	bin := buildBinary(t, "")
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ file, osErr string }{
		{dir, "is a directory"},
		{filepath.Join(regular, "c.json"), "not a directory"},
	} {
		out, errOut, code := runBinary(t, bin, "serve", "--port=0", "--data="+tc.file)
		want := "test-repo: serve: data " + tc.file + ": " + tc.osErr + "\n"
		if out != "" || errOut != want || code != 1 {
			t.Errorf("%s: stdout=%q stderr=%q exit=%d; want \"\", %q, 1", tc.file, out, errOut, code, want)
		}
	}
	if b, _ := os.ReadFile(regular); string(b) != "x" {
		t.Errorf("regular file changed to %q", b)
	}
}

func TestParseCountsValid(t *testing.T) {
	for in, want := range map[string]string{
		`{"counts":{}}`: "map[]",
		" \n{\"counts\" : {\"a\":0, \"b\":12}}\n": "map[a:0 b:12]",
		`{"counts":{"Zoë":3,"A":1}}`:              "map[A:1 Zoë:3]",
	} {
		got, err := parseCounts([]byte(in))
		if err != nil || fmt.Sprint(got) != want {
			t.Errorf("%q: got %v, %v; want %s", in, got, err, want)
		}
	}
}

// Scenario 2.5: the file's directory is gone, so the write fails.
func TestWriteFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "c.json")
	h, _ := dataHandler(t, file)
	if rec := do(h, "GET", "/greet", "name=Ann"); rec.Code != 200 {
		t.Fatalf("first greet: %d", rec.Code)
	}
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"name=Ann", "name=Bob"} {
		rec := do(h, "GET", "/greet", q)
		if rec.Code != 500 || rec.Body.String() != `{"error":"storage failed"}`+"\n" || rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("greet %q: %d %q", q, rec.Code, rec.Body.String())
		}
	}
	if got := statsBody(t, h); got != `{"counts":{"Ann":1}}`+"\n" {
		t.Errorf("/stats after failed writes: %q", got)
	}
	if rec := do(h, "GET", "/healthz", ""); rec.Code != 200 {
		t.Errorf("/healthz: %d", rec.Code)
	}
	// Once the directory is back the server writes again.
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "GET", "/greet", "name=Ann"); rec.Code != 200 {
		t.Errorf("greet after recovery: %d", rec.Code)
	}
	if got := readCounts(t, file); got["Ann"] != 2 || len(got) != 1 {
		t.Errorf("file after recovery: %v", got)
	}
}

// Scenario 2.5 against a real process.
func TestServeStorageFailureProcess(t *testing.T) {
	bin := buildBinary(t, "")
	sub := filepath.Join(t.TempDir(), "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	p := startServe(t, bin, "--data="+filepath.Join(sub, "c.json"))
	os.RemoveAll(sub)
	if code, ct, body := request(t, p.addr, "GET", "/greet", "name=Ann"); code != 500 || ct != "application/json" || body != `{"error":"storage failed"}`+"\n" {
		t.Errorf("greet: %d %q %q", code, ct, body)
	}
	if _, _, body := request(t, p.addr, "GET", "/stats", ""); body != `{"counts":{}}`+"\n" {
		t.Errorf("/stats: %q", body)
	}
	if code, _, body := request(t, p.addr, "GET", "/healthz", ""); code != 200 || body != `{"status":"ok"}`+"\n" {
		t.Errorf("/healthz: %d %q", code, body)
	}
	if code, out := stopServe(t, p, syscall.SIGTERM); code != 0 || out != "" || p.stderr.Len() != 0 {
		t.Errorf("stop: exit=%d stdout=%q stderr=%q", code, out, p.stderr.String())
	}
}

// A failed save leaves no temp files behind.
func TestNoTempFilesLeft(t *testing.T) {
	dir := t.TempDir()
	h, _ := dataHandler(t, filepath.Join(dir, "c.json"))
	for i := 0; i < 5; i++ {
		do(h, "GET", "/greet", "name=Ann")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "c.json" {
		t.Errorf("directory holds %v; want only c.json", entries)
	}
}

// Scenario 2.6
func TestServeDataArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--port=0", "--data="}, "--data: empty value"},
		{[]string{"--data=", "--port=0"}, "--data: empty value"},
		{[]string{"--port=0", "--data=a", "--data=b"}, "repeated flag"},
		{[]string{"--port=0", "--data=a", "--data="}, "repeated flag"},
		{[]string{"--data=a"}, "--port is required"},
		{[]string{"--port=0", "--rate=5"}, "unknown argument"},
		{[]string{"--port=0", "--data", "a"}, "unknown argument"},
	} {
		if _, err := parseServeArgs(tc.args); err == nil || err.Error() != tc.want {
			t.Errorf("%q: got %v; want %q", tc.args, err, tc.want)
		}
	}
	cfg, err := parseServeArgs([]string{"--data=x/c.json", "--port=7"})
	if err != nil || cfg.port != 7 || cfg.data != "x/c.json" {
		t.Errorf("got %+v, %v", cfg, err)
	}
	bin := buildBinary(t, "")
	for _, args := range [][]string{
		{"serve", "--port=0", "--data="},
		{"serve", "--port=0", "--data=a", "--data=b"},
	} {
		out, errOut, code := runBinary(t, bin, args...)
		want := map[int]string{3: "test-repo: serve: --data: empty value\n", 4: "test-repo: serve: repeated flag\n"}[len(args)]
		if out != "" || errOut != want || code != 1 {
			t.Errorf("%q: stdout=%q stderr=%q exit=%d; want \"\", %q, 1", args, out, errOut, code, want)
		}
	}
	check(t, bin, []string{"serve", "--data=a", "--help"}, wantServeUsage)
	if wantServeUsage != "Usage: test-repo serve --port=N [--data=FILE]\n" {
		t.Errorf("usage constant is %q", wantServeUsage)
	}
}

// An existing file with valid counts is extended, not replaced.
func TestExistingCountsExtended(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(file, []byte(" {\"counts\": {\"Ann\": 41}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, _ := dataHandler(t, file)
	do(h, "GET", "/greet", "name=Ann")
	if got := statsBody(t, h); got != `{"counts":{"Ann":42}}`+"\n" {
		t.Errorf("/stats: %q", got)
	}
	if got := readCounts(t, file); got["Ann"] != 42 {
		t.Errorf("file: %v", got)
	}
}

// A write replaces the file (temp file, then rename) instead of rewriting
// it in place: a handle opened before the write keeps the old contents.
func TestWriteReplacesFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.json")
	h, _ := dataHandler(t, file)
	do(h, "GET", "/greet", "name=Ann")
	old, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	do(h, "GET", "/greet", "name=Bob")
	b, _ := io.ReadAll(old)
	if string(b) != `{"counts":{"Ann":1}}`+"\n" {
		t.Errorf("old handle reads %q; the file was rewritten in place", b)
	}
	if got := readCounts(t, file); got["Ann"] != 1 || got["Bob"] != 1 {
		t.Errorf("file: %v", got)
	}
}

// loadStore refuses invalid content without a process, and leaves the file.
func TestLoadStoreRejectsInvalid(t *testing.T) {
	for _, content := range []string{``, `null`, `{}`, `{"counts":null}`, `{"counts":{"a":-1}}`, `{"counts":{}} x`} {
		file := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if st, err := loadStore(file); err != errInvalidData || st != nil {
			t.Errorf("%q: got %v, %v; want errInvalidData", content, st, err)
		}
		if b, _ := os.ReadFile(file); string(b) != content {
			t.Errorf("%q: file changed to %q", content, b)
		}
	}
}
