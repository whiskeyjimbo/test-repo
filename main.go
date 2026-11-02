package main

import "fmt"

// Greeting returns the message the program prints.
func Greeting(name string) string {
        return "Hello, " + name
}

func main() {
        fmt.Println(Greeting("world"))
}
