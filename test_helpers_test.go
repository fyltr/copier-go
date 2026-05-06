package copier

import (
	"os"
	"testing"
)

func mustWriteFile(t testing.TB, path string, data []byte, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatal(err)
	}
}
