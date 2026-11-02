package main

import "testing"

func TestNameHappyPath(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--name=Ann"}, "Hello, Ann\n")
	check(t, bin, []string{"foo", "--name=Ann"}, "Hello, Ann\n")
	check(t, bin, []string{"--name=Ann", "foo"}, "Hello, Ann\n")
}

func TestNameValueIsLiteral(t *testing.T) {
	bin := buildBinary(t, "")
	for _, v := range []string{"Ann Lee", "a=b", "%s", "--help", "--version", " ", "a\xffb"} {
		check(t, bin, []string{"--name=" + v}, "Hello, "+v+"\n")
	}
}

func TestNameHelpVersionStillWin(t *testing.T) {
	bin := buildBinary(t, "")
	for _, args := range [][]string{{"--name=Ann", "--help"}, {"--help", "--name=Ann"}} {
		check(t, bin, args, usage)
	}
	for _, args := range [][]string{
		{"--name=Ann", "--version"},
		{"--version", "--name=Ann"},
		{"--name=--help", "--version"},
	} {
		check(t, bin, args, "dev\n")
	}
}

func TestNameRepeated(t *testing.T) {
	bin := buildBinary(t, "")
	check(t, bin, []string{"--name=Ann", "--name=Bob"}, "Hello, Ann\n")
	check(t, bin, []string{"--name=", "--name=Bob"}, "Hello, Bob\n")
}

func TestNameNearMissesGreetWorld(t *testing.T) {
	bin := buildBinary(t, "")
	for _, a := range []string{"--name=", "--name", "-name=Ann", "--NAME=Ann", "--names=Ann", " --name=Ann", "Ann"} {
		check(t, bin, []string{a}, "Hello, world\n")
	}
	check(t, bin, []string{"--name", "Ann"}, "Hello, world\n")
}
