package cli

import (
	"fmt"
	"sync"

	"github.com/buyoio/goodies/progress"

	"github.com/buyoio/b/pkg/binary"
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
			tracker := pw.AddTracker(fmt.Sprintf("Ensuring %s is installed", b.Name), 0)

			go func() {
				b.Tracker = tracker
				progress.ProgressDone(
					tracker,
					fmt.Sprintf("%s is installed", b.Name),
					b.EnsureBinary(o.force),
				)
				wg.Done()
			}()
		}
	}
	wg.Wait()
	return nil
}
