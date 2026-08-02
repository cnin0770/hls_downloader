package dl

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	EventPlaylistReady     = "playlist_ready"
	EventTaskStarted       = "task_started"
	EventDownloadProgress  = "download_progress"
	EventSegmentFailed     = "segment_failed"
	EventDownloadDone      = "download_done"
	EventMergeStarted      = "merge_started"
	EventMergeProgress     = "merge_progress"
	EventMergeDone         = "merge_done"
	EventConversionStarted = "conversion_started"
	EventConversionDone    = "conversion_done"
	EventConversionFailed  = "conversion_failed"
	EventConversionSuspect = "conversion_suspect"
	EventTSDeleted         = "ts_deleted"
	EventWarning           = "warning"
	EventError             = "error"
	EventTaskDone          = "task_done"
)

type Event struct {
	Type             string  `json:"type"`
	OutputDir        string  `json:"output_dir,omitempty"`
	TSDir            string  `json:"ts_dir,omitempty"`
	TSFile           string  `json:"ts_file,omitempty"`
	MP4File          string  `json:"mp4_file,omitempty"`
	BaseName         string  `json:"base_name,omitempty"`
	Done             int     `json:"done,omitempty"`
	Total            int     `json:"total,omitempty"`
	Percent          float64 `json:"percent,omitempty"`
	Segment          *int    `json:"segment,omitempty"`
	Message          string  `json:"message,omitempty"`
	ExpectedDuration float64 `json:"expected_duration,omitempty"`
	ActualDuration   float64 `json:"actual_duration,omitempty"`
	Suspect          bool    `json:"suspect,omitempty"`
	ElapsedSeconds   float64 `json:"elapsed_seconds,omitempty"`
	DownloadSeconds  float64 `json:"download_seconds,omitempty"`
	BytesDownloaded  int64   `json:"bytes_downloaded,omitempty"`
	FileSize         int64   `json:"file_size,omitempty"`
	AverageSpeed     float64 `json:"average_speed,omitempty"`
	Speed            float64 `json:"speed,omitempty"`
	EstimatedBytes   int64   `json:"estimated_bytes,omitempty"`
	ETASeconds       float64 `json:"eta_seconds,omitempty"`
	MissingSegments     []int   `json:"missing_segments,omitempty"`
	MissingDuration     float64 `json:"missing_duration_seconds,omitempty"`
	UnexplainedDuration float64 `json:"unexplained_seconds,omitempty"`
	Gaps                []Gap   `json:"gaps,omitempty"`
	Incomplete          bool    `json:"incomplete,omitempty"`
	Bitrate             int64   `json:"bitrate,omitempty"`
}

// Gap is a discontinuity in the finished file. AtSeconds is measured on the
// output's own timeline — the amount of playable content before the gap — so it
// is where a viewer actually lands, not where the content sat in the source.
type Gap struct {
	AtSeconds      float64 `json:"at_seconds"`
	MissingSeconds float64 `json:"missing_seconds"`
}

type Reporter interface {
	Event(Event)
	Close()
}

type HumanReporter struct {
	writer       io.Writer
	errors       []string
	lastSpeed    float64
	lastEstimate int64
	lastETA      float64
}

func NewHumanReporter(writer io.Writer) *HumanReporter {
	if writer == nil {
		writer = os.Stdout
	}
	return &HumanReporter{writer: writer}
}

func (r *HumanReporter) Event(ev Event) {
	switch ev.Type {
	case EventPlaylistReady:
		line := fmt.Sprintf("[playlist] %s, %s", pluralSegments(ev.Total), formatClock(ev.ExpectedDuration))
		if ev.EstimatedBytes > 0 {
			line += fmt.Sprintf(", ~%s estimated", humanizeBytesRough(ev.EstimatedBytes))
		}
		if ev.Bitrate > 0 {
			line += fmt.Sprintf(" (%s)", humanizeBitrate(ev.Bitrate))
		}
		fmt.Fprintln(r.writer, line)
	case EventDownloadProgress:
		// Sampler ticks carry live figures; worker redraws in between do not, so
		// the last known values are kept to avoid the line flickering.
		if ev.Speed > 0 {
			r.lastSpeed = ev.Speed
		}
		if ev.EstimatedBytes > 0 {
			r.lastEstimate = ev.EstimatedBytes
		}
		if ev.ETASeconds > 0 {
			r.lastETA = ev.ETASeconds
		}
		drawStatus(r.writer, statusLine("DL", ev.Percent/100,
			r.lastEstimate, r.lastSpeed, r.lastETA, ev.Done, ev.Total))
	case EventSegmentFailed:
		r.errors = append(r.errors, ev.Message)
	case EventDownloadDone:
		fmt.Fprintln(r.writer)
		for _, err := range r.errors {
			fmt.Fprintln(r.writer, err)
		}
	case EventMergeProgress:
		drawStatus(r.writer, statusLine("Merge", ev.Percent/100, 0, 0, 0, ev.Done, ev.Total))
	case EventWarning:
		fmt.Fprintf(r.writer, "\n[warning] %s", ev.Message)
	case EventMergeDone:
		fmt.Fprintf(r.writer, "\n[output] %s\n", ev.TSFile)
	case EventConversionStarted:
		fmt.Fprintf(r.writer, "[conversion] %s -> %s\n", ev.TSFile, ev.MP4File)
	case EventConversionDone:
		fmt.Fprintf(r.writer, "[output] %s\n", ev.MP4File)
	case EventConversionFailed:
		fmt.Fprintf(r.writer, "[conversion failed] %s\n", ev.Message)
	case EventConversionSuspect:
		fmt.Fprintf(r.writer, "[conversion suspect] %s\n", ev.Message)
	case EventTSDeleted:
		fmt.Fprintf(r.writer, "[cleanup] removed %s\n", ev.TSFile)
	case EventTaskDone:
		r.printSummary(ev)
	case EventError:
		fmt.Fprintf(r.writer, "[error] %s\n", ev.Message)
	}
}

