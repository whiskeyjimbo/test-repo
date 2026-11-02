package main

import "testing"

const wantUsage = `Usage: test-repo [--help] [--version]

Prints a greeting.

Options:
  --help     Show this message and exit
  --version  Print the version and exit
`

func TestHelpFlag(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--help"}, wantUsage)
	check(t, bin, []string{"foo", "--help"}, wantUsage)
}

func TestHelpBeforeVersionWins(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--help", "--version"}, wantUsage)
	check(t, bin, []string{"--version", "--help"}, "dev\n")
}

func TestHelpNearMissArgsGreet(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range []string{"-h", "-help", "help", "--HELP", "--help=1", "--foo", "foo"} {
		check(t, bin, []string{a}, "Hello, world\n")
	}
}
