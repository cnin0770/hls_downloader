package parse

import (
	"strings"
	"testing"
)

func TestParseVODPlaylist(t *testing.T) {
	body := `#EXTM3U
#EXT-X-TARGETDURATION:10
#EXT-X-PLAYLIST-TYPE:VOD
#EXT-X-VERSION:3
#EXT-X-MEDIA-SEQUENCE:7
#EXT-X-KEY:METHOD=AES-128,URI="key.key",IV="1234567890123456"
#EXTINF:9.5,first
#EXT-X-BYTERANGE:100@50
segment-1.ts
#EXTINF:10.0,second
segment-2.ts
#EXT-X-ENDLIST`

	got, err := parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if got.TargetDuration != 10 {
		t.Fatalf("TargetDuration = %v, want 10", got.TargetDuration)
	}
	if got.PlaylistType != PlaylistTypeVOD {
		t.Fatalf("PlaylistType = %q, want VOD", got.PlaylistType)
	}
	if got.Version != 3 {
		t.Fatalf("Version = %d, want 3", got.Version)
	}
	if got.MediaSequence != 7 {
		t.Fatalf("MediaSequence = %d, want 7", got.MediaSequence)
	}
	if len(got.Keys) != 1 {
		t.Fatalf("len(Keys) = %d, want 1", len(got.Keys))
	}
	if got.Keys[1].Method != CryptMethodAES || got.Keys[1].URI != "key.key" || got.Keys[1].IV != "1234567890123456" {
		t.Fatalf("key = %+v", got.Keys[1])
	}
	if len(got.Segments) != 2 {
		t.Fatalf("len(Segments) = %d, want 2", len(got.Segments))
	}
	if got.Segments[0].URI != "segment-1.ts" || got.Segments[0].Duration != 9.5 || got.Segments[0].Title != "first" {
		t.Fatalf("first segment = %+v", got.Segments[0])
	}
	if got.Segments[0].Length != 100 || got.Segments[0].Offset != 50 {
		t.Fatalf("first segment byte range = length %d offset %d, want 100/50",
			got.Segments[0].Length, got.Segments[0].Offset)
	}
	if got.Segments[1].URI != "segment-2.ts" || got.Segments[1].KeyIndex != 1 {
		t.Fatalf("second segment = %+v", got.Segments[1])
	}
}

func TestParseMasterPlaylist(t *testing.T) {
	body := `#EXTM3U
#EXT-X-STREAM-INF:PROGRAM-ID=1,BANDWIDTH=240000,RESOLUTION=416x234,CODECS="avc1.42e00a,mp4a.40.2"
low/index.m3u8`

	got, err := parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MasterPlaylist) != 1 {
		t.Fatalf("len(MasterPlaylist) = %d, want 1", len(got.MasterPlaylist))
	}
	mp := got.MasterPlaylist[0]
	if mp.URI != "low/index.m3u8" || mp.BandWidth != 240000 || mp.Resolution != "416x234" ||
		mp.Codecs != "avc1.42e00a,mp4a.40.2" || mp.ProgramID != 1 {
		t.Fatalf("master playlist = %+v", mp)
	}
}

func TestParseRejectsInvalidPlaylist(t *testing.T) {
	_, err := parse(strings.NewReader("#EXTINF:10,\nsegment.ts"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRejectsDuplicateEXTINF(t *testing.T) {
	body := `#EXTM3U
#EXTINF:10,
#EXTINF:10,
segment.ts`

	_, err := parse(strings.NewReader(body))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseLineParameters(t *testing.T) {
	got := parseLineParameters(`#EXT-X-STREAM-INF:BANDWIDTH=240000,RESOLUTION=416x234,CODECS="avc1,mp4a"`)
	if got["BANDWIDTH"] != "240000" || got["RESOLUTION"] != "416x234" || got["CODECS"] != "avc1,mp4a" {
		t.Fatalf("params = %+v", got)
	}
}

func TestParseMediaInitAndRenditions(t *testing.T) {
	playlist := `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",DEFAULT=YES,URI="audio.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=4000000,AVERAGE-BANDWIDTH=2000000,AUDIO="aud"
video.m3u8
`
	m3u8, err := parse(strings.NewReader(playlist))
	if err != nil {
		t.Fatal(err)
	}
	if len(m3u8.Media) != 1 {
		t.Fatalf("Media = %+v, want one rendition", m3u8.Media)
	}
	audio := m3u8.Media[0]
	if audio.Type != "AUDIO" || audio.GroupID != "aud" || audio.Name != "English" || !audio.Default {
		t.Fatalf("rendition = %+v, want the parsed audio track", audio)
	}
	if audio.URI != "audio.m3u8" {
		t.Fatalf("rendition URI = %q, want audio.m3u8", audio.URI)
	}
	if len(m3u8.MasterPlaylist) != 1 {
		t.Fatalf("MasterPlaylist = %+v, want one variant", m3u8.MasterPlaylist)
	}
	variant := m3u8.MasterPlaylist[0]
	if variant.AvgBandWidth != 2000000 || variant.BandWidth != 4000000 {
		t.Fatalf("variant bandwidths = %d/%d, want 4000000/2000000", variant.BandWidth, variant.AvgBandWidth)
	}
	if variant.AudioGroup != "aud" {
		t.Fatalf("variant AudioGroup = %q, want aud", variant.AudioGroup)
	}
}

func TestParseExtXMap(t *testing.T) {
	playlist := `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-MAP:URI="/path/init.mp4"
#EXTINF:3.000,
seg0.m4s
#EXT-X-ENDLIST
`
	m3u8, err := parse(strings.NewReader(playlist))
	if err != nil {
		t.Fatal(err)
	}
	if m3u8.Map == nil {
		t.Fatal("Map = nil, want the init segment")
	}
	if m3u8.Map.URI != "/path/init.mp4" {
		t.Fatalf("Map.URI = %q, want /path/init.mp4", m3u8.Map.URI)
	}
}

func TestParseExtXMapWithByteRange(t *testing.T) {
	playlist := `#EXTM3U
#EXT-X-MAP:URI="init.mp4",BYTERANGE="718@0"
#EXTINF:3.000,
seg0.m4s
#EXT-X-ENDLIST
`
	m3u8, err := parse(strings.NewReader(playlist))
	if err != nil {
		t.Fatal(err)
	}
	if m3u8.Map == nil || m3u8.Map.Length != 718 || m3u8.Map.Offset != 0 {
		t.Fatalf("Map = %+v, want length 718 at offset 0", m3u8.Map)
	}
}

// #EXT-X-ENDLIST is what marks a playlist complete; the tag was previously
// matched against a string that never appears in a real playlist, so EndList
// was always false.
func TestParseEndListMarksVODComplete(t *testing.T) {
	vod := "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:6.0,\nseg0.ts\n#EXT-X-ENDLIST\n"
	m3u8, err := parse(strings.NewReader(vod))
	if err != nil {
		t.Fatal(err)
	}
	if !m3u8.EndList {
		t.Fatal("EndList = false, want true for a playlist ending in #EXT-X-ENDLIST")
	}

	live := "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:6.0,\nseg0.ts\n"
	m3u8, err = parse(strings.NewReader(live))
	if err != nil {
		t.Fatal(err)
	}
	if m3u8.EndList {
		t.Fatal("EndList = true, want false when the tag is absent")
	}
}