func (r *HumanReporter) printSummary(ev Event) {
	if ev.ElapsedSeconds == 0 && ev.BytesDownloaded == 0 && ev.FileSize == 0 {
		return
	}
	fmt.Fprintf(r.writer, "[summary] time %s | size %s | avg %s\n",
		humanizeDuration(ev.ElapsedSeconds),
		humanizeBytes(ev.FileSize),
		humanizeSpeed(ev.AverageSpeed))
	if ev.Incomplete {
		r.printGaps(ev)
	}
}

// printGaps is the single report for everything that came out wrong, whether it
// was found by tracking failed segments or by the duration probe. It reports in
// terms a viewer can act on — how much is gone, and where in the finished file
// the jumps land. Segment indexes stay in the JSON events for tools.
func (r *HumanReporter) printGaps(ev Event) {
	// A longer-than-expected output is a mismatch, not a loss: report it as
	// such rather than claiming content is missing.
	if ev.MissingDuration <= 0 {
		if ev.UnexplainedDuration < 0 {
			fmt.Fprintf(r.writer, "[suspect] duration mismatch: expected %s, got %s\n",
				formatClock(ev.ExpectedDuration), formatClock(ev.ActualDuration))
		}
		return
	}

	line := fmt.Sprintf("[incomplete] lost %s", humanizeDuration(ev.MissingDuration))
	if ev.ExpectedDuration > 0 {
		line += " of " + formatClock(ev.ExpectedDuration)
		if share := formatShare(ev.MissingDuration, ev.ExpectedDuration); share != "" {
			line += " (" + share + ")"
		}
	}
	// With no failed segments there are no positions to give, so say so on the
	// same line rather than printing an empty gaps list.
	if len(ev.Gaps) == 0 {
		line += ", location unknown"
	}
	fmt.Fprintln(r.writer, line)

	if len(ev.Gaps) > 0 {
		marks := make([]string, 0, len(ev.Gaps)+1)
		for _, gap := range ev.Gaps {
			marks = append(marks, fmt.Sprintf("%s (-%s)",
				formatClock(gap.AtSeconds), humanizeDuration(gap.MissingSeconds)))
		}
		if ev.UnexplainedDuration > 0 {
			marks = append(marks, "+"+humanizeDuration(ev.UnexplainedDuration)+" elsewhere")
		}
		fmt.Fprintf(r.writer, "[gaps] %s\n", strings.Join(marks, ", "))
	}
	if ev.TSDir != "" {
		fmt.Fprintf(r.writer, "[segments] kept for re-fetch: %s\n", ev.TSDir)
	}
}

func (r *HumanReporter) Close() {}

type JSONReporter struct {
	encoder *json.Encoder
}

