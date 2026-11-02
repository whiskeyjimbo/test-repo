package main

import "testing"

func TestShoutHappyPath(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range [][]string{
		{"--shout"}, {"foo", "--shout"}, {"--shout", "foo"},
		{"--shout", "--shout"}, {"--", "--shout"},
	} {
		check(t, bin, a, "HELLO, WORLD\n")
	}
}

func TestShoutWithName(t *testing.T) {
	bin := buildBinary(t, "")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--name=Ann", "--shout"}, "HELLO, ANN\n"},
		{[]string{"--shout", "--name=Ann"}, "HELLO, ANN\n"},
		{[]string{"--shout", "--name="}, "HELLO, WORLD\n"},
		{[]string{"--shout", "--name=Ann", "--name=Bob"}, "HELLO, ANN\n"},
		{[]string{"--name=%s", "--shout"}, "HELLO, %S\n"},
		{[]string{"--name=--shout", "--shout"}, "HELLO, --SHOUT\n"},
		{[]string{"--name=--shout"}, "Hello, --shout\n"},
	}
	for _, c := range cases {
		check(t, bin, c.args, c.want)
	}
}

func TestShoutHelpVersionWin(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range [][]string{{"--shout", "--help"}, {"--help", "--shout"}} {
		check(t, bin, a, usage)
	}
	for _, a := range [][]string{
		{"--shout", "--version"}, {"--version", "--shout"},
		{"--name=Ann", "--shout", "--version"},
	} {
		check(t, bin, a, "dev\n")
	}
}

func TestShoutNearMisses(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range []string{"--shout=1", "--shout=", "-shout", "-s", "--SHOUT", "--shouts", "shout", " --shout"} {
		check(t, bin, []string{a}, "Hello, world\n")
	}
}

func TestShoutNonASCII(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--name=é", "--shout"}, "HELLO, É\n")
	check(t, bin, []string{"--name=ß", "--shout"}, "HELLO, ß\n")
	check(t, bin, []string{"--name=a\xffb", "--shout"}, "HELLO, A\xef\xbf\xbdB\n")
	check(t, bin, []string{"--name=a\xffb"}, "Hello, a\xffb\n")
}

// The scan must not stop at --shout: later flags still apply.
func TestShoutScanContinues(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--shout", "--name=Ann"}, "HELLO, ANN\n")
	check(t, bin, []string{"--shout", "--version"}, "dev\n")
	check(t, bin, []string{"--shout", "--help"}, usage)
}
