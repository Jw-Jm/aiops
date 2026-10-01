package s3

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestArchiveRejectsPlaintextNonLoopbackEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://archive.ops-system.svc:8333", "http://localhost.evil:8333"} {
		if _, err := NewClient(Config{Endpoint: endpoint, Bucket: "archive", AccessKey: "fixture", SecretKey: "fixture"}); err == nil {
			t.Errorf("plaintext non-loopback archive accepted: %s", endpoint)
		}
	}
}

func TestArchiveTLSRequiresIndependentCAAndServerIdentity(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	config := Config{Endpoint: server.URL, Bucket: "archive", AccessKey: "fixture", SecretKey: "fixture"}
	if _, err := NewClient(config); err == nil {
		t.Fatal("HTTPS archive accepted without independently supplied CA")
	}
	config.CACertBundle = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.client.HeadBucket(context.Background(), &sdk.HeadBucketInput{Bucket: aws.String("archive")}); err != nil {
		t.Fatal("independently trusted archive TLS failed")
	}
	config.ServerName = "wrong-service.example"
	client, err = NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.client.HeadBucket(context.Background(), &sdk.HeadBucketInput{Bucket: aws.String("archive")}); err == nil {
		t.Fatal("wrong archive Service certificate identity accepted")
	}
}
