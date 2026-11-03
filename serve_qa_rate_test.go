package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// qrClock is a goroutine-safe settable clock; no test here sleeps.
type qrClock struct {
	mu sync.Mutex
	t  time.Time
}

func newQRClock() *qrClock {
	return &qrClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}
func (c *qrClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *qrClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}
func (c *qrClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// qrDrain takes tokens from client until the limiter refuses and returns how
// many it took (at most max+1 attempts).
func qrDrain(l *limiter, client string, max int) int {
	n := 0
	for i := 0; i <= max; i++ {
		if !l.allow(client) {
			break
		}
		n++
	}
	return n
}

func qrReq(h http.Handler, remote, method, target string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remote
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Add(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Refill boundaries: exactly one interval gives exactly one token, for rates
// whose 1s/rate does not divide evenly as well as for round ones.
func TestQARateRefillBoundary(t *testing.T) {
	for _, rate := range []int{1, 2, 3, 6, 7, 9, 11, 13, 999, 1000, 1000000} {
		clk := newQRClock()
		l := newLimiter(rate, clk.now)
		if n := qrDrain(l, "a", rate+5); n != rate {
			t.Fatalf("rate %d: first burst %d", rate, n)
		}
		step := l.interval
		clk.advance(step - 1)
		if l.allow("a") {
			t.Errorf("rate %d: token one nanosecond early", rate)
		}
		clk.advance(1)
		if !l.allow("a") {
			t.Errorf("rate %d: no token at exactly one interval", rate)
		}
		if l.allow("a") {
			t.Errorf("rate %d: a second token from one interval", rate)
		}
	}
}

// After a full second from an empty bucket the client gets exactly rate
// tokens, never rate+1, for awkward rates.
func TestQARateFullSecondExact(t *testing.T) {
	for _, rate := range []int{1, 2, 3, 6, 7, 9, 11, 13, 17, 999, 1000, 12345, 1000000} {
		clk := newQRClock()
		l := newLimiter(rate, clk.now)
		qrDrain(l, "a", rate+5)
		clk.advance(time.Second)
		if n := qrDrain(l, "a", rate+5); n != rate {
			t.Errorf("rate %d: %d tokens after 1 s, want %d", rate, n, rate)
		}
	}
}

// Partial refill carries: many small steps give the same tokens as one big
// step, and refused requests in between do not reset the partial interval.
func TestQARatePartialCarryAndDeniedPolling(t *testing.T) {
	for _, rate := range []int{2, 3, 7, 10} {
		clk := newQRClock()
		l := newLimiter(rate, clk.now)
		qrDrain(l, "a", rate+5)
		step := l.interval / 4 // never lines up with a whole token
		got := 0
		for i := 0; i < 4*rate; i++ { // 1 s of interval/4 steps, polling each step
			clk.advance(step)
			for l.allow("a") {
				got++
			}
		}
		// Top up the truncation remainder so the total is exactly rate intervals.
		clk.advance(l.interval*time.Duration(rate) - step*time.Duration(4*rate))
		for l.allow("a") {
			got++
		}
		if got != rate {
			t.Errorf("rate %d: polling every quarter interval got %d tokens in %d intervals", rate, got, rate)
		}
	}
}

// Idle time never banks more than rate tokens, including a huge jump and a
// zero-value-time starting point.
func TestQARateCapAndHugeJump(t *testing.T) {
	for _, rate := range []int{1, 3, 1000000} {
		clk := newQRClock()
		l := newLimiter(rate, clk.now)
		qrDrain(l, "a", rate+5)
		for _, jump := range []time.Duration{time.Hour, 24 * 365 * time.Hour, time.Duration(1<<63 - 1)} {
			clk.advance(jump)
			if n := qrDrain(l, "a", rate+5); n != rate {
				t.Errorf("rate %d after jump %v: %d tokens, want cap %d", rate, jump, n, rate)
			}
		}
		// Bucket created at year 1, then the clock jumps 2000+ years.
		clk.set(time.Time{})
		l = newLimiter(rate, clk.now)
		l.allow("z")
		clk.set(time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))
		if n := qrDrain(l, "z", rate+5); n != rate {
			t.Errorf("rate %d after year 1 -> 9999: %d tokens, want %d", rate, n, rate)
		}
	}
}

// The clock going backwards must never mint tokens or panic, and when it
// comes back a full bucket is still capped.
func TestQARateClockBackwards(t *testing.T) {
	clk := newQRClock()
	l := newLimiter(3, clk.now)
	start := clk.now()
	qrDrain(l, "a", 8)
	clk.set(start.Add(-time.Hour))
	if n := qrDrain(l, "a", 8); n != 0 {
		t.Errorf("clock one hour back: %d tokens minted", n)
	}
	clk.set(start.Add(-time.Nanosecond))
	if l.allow("a") {
		t.Error("clock 1 ns back: token minted")
	}
	clk.set(start.Add(time.Hour))
	if n := qrDrain(l, "a", 8); n != 3 {
		t.Errorf("clock far forward after going back: %d tokens, want 3", n)
	}
	// A client first seen while the clock is back is unaffected by it.
	clk.set(start.Add(-48 * time.Hour))
	if n := qrDrain(l, "fresh", 8); n != 3 {
		t.Errorf("fresh client with clock back: %d tokens, want 3", n)
	}
}

// Rate 1 and rate 1000000 end to end: one request per second, and 1000000
// in a burst.
func TestQARateExtremes(t *testing.T) {
	clk := newQRClock()
	l := newLimiter(1, clk.now)
	if !l.allow("a") || l.allow("a") {
		t.Fatal("rate 1: want exactly one in the first instant")
	}
	clk.advance(time.Second - 1)
	if l.allow("a") {
		t.Error("rate 1: token 1 ns before a second")
	}
	clk.advance(1)
	if !l.allow("a") || l.allow("a") {
		t.Error("rate 1: want exactly one at one second")
	}
	big := newLimiter(1000000, clk.now)
	if n := qrDrain(big, "a", 1000010); n != 1000000 {
		t.Errorf("rate 1000000: burst %d", n)
	}
}

// Per-client isolation: IPv6, ports, zones and addresses without a port.
func TestQARateClientKeys(t *testing.T) {
	clk := newQRClock()
	h := limitHandler(newHandler(), newLimiter(1, clk.now))
	get := func(remote string) int { return qrReq(h, remote, "GET", "/greet").Code }

	if get("[2001:db8::1]:1000") != 200 {
		t.Fatal("first v6 request refused")
	}
	if get("[2001:db8::1]:2000") != 429 {
		t.Error("same v6 host, new port: want the same bucket")
	}
	if get("[2001:db8::2]:1000") != 200 {
		t.Error("other v6 host shares a bucket")
	}
	if get("192.0.2.1:1000") != 200 || get("192.0.2.1:1001") != 429 {
		t.Error("v4 bucket wrong")
	}
	// v4 and v6 clients with look-alike text never collide.
	if get("[::1]:1") != 200 || get("127.0.0.1:1") != 200 {
		t.Error("::1 and 127.0.0.1 share a bucket")
	}
	// Zone: same zone shares across ports, different zones are different hosts.
	if get("[fe80::1%eth0]:1") != 200 || get("[fe80::1%eth0]:2") != 429 {
		t.Error("zoned address: same host+zone must share a bucket")
	}
	if get("[fe80::1%eth1]:1") != 200 {
		t.Error("zoned address on another interface: want its own bucket (host part is the whole text)")
	}
	// RemoteAddr without a port and the empty RemoteAddr must not panic and
	// must still be limited.
	for _, ra := range []string{"192.0.2.77", "", "garbage", "[::1]"} {
		first := get(ra)
		second := get(ra)
		if first != 200 || second != 429 {
			t.Errorf("RemoteAddr %q: got %d then %d, want 200 then 429", ra, first, second)
		}
	}
}

// Proxy headers never choose or move a bucket, in either direction.
func TestQARateProxyHeadersIgnored(t *testing.T) {
	headers := [][2]string{
		{"X-Forwarded-For", "198.51.100.1"},
		{"X-Forwarded-For", "198.51.100.1, 192.0.2.1"},
		{"Forwarded", "for=198.51.100.1"},
		{"Forwarded", `for="[2001:db8::7]:4711"`},
		{"X-Real-IP", "198.51.100.1"},
		{"True-Client-IP", "198.51.100.1"},
		{"X-Client-IP", "198.51.100.1"},
		{"CF-Connecting-IP", "198.51.100.1"},
		{"X-Forwarded-Host", "198.51.100.1"},
	}
	for _, hd := range headers {
		clk := newQRClock()
		h := limitHandler(newHandler(), newLimiter(1, clk.now))
		// c2 posing as the victim must not drain the victim's bucket.
		if rec := qrReq(h, "192.0.2.2:1", "GET", "/greet", hd[0], "192.0.2.1"); rec.Code != 200 {
			t.Fatalf("%s: spoofing client refused: %d", hd[0], rec.Code)
		}
		if rec := qrReq(h, "192.0.2.1:1", "GET", "/greet"); rec.Code != 200 {
			t.Errorf("%s: victim's bucket was drained by a spoofed header (%d)", hd[0], rec.Code)
		}
		// The exhausted victim cannot get a fresh bucket by naming another host.
		if rec := qrReq(h, "192.0.2.1:1", "GET", "/greet", hd[0], hd[1]); rec.Code != 429 {
			t.Errorf("%s: %q gave an exhausted client a fresh bucket (%d)", hd[0], hd[1], rec.Code)
		}
	}
}

// /healthz is exempt only as the exact path. Every other spelling takes a
// token, and the exempt one never takes one.
func TestQARateHealthzExactPathOnly(t *testing.T) {
	// Exhausted client: what still gets through, and what gets 429.
	clk := newQRClock()
	h := limitHandler(newHandler(), newLimiter(1, clk.now))
	qrReq(h, "192.0.2.1:1", "GET", "/greet")
	for _, tc := range []struct {
		method, target string
		limited        bool
	}{
		{"GET", "/healthz", false},
		{"GET", "/healthz?x=1", false},
		{"GET", "/healthz?", false},
		{"HEAD", "/healthz", false},
		{"POST", "/healthz", false}, // 405 from the mux, not a 429
		{"GET", "/healthz/", true},
		{"GET", "//healthz", true},
		{"GET", "/HEALTHZ", true},
		{"GET", "/healthz/x", true},
		{"GET", "/healthzz", true},
		{"GET", "/nope", true},
		{"GET", "/greet", true},
		{"GET", "/stats", true},
		{"POST", "/greet", true}, // 429 wins over 405
	} {
		rec := qrReq(h, "192.0.2.1:1", tc.method, tc.target)
		if got := rec.Code == 429; got != tc.limited {
			t.Errorf("exhausted %s %s: status %d, limited=%v want %v", tc.method, tc.target, rec.Code, got, tc.limited)
		}
	}
	if rec := qrReq(h, "192.0.2.1:1", "POST", "/healthz"); rec.Code != 405 {
		t.Errorf("POST /healthz exhausted: %d, want 405", rec.Code)
	}
	if rec := qrReq(h, "192.0.2.1:1", "GET", "/healthz"); rec.Code != 200 || rec.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Errorf("GET /healthz exhausted: %d %q", rec.Code, rec.Body.String())
	}

	// Non-exhausted client: exempt spellings take no token, others take one.
	for _, tc := range []struct {
		method, target string
		takes          bool
	}{
		{"GET", "/healthz", false},
		{"GET", "/healthz?x=1", false},
		{"HEAD", "/healthz", false},
		{"POST", "/healthz", false},
		{"GET", "/healthz/", true},
		{"GET", "//healthz", true},
		{"GET", "/HEALTHZ", true},
	} {
		clk := newQRClock()
		h := limitHandler(newHandler(), newLimiter(1, clk.now))
		for i := 0; i < 20; i++ {
			qrReq(h, "192.0.2.1:1", tc.method, tc.target)
		}
		greet := qrReq(h, "192.0.2.1:1", "GET", "/greet").Code
		if tc.takes && greet != 429 {
			t.Errorf("%s %s took no token (greet %d)", tc.method, tc.target, greet)
		}
		if !tc.takes && greet != 200 {
			t.Errorf("%s %s took a token (greet %d)", tc.method, tc.target, greet)
		}
	}
}

// 429 responses: exact bytes, headers, for every method and route that can
// be limited.
func TestQARate429Shape(t *testing.T) {
	clk := newQRClock()
	h := limitHandler(newHandler(), newLimiter(1, clk.now))
	qrReq(h, "192.0.2.1:1", "GET", "/greet")
	for _, tc := range [][2]string{
		{"GET", "/greet"}, {"GET", "/greet?name=%ff"}, {"GET", "/greet?name=%zz"},
		{"GET", "/stats"}, {"GET", "/nope"}, {"POST", "/greet"}, {"DELETE", "/stats"},
		{"HEAD", "/greet"}, {"GET", "//greet"}, {"GET", "/healthz/"},
	} {
		rec := qrReq(h, "192.0.2.1:1", tc[0], tc[1])
		if rec.Code != 429 {
			t.Errorf("%s %s: %d, want 429", tc[0], tc[1], rec.Code)
			continue
		}
		if rec.Body.String() != "{\"error\":\"rate limited\"}\n" {
			t.Errorf("%s %s: body %q", tc[0], tc[1], rec.Body.String())
		}
		hd := rec.Header()
		if v := hd.Values("Retry-After"); len(v) != 1 || v[0] != "1" {
			t.Errorf("%s %s: Retry-After %q", tc[0], tc[1], v)
		}
		if v := hd.Values("Content-Type"); len(v) != 1 || v[0] != "application/json" {
			t.Errorf("%s %s: Content-Type %q", tc[0], tc[1], v)
		}
	}
}

// A 429 reaches neither /stats nor the --data file; only the 200s do.
func TestQARateLimitedNotCountedNotPersisted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "counts.json")
	st, err := loadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	clk := newQRClock()
	h := limitHandler(newHandlerWith(st), newLimiter(2, clk.now))
	for i := 0; i < 2; i++ {
		if rec := qrReq(h, "192.0.2.1:1", "GET", "/greet?name=A"); rec.Code != 200 {
			t.Fatalf("greet %d: %d", i, rec.Code)
		}
	}
	for _, target := range []string{"/greet?name=A", "/greet?name=B", "/greet", "/greet?name=%ff"} {
		if rec := qrReq(h, "192.0.2.1:1", "GET", target); rec.Code != 429 {
			t.Fatalf("%s: %d", target, rec.Code)
		}
	}
	if got := qrReq(h, "192.0.2.2:1", "GET", "/stats").Body.String(); got != "{\"counts\":{\"A\":2}}\n" {
		t.Errorf("stats %q", got)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "{\"counts\":{\"A\":2}}\n" && string(b) != "{\"counts\":{\"A\":2}}" {
		t.Errorf("data file %q, %v", b, err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("stray files in data dir: %v", ents)
	}
	// After a refill the same client counts again.
	clk.advance(time.Second)
	if rec := qrReq(h, "192.0.2.1:1", "GET", "/greet?name=B"); rec.Code != 200 {
		t.Errorf("after refill: %d", rec.Code)
	}
	if got := qrReq(h, "192.0.2.2:1", "GET", "/stats").Body.String(); got != "{\"counts\":{\"A\":2,\"B\":1}}\n" {
		t.Errorf("stats after refill %q", got)
	}
}

// A burst of goroutines from one client at one instant gets exactly rate
// requests, and many distinct clients at once each get their own full bucket.
func TestQARateConcurrentBurst(t *testing.T) {
	for _, rate := range []int{1, 10, 100, 1000} {
		clk := newQRClock()
		l := newLimiter(rate, clk.now)
		var ok atomic.Int64
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 4*rate+50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if l.allow("192.0.2.1") {
					ok.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if ok.Load() != int64(rate) {
			t.Errorf("rate %d: %d allowed from a concurrent burst", rate, ok.Load())
		}
	}
}

func TestQARateConcurrentManyClientsAndHandler(t *testing.T) {
	clk := newQRClock()
	h := limitHandler(newHandler(), newLimiter(3, clk.now))
	const clients = 300
	var wg sync.WaitGroup
	var bad atomic.Int64
	start := make(chan struct{})
	for c := 0; c < clients; c++ {
		remote := fmt.Sprintf("10.%d.%d.1:5000", c/250, c%250)
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				rec := qrReq(h, remote, "GET", "/greet?name=Q")
				if rec.Code != 200 && rec.Code != 429 {
					bad.Add(1)
				}
			}()
		}
	}
	close(start)
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d responses neither 200 nor 429", bad.Load())
	}
	// Each client sent 6 and was entitled to 3: stats from a fresh client.
	if got := qrReq(h, "203.0.113.1:1", "GET", "/stats").Body.String(); got != fmt.Sprintf("{\"counts\":{\"Q\":%d}}\n", clients*3) {
		t.Errorf("stats after concurrent burst: %s", got)
	}
}

