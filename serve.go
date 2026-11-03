package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const serveUsage = "Usage: test-repo serve --port=N [--data=FILE]\n"

// maxNameBytes is the longest name /greet accepts, in bytes after decoding.
const maxNameBytes = 256

// shutdownTimeout bounds how long in-flight requests get after a signal.
const shutdownTimeout = 5 * time.Second

// serveConfig is the result of parsing the serve arguments.
type serveConfig struct {
	port int
	data string // counts file; "" keeps counts in memory only
}

// errServeHelp is returned by parseServeArgs when "--help" is given.
var errServeHelp = errors.New("help")

// parseServeArgs parses the arguments after "serve". Arguments are checked
// in order and the first problem is returned. Errors never echo a value.
func parseServeArgs(args []string) (serveConfig, error) {
	for _, arg := range args {
		if arg == "--help" {
			return serveConfig{}, errServeHelp
		}
	}
	var cfg serveConfig
	seenPort, seenData := false, false
	for _, arg := range args {
		if v, ok := strings.CutPrefix(arg, "--port="); ok {
			if seenPort {
				return serveConfig{}, errors.New("repeated flag")
			}
			seenPort = true
			p, ok := parsePort(v)
			if !ok {
				return serveConfig{}, errors.New("--port: want an integer 0-65535")
			}
			cfg.port = p
		} else if v, ok := strings.CutPrefix(arg, "--data="); ok {
			if seenData {
				return serveConfig{}, errors.New("repeated flag")
			}
			seenData = true
			if v == "" {
				return serveConfig{}, errors.New("--data: empty value")
			}
			cfg.data = v
		} else {
			return serveConfig{}, errors.New("unknown argument")
		}
	}
	if !seenPort {
		return serveConfig{}, errors.New("--port is required")
	}
	return cfg, nil
}

// parsePort accepts only ASCII decimal digits with a value of 0-65535.
func parsePort(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	p, err := strconv.Atoi(s)
	if err != nil || p > 65535 {
		return 0, false
	}
	return p, true
}

// runServe runs "test-repo serve" with args and returns the exit code.
func runServe(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseServeArgs(args)
	if errors.Is(err, errServeHelp) {
		fmt.Fprint(stdout, serveUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "test-repo: serve: %v\n", err)
		return 1
	}
	st, err := loadStore(cfg.data)
	if err != nil {
		fmt.Fprintf(stderr, "test-repo: serve: data %s: %v\n", cfg.data, err)
		return 1
	}
	// Catch signals before announcing the port, so a signal sent as soon
	// as the "listening on" line appears is never fatal.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(stop)
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.port)))
	if err != nil {
		fmt.Fprintf(stderr, "test-repo: serve: %v\n", err)
		return 1
	}
	return serveOn(ln, newHandlerWith(st), stdout, stderr, stop, shutdownTimeout)
}

// serveOn announces ln, serves h on it until stop fires, then shuts down,
// giving in-flight requests up to timeout. It returns the exit code.
func serveOn(ln net.Listener, h http.Handler, stdout, stderr io.Writer, stop <-chan os.Signal, timeout time.Duration) int {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(stdout, "listening on %s\n", ln.Addr())
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-stop:
	case err := <-errc:
		fmt.Fprintf(stderr, "test-repo: serve: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		fmt.Fprintf(stderr, "test-repo: serve: shutdown: %v\n", err)
		return 1
	}
	return 0
}

// newHandler returns the server's routes. Unknown paths get 404 and wrong
// methods on a known path get 405, both from http.ServeMux.
func newHandler() http.Handler {
	return newHandlerWith(&store{counts: map[string]int64{}})
}

// newHandlerWith is newHandler counting greetings in st.
func newHandlerWith(st *store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /greet", func(w http.ResponseWriter, r *http.Request) { handleGreet(st, w, r) })
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"counts": st.snapshot()})
	})
	return mux
}

func handleGreet(st *store, w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid query"})
		return
	}
	name := q.Get("name")
	if len(name) > maxNameBytes || !utf8.ValidString(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
		return
	}
	if name == "" {
		name = "world"
	}
	if err := st.add(name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storage failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"greeting": Greeting(name)})
}

// writeJSON sends v as one compact line with json.Encoder defaults.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
