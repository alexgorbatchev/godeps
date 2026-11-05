package godeps

import (
	"errors"
	"io"
	"os"
	"testing"
)

func TestCaptureRestoresAndClosesStream(t *testing.T) {
	tests := []struct {
		name    string
		collect bool
		skip    bool
	}{
		{name: "collected", collect: true},
		{name: "returned before collection"},
		{name: "exited before collection", skip: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := os.Stdout
			stream := original
			var captured capturedStream
			t.Run("capture", func(t *testing.T) {
				captured = capture(t, &stream)
				const message = "captured output\n"
				if _, err := io.WriteString(stream, message); err != nil {
					t.Fatal(err)
				}
				if tt.skip {
					// SkipNow unwinds through Goexit, as FailNow does, without
					// making this regression test intentionally fail.
					t.SkipNow()
				}
				if tt.collect {
					if got := captured.collect(); got != message {
						t.Errorf("captured %q, want %q", got, message)
					}
					if stream != original {
						t.Error("collection did not restore the stream")
					}
					assertCaptureClosed(t, captured)
				}
			})
			if stream != original {
				t.Error("cleanup did not restore the stream")
			}
			assertCaptureClosed(t, captured)
		})
	}
}

func assertCaptureClosed(t *testing.T, captured capturedStream) {
	t.Helper()
	for _, endpoint := range []struct {
		name string
		file *os.File
	}{
		{name: "reader", file: captured.reader},
		{name: "writer", file: captured.writer},
	} {
		if endpoint.file == nil {
			t.Fatalf("capture did not create a %s", endpoint.name)
		}
		if _, err := endpoint.file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("%s after capture: got %v, want os.ErrClosed", endpoint.name, err)
		}
	}
}
