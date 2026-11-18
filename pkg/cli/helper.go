package cli

import (
	"fmt"
	"sync"
	"time"

	"github.com/fentas/goodies/progress"

	"github.com/fentas/b/pkg/binary"
)

func (o *CmdBinaryOptions) lookupLocals() ([]*binary.LocalBinary, error) {
	wg := sync.WaitGroup{}
	ch := make(chan *binary.LocalBinary, 1)

	for b, do := range o.ensure {
		if *do {
			wg.Add(1)
			go func() {
				ch <- b.LocalBinary(true)
				wg.Done()
			}()
		}
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	locals := make([]*binary.LocalBinary, 0)
	for l := range ch {
		locals = append(locals, l)
	}

	return locals, nil
}

// installBinaries installs the binaries of -i (with -u: updates them, with
// -f: downloads them again). A failed download stops nothing else. When
// downloads failed, it returns their error (failedDownloads), as b install
// and b update do, so the command exits 1.
func (o *CmdBinaryOptions) installBinaries() error {
	wg := sync.WaitGroup{}
	pw := newProgressRun(o.IO.Out)
	defer pw.Stop()

	// The error of each binary whose download failed. The goroutines write
	// it under failedMu.
	var failedMu sync.Mutex
	failed := make(map[*binary.Binary]error)

	for b, do := range o.ensure {
		if *do {
			wg.Add(1)

			go func(b *binary.Binary) {
				b.Writer = pw.Writer
				b.Tracker = pw.AddTracker(fmt.Sprintf("Ensuring %s is installed", b.Name), 0)

				var err error
				if o.force {
					err = b.DownloadBinary()
				} else {
					err = b.EnsureBinary(o.update)
				}
				if err != nil {
					failedMu.Lock()
					failed[b] = err
					failedMu.Unlock()
				}

				progress.ProgressDone(
					b.Tracker,
					fmt.Sprintf("%s is installed", b.Name),
					err,
				)
				wg.Done()
			}(b)
		}
	}
	wg.Wait()
	// let the progress bar render
	time.Sleep(200 * time.Millisecond)
	// o.Binaries holds the binaries of o.ensure in a fixed order: the error
	// has one line per failed binary, in that order.
	return failedDownloads(o.Binaries, failed, "")
}
