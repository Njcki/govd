package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/networking"
)

func vfmt(id string, height int32, bitrate int64, duration int32, size int64) *models.MediaFormat {
	return &models.MediaFormat{
		FormatID:   id,
		Type:       database.MediaTypeVideo,
		VideoCodec: database.MediaCodecAvc,
		AudioCodec: database.MediaCodecAac,
		Height:     height,
		Bitrate:    bitrate,
		Duration:   duration,
		FileSize:   size,
	}
}

func estSize(f *models.MediaFormat) int64 { return f.EstimatedSize() }

const mb = int64(1024 * 1024)

func TestPickFormatWithinSize_KeepsPreferredWhenFits(t *testing.T) {
	c := []*models.MediaFormat{vfmt("1080", 1080, 0, 0, 500*mb), vfmt("720", 720, 0, 0, 200*mb)}
	got, size, down := pickFormatWithinSize(c, nil, sizeLimit(1000*mb), estSize)
	if got.FormatID != "1080" || down || size != 500*mb {
		t.Fatalf("got %s down=%v size=%d", got.FormatID, down, size)
	}
}

func TestPickFormatWithinSize_DowngradesToBestFitting(t *testing.T) {
	// mr_diser-like: 1h03m, bitrate-only sizes
	dur := int32(3823)
	c := []*models.MediaFormat{
		vfmt("1080", 1080, 8768000, dur, 0), // ~4.19GB
		vfmt("720", 720, 1280000, dur, 0),   // ~612MB
		vfmt("540", 540, 832000, dur, 0),
	}
	got, _, down := pickFormatWithinSize(c, nil, sizeLimit(2097152000), estSize)
	if got.FormatID != "720" || !down {
		t.Fatalf("expected 720 downgrade, got %s down=%v", got.FormatID, down)
	}
}

func TestPickFormatWithinSize_SafetyMargin(t *testing.T) {
	// 980MB fits 1000MB raw but not with the 5% margin
	c := []*models.MediaFormat{vfmt("a", 1080, 0, 0, 980*mb), vfmt("b", 720, 0, 0, 900*mb)}
	got, _, down := pickFormatWithinSize(c, nil, sizeLimit(1000*mb), estSize)
	if got.FormatID != "b" || !down {
		t.Fatalf("expected b, got %s", got.FormatID)
	}
}

func TestPickFormatWithinSize_NothingFitsKeepsPreferred(t *testing.T) {
	c := []*models.MediaFormat{vfmt("a", 1080, 0, 0, 3000*mb), vfmt("b", 720, 0, 0, 2000*mb)}
	got, size, down := pickFormatWithinSize(c, nil, sizeLimit(1000*mb), estSize)
	if got.FormatID != "a" || down || size != 3000*mb {
		t.Fatalf("expected preferred a, got %s down=%v", got.FormatID, down)
	}
}

func TestPickFormatWithinSize_UnknownSizeTreatedAsFitting(t *testing.T) {
	c := []*models.MediaFormat{vfmt("a", 1080, 0, 0, 0), vfmt("b", 720, 0, 0, 10*mb)}
	got, _, down := pickFormatWithinSize(c, nil, sizeLimit(1000*mb), estSize)
	if got.FormatID != "a" || down {
		t.Fatalf("expected a, got %s", got.FormatID)
	}
}

func TestPickFormatWithinSize_AddsSeparateAudio(t *testing.T) {
	v1 := vfmt("v1", 1080, 0, 0, 900*mb)
	v1.AudioCodec = ""
	v2 := vfmt("v2", 720, 0, 0, 600*mb)
	v2.AudioCodec = ""
	audio := &models.MediaFormat{FormatID: "aud", Type: database.MediaTypeAudio, AudioCodec: database.MediaCodecAac, FileSize: 100 * mb}
	got, size, down := pickFormatWithinSize([]*models.MediaFormat{v1, v2}, audio, sizeLimit(1000*mb), estSize)
	if got.FormatID != "v2" || !down || size != 700*mb {
		t.Fatalf("expected v2 (700MB), got %s size=%d", got.FormatID, size)
	}
	// audio not added when the video already carries audio
	v3 := vfmt("v3", 1080, 0, 0, 900*mb)
	got, _, down = pickFormatWithinSize([]*models.MediaFormat{v3}, audio, sizeLimit(1000*mb), estSize)
	if got.FormatID != "v3" || down {
		t.Fatalf("expected v3, got %s", got.FormatID)
	}
}

