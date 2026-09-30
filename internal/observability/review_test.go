package observability

import (
	"log/slog"
	"strings"
	"testing"
)

func TestStructuredAnyValuesRedactNestedSensitiveFields(t *testing.T) {
	var output strings.Builder
	logger := NewLogger(&output, slog.LevelInfo)
	logger.Info("fixture", "metadata", map[string]any{"items": []any{map[string]any{"prompt": "private-prompt-body", "command": "private-command-body", "password": "private-password-body"}}})
	for _, value := range []string{"private-prompt-body", "private-command-body", "private-password-body"} {
		if strings.Contains(output.String(), value) {
			t.Fatal("nested structured log value leaked")
		}
	}
}
