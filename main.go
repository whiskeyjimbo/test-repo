package main

import (
	"fmt"
	"os"
)

// version is overridden at build time with -ldflags "-X main.version=<v>".
var version = "dev"

// Greeting returns the message the program prints.
func Greeting(name string) string {
	return "Hello, " + name
}

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" {
			fmt.Println(version)
			return
		}
	}
	fmt.Println(Greeting("world"))
}
