package cli

import (
	"io"
	"sync"

	"github.com/fentas/goodies/progress"
	pretty "github.com/jedib0t/go-pretty/v6/progress"
)

// progressRun renders the progress trackers of one command, and stops the
// rendering without a data race.
//
// go-pretty's Render first marks the render as started, and then, without a
// lock, sets up the context that Stop cancels. A Stop between these two steps
// reads that context while Render writes it: a data race (go test -race
// reports it), or a call of a nil cancel func. A Stop before Render starts
// does nothing, and Render then runs on.
//
// progressRun therefore starts Render at the first tracker, and Stop waits
// for the first frame that Render writes, which Render writes only after the
// set-up. Stop then waits until Render has written its last frame and
// returned, so nothing writes to the output after Stop.
type progressRun struct {
	*progress.Writer
	out   *firstWrite
	start sync.Once
	done  chan struct{}
}

// newProgressRun returns a progressRun that writes to out.
func newProgressRun(out io.Writer) *progressRun {
	w := &firstWrite{Writer: out, written: make(chan struct{})}
	pw := progress.NewWriter(progress.StyleDownload, w)
	pw.Style().Visibility.Percentage = true
	return &progressRun{Writer: pw, out: w, done: make(chan struct{})}
}

// AddTracker adds a tracker. The first tracker starts the rendering.
func (r *progressRun) AddTracker(message string, total int64) *pretty.Tracker {
	t := r.Writer.AddTracker(message, total)
	r.start.Do(func() {
		go func() {
			defer close(r.done)
			r.Writer.Render()
		}()
	})
	return t
}

// Stop renders the last frame and stops the rendering. Call it once, when
// the trackers are done. Without a tracker, nothing was rendered, and Stop
// returns at once.
func (r *progressRun) Stop() {
	started := true
	r.start.Do(func() { started = false })
	if !started {
		return
	}
	<-r.out.written
	r.Writer.Stop()
	<-r.done
}

// firstWrite is a writer that closes written at its first Write.
type firstWrite struct {
	io.Writer
	once    sync.Once
	written chan struct{}
}

func (w *firstWrite) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.written) })
	return w.Writer.Write(p)
}
