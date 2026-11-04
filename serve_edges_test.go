package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// runMustFail runs "bin serve --port=0 args..." with a deadline, so a server
// that starts by mistake fails the test instead of hanging it.
func runMustFail(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"serve", "--port=0"}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("server did not exit; stdout %q stderr %q", out.String(), errb.String())
	}
	return out.String(), errb.String(), cmd.ProcessState.ExitCode()
}

const validData = `{"counts":{"Ann":2}}` + "\n"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// Scenario 1
func TestEdgesMissingFolder(t *testing.T) {
	bin := buildBinary(t, "")
	tmp := t.TempDir()
	file := filepath.Join(tmp, "missing", "d.json")
	stdout, stderr, code := runMustFail(t, bin, "--data="+file)
	if code != 1 || stdout != "" {
		t.Fatalf("code %d stdout %q", code, stdout)
	}
	if want := "test-repo: serve: data " + file + ": no such file or directory\n"; stderr != want {
		t.Fatalf("stderr %q; want %q", stderr, want)
	}
	if _, err := os.Stat(filepath.Join(tmp, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing folder was created: %v", err)
	}
}

// Scenario 2
func TestEdgesReadOnlyFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	bin := buildBinary(t, "")
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "d.json")
	writeFile(t, file, validData)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	before, _ := os.Stat(file)
	stdout, stderr, code := runMustFail(t, bin, "--data="+file)
	if code != 1 || stdout != "" {
		t.Fatalf("code %d stdout %q", code, stdout)
	}
	if want := "test-repo: serve: data " + file + ": permission denied\n"; stderr != want {
		t.Fatalf("stderr %q; want %q", stderr, want)
	}
	got, _ := os.ReadFile(file)
	after, _ := os.Stat(file)
	if string(got) != validData || after.Mode() != before.Mode() {
		t.Fatalf("FILE changed: %q %v", got, after.Mode())
	}
}

// Scenario 3
func TestEdgesHappyPathUnchanged(t *testing.T) {
	bin := buildBinary(t, "")
	for _, withFile := range []bool{false, true} {
		dir := t.TempDir()
		file := filepath.Join(dir, "d.json")
		if withFile {
			writeFile(t, file, validData)
		}
		before := names(t, dir)
		p := startServe(t, bin, "--data="+file)
		if code, _, _ := request(t, p.addr, "GET", "/healthz", ""); code != 200 {
			t.Fatalf("healthz %d", code)
		}
		if got := names(t, dir); strings.Join(got, ",") != strings.Join(before, ",") {
			t.Fatalf("folder after start %v; before %v", got, before)
		}
		if code, out := stopServe(t, p, syscall.SIGTERM); code != 0 || out != "" || p.stderr.Len() != 0 {
			t.Fatalf("code %d stdout %q stderr %q", code, out, p.stderr.String())
		}
		if got := names(t, dir); strings.Join(got, ",") != strings.Join(before, ",") {
			t.Fatalf("folder after stop %v; before %v", got, before)
		}
	}
}

