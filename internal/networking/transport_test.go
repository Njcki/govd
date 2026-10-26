package networking

import (
	"testing"
)

func TestNewDownloadTransportDisablesHTTP2(t *testing.T) {
	tr := NewDownloadTransport()
	if tr.ForceAttemptHTTP2 {
		t.Fatal("ForceAttemptHTTP2 should be false for download transport")
	}
	if tr.TLSNextProto == nil {
		t.Fatal("TLSNextProto should be empty map, not nil")
	}
	if len(tr.TLSNextProto) != 0 {
		t.Fatalf("TLSNextProto should be empty, got %d entries", len(tr.TLSNextProto))
	}
	api := NewTransport()
	if !api.ForceAttemptHTTP2 {
		t.Fatal("API transport should keep ForceAttemptHTTP2")
	}
}
