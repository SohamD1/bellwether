package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunLoadsCompatibleModel(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "internal", "inference", "testdata", "compatibility_model.txt")
	if err := run([]string{"--model", path}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunRequiresModelPath(t *testing.T) {
	t.Parallel()

	err := run(nil)
	if err == nil || !strings.Contains(err.Error(), "--model") {
		t.Fatalf("run() error = %v, want missing --model error", err)
	}
}
