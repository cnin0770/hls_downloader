package dl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cnin0770/hls_downloader/parse"
	"github.com/cnin0770/hls_downloader/tool"
)

const (
	tsExt             = ".ts"
	defaultTimeLayout = "20060102150405"
	tsTempFileSuffix  = "_tmp"

	// DefaultRetries is how many times a segment is attempted before it is
	// given up on. Attempts are spaced by retryBackoff, capped at maxBackoff.
	DefaultRetries = 3
	retryBackoff   = time.Second
	maxBackoff     = 8 * time.Second
)

type Downloader struct {
	folder   string
	tsFolder string
	filename string
	finish   int32
	segLen   int
	bytes    int64
	doneMS   int64 // playlist duration of completed segments, in milliseconds
	doneB    int64 // bytes written by completed segments

	failedMu   sync.Mutex
	failedSegs []int // segments given up on after exhausting their retries

	result   *parse.Result
	warnings []string   // raised by preflight, emitted once the reporter exists
	progress chan Event // all goroutines send here; reporter owns stdout
	reporter Reporter
	remuxer  Remuxer
	prober   DurationProber

	retries      int
	convertToMP4 bool
	keepTS       bool
	keepSegments bool
	keptSegments bool // set when the segment folder was actually retained
}

// NewTask returns a Task instance
func NewTask(output string, url string, name string) (*Downloader, error) {
	result, err := parse.FromURL(url)
	if err != nil {
		return nil, err
	}
	// Reject what cannot work before creating any folders or fetching anything:
	// the alternative is a full download that was never going to produce a
	// usable file.
	warnings, err := preflight(result, url)
	if err != nil {
		return nil, err
	}
	var folder string
	if output == "" {
		current, err := tool.CurrentDir()
		if err != nil {
			return nil, err
		}
		folder = filepath.Join(current, output)
	} else {
		folder = output
	}
	if err := os.MkdirAll(folder, os.ModePerm); err != nil {
		return nil, fmt.Errorf("create storage folder failed: %s", err.Error())
	}
	taskName := taskTimestamp()
	tsFolder := filepath.Join(folder, taskName)
	if err := os.MkdirAll(tsFolder, os.ModePerm); err != nil {
		return nil, fmt.Errorf("create ts folder '[%s]' failed: %s", tsFolder, err.Error())
	}
	d := &Downloader{
		folder:   folder,
		tsFolder: tsFolder,
		filename: outputFilename(name, taskName),
		result:   result,
		// buffer generously so download goroutines never block on reporting
		progress: make(chan Event, 128),
		reporter: NewHumanReporter(os.Stdout),
		remuxer:  FFmpegRemuxer{},
		prober:   FFprobeDurationProber{},
		keepTS:   true,
		retries:  DefaultRetries,
		warnings: warnings,
	}
	d.segLen = len(result.M3u8.Segments)
	return d, nil
}

// SetRetries sets how many times each segment is attempted before being given
// up on. Values below 1 fall back to a single attempt.
func (d *Downloader) SetRetries(retries int) {
	if retries < 1 {
		retries = 1
	}
	d.retries = retries
}

// SetKeepSegments keeps the downloaded segment files when the download ends up
// incomplete, so the missing ones can be re-fetched and merged in. A complete
// download always cleans up: keeping a full copy of every segment alongside the
// finished file would just double the space used.
func (d *Downloader) SetKeepSegments(keep bool) {
	d.keepSegments = keep
}

func (d *Downloader) SetReporter(reporter Reporter) {
	if reporter == nil {
		reporter = NewHumanReporter(os.Stdout)
	}
	d.reporter = reporter
}

func (d *Downloader) SetConversion(convertToMP4 bool, keepTS bool) {
	d.convertToMP4 = convertToMP4
	d.keepTS = keepTS
}

func (d *Downloader) SetRemuxer(remuxer Remuxer) {
	if remuxer == nil {
		remuxer = FFmpegRemuxer{}
	}
	d.remuxer = remuxer
}

func (d *Downloader) SetDurationProber(prober DurationProber) {
	if prober == nil {
		prober = FFprobeDurationProber{}
	}
	d.prober = prober
}

// Start downloads, merges and optionally converts the playlist.
func (d *Downloader) Start(concurrency int) error {
	return d.StartContext(context.Background(), concurrency)
}

