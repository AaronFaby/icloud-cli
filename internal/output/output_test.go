package output

import (
	"errors"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestWriteFailureChangesExitCode(t *testing.T) {
	if code := Success(failingWriter{}, "test", "test", map[string]bool{"done": true}); code != ExitUnexpected {
		t.Fatalf("successful operation with failed output returned exit %d", code)
	}
	if code := Failure(failingWriter{}, "test", "test", Validation("test", "test", nil)); code != ExitUnexpected {
		t.Fatalf("failed operation with failed output returned exit %d", code)
	}
}
