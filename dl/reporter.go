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
		suffix := fmt.Sprintf("%d/%d", ev.Done, ev.Total)
		if r.lastSpeed > 0 {
			suffix += "  " + humanizeSpeed(r.lastSpeed)
		}
		if r.lastEstimate > 0 {
			suffix += "  ~" + humanizeBytes(r.lastEstimate)
		}
		if eta := formatETA(r.lastETA); eta != "" {
			suffix += "  ETA " + eta
		}
		drawBar(r.writer, "Downloading", ev.Percent/100, progressWidth, suffix)
	case EventSegmentFailed:
		r.errors = append(r.errors, ev.Message)
	case EventDownloadDone:
		fmt.Fprintln(r.writer)
		for _, err := range r.errors {
			fmt.Fprintln(r.writer, err)
		}
	case EventMergeProgress:
		drawBar(r.writer, "Merging", ev.Percent/100, progressWidth, "")
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

func humanizeBytes(n int64) string {
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
	return fmt.Sprintf("%.2f %s", float64(n)/float64(div), units[exp])
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

func humanizeSpeed(bytesPerSecond float64) string {
	if bytesPerSecond <= 0 {
		return "0 B/s"
	}
	return humanizeBytes(int64(bytesPerSecond)) + "/s"
}

func drawBar(writer io.Writer, prefix string, proportion float64, barWidth int, suffix string) {
	cols := terminalWidth(writer)
	line := renderBar(prefix, proportion, barWidth, suffix, cols)
	if cols > 0 {
		// The line is sized to fit the terminal, so it never wraps onto extra
		// rows; \033[K clears any leftovers from a previous, longer line.
		fmt.Fprintf(writer, "\r%s\033[K", line)
		return
	}
	fmt.Fprintf(writer, "\r%s", line)
}

// renderBar builds the status line, sized to fit cols columns (cols <= 0 means
// unlimited). It shrinks the bar first, then falls back to an ellipsis-truncated
// text line, so a narrow terminal never wraps the status onto multiple rows.
func renderBar(prefix string, proportion float64, barWidth int, suffix string, cols int) string {
	if proportion < 0 {
		proportion = 0
	}
	if proportion > 1 {
		proportion = 1
	}
	head := fmt.Sprintf("[%s] ", prefix)
	tail := fmt.Sprintf("%6.2f%%", proportion*100)
	if suffix != "" {
		tail += " " + suffix
	}
	if cols > 0 {
		budget := cols - 1
		if budget < 1 {
			budget = 1
		}
		// A bar narrower than this conveys almost nothing, so below it we drop
		// the bar entirely rather than show a 1-2 cell stub.
		const minBar = 4
		// fixed chars around the bar contents: "[" plus "] "
		fixed := utf8.RuneCountInString(head) + 3 + utf8.RuneCountInString(tail)
		avail := budget - fixed
		if avail < minBar {
			return compactLine(prefix, proportion, suffix, budget)
		}
		if avail < barWidth {
			barWidth = avail
		}
	}
	if barWidth < 0 {
		barWidth = 0
	}
	pos := int(proportion * float64(barWidth))
	if pos > barWidth {
		pos = barWidth
	}
	bar := strings.Repeat("■", pos) + strings.Repeat(" ", barWidth-pos)
	return head + "[" + bar + "] " + tail
}

// compactLine renders a bar-less status for terminals too narrow to fit a
// useful bar. It keeps the most informative fields, first dropping the phase
// label, and only ellipsis-truncates when even that will not fit.
func compactLine(prefix string, proportion float64, suffix string, budget int) string {
	parts := fmt.Sprintf("%.2f%%", proportion*100)
	if suffix != "" {
		parts += " " + suffix
	}
	if withPrefix := prefix + " " + parts; utf8.RuneCountInString(withPrefix) <= budget {
		return withPrefix
	}
	if utf8.RuneCountInString(parts) <= budget {
		return parts
	}
	return truncateRunes(parts, budget)
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
