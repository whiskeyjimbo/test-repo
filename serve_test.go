package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// wantServeUsage is the serve usage line. Every serve help assertion uses it.
const wantServeUsage = "Usage: test-repo serve --port=N\n"

// bs is a backslash, kept out of literals so JSON escapes stay readable.
const bs = "\x5c"

func TestParseServeArgsValid(t *testing.T) {
	for _, tc := range []struct {
		args []string
		port int
	}{
		{[]string{"--port=0"}, 0},
		{[]string{"--port=80"}, 80},
		{[]string{"--port=65535"}, 65535},
	} {
		cfg, err := parseServeArgs(tc.args)
		if err != nil || cfg.port != tc.port {
			t.Errorf("%q: got %+v, %v; want port %d", tc.args, cfg, err, tc.port)
		}
	}
}

func TestParseServeArgsErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "--port is required"},
		{[]string{"--port="}, "--port: want an integer 0-65535"},
		{[]string{"--port=x"}, "--port: want an integer 0-65535"},
		{[]string{"--port=-1"}, "--port: want an integer 0-65535"},
		{[]string{"--port=65536"}, "--port: want an integer 0-65535"},
		{[]string{"--port=+80"}, "--port: want an integer 0-65535"},
		{[]string{"--port= 80"}, "--port: want an integer 0-65535"},
		{[]string{"--port=0x50"}, "--port: want an integer 0-65535"},
		{[]string{"--port=99999999999999999999"}, "--port: want an integer 0-65535"},
		{[]string{"--port", "80"}, "unknown argument"},
		{[]string{"--port=0", "--port=0"}, "repeated flag"},
		{[]string{"--port=0", "--bogus"}, "unknown argument"},
		{[]string{"--color=1", "--port=0"}, "unknown argument"},
		{[]string{"--PORT=0"}, "unknown argument"},
		{[]string{""}, "unknown argument"},
	} {
		_, err := parseServeArgs(tc.args)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%q: got %v; want %q", tc.args, err, tc.want)
		}
	}
}

func TestParseServeArgsHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--port=x", "--help"}, {"--bogus", "--help"}} {
		if _, err := parseServeArgs(args); err != errServeHelp {
			t.Errorf("%q: got %v; want errServeHelp", args, err)
		}
	}
}

func TestRunServeHelpAndErrors(t *testing.T) {
	var o, e bytes.Buffer
	if code := runServe([]string{"--help"}, &o, &e); code != 0 || o.String() != wantServeUsage || e.Len() != 0 {
		t.Errorf("--help: exit=%d stdout=%q stderr=%q", code, o.String(), e.String())
	}
	o.Reset()
	e.Reset()
	code := runServe([]string{"--port=secret123"}, &o, &e)
	if code != 1 || o.Len() != 0 || e.String() != "test-repo: serve: --port: want an integer 0-65535\n" {
		t.Errorf("bad port: exit=%d stdout=%q stderr=%q", code, o.String(), e.String())
	}
}

// do sends one request with an unparsed raw query to h.
func do(h http.Handler, method, path, rawQuery string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/", nil)
	req.URL.Path = path
	req.URL.RawQuery = rawQuery
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerResponses(t *testing.T) {
	h := newHandler()
	for _, tc := range []struct {
		method, path, query string
		code                int
		body                string
	}{
		{"GET", "/healthz", "", 200, `{"status":"ok"}`},
		{"GET", "/greet", "name=Ann", 200, `{"greeting":"Hello, Ann"}`},
		{"GET", "/greet", "", 200, `{"greeting":"Hello, world"}`},
		{"GET", "/greet", "name=", 200, `{"greeting":"Hello, world"}`},
		{"GET", "/greet", "name=Zo%C3%AB", 200, `{"greeting":"Hello, Zoë"}`},
		{"GET", "/greet", "name=a&name=b", 200, `{"greeting":"Hello, a"}`},
		{"GET", "/greet", "other=1&name=Bo+b", 200, `{"greeting":"Hello, Bo b"}`},
		{"GET", "/greet", "name=" + strings.Repeat("a", 256), 200, `{"greeting":"Hello, ` + strings.Repeat("a", 256) + `"}`},
		{"GET", "/greet", "name=%3Cb%3E%26", 200, `{"greeting":"Hello, ` + bs + `u003cb` + bs + `u003e` + bs + `u0026"}`},
		{"GET", "/greet", "name=%22" + bs, 200, `{"greeting":"Hello, ` + bs + `"` + bs + bs + `"}`},
		{"GET", "/greet", "name=%zz", 400, `{"error":"invalid query"}`},
		{"GET", "/greet", "name=a;b", 400, `{"error":"invalid query"}`},
		{"GET", "/greet", "name=" + strings.Repeat("a", 257), 400, `{"error":"invalid name"}`},
		{"GET", "/greet", "name=%FF", 400, `{"error":"invalid name"}`},
		{"GET", "/greet", "name=ok&name=%FF", 200, `{"greeting":"Hello, ok"}`},
	} {
		rec := do(h, tc.method, tc.path, tc.query)
		if rec.Code != tc.code || rec.Body.String() != tc.body+"\n" || rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s %s?%.40s: got %d %q (Content-Type %q); want %d %q",
				tc.method, tc.path, tc.query, rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"), tc.code, tc.body+"\n")
		}
	}
}

func TestHandlerNotFoundAndMethod(t *testing.T) {
	h := newHandler()
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/nope", 404},
		{"GET", "/", 404},
		{"GET", "/greet/x", 404},
		{"GET", "/healthz/", 404},
		{"POST", "/greet", 405},
		{"PUT", "/healthz", 405},
		{"DELETE", "/greet", 405},
	} {
		if rec := do(h, tc.method, tc.path, ""); rec.Code != tc.code {
			t.Errorf("%s %s: got %d; want %d", tc.method, tc.path, rec.Code, tc.code)
		}
	}
}

// TestServeOnShutdownTimeout checks that a request outliving the shutdown
// limit makes serveOn report the error and exit 1.
func TestServeOnShutdownTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})
	stop := make(chan os.Signal, 1)
	var o, e bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- serveOn(ln, h, &o, &e, stop, 50*time.Millisecond) }()
	go http.Get("http://" + ln.Addr().String() + "/")
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("request never reached the handler")
	}
	stop <- os.Interrupt
	select {
	case code := <-done:
		want := "test-repo: serve: shutdown: context deadline exceeded\n"
		if code != 1 || e.String() != want {
			t.Errorf("exit=%d stderr=%q; want exit=1 stderr=%q", code, e.String(), want)
		}
		if o.String() != "listening on "+ln.Addr().String()+"\n" {
			t.Errorf("stdout=%q", o.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveOn did not return")
	}
}
