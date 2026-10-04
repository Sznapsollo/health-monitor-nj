package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func TestConcurrentWritersDoNotFailBusy(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	const writers, batches, rows = 4, 20, 200
	var wg sync.WaitGroup
	errs := make(chan error, writers*batches)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range batches {
				day := base.AddDate(0, 0, b%5)
				batch := make([]store.LogRow, 0, rows)
				for i := range rows {
					batch = append(batch, logRow(day.Add(time.Duration(i)*time.Second),
						fmt.Sprintf("kind%d", (w+b)%3), map[string]any{"n": i}))
				}
				if err := s.WriteLogs(ctx, batch); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
