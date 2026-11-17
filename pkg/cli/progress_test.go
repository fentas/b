package cli

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// afterStop is a writer that records what is written to it, and fails the
// test when something is written after stopped is set.
type afterStop struct {
	t       *testing.T
	mu      sync.Mutex
	buf     bytes.Buffer
	stopped bool
}

func (w *afterStop) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		w.t.Errorf("a write after Stop: %q", p)
	}
	return w.buf.Write(p)
}

// stopWithin runs stop and fails the test when it does not return in time.
func stopWithin(t *testing.T, stop func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
}

// TestProgressRun_StopWithoutTrackers: without a tracker nothing renders,
// and Stop returns at once.
func TestProgressRun_StopWithoutTrackers(t *testing.T) {
	w := &afterStop{t: t}
	pw := newProgressRun(w)
	stopWithin(t, pw.Stop)
	if w.buf.Len() != 0 {
		t.Errorf("output without a tracker: %q", w.buf.String())
	}
}

// TestProgressRun_StopRendersTheLastFrame: trackers that are done at once
// (as a kept binary is) are rendered with their last message, and nothing is
// written after Stop. Under -race this also checks that Stop does not race
// with the start of the rendering.
func TestProgressRun_StopRendersTheLastFrame(t *testing.T) {
	for i := 0; i < 5; i++ {
		w := &afterStop{t: t}
		pw := newProgressRun(w)
		var wg sync.WaitGroup
		for _, name := range []string{"one", "two", "three"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tr := pw.AddTracker("Installing "+name, 0)
				tr.UpdateMessage(name + " done")
				tr.MarkAsDone()
			}()
		}
		wg.Wait()
		stopWithin(t, pw.Stop)
		w.mu.Lock()
		w.stopped = true
		out := w.buf.String()
		w.mu.Unlock()
		for _, name := range []string{"one", "two", "three"} {
			if !strings.Contains(out, name+" done") {
				t.Errorf("run %d: the output has no last message of %s:\n%s", i, name, out)
			}
		}
	}
	// Each Render has returned: a late tick of one would write by now.
	time.Sleep(300 * time.Millisecond)
}
