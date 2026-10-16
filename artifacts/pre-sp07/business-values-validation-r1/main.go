//go:build ignore

// Historical pre-SP07 review probe; excluded from module builds and go vet ./...
package main

import (
	"fmt"
	"ops-platform/internal/bundle"
	"os"
)

func main() {
	f, e := os.Open(os.Args[1])
	if e != nil {
		panic(e)
	}
	defer f.Close()
	_, e = bundle.ReadBusinessValues(f)
	if e != nil {
		panic(e)
	}
	fmt.Println("public product BusinessValues parser validated complete explicit bound values; no environment mutation")
}
