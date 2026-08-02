package dl

import (
	"strings"
	"testing"

	"github.com/cnin0770/hls_downloader/parse"
)

func mediaPlaylist(segments ...*parse.Segment) *parse.Result {
	return &parse.Result{
		M3u8: &parse.M3u8{Segments: segments, EndList: true},
	}
}

func TestPreflightAcceptsPlainVODPlaylist(t *testing.T) {
	warnings, err := preflight(mediaPlaylist(&parse.Segment{Duration: 6}), "http://example.com/i.m3u8")
	if err != nil {
		t.Fatalf("a plain VOD playlist should be accepted: %s", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
}

// The real-world case: an fMP4/CMAF playlist would download every fragment and
// then produce a file no player can open, so it must be refused up front.
func TestPreflightRejectsFMP4(t *testing.T) {
	result := mediaPlaylist(&parse.Segment{Duration: 3})
	result.M3u8.Map = &parse.MediaInit{URI: "init.mp4"}

	_, err := preflight(result, "http://example.com/i.m3u8")
	if err == nil {
		t.Fatal("expected fMP4 playlists to be rejected")
	}
	var unsupported *UnsupportedPlaylistError
	if !asUnsupported(err, &unsupported) {
		t.Fatalf("error = %T, want *UnsupportedPlaylistError", err)
	}
	for _, want := range []string{"#EXT-X-MAP", "ffmpeg"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to mention %q", err.Error(), want)
		}
	}
}

func TestPreflightRejectsByteRangeSegments(t *testing.T) {
	result := mediaPlaylist(
		&parse.Segment{Duration: 6},
		&parse.Segment{Duration: 6, Length: 1024, Offset: 2048},
	)

	_, err := preflight(result, "http://example.com/i.m3u8")
	if err == nil {
		t.Fatal("expected byte-range playlists to be rejected")
	}
	if !strings.Contains(err.Error(), "#EXT-X-BYTERANGE") {
		t.Fatalf("error = %q, want it to name the tag", err.Error())
	}
}

// Audio in a separate rendition means a silent output, which is worth saying
// out loud rather than leaving the user to discover it in a player.
func TestPreflightWarnsAboutSeparateAudio(t *testing.T) {
	result := mediaPlaylist(&parse.Segment{Duration: 6})
	result.Master = &parse.M3u8{
		Media: []*parse.Rendition{
			{Type: "AUDIO", GroupID: "aud", Name: "English", URI: "audio.m3u8"},
		},
	}
	result.Variant = &parse.MasterPlaylist{URI: "v.m3u8", AudioGroup: "aud"}

	warnings, err := preflight(result, "http://example.com/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no sound") {
		t.Fatalf("warnings = %v, want one about missing audio", warnings)
	}
	if !strings.Contains(warnings[0], "English") {
		t.Fatalf("warning = %q, want it to name the rendition", warnings[0])
	}
}

// A variant bound to one audio group should not be warned about renditions
// belonging to a different group.
func TestPreflightIgnoresUnrelatedAudioGroup(t *testing.T) {
	result := mediaPlaylist(&parse.Segment{Duration: 6})
	result.Master = &parse.M3u8{
		Media: []*parse.Rendition{{Type: "AUDIO", GroupID: "other", Name: "Commentary"}},
	}
	result.Variant = &parse.MasterPlaylist{AudioGroup: "aud"}

	warnings, err := preflight(result, "http://example.com/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none for an unrelated audio group", warnings)
	}
}

func TestPreflightWarnsAboutLivePlaylist(t *testing.T) {
	result := mediaPlaylist(&parse.Segment{Duration: 6})
	result.M3u8.EndList = false

	warnings, err := preflight(result, "http://example.com/live.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "live playlist") {
		t.Fatalf("warnings = %v, want one about the live playlist", warnings)
	}
}

func TestPlaylistSummaryEstimatesFromBitrate(t *testing.T) {
	result := mediaPlaylist(&parse.Segment{Duration: 6})
	// Prefers AVERAGE-BANDWIDTH: the peak overstates a variable-bitrate stream.
	result.Variant = &parse.MasterPlaylist{BandWidth: 4_000_000, AvgBandWidth: 2_000_000}

	summary := playlistSummary(result, 725, 7264)

	if summary.Type != EventPlaylistReady {
		t.Fatalf("Type = %q, want %q", summary.Type, EventPlaylistReady)
	}
	if summary.Bitrate != 2_000_000 {
		t.Fatalf("Bitrate = %d, want the average bandwidth", summary.Bitrate)
	}
	want := int64(2_000_000 / 8 * 7264)
	if summary.EstimatedBytes != want {
		t.Fatalf("EstimatedBytes = %d, want %d", summary.EstimatedBytes, want)
	}
}

func TestPlaylistSummaryWithoutMasterHasNoEstimate(t *testing.T) {
	summary := playlistSummary(mediaPlaylist(&parse.Segment{Duration: 6}), 10, 60)

	if summary.EstimatedBytes != 0 || summary.Bitrate != 0 {
		t.Fatalf("summary = %+v, want no size estimate without a master playlist", summary)
	}
	if summary.Total != 10 || summary.ExpectedDuration != 60 {
		t.Fatalf("summary = %+v, want the counts to still be reported", summary)
	}
}

func asUnsupported(err error, target **UnsupportedPlaylistError) bool {
	unsupported, ok := err.(*UnsupportedPlaylistError)
	if ok {
		*target = unsupported
	}
	return ok
}
