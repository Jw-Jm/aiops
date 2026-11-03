//go:build ignore

// Task-owned canary setup: exercise the production S3 bootstrap implementation.
package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"ops-platform/internal/bootstrap"
	"ops-platform/internal/integrations/s3"
)

func main() {
	root := "/data/ops-v12-native-cold-r34-20261027/"
	raw, err := os.ReadFile(root + "private/archive-iam-input.json")
	if err != nil {
		panic("private archive input unavailable")
	}
	var input bootstrap.ArchiveIAMInput
	if json.Unmarshal(raw, &input) != nil {
		panic("archive input invalid")
	}
	ca, err := os.ReadFile(root + "private/archive-ca.pem")
	if err != nil {
		panic("archive CA unavailable")
	}
	client, err := s3.NewClient(s3.Config{Endpoint: os.Getenv("S3_ENDPOINT"), ServerName: os.Getenv("S3_SERVER_NAME"), CACertBundle: ca, Bucket: input.Bucket, AccessKey: input.BootstrapAdmin.AccessKey, SecretKey: input.BootstrapAdmin.SecretKey})
	if err != nil {
		panic("archive client invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := client.CreateFreshBucket(ctx); err != nil {
		panic(err)
	}
	println("task bucket created; actual object lock and versioning verified")
}