// StartContext is Start with cancellation: when ctx is cancelled the workers
// stop taking new segments and the run unwinds instead of finishing.
func (d *Downloader) StartContext(ctx context.Context, concurrency int) error {
	var wg sync.WaitGroup
	taskStart := time.Now()

	// --- reporter goroutine: sole owner of stdout during download ---
	reporterDone := make(chan struct{})
	go func() {
		defer close(reporterDone)
		for ev := range d.progress {
			d.reporter.Event(ev)
		}
	}()
	// What is about to be downloaded, before it starts: a wrong URL or an
	// unwanted variant is obvious immediately rather than an hour later.
	d.reporter.Event(playlistSummary(d.result, d.segLen, d.expectedDuration()))
	for _, warning := range d.warnings {
		d.reporter.Event(Event{Type: EventWarning, Message: warning})
	}
	d.reporter.Event(Event{
		Type:             EventTaskStarted,
		OutputDir:        d.folder,
		TSDir:            d.tsFolder,
		BaseName:         strings.TrimSuffix(d.filename, tsExt),
		Total:            d.segLen,
		ExpectedDuration: d.expectedDuration(),
	})

	// --- live speed sampler: periodically emits a download_progress event
	// carrying the current download speed (bytes in the last interval), so the
	// progress bar keeps a fresh speed even between segment completions.
	sampleStop := make(chan struct{})
	sampleStopped := make(chan struct{})
	totalDuration := d.expectedDuration()
	go func() {
		defer close(sampleStopped)
		const interval = time.Second
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		lastTime := taskStart
		lastBytes := int64(0)
		for {
			select {
			case <-sampleStop:
				return
			case now := <-ticker.C:
				curBytes := atomic.LoadInt64(&d.bytes)
				dt := now.Sub(lastTime).Seconds()
				var speed float64
				if dt > 0 {
					speed = float64(curBytes-lastBytes) / dt
				}
				lastTime, lastBytes = now, curBytes
				done := int(atomic.LoadInt32(&d.finish))
				ev := progressEvent(EventDownloadProgress, done, d.segLen)
				ev.Speed = speed
				doneDuration := float64(atomic.LoadInt64(&d.doneMS)) / 1000
				fraction := completedFraction(doneDuration, totalDuration, done, d.segLen)
				// Extrapolate from completed segments only (see doneB/doneMS).
				ev.EstimatedBytes = extrapolateTotalBytes(atomic.LoadInt64(&d.doneB), fraction)
				// ETA uses the average speed since the task started rather than
				// the last interval: it is far steadier on bursty connections.
				if elapsed := now.Sub(taskStart).Seconds(); elapsed > 0 {
					ev.ETASeconds = estimateETASeconds(ev.EstimatedBytes-curBytes, float64(curBytes)/elapsed)
				}
				select {
				case d.progress <- ev:
				default:
				}
			}
		}
	}()

	// --- download workers ---
	// A fixed pool draining a job channel: it always terminates once the jobs
	// are exhausted, whether or not every segment succeeded. Sending on an
	// unbuffered channel provides the backpressure that a semaphore used to,
	// with no spinning while workers are busy.
	if concurrency < 1 {
		concurrency = 1
	}
	jobs := make(chan int)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				d.fetchSegment(ctx, idx)
			}
		}()
	}
dispatch:
	for i := 0; i < d.segLen; i++ {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	downloadElapsed := time.Since(taskStart)

	// stop the sampler and wait for it to exit before closing d.progress,
	// so it can never send on a closed channel.
	close(sampleStop)
	<-sampleStopped

	// signal reporter to finish and wait for it to drain
	close(d.progress)
	<-reporterDone
	d.reporter.Event(Event{Type: EventDownloadDone})

	if err := ctx.Err(); err != nil {
		return err
	}

	// Segments given up on are exact knowledge that the result is short, so the
	// output is marked incomplete without relying on the duration heuristic.
	// Failures are not announced here: they are folded into the single
	// completion report below, so one defect is not reported three times.
	failed := d.failedSegments()
	gaps, knownMissing := gapsFromFailures(d.playlistSegments(), failed)

	tsFile, err := d.merge(failed)
	if err != nil {
		return err
	}
	taskDone := Event{
		Type:             EventTaskDone,
		TSFile:           tsFile,
		MissingSegments:  failed,
		Gaps:             gaps,
		MissingDuration:  knownMissing,
		ExpectedDuration: d.expectedDuration(),
		Incomplete:       len(failed) > 0,
	}
	if d.keptSegments {
		taskDone.TSDir = d.tsFolder
	}
	if d.convertToMP4 {
		conv, err := d.convert(tsFile, len(failed) > 0, knownMissing)
		if err != nil {
			return err
		}
		taskDone.MP4File = conv.mp4File
		taskDone.ActualDuration = conv.actual
		if conv.hasUnexplained {
			taskDone.UnexplainedDuration = conv.unexplained
			taskDone.Incomplete = true
			// Only a shortfall adds to what was lost; a longer-than-expected
			// output is a mismatch, not missing content.
			if conv.unexplained > 0 {
				taskDone.MissingDuration += conv.unexplained
			}
		}
	}
	d.fillSummary(&taskDone, taskStart, downloadElapsed)
	d.reporter.Event(taskDone)
	d.reporter.Close()
	return nil
}

