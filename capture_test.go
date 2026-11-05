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
			var writer *os.File
			t.Run("capture", func(t *testing.T) {
				collect := capture(t, &stream)
				writer = stream
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
					if got := collect(); got != message {
						t.Errorf("captured %q, want %q", got, message)
					}
					if stream != original {
						t.Error("collection did not restore the stream")
					}
				}
			})
			if stream != original {
				t.Error("cleanup did not restore the stream")
			}
			if writer == nil {
				t.Fatal("capture did not create a writer")
			}
			if _, err := writer.WriteString("after cleanup"); !errors.Is(err, os.ErrClosed) {
				t.Errorf("write after cleanup: got %v, want os.ErrClosed", err)
			}
		})
	}
}
