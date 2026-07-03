package dl

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
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
}

type Reporter interface {
	Event(Event)
	Close()
}

type HumanReporter struct {
	writer io.Writer
	errors []string
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
		drawBar(r.writer, "Downloading", ev.Percent/100, progressWidth,
			fmt.Sprintf("%d/%d", ev.Done, ev.Total))
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
	case EventError:
		fmt.Fprintf(r.writer, "[error] %s\n", ev.Message)
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

func drawBar(writer io.Writer, prefix string, proportion float64, width int, suffix string) {
	pos := int(proportion * float64(width))
	bar := strings.Repeat("■", pos) + strings.Repeat(" ", width-pos)
	fmt.Fprintf(writer, "\r[%s] [%s] %6.2f%% %s", prefix, bar, proportion*100, suffix)
}