// fillSummary populates the task_done event with timing, size and throughput
// stats. Average speed is measured over the download phase only, since merge
// and conversion do not use the network.
func (d *Downloader) fillSummary(ev *Event, taskStart time.Time, downloadElapsed time.Duration) {
	downloaded := atomic.LoadInt64(&d.bytes)
	ev.BytesDownloaded = downloaded
	ev.ElapsedSeconds = time.Since(taskStart).Seconds()
	ev.DownloadSeconds = downloadElapsed.Seconds()
	if downloadElapsed > 0 {
		ev.AverageSpeed = float64(downloaded) / downloadElapsed.Seconds()
	}
	finalFile := ev.TSFile
	if ev.MP4File != "" {
		finalFile = ev.MP4File
	}
	if info, err := os.Stat(finalFile); err == nil {
		ev.FileSize = info.Size()
	}
}

func (d *Downloader) download(segIndex int) error {
	tsFilename := tsFilename(segIndex)
	tsUrl := d.tsURL(segIndex)
	b, e := tool.Get(tsUrl)
	if e != nil {
		return fmt.Errorf("request %s, %s", tsUrl, e.Error())
	}
	defer b.Close()
	fPath := filepath.Join(d.tsFolder, tsFilename)
	fTemp := fPath + tsTempFileSuffix
	f, err := os.Create(fTemp)
	if err != nil {
		return fmt.Errorf("create file: %s, %s", tsFilename, err.Error())
	}
	// Count bytes as they stream (not once at completion) so the live speed
	// sampler sees real throughput even mid-segment.
	bytes, err := ioutil.ReadAll(&countingReader{reader: b, counter: &d.bytes})
	if err != nil {
		return fmt.Errorf("read bytes: %s, %s", tsUrl, err.Error())
	}
	sf := d.result.M3u8.Segments[segIndex]
	if sf == nil {
		return fmt.Errorf("invalid segment index: %d", segIndex)
	}
	key, ok := d.result.Keys[sf.KeyIndex]
	if ok && key != "" {
		bytes, err = tool.AES128Decrypt(bytes, []byte(key),
			[]byte(d.result.M3u8.Keys[sf.KeyIndex].IV))
		if err != nil {
			return fmt.Errorf("decryt: %s, %s", tsUrl, err.Error())
		}
	}
	// Some TS files do not start with SyncByte 0x47; strip leading bytes before it.
	syncByte := uint8(71) // 0x47
	bLen := len(bytes)
	for j := 0; j < bLen; j++ {
		if bytes[j] == syncByte {
			bytes = bytes[j:]
			break
		}
	}
	w := bufio.NewWriter(f)
	if _, err := w.Write(bytes); err != nil {
		return fmt.Errorf("write to %s: %s", fTemp, err.Error())
	}
	_ = f.Close()
	if err = os.Rename(fTemp, fPath); err != nil {
		return err
	}
	atomic.AddInt32(&d.finish, 1)
	// Track completed segments' written size alongside their playlist duration.
	// Both counters advance together, so the size estimate is not skewed by
	// bytes from segments that are still in flight.
	atomic.AddInt64(&d.doneB, int64(len(bytes)))
	atomic.AddInt64(&d.doneMS, int64(float64(sf.Duration)*1000))
	// notify reporter (non-blocking: channel is buffered)
	select {
	case d.progress <- progressEvent(EventDownloadProgress, int(atomic.LoadInt32(&d.finish)), d.segLen):
	default:
	}
	return nil
}

