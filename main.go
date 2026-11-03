package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// version is overridden at build time with -ldflags "-X main.version=<v>".
var version = "dev"

const usage = `Usage: test-repo [--help] [--version]

Prints a greeting.

Options:
  --help     Show this message and exit
  --version  Print the version and exit
`

// Greeting returns the message the program prints.
func Greeting(name string) string {
        return "Hello, " + name
}

// loadConfig reads and validates the config file at path and returns the
// greeting word it sets ("Hello" when unset or empty) and its name value
// ("" when unset or empty). Errors never echo the file's contents.
func loadConfig(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return "", "", pe.Err
		}
		return "", "", err
	}
	greeting, cfgName := "", ""
	seenGreeting, seenName := false, false
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return "", "", fmt.Errorf("line %d: expected key=value", i+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return "", "", fmt.Errorf("line %d: empty key", i+1)
		}
		switch key {
		case "greeting":
			if seenGreeting {
				return "", "", fmt.Errorf("line %d: repeated key", i+1)
			}
			seenGreeting = true
			greeting = strings.TrimSpace(val)
		case "name":
			if seenName {
				return "", "", fmt.Errorf("line %d: repeated key", i+1)
			}
			seenName = true
			cfgName = strings.TrimSpace(val)
		default:
			return "", "", fmt.Errorf("line %d: unknown key", i+1)
		}
	}
	if greeting == "" {
		greeting = "Hello"
	}
	return greeting, cfgName, nil
}

func main() {
	name := ""
	config := ""
	lang := ""
	shout := false
	quiet := false
	for _, arg := range os.Args[1:] {
		if arg == "--help" {
			fmt.Print(usage)
			return
		}
		if arg == "--version" {
			fmt.Println(version)
			return
		}
		if arg == "--version-json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			enc.Encode(map[string]string{"version": version})
			return
		}
		if arg == "--quiet" {
			quiet = true
		}
		if arg == "--shout" {
			shout = true
		}
		if v, ok := strings.CutPrefix(arg, "--name="); ok && name == "" {
			name = v
		}
		if v, ok := strings.CutPrefix(arg, "--config="); ok && config == "" {
			config = v
		}
		if v, ok := strings.CutPrefix(arg, "--lang="); ok && lang == "" {
			lang = v
		}
	}
	if quiet {
		return
	}
	words := map[string]string{"en": "Hello", "es": "Hola"}
	if _, ok := words[lang]; lang != "" && !ok {
		fmt.Fprintln(os.Stderr, "test-repo: lang: unknown code, want en or es")
		os.Exit(1)
	}
	greeting := "Hello"
	if config != "" {
		g, n, err := loadConfig(config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "test-repo: config %s: %v\n", config, err)
			os.Exit(1)
		}
		greeting = g
		if name == "" {
			name = n
		}
	}
	if lang != "" {
		greeting = words[lang]
	}
	if name == "" {
		name = "world"
	}
	out := greeting + strings.TrimPrefix(Greeting(name), "Hello")
	if shout {
		out = strings.ToUpper(out)
	}
	fmt.Println(out)
}
