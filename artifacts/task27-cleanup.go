package main

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"ops-platform/internal/bundle"
	"ops-platform/internal/bundle/drivers"
	"ops-platform/internal/profile"
	"os"
)

func main() {
	b, e := os.ReadFile("artifacts/profiles/task-2.7-qualified-resolved.yaml")
	if e != nil {
		panic(e)
	}
	var p profile.ResolvedProfile
	if e = yaml.Unmarshal(b, &p); e != nil {
		panic(e)
	}
	for _, r := range []string{"ops-dependencies", "vmalert", "ops-platform"} {
		if e = bundle.CleanupRelease(context.Background(), p, r, drivers.Run); e != nil {
			panic(e)
		}
		fmt.Println("Cleaned owned release", r, "with PVCs excluded")
	}
}
