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
  "referer": "",
  "userAgent": "",
  "convertToMp4": false,
  "keepTs": true,
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
mismatch is larger than `max(5 seconds, 1%)`, the MP4 is kept but tagged with the
expected duration (e.g. `movie.030634.mp4` for a 3h06m34s playlist) and the original
TS is kept.
ffmpeg output is hidden by default. Use `-ffmpeg-log` to show ffmpeg's own conversion
details.
In JSON mode, successful TS cleanup emits a `ts_deleted` event.

### Progress, live speed, and summary

During the download, the progress bar shows a live download speed (sampled once per
second) alongside the segment count:

```text
[Downloading] [■■■■■■■■■■■■■■■               ]  50.00% 4/8  1.61 MB/s
```

The progress bar adapts to the terminal width so it never wraps onto multiple rows: on
a narrow window the bar shrinks, on a very narrow one the bar is dropped and only the
key fields are shown (e.g. `50.00% 4/8`), and in the extreme case the line is truncated
with an ellipsis.

When a task completes, a summary line reports the total time, output file size, and
average download speed:

```text
[summary] time 1m23.4s | size 256.40 MB | avg 6.30 MB/s
```

Average speed is measured over the download phase only (merging and MP4 conversion do
not use the network).

### JSON events

In JSON mode, `download_progress` events carry the current `speed` (bytes per second)
roughly once per second, and the summary values ride on the `task_done` event as
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

## Download

[Binary packages](https://github.com/cnin0770/hls_downloader/releases)

## Screenshots

![Demo](./screenshots/demo.gif)

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
