package main

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// waitLimit is an upper bound on waiting for the server; tests never sleep.
const waitLimit = 10 * time.Second

type serveProc struct {
	cmd    *exec.Cmd
	addr   string // host:port from the "listening on" line
	stdout *bufio.Reader
	stderr *bytes.Buffer // read it only after exited is closed
	exited chan struct{}
}

// startServe starts "bin serve --port=0 extra..." and waits for its
// "listening on" line. Every process-level serve test starts here.
func startServe(t *testing.T, bin string, extra ...string) *serveProc {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"serve", "--port=0"}, extra...)...)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &serveProc{cmd: cmd, stdout: bufio.NewReader(r), stderr: &bytes.Buffer{}, exited: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = w, p.stderr
	err = cmd.Start()
	w.Close()
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	go func() {
		cmd.Wait()
		close(p.exited)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-p.exited
		r.Close()
	})
	lines := make(chan string, 1)
	go func() {
		line, _ := p.stdout.ReadString('\n')
		lines <- line
	}()
	var line string
	select {
	case line = <-lines:
	case <-time.After(waitLimit):
		t.Fatal("no listening line")
	}
	addr, ok := strings.CutPrefix(line, "listening on ")
	addr, nl := strings.CutSuffix(addr, "\n")
	host, port, err := net.SplitHostPort(addr)
	if !ok || !nl || err != nil || host != "127.0.0.1" {
		cmd.Process.Kill()
		<-p.exited
		t.Fatalf("first stdout line %q; want \"listening on 127.0.0.1:<port>\\n\" (stderr %q)", line, p.stderr.String())
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		t.Fatalf("bad port in %q", line)
	}
	p.addr = addr
	return p
}

// stopServe sends sig, waits for the exit and returns the exit code and
// whatever stdout held after the listening line.
func stopServe(t *testing.T, p *serveProc, sig os.Signal) (int, string) {
	t.Helper()
	rest := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(p.stdout)
		rest <- string(b)
	}()
	if err := p.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.exited:
	case <-time.After(waitLimit):
		t.Fatal("server did not exit")
	}
	select {
	case s := <-rest:
		return p.cmd.ProcessState.ExitCode(), s
	case <-time.After(waitLimit):
		t.Fatal("stdout not closed after exit")
	}
	return 0, ""
}

// request sends method to the server with rawQuery sent as is.
func request(t *testing.T, addr, method, path, rawQuery string) (int, string, string) {
	t.Helper()
	u := &url.URL{Scheme: "http", Host: addr, Path: path, RawQuery: rawQuery}
	req, err := http.NewRequest(method, u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: waitLimit}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s?%.40s: %v", method, path, rawQuery, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
}

// Scenario 1.1
func TestServeHappyPath(t *testing.T) {
	p := startServe(t, buildBinary(t, ""))
	for _, tc := range []struct{ path, query, body string }{
		{"/healthz", "", `{"status":"ok"}`},
		{"/greet", "name=Ann", `{"greeting":"Hello, Ann"}`},
		{"/greet", "", `{"greeting":"Hello, world"}`},
		{"/greet", "name=", `{"greeting":"Hello, world"}`},
		{"/greet", "name=Zo%C3%AB", `{"greeting":"Hello, Zoë"}`},
		{"/greet", "name=a&name=b", `{"greeting":"Hello, a"}`},
	} {
		code, ct, body := request(t, p.addr, "GET", tc.path, tc.query)
		if code != 200 || ct != "application/json" || body != tc.body+"\n" {
			t.Errorf("GET %s?%s: got %d %q %q; want 200 application/json %q", tc.path, tc.query, code, ct, body, tc.body+"\n")
		}
	}
}

// Scenario 1.3
func TestServeBadRequests(t *testing.T) {
	p := startServe(t, buildBinary(t, ""))
	for _, tc := range []struct {
		method, path, query string
		code                int
		body                string
	}{
		{"GET", "/greet", "name=%zz", 400, `{"error":"invalid query"}`},
		{"GET", "/greet", "name=" + strings.Repeat("x", 257), 400, `{"error":"invalid name"}`},
		{"GET", "/greet", "name=%FF", 400, `{"error":"invalid name"}`},
		{"GET", "/greet", "name=" + strings.Repeat("x", 256), 200, `{"greeting":"Hello, ` + strings.Repeat("x", 256) + `"}`},
	} {
		code, ct, body := request(t, p.addr, tc.method, tc.path, tc.query)
		if code != tc.code || ct != "application/json" || body != tc.body+"\n" {
			t.Errorf("%s %s?%.40s: got %d %q %q; want %d %q", tc.method, tc.path, tc.query, code, ct, body, tc.code, tc.body+"\n")
		}
	}
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/nope", 404},
		{"POST", "/greet", 405},
	} {
		if code, _, _ := request(t, p.addr, tc.method, tc.path, ""); code != tc.code {
			t.Errorf("%s %s: got %d; want %d", tc.method, tc.path, code, tc.code)
		}
	}
}

