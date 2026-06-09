package goproject

import "fmt"

// Run uses Add and Greet from helper.go (cross-file references).
func Run() {
	sum := Add(1, 2)
	msg := Greet("world")
	fmt.Println(sum, msg)
}