// Scenario 4
func TestEdgesStaleCleanup(t *testing.T) {
	bin := buildBinary(t, "")
	root := t.TempDir()
	dir := filepath.Join(root, "data")
	os.Mkdir(dir, 0o755)
	outside := filepath.Join(root, "outside.txt")
	writeFile(t, outside, "outside")
	file := filepath.Join(dir, "d.json")
	writeFile(t, file, validData)
	stale := []string{"d.json.tmp1", "d.json.tmp4294967295"}
	keep := []string{"d.json.tmp", "d.json.tmpx", "d.json.tmp12x", "d.json.tmp1.bak", "D.json.tmp1", "other.json.tmp1"}
	for _, n := range append(append([]string{}, stale...), keep...) {
		writeFile(t, filepath.Join(dir, n), "content of "+n)
	}
	os.Mkdir(filepath.Join(dir, "d.json.tmp7"), 0o755)
	writeFile(t, filepath.Join(dir, "d.json.tmp7", "inner"), "inner")
	if err := os.Symlink(outside, filepath.Join(dir, "d.json.tmp8")); err != nil {
		t.Fatal(err)
	}
	p := startServe(t, bin, "--data="+file)
	for _, n := range stale {
		if _, err := os.Lstat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("%s still exists (err %v)", n, err)
		}
	}
	for _, n := range keep {
		if b, err := os.ReadFile(filepath.Join(dir, n)); err != nil || string(b) != "content of "+n {
			t.Errorf("%s changed: %q %v", n, b, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "d.json.tmp7", "inner")); err != nil || string(b) != "inner" {
		t.Errorf("directory contents changed: %q %v", b, err)
	}
	if tgt, err := os.Readlink(filepath.Join(dir, "d.json.tmp8")); err != nil || tgt != outside {
		t.Errorf("symlink changed: %q %v", tgt, err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "outside" {
		t.Errorf("symlink target changed: %q", b)
	}
	if b, _ := os.ReadFile(file); string(b) != validData {
		t.Errorf("FILE changed: %q", b)
	}
	stopServe(t, p, syscall.SIGTERM)
}

// Scenario 5
func TestEdgesRefusedFileLeavesFolderUntouched(t *testing.T) {
	bin := buildBinary(t, "")
	dir := t.TempDir()
	file := filepath.Join(dir, "d.json")
	writeFile(t, file, "{}")
	writeFile(t, filepath.Join(dir, "d.json.tmp1"), "x")
	stdout, stderr, code := runMustFail(t, bin, "--data="+file)
	if code != 1 || stdout != "" {
		t.Fatalf("code %d stdout %q", code, stdout)
	}
	if want := "test-repo: serve: data " + file + ": invalid\n"; stderr != want {
		t.Fatalf("stderr %q; want %q", stderr, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "d.json.tmp1")); err != nil {
		t.Fatalf("d.json.tmp1: %v", err)
	}
}

// lockedClock is a fake clock safe for concurrent use.
type lockedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *lockedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *lockedClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Scenario 6
func TestEdgesLimiterMapBounded(t *testing.T) {
	clk := &lockedClock{t: t0}
	l := newLimiter(5, clk.now)
	for i := 0; i < 10000; i++ {
		l.allow(fmt.Sprintf("c%d", i))
	}
	if len(l.clients) != 10000 {
		t.Fatalf("before sweep: %d clients", len(l.clients))
	}
	clk.set(t0.Add(sweepInterval + time.Second))
	l.allow("late")
	if len(l.clients) != 1 {
		t.Fatalf("%d clients after sweep; want 1", len(l.clients))
	}
}

// Scenario 7
func TestEdgesBusyBucketsKept(t *testing.T) {
	clk := &lockedClock{t: t0}
	l := newLimiter(1, clk.now)
	l.allow("first")
	clk.set(t0.Add(sweepInterval - 500*time.Millisecond))
	if !l.allow("A") {
		t.Fatal("A first call denied")
	}
	clk.set(t0.Add(sweepInterval))
	l.allow("B")
	if _, ok := l.clients["A"]; !ok {
		t.Fatal("A dropped 500ms after draining")
	}
	if l.allow("A") {
		t.Fatal("A allowed: its bucket was not kept")
	}

	// A drained exactly one refill period (1s) before the sweep.
	clk = &lockedClock{t: t0}
	l = newLimiter(1, clk.now)
	l.allow("first")
	clk.set(t0.Add(sweepInterval - time.Second))
	l.allow("A")
	clk.set(t0.Add(sweepInterval))
	l.allow("B")
	if _, ok := l.clients["A"]; ok {
		t.Fatal("A kept a full refill period after draining")
	}
}

// Scenario 8
func TestEdgesEvictionUnobservable(t *testing.T) {
	for _, rate := range []int{1, 7, 1000000} {
		rng := rand.New(rand.NewSource(int64(rate)))
		clk := &lockedClock{t: t0}
		normal := newLimiter(rate, clk.now)
		ref := newLimiter(rate, clk.now)
		ref.sweepInterval = 1 << 62
		cur := t0
		shrank, prev := false, 0
		for i := 0; i < 30000; i++ {
			// Mostly short steps, sometimes jumps across sweep intervals.
			switch n := rng.Intn(100); {
			case n < 3:
				cur = cur.Add(time.Duration(rng.Int63n(int64(3 * sweepInterval))))
			case n < 50:
				cur = cur.Add(time.Duration(rng.Int63n(int64(2 * time.Second / time.Duration(rate)))))
			}
			clk.set(cur)
			c := fmt.Sprintf("c%d", rng.Intn(60))
			if a, b := normal.allow(c), ref.allow(c); a != b {
				t.Fatalf("rate %d call %d client %s: normal %v, reference %v", rate, i, c, a, b)
			}
			if len(normal.clients) < prev {
				shrank = true
			}
			prev = len(normal.clients)
		}
		if !shrank {
			t.Errorf("rate %d: map never shrank", rate)
		}
	}
}

// Scenario 9: run with -race in CI; not run in the sandbox.
func TestEdgesConcurrentAllowAcrossSweep(t *testing.T) {
	clk := &lockedClock{t: t0}
	l := newLimiter(100, clk.now)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if i == 250 {
					clk.set(t0.Add(sweepInterval + time.Second))
				}
				l.allow(fmt.Sprintf("c%d", (g*7+i)%40))
			}
		}(g)
	}
	wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.clients) > 40 {
		t.Fatalf("%d clients", len(l.clients))
	}
}

// The sweep runs at most once per sweepInterval: idle buckets survive calls
// made before the next sweep is due.
func TestEdgesSweepIsGated(t *testing.T) {
	clk := &lockedClock{t: t0}
	l := newLimiter(5, clk.now)
	l.allow("idle")
	clk.set(t0.Add(sweepInterval - time.Second))
	l.allow("other")
	if _, ok := l.clients["idle"]; !ok {
		t.Fatal("swept before sweepInterval elapsed")
	}
	clk.set(t0.Add(sweepInterval))
	l.allow("other")
	if _, ok := l.clients["idle"]; ok {
		t.Fatal("not swept at sweepInterval")
	}
}