// fetchSegment downloads one segment, retrying up to d.retries times with a
// growing pause between attempts. When the attempts are exhausted the segment
// is recorded as failed and the run continues: giving up on a segment is what
// lets the download terminate at all, so the caller must treat the recorded
// failures as an incomplete result rather than a success.
func (d *Downloader) fetchSegment(ctx context.Context, segIndex int) {
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return
		}
		err := d.download(segIndex)
		if err == nil {
			return
		}
		if attempt >= d.retries {
			d.recordFailure(segIndex, err)
			return
		}
		d.report(segmentFailedEvent(segIndex,
			fmt.Sprintf("[retry %d/%d] seg %d: %s", attempt, d.retries-1, segIndex, err.Error())))
		select {
		case <-time.After(retryDelay(attempt)):
		case <-ctx.Done():
			return
		}
	}
}

// retryDelay backs off between attempts so a struggling server is not hammered.
func retryDelay(attempt int) time.Duration {
	delay := retryBackoff << (attempt - 1)
	if delay > maxBackoff || delay <= 0 {
		return maxBackoff
	}
	return delay
}

func (d *Downloader) recordFailure(segIndex int, err error) {
	d.failedMu.Lock()
	d.failedSegs = append(d.failedSegs, segIndex)
	d.failedMu.Unlock()
	d.report(segmentFailedEvent(segIndex,
		fmt.Sprintf("[failed] seg %d after %s: %s", segIndex, plural(d.retries, "attempt"), err.Error())))
}

// failedSegments returns the sorted indexes of segments that were given up on.
func (d *Downloader) failedSegments() []int {
	d.failedMu.Lock()
	defer d.failedMu.Unlock()
	out := append([]int(nil), d.failedSegs...)
	sort.Ints(out)
	return out
}

// report queues an event without ever blocking a worker.
func (d *Downloader) report(ev Event) {
	select {
	case d.progress <- ev:
	default:
	}
}

// gapsFromFailures maps failed segment indexes onto the finished file's own
// timeline. A gap is a point, not a span: the missing content is simply absent,
// so everything after it shifts earlier and consecutive failures collapse into
// one discontinuity. It also returns the total duration lost.
func gapsFromFailures(segments []*parse.Segment, failed []int) ([]Gap, float64) {
	if len(failed) == 0 || len(segments) == 0 {
		return nil, 0
	}
	missing := make(map[int]bool, len(failed))
	for _, seg := range failed {
		missing[seg] = true
	}
	var gaps []Gap
	var kept, lost float64
	inGap := false
	for idx, segment := range segments {
		var duration float64
		if segment != nil {
			duration = float64(segment.Duration)
		}
		if missing[idx] {
			lost += duration
			if inGap {
				// Still the same discontinuity: widen it rather than adding one.
				gaps[len(gaps)-1].MissingSeconds += duration
			} else {
				gaps = append(gaps, Gap{AtSeconds: kept, MissingSeconds: duration})
				inGap = true
			}
			continue
		}
		// Only surviving segments advance the output timeline.
		kept += duration
		inGap = false
	}
	return gaps, lost
}

// plural renders a count with its unit, adding an "s" only when needed, so
// messages never read "1 attempts".
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func pluralSegments(n int) string {
	return plural(n, "segment")
}

// summarizeSegments renders segment indexes compactly, collapsing runs into
// ranges so a long outage reads as "seg 41-89" rather than fifty numbers.
func summarizeSegments(segments []int) string {
	if len(segments) == 0 {
		return ""
	}
	const maxGroups = 5
	var groups []string
	start, prev := segments[0], segments[0]
	flush := func() {
		if start == prev {
			groups = append(groups, strconv.Itoa(start))
			return
		}
		groups = append(groups, fmt.Sprintf("%d-%d", start, prev))
	}
	for _, seg := range segments[1:] {
		if seg == prev+1 {
			prev = seg
			continue
		}
		flush()
		start, prev = seg, seg
	}
	flush()
	if len(groups) > maxGroups {
		return strings.Join(groups[:maxGroups], ", ") +
			fmt.Sprintf(", and %d more", len(groups)-maxGroups)
	}
	return strings.Join(groups, ", ")
}

