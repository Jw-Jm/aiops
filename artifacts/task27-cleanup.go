//go:build ignore

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "Historical cleanup disabled. Use opsctl cleanup to review a plan before explicitly applying it.")
	os.Exit(1)
}
