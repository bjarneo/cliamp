package clipboard

import (
	"os"
	"testing"
)

func TestCopyNoBackend(t *testing.T) {
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", "/nonexistent-dir-for-test")
	defer os.Setenv("PATH", oldPath)
	if err := Copy("x"); err == nil {
		t.Fatal("expected error with no backends on PATH")
	}
}
