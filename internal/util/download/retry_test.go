package download

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsTransientDownloadError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("failed to write chunk 11: stream error: stream ID 25; PROTOCOL_ERROR; received from peer"), true},
		{fmt.Errorf("failed to read chunk 5: %w", errors.New("http2: stream closed")), true},
		{errors.New("connection reset by peer"), true},
		{errors.New("unexpected EOF"), true},
		{errors.New("expected status 206, got 403"), false},
		{errors.New("no URL found for selected format"), false},
	}
	for _, c := range cases {
		if got := isTransientDownloadError(c.err); got != c.want {
			t.Fatalf("isTransientDownloadError(%v)=%v want %v", c.err, got, c.want)
		}
	}
}
