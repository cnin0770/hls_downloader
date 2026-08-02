# HLS Downloader

A small, fast HLS/M3U8 downloader written in Go. Point it at a playlist URL and it
concurrently downloads every TS (Transport Stream) segment, decrypts if needed, and
merges them into a single file — with an optional remux to MP4.

Specify the core flags (`u`, `o`, `c`) and the downloader fetches all TS segments and
consolidates them into one TS file. Use `n` to set the output filename; otherwise a
timestamp is used.

> This project began as a fork of [oopsguy/m3u8](https://github.com/oopsguy/m3u8) and
> has since been substantially rewritten and extended. See [Acknowledgments](#acknowledgments).

## Features

Core downloading:

- Download and parse M3U8 (VOD) playlists, including master playlists
- AES-128 decryption of encrypted segments
- Concurrent segment download with retry on failure
- Merge segments into a single TS file

Added in this version:

- **Optional remux to MP4** (no re-encode) with playlist-duration verification
- **Config file** with command-line flag overrides
- **Newline-delimited JSON events** for UI / tooling integration
- **Live download speed** and a **completion summary** (time / file size / average speed)
- **Terminal-width-aware progress bar** that never wraps a narrow window

## Build

Standard Go modules — no `GOPATH` setup required:

```bash
go build -o hlsdl .
```

Run it directly from source with `go run .`, and run the tests with `go test ./...`.

## Usage

### From source

```bash
go run . -u=http://example.com/index.m3u8 -o=/data/example
```

Use a custom output filename:

```bash
go run . -u=http://example.com/index.m3u8 -o=/data/example -n=testName
```

Use a custom config file:

```bash
go run . -u=http://example.com/index.m3u8 -config=/path/to/config.json
```

Config values are loaded from the file, then command-line flags override them.
On macOS, the default config path is `~/Library/Application Support/HLS Downloader/config.json`.

```json
{
  "downloadDir": "/data/example",
  "concurrency": 25,
  "retries": 3,
  "referer": "",
  "userAgent": "",
  "convertToMp4": false,
  "keepTs": true,
  "keepSegments": false,
  "jsonOutput": false,
  "ffmpegLog": false
}
```

Print the effective config and exit:

```bash
go run . -print-config -config=/path/to/config.json
```

Emit newline-delimited JSON events for UI integration:

```bash
go run . -u=http://example.com/index.m3u8 -o=/data/example -json
```

Convert the merged TS file to MP4 with local `ffmpeg`:

```bash
go run . -u=http://example.com/index.m3u8 -o=/data/example -mp4
```

The conversion remuxes without re-encoding:

```bash
ffmpeg -i input.ts -c copy -movflags +faststart output.mp4
```

The merged TS file is kept by default. Use `-keep-ts=false` to remove it after a
successful MP4 conversion.
After conversion, the MP4 duration is compared with the playlist duration. If the
mismatch is larger than `max(5 seconds, 1%)` and is not explained by segments that
failed to download, the MP4 is tagged with the expected duration (e.g.
`movie.030634.mp4` for a 3h06m34s playlist) and the merged TS is kept — in that case the
remux itself is in doubt, so the TS is the pristine copy worth holding on to.
ffmpeg output is hidden by default. Use `-ffmpeg-log` to show ffmpeg's own conversion
details.
In JSON mode, successful TS cleanup emits a `ts_deleted` event.

### Preflight

Before anything is downloaded, the playlist is checked for shapes this tool cannot
handle correctly, and a summary of what is about to be fetched is printed:

```text
[playlist] 725 segments, 2:01:04, ~1.9 GB estimated (2.1 Mbps)
```

The size estimate comes from the master playlist's `AVERAGE-BANDWIDTH` (falling back to
`BANDWIDTH`, which overstates variable-bitrate content); a bare media playlist reports
only the segment count and duration.

Two playlist types are refused outright, because downloading them would produce a file
no player can open:

```text
[error] this playlist uses fMP4 segments (#EXT-X-MAP), which are not supported yet.
        Convert it directly instead: ffmpeg -i "URL" -c copy output.mp4
```

- **`#EXT-X-MAP`** — fMP4/CMAF fragments, which need an initialization segment
- **`#EXT-X-BYTERANGE`** — segments addressed as byte ranges of a larger file

Two more are warnings, since the download still works but is not the whole story:

```text
[warning] audio is a separate rendition (English) and will not be included: the output
          will have no sound. For both tracks: ffmpeg -i "URL" -map 0 -c copy output.mp4
[warning] live playlist (no #EXT-X-ENDLIST): only the currently advertised window will
          be downloaded
```

### Failed segments

Each segment is attempted up to `-retries` times (default 3), with a growing pause
between attempts:

```bash
go run . -u=http://example.com/index.m3u8 -o=/data/example -retries=5
```

If a segment still fails, it is given up on so the download always terminates rather
than retrying forever. The result is then reported as incomplete, the surviving segments
are still merged, and — because missing segments are exact knowledge rather than a guess
— the MP4 is tagged regardless of the duration check. The merged TS is *not* retained
for this: it has the identical gaps, so it offers no way to recover the missing content
(see `-keep-segments` below for what does).

```text
[failed] seg 2 after 3 attempts: ... status code 404
...
[incomplete] lost 36s of 2:00 (30.0%)
[gaps] 0:12 (-6s), 0:48 (-24s), 1:12 (-6s)
```

Everything that came out wrong is reported once, at the end. Two checks feed that one
report: segments that failed to download (exact), and a duration probe of the converted
MP4 that catches content lost some other way — a truncated response that still returned
`200`, or a bad remux. When the probe finds more missing than the failed segments
explain, the remainder is shown as `+14s elsewhere`; when nothing failed to download,
there are no positions to give and the line ends `, location unknown`.

Use `-keep-segments` to preserve the downloaded parts when a download ends up
incomplete, so the missing ones can be re-fetched and merged in rather than pulling the
whole video again:

```bash
go run . -u=http://example.com/index.m3u8 -o=/data/example -keep-segments
```

```text
[segments] kept for re-fetch: /data/example/20260727205231
```

The files are named by playlist index (`9.ts`, `10.ts`, …), matching the
`missing_segments` list in the JSON events, so it is clear which ones to fetch and what
to call them. A complete download always cleans up, since a second copy of every segment
would just double the space used.

The gaps are reported on the **finished file's own timeline**: `0:48 (-24s)` means that
48 seconds into the video you actually have, 24 seconds of the source are skipped. Since
the missing content is simply absent, everything after a gap shifts earlier and
consecutive missing segments collapse into a single jump rather than a span. These
positions line up exactly with the MP4's timecode; a merged TS keeps the original
timestamps, so a player may show a different clock there, but the position in playable
content is the same.

Segment indexes are not printed — they are in the JSON events, where `task_done` carries
`incomplete: true`, `missing_segments`, `missing_duration_seconds`,
`unexplained_seconds`, and `gaps` as `[{at_seconds, missing_seconds}]`.

### Progress, live speed, and summary

During the download, a single status line is redrawn in place with a live download
speed, an estimated final size, and an ETA (all sampled once per second) alongside the
segment count:

```text
[DL]  50% of ~256 MB at 1.6 MB/s  ETA 2:05  seg 4/8
```

The figures become known progressively, so the line starts as `[DL]  10%  seg 1/10` and
fills out once the first sample has been taken. Merging uses the same line without the
transfer figures:

```text
[Merge]  50%  seg 4/8
```

The size estimate extrapolates from the segments finished so far, weighted by playlist
duration rather than segment count so it tracks variable-bitrate streams. It is marked
`~` because it is a projection: if the stream's bitrate ramps up later, early estimates
read low and converge as the download proceeds. It is shown without decimals below a
gigabyte, and with one from a gigabyte up, where `~1.9 GB` says something meaningfully
different from `~2 GB`.

The line fits the terminal width, so it never wraps onto multiple rows; in a very narrow
window it is truncated with an ellipsis.

When a task completes, a summary line reports the total time, output file size, and
average download speed:

```text
[summary] time 1m23.4s | size 256.40 MB | avg 6.30 MB/s
```

Average speed is measured over the download phase only (merging and MP4 conversion do
not use the network).

### JSON events

In JSON mode, `download_progress` events carry the current `speed` (bytes per second),
`estimated_bytes`, and `eta_seconds` roughly once per second, and the summary values
ride on the `task_done` event as
`elapsed_seconds`, `download_seconds`, `bytes_downloaded`, `file_size`, and
`average_speed` (`bytes_downloaded` counts retried segments as bandwidth used).

JSON event types are:

```text
task_started
download_progress
segment_failed
download_done
merge_started
merge_progress
merge_done
conversion_started
conversion_done
conversion_failed
conversion_suspect
ts_deleted
warning
error
task_done
```

The CLI exits with code `0` on success and code `1` on failure. In JSON mode, failures
emit an `error` event.

### Prebuilt binary

Linux & macOS:

```
./hlsdl -u=http://example.com/index.m3u8 -o=/data/example
```

Windows PowerShell:

```
.\hlsdl.exe -u="http://example.com/index.m3u8" -o="D:\data\example"
```

## Acknowledgments

This project started from [oopsguy/m3u8](https://github.com/oopsguy/m3u8), whose original
M3U8 downloader provided the initial parsing, decryption, and merging foundation. The
codebase has since been reorganized and extended with the features listed above. The
original work is MIT-licensed, and that license is retained alongside this project's own
(see [LICENSE](./LICENSE)).

## References

- [TS科普 2 包头](https://blog.csdn.net/cabbage2008/article/details/49281729)
- [HTTP Live Streaming draft-pantos-http-live-streaming-23](https://tools.ietf.org/html/draft-pantos-http-live-streaming-23#section-4.3.4.2)
- [MPEG transport stream - Wikipedia](https://en.wikipedia.org/wiki/MPEG_transport_stream)

## License

[MIT License](./LICENSE)
