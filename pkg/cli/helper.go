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
				ch <- b.LocalBinary()
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

func (o *CmdBinaryOptions) installBinaries() error {
	wg := sync.WaitGroup{}
	pw := progress.NewWriter(progress.StyleDownload, o.IO.Out)
	pw.Style().Visibility.Percentage = true
	go pw.Render()
	defer pw.Stop()

	for b, do := range o.ensure {
		if *do {
			wg.Add(1)

			go func(b *binary.Binary) {
				tracker := pw.AddTracker(fmt.Sprintf("Ensuring %s is installed", b.Name), 0)
				b.Tracker = tracker
				b.Writer = pw

				var err error
				if o.force {
					err = b.DownloadBinary()
				} else {
					err = b.EnsureBinary(o.update)
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
	return nil
}
