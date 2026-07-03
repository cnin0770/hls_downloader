# M3U8

M3U8 - a mini M3U8 downloader written in Golang for downloading and merging TS(Transport Stream) files.

You only need to specify the flags(`u`, `o`, `c`) to run, downloader will automatically download all TS files and consolidate them into a single TS file. Use `n` to set the output filename; otherwise a timestamp is used.

## Features

- Download and parse M3U8（VOD）
- Retry on download TS failure
- Parse Master playlist
- Decrypt TS
- Merge TS

## Usage

### source

```bash
go run main.go -u=http://example.com/index.m3u8 -o=/data/example
```

Use a custom output filename:

```bash
go run main.go -u=http://example.com/index.m3u8 -o=/data/example -n=testName
```

Use a custom config file:

```bash
go run main.go -u=http://example.com/index.m3u8 -config=/path/to/config.json
```

Config values are loaded from the file, then command-line flags override them.
On macOS, the default config path is `~/Library/Application Support/M3U8/config.json`.

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
go run main.go -print-config -config=/path/to/config.json
```

Emit newline-delimited JSON events for UI integration:

```bash
go run main.go -u=http://example.com/index.m3u8 -o=/data/example -json
```

Convert the merged TS file to MP4 with local `ffmpeg`:

```bash
go run main.go -u=http://example.com/index.m3u8 -o=/data/example -mp4
```

The conversion remuxes without re-encoding:

```bash
ffmpeg -i input.ts -c copy -movflags +faststart output.mp4
```

The merged TS file is kept by default. Use `-keep-ts=false` to remove it after a successful MP4 conversion.
After conversion, the MP4 duration is compared with the playlist duration. If the mismatch is larger than `max(5 seconds, 1%)`, the MP4 is kept with a `.suspect.mp4` suffix and the original TS is kept.
ffmpeg output is hidden by default. Use `-ffmpeg-log` to show ffmpeg's own conversion details.
In JSON mode, successful TS cleanup emits a `ts_deleted` event.

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

The CLI exits with code `0` on success and code `1` on failure. In JSON mode, failures emit an `error` event.

### binary:

Linux & MacOS

```
./m3u8 -u=http://example.com/index.m3u8 -o=/data/example
```

Windows PowerShell

```
.\m3u8.exe -u="http://example.com/index.m3u8" -o="D:\data\example"
```

## Download

[Binary packages](https://github.com/cnin0770/m3u8_ui/releases)

## Screenshots

![Demo](./screenshots/demo.gif)

## References

- [TS科普 2 包头](https://blog.csdn.net/cabbage2008/article/details/49281729)
- [HTTP Live Streaming draft-pantos-http-live-streaming-23](https://tools.ietf.org/html/draft-pantos-http-live-streaming-23#section-4.3.4.2)
- [MPEG transport stream - Wikipedia](https://en.wikipedia.org/wiki/MPEG_transport_stream)


## License

[MIT License](./LICENSE)