// merge concatenates the downloaded segments. known lists segments already
// reported as failed, so merge only warns about files it finds missing for some
// other reason — the failures themselves have already been announced.
func (d *Downloader) merge(known []int) (string, error) {
	d.reporter.Event(Event{Type: EventMergeStarted, Total: d.segLen})
	expected := make(map[int]bool, len(known))
	for _, seg := range known {
		expected[seg] = true
	}
	var unexpected []int
	for idx := 0; idx < d.segLen; idx++ {
		f := filepath.Join(d.tsFolder, tsFilename(idx))
		if _, err := os.Stat(f); err != nil && !expected[idx] {
			unexpected = append(unexpected, idx)
		}
	}
	if len(unexpected) > 0 {
		d.reporter.Event(Event{
			Type:            EventWarning,
			MissingSegments: unexpected,
			Message: fmt.Sprintf("%s missing from disk before merge: %s",
				pluralSegments(len(unexpected)), summarizeSegments(unexpected)),
		})
	}
	mFilePath := filepath.Join(d.folder, d.filename)
	mFile, err := os.Create(mFilePath)
	if err != nil {
		return "", fmt.Errorf("create main TS file failed: %s", err.Error())
	}
	defer mFile.Close()

	writer := bufio.NewWriter(mFile)
	mergedCount := 0
	for segIndex := 0; segIndex < d.segLen; segIndex++ {
		tsFilename := tsFilename(segIndex)
		bytes, err := ioutil.ReadFile(filepath.Join(d.tsFolder, tsFilename))
		if err != nil {
			continue
		}
		_, err = writer.Write(bytes)
		if err != nil {
			continue
		}
		mergedCount++
		d.reporter.Event(progressEvent(EventMergeProgress, mergedCount, d.segLen))
	}
	_ = writer.Flush()
	if d.keepSegments && len(known) > 0 {
		// Retained only because segments are missing: the surviving files let
		// the gaps be re-fetched by index and merged in.
		d.keptSegments = true
	} else {
		_ = os.RemoveAll(d.tsFolder)
	}

	// Only surprises are worth a warning here: segments already given up on
	// during download were reported at the time.
	if short := d.segLen - mergedCount - len(known); short > 0 {
		d.reporter.Event(Event{
			Type:    EventWarning,
			Message: fmt.Sprintf("%s could not be merged", pluralSegments(short)),
		})
	}
	d.reporter.Event(Event{Type: EventMergeDone, TSFile: mFilePath})
	return mFilePath, nil
}

// conversion carries what the remux and duration probe learned, so the caller
// can fold it into a single completion report.
type conversion struct {
	mp4File        string
	actual         float64
	unexplained    float64
	hasUnexplained bool
}

// convert remuxes the merged TS to MP4. knownMissing is the duration already
// known to be absent because segments failed to download; anything short beyond
// that is unexplained and points at the remux or the source data rather than at
// the download.
func (d *Downloader) convert(tsFile string, incomplete bool, knownMissing float64) (conversion, error) {
	mp4File := mp4Filename(tsFile)
	d.reporter.Event(Event{Type: EventConversionStarted, TSFile: tsFile, MP4File: mp4File})
	if err := d.remuxer.RemuxTS(tsFile, mp4File); err != nil {
		d.reporter.Event(Event{Type: EventConversionFailed, TSFile: tsFile, MP4File: mp4File, Message: err.Error()})
		return conversion{}, err
	}
	expected := d.expectedDuration()
	actual, err := d.prober.Duration(mp4File)
	if err != nil {
		d.reporter.Event(Event{Type: EventConversionFailed, TSFile: tsFile, MP4File: mp4File, Message: err.Error()})
		return conversion{}, err
	}
	unexplained, hasUnexplained := unexplainedShortfall(expected, actual, knownMissing)
	suspect := incomplete || hasUnexplained
	if suspect {
		suspectFile := suspectMP4Filename(mp4File, expected)
		if err := os.Rename(mp4File, suspectFile); err != nil {
			d.reporter.Event(Event{Type: EventConversionFailed, TSFile: tsFile, MP4File: mp4File, Message: err.Error()})
			return conversion{}, err
		}
		mp4File = suspectFile
		reason := fmt.Sprintf("expected %.3fs, got %.3fs", expected, actual)
		if incomplete {
			reason = "segments are missing; " + reason
		}
		d.reporter.Event(Event{
			Type:             EventConversionSuspect,
			TSFile:           tsFile,
			MP4File:          mp4File,
			ExpectedDuration: expected,
			ActualDuration:   actual,
			Suspect:          true,
			Message:          reason,
		})
	}
	d.reporter.Event(Event{
		Type:             EventConversionDone,
		TSFile:           tsFile,
		MP4File:          mp4File,
		ExpectedDuration: expected,
		ActualDuration:   actual,
		Suspect:          suspect,
	})
	result := conversion{mp4File: mp4File, actual: actual, unexplained: unexplained, hasUnexplained: hasUnexplained}
	// The merged TS is only worth retaining when the remux itself is in doubt:
	// then it is the pristine copy and the MP4 is the questionable artifact. If
	// segments are simply missing, the TS has the identical gaps and offers no
	// recovery path, so the user's -keep-ts choice stands.
	if hasUnexplained {
		return result, nil
	}
	if !d.keepTS {
		d.cleanupTS(tsFile)
	}
	return result, nil
}

