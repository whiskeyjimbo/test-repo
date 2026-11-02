package main

import "testing"

func TestGreeting(t *testing.T) {
        got := Greeting("world")
        want := "Hello, world!"
        if got != want {
                t.Errorf("Greeting(%q) = %q, want %q", "world", got, want)
        }
}
