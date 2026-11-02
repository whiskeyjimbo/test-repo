package main

import (
	"fmt"
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

func main() {
	name := ""
	shout := false
	for _, arg := range os.Args[1:] {
		if arg == "--help" {
			fmt.Print(usage)
			return
		}
		if arg == "--version" {
			fmt.Println(version)
			return
		}
		if arg == "--shout" {
			shout = true
		}
		if v, ok := strings.CutPrefix(arg, "--name="); ok && name == "" {
			name = v
		}
	}
	if name == "" {
		name = "world"
	}
	out := Greeting(name)
	if shout {
		out = strings.ToUpper(out)
	}
	fmt.Println(out)
}
