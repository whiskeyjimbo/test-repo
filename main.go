package main

import "fmt"

// Greeting returns the message the program prints.
func Greeting(name string) string {
        return "Hello, " + name
}

func main() {
        fmt.Println(Greeting("world"))
}

main_test.go
package main

import "testing"

func TestGreeting(t *testing.T) {
        got := Greeting("world")
        want := "Hello, world!"
        if got != want {
                t.Errorf("Greeting(%q) = %q, want %q", "world", got, want)
        }
}