func NewJSONReporter(writer io.Writer) *JSONReporter {
	if writer == nil {
		writer = os.Stdout
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return &JSONReporter{encoder: encoder}
}

func (r *JSONReporter) Event(ev Event) {
	_ = r.encoder.Encode(ev)
}

func (r *JSONReporter) Close() {}

func humanizeDuration(seconds float64) string {
	if seconds <= 0 {
		return "0s"
	}
	d := time.Duration(seconds * float64(time.Second))
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

// autoDecimals asks formatBytes to pick the precision from the unit.
const autoDecimals = -1

// humanizeBytes formats a known, measured size, where full precision is wanted.
func humanizeBytes(n int64) string {
	return formatBytes(n, 2)
}

// humanizeBytesRough formats a projected size. Decimals only earn their space
// at GB and above — "1.9 GB" says something meaningfully different from "2 GB",
// whereas the .3 in "9.3 MB" is noise on an estimate.
func humanizeBytesRough(n int64) string {
	return formatBytes(n, autoDecimals)
}

func formatBytes(n int64, decimals int) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	if decimals == autoDecimals {
		// exp 0=KB, 1=MB, 2=GB: keep a decimal from GB upwards.
		if exp >= 2 {
			decimals = 1
		} else {
			decimals = 0
		}
	}
	return fmt.Sprintf("%.*f %s", decimals, float64(n)/float64(div), units[exp])
}

// formatPercent renders progress as a fixed-width whole percentage. It floors
// rather than rounds so the line cannot read "100%" while work is still in
// flight; two decimals were false precision on a progress bar.
func formatPercent(proportion float64) string {
	return fmt.Sprintf("%3.0f%%", math.Floor(proportion*100))
}

// formatClock renders a position within a video as m:ss (or h:mm:ss). Unlike
// formatETA it has no "unknown" case and no upper bound: 0:00 is a real
// position rather than a missing value.
func formatClock(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds + 0.5)
	hours := total / 3600
	minutes := (total % 3600) / 60
	secs := total % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

// formatShare renders a proportion of the whole, avoiding a misleading "0.0%"
// for a loss that is small but not nothing.
func formatShare(part float64, whole float64) string {
	if whole <= 0 || part <= 0 {
		return ""
	}
	percent := part / whole * 100
	if percent < 0.1 {
		return "<0.1%"
	}
	return fmt.Sprintf("%.1f%%", percent)
}

// formatETA renders remaining time as m:ss (or h:mm:ss), the compact form
// download tools conventionally use. It returns "" when there is nothing
// meaningful to show, and refuses to quote absurd values from a stalled start.
func formatETA(seconds float64) string {
	const maxETA = 24 * 60 * 60
	if seconds <= 0 || seconds > maxETA {
		return ""
	}
	// Round up: while any bytes remain, "0:01" is honest where "0:00" would
	// read as already finished.
	total := int(math.Ceil(seconds))
	hours := total / 3600
	minutes := (total % 3600) / 60
	secs := total % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

// humanizeBitrate formats a declared stream bitrate, which playlists quote in
// bits per second rather than bytes.
func humanizeBitrate(bitsPerSecond int64) string {
	switch {
	case bitsPerSecond >= 1_000_000:
		return fmt.Sprintf("%.1f Mbps", float64(bitsPerSecond)/1_000_000)
	case bitsPerSecond >= 1_000:
		return fmt.Sprintf("%.0f kbps", float64(bitsPerSecond)/1_000)
	default:
		return fmt.Sprintf("%d bps", bitsPerSecond)
	}
}

// humanizeSpeed formats a throughput. One decimal is plenty for a figure that
// moves every second.
func humanizeSpeed(bytesPerSecond float64) string {
	if bytesPerSecond <= 0 {
		return "0 B/s"
	}
	return formatBytes(int64(bytesPerSecond), 1) + "/s"
}

// drawStatus redraws the single-line status in place, trimmed to the terminal
// so it can never wrap onto a second row.
func drawStatus(writer io.Writer, line string) {
	if cols := terminalWidth(writer); cols > 0 {
		// \033[K clears any leftovers from a previous, longer line.
		fmt.Fprintf(writer, "\r%s\033[K", truncateRunes(line, cols-1))
		return
	}
	fmt.Fprintf(writer, "\r%s", line)
}

// statusLine assembles the progress line, e.g.
//
//	[DL]  50% of ~256 MB at 1.6 MB/s  ETA 2:05  seg 4/8
//
// The figures become known progressively — size, speed and ETA only exist once
// the sampler has ticked — so each clause is included only when it has a value
// and the line reads correctly at every stage.
func statusLine(prefix string, proportion float64, estimate int64, speed float64, etaSeconds float64, done int, total int) string {
	if proportion < 0 {
		proportion = 0
	}
	if proportion > 1 {
		proportion = 1
	}
	// "50% of ~256 MB at 1.6 MB/s" reads as one phrase: the connectives say
	// which quantity the percentage and the rate belong to.
	phrase := formatPercent(proportion)
	if estimate > 0 {
		phrase += " of ~" + humanizeBytesRough(estimate)
	}
	if speed > 0 {
		phrase += " at " + humanizeSpeed(speed)
	}
	groups := []string{phrase}
	if eta := formatETA(etaSeconds); eta != "" {
		groups = append(groups, "ETA "+eta)
	}
	if total > 0 {
		groups = append(groups, fmt.Sprintf("seg %d/%d", done, total))
	}
	return "[" + prefix + "] " + strings.Join(groups, "  ")
}

// truncateRunes shortens s to at most max display columns, marking a cut with an
// ellipsis. It counts runes rather than bytes so multi-byte characters are not
// split mid-encoding.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// terminalWidth reports the column count when writer is a terminal, else 0.
func terminalWidth(writer io.Writer) int {
	f, ok := writer.(*os.File)
	if !ok {
		return 0
	}
	if w := terminalWidthFD(f.Fd()); w > 0 {
		return w
	}
	if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && cols > 0 {
		return cols
	}
	return 0
}