func (d *Downloader) cleanupTS(tsFile string) {
	if err := os.Remove(tsFile); err != nil {
		d.reporter.Event(Event{Type: EventWarning, TSFile: tsFile, Message: fmt.Sprintf("remove TS file failed: %s", err.Error())})
		return
	}
	d.reporter.Event(Event{Type: EventTSDeleted, TSFile: tsFile})
}

func (d *Downloader) tsURL(segIndex int) string {
	seg := d.result.M3u8.Segments[segIndex]
	return tool.ResolveURL(d.result.URL, seg.URI)
}

// playlistSegments returns the parsed segments, or nil when the playlist is
// unavailable (as in unit tests that build a Downloader directly).
func (d *Downloader) playlistSegments() []*parse.Segment {
	if d.result == nil || d.result.M3u8 == nil {
		return nil
	}
	return d.result.M3u8.Segments
}

func (d *Downloader) expectedDuration() float64 {
	if d.result == nil || d.result.M3u8 == nil {
		return 0
	}
	var total float64
	for _, segment := range d.result.M3u8.Segments {
		if segment != nil {
			total += float64(segment.Duration)
		}
	}
	return total
}

// completedFraction reports how much of the playlist is done, preferring
// playlist duration over segment counts: segments vary in length, so weighting
// by duration tracks a variable-bitrate stream far more closely than counting
// files. It falls back to the segment count when durations are unavailable.
func completedFraction(doneDuration float64, totalDuration float64, done int, total int) float64 {
	var fraction float64
	switch {
	case doneDuration > 0 && totalDuration > 0:
		fraction = doneDuration / totalDuration
	case total > 0:
		fraction = float64(done) / float64(total)
	}
	if fraction < 0 {
		return 0
	}
	if fraction > 1 {
		return 1
	}
	return fraction
}

// extrapolateTotalBytes estimates the final download size from the bytes seen so
// far. It returns 0 while there is not yet enough data to guess responsibly.
func extrapolateTotalBytes(bytesSoFar int64, fraction float64) int64 {
	if bytesSoFar <= 0 || fraction <= 0 {
		return 0
	}
	if fraction >= 1 {
		return bytesSoFar
	}
	return int64(float64(bytesSoFar) / fraction)
}

// estimateETASeconds returns the seconds of download remaining at the given
// speed, or 0 when it cannot be estimated.
func estimateETASeconds(remainingBytes int64, speed float64) float64 {
	if remainingBytes <= 0 || speed <= 0 {
		return 0
	}
	return float64(remainingBytes) / speed
}

// countingReader wraps an io.Reader and adds the number of bytes read to a
// shared atomic counter, so download throughput can be sampled while segments
// are still in flight.
type countingReader struct {
	reader  io.Reader
	counter *int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if n > 0 {
		atomic.AddInt64(c.counter, int64(n))
	}
	return n, err
}

func tsFilename(ts int) string {
	return strconv.Itoa(ts) + tsExt
}

func taskTimestamp() string {
	return time.Now().Format(defaultTimeLayout)
}

func outputFilename(name string, defaultName string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultName
	}
	name = filepath.Base(name)
	if filepath.Ext(name) == tsExt {
		return name
	}
	return name + tsExt
}

func progressEvent(eventType string, done int, total int) Event {
	var percent float64
	if total > 0 {
		percent = float64(done) / float64(total) * 100
	}
	return Event{
		Type:    eventType,
		Done:    done,
		Total:   total,
		Percent: percent,
	}
}

func segmentFailedEvent(segment int, message string) Event {
	return Event{
		Type:    EventSegmentFailed,
		Segment: &segment,
		Message: message,
	}
}
