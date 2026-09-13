package instagram

import (
	"testing"

	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/models"
)

func TestShortcodeToMediaID(t *testing.T) {
	cases := map[string]string{
		"DdG7ShfOjPN": "3983131678107907021",
		"DdNzUIpN9dn": "3985066929335818087",
	}
	for shortcode, want := range cases {
		got, err := ShortcodeToMediaID(shortcode)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", shortcode, err)
		}
		if got != want {
			t.Fatalf("%s: got %s want %s", shortcode, got, want)
		}
	}
}

func TestParseMediaInfoMixedCarousel(t *testing.T) {
	ctx := &models.ExtractorContext{
		ContentID:  "testCarousel",
		ContentURL: "https://www.instagram.com/p/testCarousel/",
		Extractor:  Extractor,
	}
	item := &MediaInfoItem{
		MediaType: mediaTypeCarousel,
		Caption:   &MediaCaption{Text: "mixed album"},
		CarouselMedia: []*MediaInfoItem{
			{
				MediaType: mediaTypePhoto,
				ImageVersions: &ImageVersions{
					Candidates: []*Candidates{
						{Width: 1080, Height: 1080, URL: "https://example.com/photo.jpg"},
						{Width: 640, Height: 640, URL: "https://example.com/photo-small.jpg"},
					},
				},
			},
			{
				MediaType: mediaTypeVideo,
				VideoVersions: []*VideoVersions{
					{Width: 720, Height: 1280, URL: "https://example.com/video.mp4"},
					{Width: 480, Height: 854, URL: "https://example.com/video-small.mp4"},
				},
				ImageVersions: &ImageVersions{
					Candidates: []*Candidates{
						{Width: 720, Height: 1280, URL: "https://example.com/thumb.jpg"},
					},
				},
				VideoDuration: 12.5,
			},
		},
	}

	media, err := ParseMediaInfoItem(ctx, item)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if media.Caption != "mixed album" {
		t.Fatalf("caption = %q", media.Caption)
	}
	if len(media.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(media.Items))
	}
	if media.Items[0].Formats[0].Type != database.MediaTypePhoto {
		t.Fatalf("first item type = %s", media.Items[0].Formats[0].Type)
	}
	if media.Items[0].Formats[0].URL[0] != "https://example.com/photo.jpg" {
		t.Fatalf("expected best photo url, got %s", media.Items[0].Formats[0].URL[0])
	}
	if media.Items[1].Formats[0].Type != database.MediaTypeVideo {
		t.Fatalf("second item type = %s", media.Items[1].Formats[0].Type)
	}
	if media.Items[1].Formats[0].URL[0] != "https://example.com/video.mp4" {
		t.Fatalf("expected best video url, got %s", media.Items[1].Formats[0].URL[0])
	}
}
