package metadata

import (
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/s3client"
)

func TestParseArtworkObjectKeyRebuildsOriginalVariant(t *testing.T) {
	t.Parallel()
	got, ok := parseArtworkObjectKey(s3client.ObjectInfo{
		Key: "local/movies/31190/19f56348/poster/w780.abc123.webp",
	})
	if !ok {
		t.Fatal("expected a well-formed ladder key to parse")
	}
	want := "local/movies/31190/19f56348/poster/original.abc123.webp"
	if got.original != want {
		t.Fatalf("original path = %q, want %q", got.original, want)
	}
	if got.key != "local/movies/31190/19f56348/poster/w780.abc123.webp" {
		t.Fatalf("key was rewritten: %q", got.key)
	}
}

func TestParseArtworkObjectKeyLeavesOriginalUnchanged(t *testing.T) {
	t.Parallel()
	// The original variant must map to itself, or every currently-referenced
	// object would look unreferenced and be deleted.
	key := "tmdb/people/1352462/profile/original.deadbeef.webp"
	got, ok := parseArtworkObjectKey(s3client.ObjectInfo{Key: key})
	if !ok {
		t.Fatal("expected the original variant to parse")
	}
	if got.original != key {
		t.Fatalf("original path = %q, want %q", got.original, key)
	}
}

func TestParseArtworkObjectKeyRejectsUnrecognizedShapes(t *testing.T) {
	t.Parallel()
	// Anything that does not decompose is skipped rather than guessed at:
	// an unmodelled key is the one case where deleting could destroy
	// something this code does not understand.
	for _, key := range []string{
		"local/movies/31190/poster/original.webp", // no hash segment
		"local/movies/31190/poster/a.b.c.d.webp",  // too many segments
		"orphan-at-root.webp",                     // no directory
		"local/movies/31190/poster/.abc123.webp",  // empty variant
		"local/movies/31190/poster/w300..webp",    // empty hash
		"local/movies/31190/poster/w300.abc123.",  // empty extension
		"local/movies/31190/poster/",              // directory marker
	} {
		if _, ok := parseArtworkObjectKey(s3client.ObjectInfo{Key: key}); ok {
			t.Errorf("key %q parsed but should have been rejected", key)
		}
	}
}

func TestParseArtworkObjectKeyCarriesModifiedTime(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	got, ok := parseArtworkObjectKey(s3client.ObjectInfo{
		Key:          "local/movies/1/poster/w300.abc.webp",
		LastModified: &when,
	})
	if !ok {
		t.Fatal("expected key to parse")
	}
	if got.modified == nil || !got.modified.Equal(when) {
		t.Fatalf("modified = %v, want %v", got.modified, when)
	}
}

func TestNewArtworkStorageSweeperRequiresDependencies(t *testing.T) {
	t.Parallel()
	if s := NewArtworkStorageSweeper(nil, nil); s != nil {
		t.Fatal("expected nil sweeper without a pool or storage client")
	}
}

// The anomaly guard is the difference between this task reclaiming space and
// this task deleting the artwork library, so its thresholds are asserted
// directly rather than left implicit.
func TestArtworkSweepAnomalyGuardThresholds(t *testing.T) {
	t.Parallel()
	if artworkSweepAnomalyRatio <= 0 || artworkSweepAnomalyRatio >= 1 {
		t.Fatalf("anomaly ratio %v must be a fraction between 0 and 1", artworkSweepAnomalyRatio)
	}
	if artworkSweepAnomalyFloor < 1 {
		t.Fatal("anomaly floor must exempt at least the smallest pages")
	}
	if artworkSweepMinAge <= 0 {
		t.Fatal("age floor must be positive or freshly cached artwork can be deleted")
	}

	// A page where nearly everything is unreferenced is far more likely a
	// broken reference check than a genuinely empty catalog.
	parsed, doomed := 1000, 900
	if !(parsed >= artworkSweepAnomalyFloor && float64(doomed) > artworkSweepAnomalyRatio*float64(parsed)) {
		t.Fatal("a 90%-unreferenced full page must trip the anomaly guard")
	}

	// Normal cleanup must not trip it: the measured orphan rate on a real
	// deployment was 27%, and the worst single category was 52%.
	parsed, doomed = 1000, 520
	if parsed >= artworkSweepAnomalyFloor && float64(doomed) > artworkSweepAnomalyRatio*float64(parsed) {
		t.Fatal("a 52%-unreferenced page is normal cleanup and must not trip the guard")
	}

	// A short final page may legitimately be entirely unreferenced.
	parsed, doomed = 10, 10
	if parsed >= artworkSweepAnomalyFloor && float64(doomed) > artworkSweepAnomalyRatio*float64(parsed) {
		t.Fatal("a short trailing page must be exempt from the anomaly guard")
	}
}