func TestPickFormatWithinSize_UsesSizeOfOverride(t *testing.T) {
	// a probe returning the real (smaller) size keeps the preferred format
	c := []*models.MediaFormat{vfmt("1080", 1080, 8768000, 3823, 0), vfmt("720", 720, 1280000, 3823, 0)}
	probe := func(f *models.MediaFormat) int64 {
		if f.FormatID == "1080" {
			return 1434238189
		}
		return f.EstimatedSize()
	}
	got, _, down := pickFormatWithinSize(c, nil, sizeLimit(2097152000), probe)
	if got.FormatID != "1080" || down {
		t.Fatalf("expected 1080, got %s", got.FormatID)
	}
}

func TestPickFormatWithinSize_Empty(t *testing.T) {
	if got, _, _ := pickFormatWithinSize(nil, nil, 100, estSize); got != nil {
		t.Fatal("expected nil")
	}
}

func TestParseContentRangeTotal(t *testing.T) {
	cases := map[string]int64{
		"bytes 0-0/1434238189": 1434238189,
		"bytes 0-0/*":          0,
		"":                     0,
		"bytes 0-0/":           0,
	}
	for in, want := range cases {
		if got := parseContentRangeTotal(in); got != want {
			t.Errorf("%q: got %d want %d", in, got, want)
		}
	}
}

func TestGetVideoFormatCandidatesRespectsMaxHeight(t *testing.T) {
	item := &models.MediaItem{Formats: []*models.MediaFormat{
		vfmt("1080", 1080, 8000000, 10, 0),
		vfmt("720", 720, 2000000, 10, 0),
		vfmt("480", 480, 1000000, 10, 0),
	}}
	c := item.GetVideoFormatCandidates(720)
	if len(c) != 2 || c[0].FormatID != "720" || c[1].FormatID != "480" {
		t.Fatalf("unexpected candidates: %v", ids(c))
	}
	c = item.GetVideoFormatCandidates(0)
	if len(c) != 3 || c[0].FormatID != "1080" {
		t.Fatalf("unexpected candidates: %v", ids(c))
	}
	c = item.GetVideoFormatCandidates(240)
	if len(c) == 0 || c[0].FormatID != "480" {
		t.Fatalf("expected lowest-height fallback, got %v", ids(c))
	}
	if item.GetDefaultVideoFormat(720).FormatID != "720" {
		t.Fatal("default format mismatch")
	}
}

func TestEstimatedSize(t *testing.T) {
	if s := vfmt("x", 0, 8000000, 10, 0).EstimatedSize(); s != 10000000 {
		t.Fatalf("got %d", s)
	}
	if s := vfmt("x", 0, 8000000, 10, 42).EstimatedSize(); s != 42 {
		t.Fatalf("got %d", s)
	}
	if s := vfmt("x", 0, 0, 10, 0).EstimatedSize(); s != 0 {
		t.Fatalf("got %d", s)
	}
}

func ids(fs []*models.MediaFormat) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.FormatID
	}
	return out
}

func TestProbeFormatSize(t *testing.T) {
	const total = 1434238189
	head := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(total))
		w.WriteHeader(http.StatusOK)
	}))
	defer head.Close()
	// server that rejects HEAD but supports ranged GET
	ranged := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Range") != "bytes=0-0" {
			t.Errorf("unexpected range %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 0-0/"+strconv.Itoa(total))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte{0})
	}))
	defer ranged.Close()

	ctx := &models.ExtractorContext{
		HTTPClient: networking.NewHTTPClient(nil),
		Context:    context.Background(),
	}
	for _, srv := range []*httptest.Server{head, ranged} {
		f := vfmt("v", 1080, 0, 0, 0)
		f.URL = []string{srv.URL + "/video.mp4"}
		if got := probeFormatSize(ctx, f); got != total {
			t.Errorf("%s: got %d want %d", srv.URL, got, total)
		}
	}
	// playlists are not probed
	f := vfmt("v", 1080, 0, 0, 0)
	f.URL = []string{head.URL + "/pl.m3u8"}
	if got := probeFormatSize(ctx, f); got != 0 {
		t.Errorf("m3u8 probed: %d", got)
	}
}
