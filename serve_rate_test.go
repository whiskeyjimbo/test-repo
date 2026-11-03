package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeClock is a settable clock for the limiter; tests never sleep.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newRateHandler(rate int) (http.Handler, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	return limitHandler(newHandler(), newLimiter(rate, clk.now)), clk
}

// rdo sends one request from remote (host:port) and returns the recorder.
func rdo(h http.Handler, remote, method, target string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remote
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const (
	c1 = "192.0.2.1:5000"
	c2 = "192.0.2.2:5000"
	c6 = "[2001:db8::1]:5000"
)

const limitedBody = "{\"error\":\"rate limited\"}\n"

func wantLimited(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != 429 || rec.Body.String() != limitedBody || rec.Header().Get("Retry-After") != "1" ||
		rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("%s: got %d %q Retry-After=%q, want 429 %q Retry-After=1", what, rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"), limitedBody)
	}
}

// allowedBurst counts requests accepted from remote at the current instant.
func allowedBurst(h http.Handler, remote string, max int) int {
	n := 0
	for i := 0; i < max; i++ {
		if rdo(h, remote, "GET", "/greet?name=B").Code == 200 {
			n++
		}
	}
	return n
}

func TestRateScenario1(t *testing.T) {
	h, _ := newRateHandler(3)
	for i := 0; i < 3; i++ {
		if rec := rdo(h, c1, "GET", "/greet?name=Ann"); rec.Code != 200 {
			t.Fatalf("request %d: %d", i+1, rec.Code)
		}
	}
	wantLimited(t, rdo(h, c1, "GET", "/greet?name=Ann"), "4th")
	// /stats is limited too, so look from another client.
	if got := rdo(h, c2, "GET", "/stats").Body.String(); got != "{\"counts\":{\"Ann\":3}}\n" {
		t.Errorf("stats %q: the limited request was counted", got)
	}
}

func TestRateScenario2Refill(t *testing.T) {
	h, clk := newRateHandler(3)
	if n := allowedBurst(h, c1, 10); n != 3 {
		t.Fatalf("initial burst %d", n)
	}
	clk.advance(time.Second / 3)
	if n := allowedBurst(h, c1, 10); n != 1 {
		t.Errorf("after 1/3 s: %d allowed, want 1", n)
	}
	clk.advance(time.Second)
	if n := allowedBurst(h, c1, 10); n != 3 {
		t.Errorf("after 1 s: %d allowed, want 3", n)
	}
	clk.advance(time.Hour)
	if n := allowedBurst(h, c1, 10); n != 3 {
		t.Errorf("after an hour: %d allowed, want the cap of 3", n)
	}
}

func TestRatePartialRefillCarries(t *testing.T) {
	h, clk := newRateHandler(2)
	allowedBurst(h, c1, 10)
	clk.advance(300 * time.Millisecond)
	if n := allowedBurst(h, c1, 10); n != 0 {
		t.Fatalf("after 0.3 s at rate 2: %d", n)
	}
	clk.advance(300 * time.Millisecond) // 0.6 s total: one token
	if n := allowedBurst(h, c1, 10); n != 1 {
		t.Errorf("after 0.6 s total: %d, want 1", n)
	}
}

func TestRateScenario3PerClient(t *testing.T) {
	h, _ := newRateHandler(3)
	allowedBurst(h, c1, 3)
	wantLimited(t, rdo(h, c1, "GET", "/greet"), "exhausted client")
	for _, c := range []string{c2, c6} {
		if rec := rdo(h, c, "GET", "/greet"); rec.Code != 200 {
			t.Errorf("%s got %d", c, rec.Code)
		}
	}
	for i := 0; i < 5; i++ {
		if rec := rdo(h, c1, "GET", "/healthz"); rec.Code != 200 || rec.Body.String() != "{\"status\":\"ok\"}\n" {
			t.Errorf("healthz while exhausted: %d %q", rec.Code, rec.Body.String())
		}
	}
	wantLimited(t, rdo(h, c1, "GET", "/greet", "X-Forwarded-For", "192.0.2.9"), "forwarded")
	wantLimited(t, rdo(h, c1, "GET", "/stats"), "/stats")
}

func TestRateXForwardedForIgnored(t *testing.T) {
	h, _ := newRateHandler(1)
	// Different forwarded addresses must not give a client fresh buckets,
	// and a forwarded address must not drain another client's bucket.
	rdo(h, c1, "GET", "/greet", "X-Forwarded-For", "192.0.2.50")
	wantLimited(t, rdo(h, c1, "GET", "/greet", "X-Forwarded-For", "192.0.2.51"), "other forwarded value")
	if rec := rdo(h, c2, "GET", "/greet", "X-Forwarded-For", "192.0.2.1"); rec.Code != 200 {
		t.Errorf("c2 claiming to be c1 got %d", rec.Code)
	}
}

func TestRatePortDropped(t *testing.T) {
	h, _ := newRateHandler(1)
	rdo(h, "192.0.2.1:1111", "GET", "/greet")
	wantLimited(t, rdo(h, "192.0.2.1:2222", "GET", "/greet"), "same host, other port")
}

func TestRateHeadTakesToken(t *testing.T) {
	h, _ := newRateHandler(2)
	if rec := rdo(h, c1, "HEAD", "/greet"); rec.Code != 200 {
		t.Fatalf("HEAD %d", rec.Code)
	}
	if rec := rdo(h, c1, "HEAD", "/stats"); rec.Code != 200 {
		t.Fatalf("HEAD stats %d", rec.Code)
	}
	if rec := rdo(h, c1, "HEAD", "/greet"); rec.Code != 429 {
		t.Errorf("third HEAD %d, want 429", rec.Code)
	}
	if rec := rdo(h, c1, "GET", "/greet"); rec.Code != 429 {
		t.Errorf("GET after HEADs %d, want 429", rec.Code)
	}
}

func TestRateUnknownPathsAndBadRequestsTakeTokens(t *testing.T) {
	h, _ := newRateHandler(2)
	if rec := rdo(h, c1, "GET", "/nope"); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
	if rec := rdo(h, c1, "GET", "/greet?name=%ff"); rec.Code != 400 {
		t.Fatalf("%d", rec.Code)
	}
	wantLimited(t, rdo(h, c1, "GET", "/nope"), "third")
}

func TestRateDefaultIsTen(t *testing.T) {
	cfg, err := parseServeArgs([]string{"--port=0"})
	if err != nil || cfg.rate != 10 {
		t.Fatalf("default rate %d, %v", cfg.rate, err)
	}
	h, _ := newRateHandler(cfg.rate)
	for i := 1; i <= 10; i++ {
		if rec := rdo(h, c1, "GET", "/greet"); rec.Code != 200 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	wantLimited(t, rdo(h, c1, "GET", "/greet"), "11th")
}

func TestRateHealthzNeverTakesToken(t *testing.T) {
	h, _ := newRateHandler(1)
	for i := 0; i < 50; i++ {
		if rec := rdo(h, c1, "GET", "/healthz"); rec.Code != 200 {
			t.Fatalf("healthz %d: %d", i, rec.Code)
		}
	}
	if rec := rdo(h, c1, "GET", "/greet"); rec.Code != 200 {
		t.Errorf("greet after healthz: %d", rec.Code)
	}
}

func TestRateArgs(t *testing.T) {
	const bad = "--rate: want an integer 1-1000000"
	cases := []struct {
		args []string
		want string // "" = valid
		rate int
	}{
		{[]string{"--port=0", "--rate=5"}, "", 5},
		{[]string{"--rate=1000000", "--port=0"}, "", 1000000},
		{[]string{"--port=0", "--rate=1"}, "", 1},
		{[]string{"--port=0", "--rate="}, bad, 0},
		{[]string{"--port=0", "--rate=0"}, bad, 0},
		{[]string{"--port=0", "--rate=1000001"}, bad, 0},
		{[]string{"--port=0", "--rate=x"}, bad, 0},
		{[]string{"--port=0", "--rate=-1"}, bad, 0},
		{[]string{"--port=0", "--rate=+1"}, bad, 0},
		{[]string{"--port=0", "--rate= 1"}, bad, 0},
		{[]string{"--port=0", "--rate=1.5"}, bad, 0},
		{[]string{"--port=0", "--rate=99999999999999999999"}, bad, 0},
		{[]string{"--port=0", "--rate=5", "--rate=5"}, "repeated flag", 0},
		{[]string{"--port=0", "--rate=5", "--rate=x"}, "repeated flag", 0},
		{[]string{"--port=0", "--rate", "5"}, "unknown argument", 0},
	}
	for _, c := range cases {
		cfg, err := parseServeArgs(c.args)
		switch {
		case c.want == "" && (err != nil || cfg.rate != c.rate):
			t.Errorf("%q: rate %d err %v, want rate %d", c.args, cfg.rate, err, c.rate)
		case c.want != "" && (err == nil || err.Error() != c.want):
			t.Errorf("%q: err %v, want %q", c.args, err, c.want)
		}
	}
}

func TestRateArgsProcess(t *testing.T) {
	bin := buildBinary(t, "")
	for _, v := range []string{"", "0", "1000001", "x"} {
		out, errOut, code := runBinary(t, bin, "serve", "--port=0", "--rate="+v)
		if code != 1 || out != "" || errOut != "test-repo: serve: --rate: want an integer 1-1000000\n" {
			t.Errorf("--rate=%q: out=%q err=%q exit=%d", v, out, errOut, code)
		}
	}
	out, errOut, code := runBinary(t, bin, "serve", "--port=0", "--rate=1", "--rate=1")
	if code != 1 || out != "" || errOut != "test-repo: serve: repeated flag\n" {
		t.Errorf("repeated: out=%q err=%q exit=%d", out, errOut, code)
	}
	check(t, bin, []string{"serve", "--rate=x", "--help"}, wantServeUsage)
}

func TestRateProcessStartsWithRate(t *testing.T) {
	bin := buildBinary(t, "")
	p := startServe(t, bin, "--rate=5")
	if code, _, body := request(t, p.addr, "GET", "/healthz", ""); code != 200 || body != "{\"status\":\"ok\"}\n" {
		t.Errorf("healthz %d %q", code, body)
	}
}
