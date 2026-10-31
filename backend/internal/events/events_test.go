package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
)

type fakeStore struct {
	mu    sync.Mutex
	names []string
	err   error
}

func (f *fakeStore) Add(_ context.Context, _ int64, name string, _ Props) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names = append(f.names, name)
	return f.err
}

func TestTrack(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &fakeStore{}
	tr := NewTracker(store, log)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the request is over: the event still gets written
	tr.Track(ctx, 1, Start, nil)
	tr.Wait()
	if len(store.names) != 1 {
		t.Errorf("written = %q", store.names)
	}

	store.err = errors.New("db down")
	tr.Track(context.Background(), 1, Start, nil) // logged, nothing else
	tr.Wait()

	var off *Tracker
	off.Track(context.Background(), 1, Start, nil)
	off.Wait()
}
