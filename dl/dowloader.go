package dl

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
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
	progressWidth     = 40
)

type Downloader struct {
	lock     sync.Mutex
	queue    []int
	folder   string
	tsFolder string
	filename string
	finish   int32
	segLen   int
	bytes    int64

	result   *parse.Result
	progress chan Event // all goroutines send here; reporter owns stdout
	reporter Reporter
	remuxer  Remuxer
	prober   DurationProber

	convertToMP4 bool
	keepTS       bool
}

// NewTask returns a Task instance
func NewTask(output string, url string, name string) (*Downloader, error) {
	result, err := parse.FromURL(url)
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
	}
	d.segLen = len(result.M3u8.Segments)
	d.queue = genSlice(d.segLen)
	return d, nil
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

// Start runs the downloader with a concurrent progress bar.
func (d *Downloader) Start(concurrency int) error {
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
				select {
				case d.progress <- ev:
				default:
				}
			}
		}
	}()

	// --- download workers ---
	limitChan := make(chan struct{}, concurrency)
	for {
		tsIdx, end, err := d.next()
		if err != nil {
			if end {
				break
			}
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if err := d.download(idx); err != nil {
				d.progress <- segmentFailedEvent(idx, fmt.Sprintf("[failed] seg %d: %s", idx, err.Error()))
				if err := d.back(idx); err != nil {
					d.progress <- Event{Type: EventWarning, Message: err.Error()}
				}
			}
			<-limitChan
		}(tsIdx)
		limitChan <- struct{}{}
	}
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

	tsFile, err := d.merge()
	if err != nil {
		return err
	}
	taskDone := Event{Type: EventTaskDone, TSFile: tsFile}
	if d.convertToMP4 {
		mp4File, err := d.convert(tsFile)
		if err != nil {
			return err
		}
		taskDone.MP4File = mp4File
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
	// notify reporter (non-blocking: channel is buffered)
	select {
	case d.progress <- progressEvent(EventDownloadProgress, int(atomic.LoadInt32(&d.finish)), d.segLen):
	default:
	}
	return nil
}

func (d *Downloader) next() (segIndex int, end bool, err error) {
	d.lock.Lock()
	defer d.lock.Unlock()
	if len(d.queue) == 0 {
		err = fmt.Errorf("queue empty")
		// d.finish is updated atomically by worker goroutines that do not hold
		// d.lock, so it must be read atomically here too.
		if atomic.LoadInt32(&d.finish) == int32(d.segLen) {
			end = true
			return
		}
		end = false
		return
	}
	segIndex = d.queue[0]
	d.queue = d.queue[1:]
	return
}

func (d *Downloader) back(segIndex int) error {
	d.lock.Lock()
	defer d.lock.Unlock()
	if sf := d.result.M3u8.Segments[segIndex]; sf == nil {
		return fmt.Errorf("invalid segment index: %d", segIndex)
	}
	d.queue = append(d.queue, segIndex)
	return nil
}

func (d *Downloader) merge() (string, error) {
	d.reporter.Event(Event{Type: EventMergeStarted, Total: d.segLen})
	missingCount := 0
	for idx := 0; idx < d.segLen; idx++ {
		tsFilename := tsFilename(idx)
		f := filepath.Join(d.tsFolder, tsFilename)
		if _, err := os.Stat(f); err != nil {
			missingCount++
		}
	}
	if missingCount > 0 {
		d.reporter.Event(Event{Type: EventWarning, Message: fmt.Sprintf("%d files missing", missingCount)})
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
	_ = os.RemoveAll(d.tsFolder)

	if mergedCount != d.segLen {
		d.reporter.Event(Event{Type: EventWarning, Message: fmt.Sprintf("%d files merge failed", d.segLen-mergedCount)})
	}
	d.reporter.Event(Event{Type: EventMergeDone, TSFile: mFilePath})
	return mFilePath, nil
}

func (d *Downloader) convert(tsFile string) (string, error) {
	mp4File := mp4Filename(tsFile)
	d.reporter.Event(Event{Type: EventConversionStarted, TSFile: tsFile, MP4File: mp4File})
	if err := d.remuxer.RemuxTS(tsFile, mp4File); err != nil {
		d.reporter.Event(Event{Type: EventConversionFailed, TSFile: tsFile, MP4File: mp4File, Message: err.Error()})
		return "", err
	}
	expected := d.expectedDuration()
	actual, err := d.prober.Duration(mp4File)
	if err != nil {
		d.reporter.Event(Event{Type: EventConversionFailed, TSFile: tsFile, MP4File: mp4File, Message: err.Error()})
		return "", err
	}
	suspect := durationSuspect(expected, actual)
	if suspect {
		suspectFile := suspectMP4Filename(mp4File, expected)
		if err := os.Rename(mp4File, suspectFile); err != nil {
			d.reporter.Event(Event{Type: EventConversionFailed, TSFile: tsFile, MP4File: mp4File, Message: err.Error()})
			return "", err
		}
		mp4File = suspectFile
		d.reporter.Event(Event{
			Type:             EventConversionSuspect,
			TSFile:           tsFile,
			MP4File:          mp4File,
			ExpectedDuration: expected,
			ActualDuration:   actual,
			Suspect:          true,
			Message:          fmt.Sprintf("expected %.3fs, got %.3fs", expected, actual),
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
	if suspect {
		return mp4File, nil
	}
	if !d.keepTS {
		d.cleanupTS(tsFile)
	}
	return mp4File, nil
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

func genSlice(len int) []int {
	s := make([]int, 0)
	for i := 0; i < len; i++ {
		s = append(s, i)
	}
	return s
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
