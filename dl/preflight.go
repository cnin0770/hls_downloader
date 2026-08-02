package dl

import (
	"fmt"
	"strings"

	"github.com/cnin0770/hls_downloader/parse"
)

// UnsupportedPlaylistError reports a playlist this downloader cannot handle
// correctly. It is returned before anything is fetched, because the alternative
// is spending a whole download to produce a file that was never going to work.
type UnsupportedPlaylistError struct {
	Reason string
	Hint   string
}

func (e *UnsupportedPlaylistError) Error() string {
	if e.Hint == "" {
		return e.Reason
	}
	return e.Reason + " " + e.Hint
}

// ffmpegHint points at the tool that does handle these playlists, with the
// user's own URL filled in.
func ffmpegHint(url string) string {
	return fmt.Sprintf("Convert it directly instead: ffmpeg -i %q -c copy output.mp4", url)
}

// preflight inspects a parsed playlist before any segment is fetched. It
// returns an error for inputs that cannot produce a correct file, and warnings
// for inputs that work only partially.
func preflight(result *parse.Result, url string) ([]string, error) {
	if result == nil || result.M3u8 == nil {
		return nil, nil
	}
	playlist := result.M3u8

	// fMP4/CMAF: the fragments are meaningless without the init segment, and
	// concatenating them as TS produces a file no player can open.
	if playlist.Map != nil {
		return nil, &UnsupportedPlaylistError{
			Reason: "this playlist uses fMP4 segments (#EXT-X-MAP), which are not supported yet.",
			Hint:   ffmpegHint(url),
		}
	}

	// Byte-range segments address slices of one large file. Without Range
	// requests every segment would fetch the whole file.
	for _, segment := range playlist.Segments {
		if segment != nil && segment.Length > 0 {
			return nil, &UnsupportedPlaylistError{
				Reason: "this playlist uses byte-range segments (#EXT-X-BYTERANGE), which are not supported yet.",
				Hint:   ffmpegHint(url),
			}
		}
	}

	var warnings []string

	// Audio carried as a separate rendition is never fetched by this
	// downloader, so the output would be silent with no other clue why.
	if audio := audioRenditions(result); len(audio) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"audio is a separate rendition (%s) and will not be included: the output will have no sound. "+
				"For both tracks: ffmpeg -i %q -map 0 -c copy output.mp4",
			strings.Join(audio, ", "), url))
	}

	// Without an end marker the playlist is live: what we download is whatever
	// window the server is currently advertising.
	if !playlist.EndList {
		warnings = append(warnings,
			"live playlist (no #EXT-X-ENDLIST): only the currently advertised window will be downloaded")
	}

	return warnings, nil
}

// audioRenditions names the alternate audio tracks the master playlist declares
// for the selected variant.
func audioRenditions(result *parse.Result) []string {
	if result.Master == nil {
		return nil
	}
	var names []string
	for _, rendition := range result.Master.Media {
		if rendition == nil || !strings.EqualFold(rendition.Type, "AUDIO") {
			continue
		}
		// A variant naming an audio group takes its audio from that group; when
		// no group is named, report any audio rendition as a possibility.
		if result.Variant != nil && result.Variant.AudioGroup != "" &&
			!strings.EqualFold(result.Variant.AudioGroup, rendition.GroupID) {
			continue
		}
		name := rendition.Name
		if name == "" {
			name = rendition.GroupID
		}
		if name == "" {
			name = "audio"
		}
		names = append(names, name)
	}
	return names
}

// playlistSummary describes what is about to be downloaded, so a mistake is
// obvious in the first second rather than after a long download.
func playlistSummary(result *parse.Result, segments int, duration float64) Event {
	summary := Event{
		Type:             EventPlaylistReady,
		Total:            segments,
		ExpectedDuration: duration,
	}
	if result != nil && result.Variant != nil && duration > 0 {
		// AVERAGE-BANDWIDTH describes the stream better than the peak
		// BANDWIDTH, which overstates size on variable-bitrate content.
		bitrate := result.Variant.AvgBandWidth
		if bitrate == 0 {
			bitrate = result.Variant.BandWidth
		}
		if bitrate > 0 {
			summary.EstimatedBytes = int64(float64(bitrate) / 8 * duration)
			summary.Bitrate = int64(bitrate)
		}
	}
	return summary
}