// Argument parsing edges for --rate.
func TestQARateArgEdges(t *testing.T) {
	const bad = "--rate: want an integer 1-1000000"
	valid := map[string]int{
		"1": 1, "01": 1, "0000010": 10, "000005": 5, "1000000": 1000000, "0999999": 999999,
	}
	for v, want := range valid {
		cfg, err := parseServeArgs([]string{"--port=0", "--rate=" + v})
		if err != nil || cfg.rate != want {
			t.Errorf("--rate=%s: rate %d err %v, want %d", v, cfg.rate, err, want)
		}
	}
	invalid := []string{
		"", "0", "00", "1000001", "1e3", "1E3", "+5", "-5", " 5", "5 ", "5\n", "0x10", "1_000", "5.0", ".5",
		"５", "٥", "१०", "５５", "five", "1,000", "NaN", "Inf",
		"99999999999999999999999999", "18446744073709551617", "9223372036854775808",
	}
	for _, v := range invalid {
		_, err := parseServeArgs([]string{"--port=0", "--rate=" + v})
		if err == nil || err.Error() != bad {
			t.Errorf("--rate=%q: err %v, want %q", v, err, bad)
		}
		if err != nil && len(v) > 2 && strings.Contains(err.Error(), v) {
			t.Errorf("--rate=%q echoed in the error", v)
		}
	}
	// Repeats are an error whatever the values; an invalid first value is
	// reported first.
	for _, args := range [][]string{
		{"--rate=5", "--rate=5"}, {"--rate=5", "--rate=6"}, {"--rate=5", "--port=0", "--rate=7"},
		{"--rate=5", "--rate="}, {"--rate=5", "--rate=0"},
	} {
		_, err := parseServeArgs(append([]string{"--port=0"}, args...))
		if err == nil || err.Error() != "repeated flag" {
			t.Errorf("%q: %v, want repeated flag", args, err)
		}
	}
	if _, err := parseServeArgs([]string{"--port=0", "--rate=0", "--rate=0"}); err == nil || err.Error() != bad {
		t.Errorf("invalid then repeated: %v, want the --rate error first", err)
	}
	// Near-miss spellings are unknown arguments, not --rate.
	for _, a := range []string{"--rate", "--rate5", "--RATE=5", "-rate=5", "--rates=5", "rate=5", "--rate:5", "--Rate=5"} {
		_, err := parseServeArgs([]string{"--port=0", a})
		if err == nil || err.Error() != "unknown argument" {
			t.Errorf("%q: %v, want unknown argument", a, err)
		}
	}
	// Combined with the other flags in any order.
	cfg, err := parseServeArgs([]string{"--rate=7", "--data=/tmp/x", "--port=9"})
	if err != nil || cfg.rate != 7 || cfg.data != "/tmp/x" || cfg.port != 9 {
		t.Errorf("combined: %+v %v", cfg, err)
	}
	// --help wins over everything, including a bad or repeated --rate.
	for _, args := range [][]string{{"--rate=x", "--help"}, {"--help", "--rate=0"}, {"--rate=1", "--rate=1", "--help"}} {
		if _, err := parseServeArgs(args); err != errServeHelp {
			t.Errorf("%q: %v, want help", args, err)
		}
	}
}

