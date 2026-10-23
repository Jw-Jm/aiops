package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSubjectInventoryImportValidatesBeforeConnectingAndKeepsConnectionPrivate(t *testing.T) {
	config := writePrivateFile(t, "connection.json", `{"databaseUrl":"postgresql://migration:fixture-secret@127.0.0.1:1/no-database"}`, 0600)
	inventory := writePrivateFile(t, "review.json", `{"schemaVersion":"native-subject-inventory/v1","loginName":"same-name"}`, 0600)
	var out, log bytes.Buffer
	err := runImportSubjectInventory(context.Background(), []string{"--input", config, "--inventory", inventory}, &out, &log)
	if err == nil || !strings.Contains(err.Error(), "no database connection was attempted") {
		t.Fatalf("invalid inventory reached DB: %v", err)
	}
	if out.Len() != 0 || strings.Contains(log.String()+err.Error(), "fixture-secret") {
		t.Fatal("private input leaked")
	}
	shared := writePrivateFile(t, "shared.json", `{}`, 0644)
	if err = runImportSubjectInventory(context.Background(), []string{"--input", shared, "--inventory", inventory}, &out, &log); err == nil {
		t.Fatal("shared connection file accepted")
	}
}