// Scenario 1.2
func TestServeCleanShutdown(t *testing.T) {
	bin := buildBinary(t, "")
	for _, sig := range []os.Signal{syscall.SIGTERM, os.Interrupt} {
		p := startServe(t, bin)
		if code, _, _ := request(t, p.addr, "GET", "/healthz", ""); code != 200 {
			t.Fatalf("%v: healthz before signal: %d", sig, code)
		}
		code, rest := stopServe(t, p, sig)
		if code != 0 || rest != "" || p.stderr.Len() != 0 {
			t.Errorf("%v: exit=%d stdout after listening=%q stderr=%q; want 0, \"\", \"\"", sig, code, rest, p.stderr.String())
		}
		if c, err := net.DialTimeout("tcp", p.addr, waitLimit); err == nil {
			c.Close()
			t.Errorf("%v: %s still accepts connections after exit", sig, p.addr)
		}
	}
}

// Decision 2: only 127.0.0.1 is served, not other loopback or IPv6 addresses.
func TestServeLoopbackOnly(t *testing.T) {
	p := startServe(t, buildBinary(t, ""))
	_, port, _ := net.SplitHostPort(p.addr)
	for _, host := range []string{"127.0.0.2", "::1"} {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), waitLimit); err == nil {
			c.Close()
			t.Errorf("%s accepted a connection on port %s; want 127.0.0.1 only", host, port)
		}
	}
}

// Scenario 1.4
func TestServeBadArgs(t *testing.T) {
	bin := buildBinary(t, "")
	const portErr = "test-repo: serve: --port: want an integer 0-65535\n"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"serve"}, "test-repo: serve: --port is required\n"},
		{[]string{"serve", "--port="}, portErr},
		{[]string{"serve", "--port=x"}, portErr},
		{[]string{"serve", "--port=-1"}, portErr},
		{[]string{"serve", "--port=65536"}, portErr},
		{[]string{"serve", "--port=+80"}, portErr},
		{[]string{"serve", "--port", "80"}, "test-repo: serve: unknown argument\n"},
		{[]string{"serve", "--port=0", "--port=0"}, "test-repo: serve: repeated flag\n"},
		{[]string{"serve", "--port=0", "--bogus"}, "test-repo: serve: unknown argument\n"},
	} {
		out, errOut, code := runBinary(t, bin, tc.args...)
		if out != "" || errOut != tc.want || code != 1 {
			t.Errorf("%q: stdout=%q stderr=%q exit=%d; want \"\", %q, 1", tc.args, out, errOut, code, tc.want)
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	out, errOut, code := runBinary(t, bin, "serve", "--port="+port)
	if out != "" || code != 1 || !strings.HasPrefix(errOut, "test-repo: serve: ") ||
		!strings.HasSuffix(errOut, "\n") || strings.Count(errOut, "\n") != 1 {
		t.Errorf("port in use: stdout=%q stderr=%q exit=%d; want one stderr line, exit 1", out, errOut, code)
	}
}

// Scenario 1.5
func TestServeHelp(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"serve", "--help"}, wantServeUsage)
	check(t, bin, []string{"serve", "--port=x", "--help"}, wantServeUsage)
}

// Scenario 1.6: "serve" is a subcommand only as the very first argument.
func TestServeOnlyAsFirstArg(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--name=Ann", "serve"}, "Hello, Ann\n")
	check(t, bin, []string{"x", "serve"}, "Hello, world\n")
	check(t, bin, []string{"SERVE"}, "Hello, world\n")
	check(t, bin, []string{"--help", "serve"}, wantUsage)
	check(t, bin, []string{"--version-json", "serve"}, `{"version":"dev"}`+"\n")
	check(t, bin, nil, "Hello, world\n")
}