// Process level: --rate=1000000 starts, a limited server answers 429 over
// real HTTP (HEAD included) and still exits 0 on SIGTERM with empty output.
func TestQARateProcessLimitedThenSIGTERM(t *testing.T) {
	bin := buildBinary(t, "")
	dir := t.TempDir()
	data := filepath.Join(dir, "d.json")
	p := startServe(t, bin, "--rate=1", "--data="+data)

	// Real time passes between requests, so count instead of assuming. At
	// rate 1 a 429 shows up within a few quick requests.
	ok, limited := 0, 0
	for i := 0; i < 40 && limited == 0; i++ {
		code, ct, body := request(t, p.addr, "GET", "/greet", "name=P")
		switch code {
		case 200:
			ok++
		case 429:
			limited++
			if ct != "application/json" || body != "{\"error\":\"rate limited\"}\n" {
				t.Errorf("429 over HTTP: %q %q", ct, body)
			}
		default:
			t.Fatalf("status %d", code)
		}
	}
	if limited == 0 {
		t.Fatal("no 429 from --rate=1 after 40 requests")
	}
	if ok < 1 {
		t.Error("first request of a fresh client must be allowed")
	}
	// HEAD while limited: 429, empty body, Retry-After on the wire.
	req, _ := http.NewRequest("HEAD", "http://"+p.addr+"/greet", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 429 && resp.Header.Get("Retry-After") != "1" {
		t.Errorf("HEAD 429 Retry-After %q", resp.Header.Get("Retry-After"))
	}
	// /healthz always answers.
	for i := 0; i < 5; i++ {
		if code, _, body := request(t, p.addr, "GET", "/healthz", ""); code != 200 || body != "{\"status\":\"ok\"}\n" {
			t.Errorf("healthz while limited: %d %q", code, body)
		}
	}
	code, rest := stopServe(t, p, syscall.SIGTERM)
	if code != 0 || rest != "" || p.stderr.Len() != 0 {
		t.Errorf("SIGTERM: exit %d stdout %q stderr %q", code, rest, p.stderr.String())
	}
	b, err := os.ReadFile(data)
	want := fmt.Sprintf("{\"counts\":{\"P\":%d}}", ok)
	if err != nil || strings.TrimSpace(string(b)) != want {
		t.Errorf("data file %q (%v), want %s: 429s must not be persisted", b, err, want)
	}
}

func TestQARateProcessMaxRateStarts(t *testing.T) {
	bin := buildBinary(t, "")
	p := startServe(t, bin, "--rate=1000000")
	for i := 0; i < 200; i++ {
		if code, _, _ := request(t, p.addr, "GET", "/greet", ""); code != 200 {
			t.Fatalf("request %d: %d at --rate=1000000", i, code)
		}
	}
	if code, _ := stopServe(t, p, syscall.SIGTERM); code != 0 {
		t.Errorf("exit %d", code)
	}
}
