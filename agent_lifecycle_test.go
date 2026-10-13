package tinyagent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/store"
)

type blockingStore struct {
	store.Store
	entered, release chan struct{}
	writes           atomic.Int32
}

func (s *blockingStore) Replace(ctx context.Context, id string, msgs []core.Message) error {
	if s.writes.Add(1) == 1 {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.Replace(ctx, id, msgs)
}

func TestPersistenceOwnsSessionUntilCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st := &blockingStore{Store: store.NewMemoryStore(), entered: make(chan struct{}), release: make(chan struct{})}
	mdl := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, Content: "a"}}, {Message: core.Message{Role: core.RoleAssistant, Content: "b"}}}}
	ag, _ := New(Config{Model: mdl, Store: st})
	done := make(chan error, 1)
	go func() { _, err := ag.Chat(ctx, "s", "first"); done <- err }()
	select {
	case <-st.entered:
	case <-ctx.Done():
		close(st.release)
		t.Fatal(ctx.Err())
	}
	_, err := ag.Chat(ctx, "s", "second")
	if !errors.Is(err, core.ErrSessionBusy) {
		close(st.release)
		t.Fatalf("err=%v want busy", err)
	}
	close(st.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Chat(ctx, "s", "second"); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Load(ctx, "s")
	if err != nil || len(snap.Messages) != 4 {
		t.Fatalf("snapshot=%+v err=%v", snap, err)
	}
}

type failingStore struct {
	store.Store
	err  error
	wait bool
}

func (s failingStore) Replace(ctx context.Context, _ string, _ []core.Message) error {
	if s.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return s.err
}
func TestPersistenceFailureReachesCaller(t *testing.T) {
	for _, wait := range []bool{false, true} {
		st := failingStore{Store: store.NewMemoryStore(), err: errors.New("disk full"), wait: wait}
		ag, _ := New(Config{Model: &scriptedModel{}, Store: st, PersistTimeout: 20 * time.Millisecond})
		_, err := ag.Chat(context.Background(), "s", "go")
		if err == nil {
			t.Fatal("persistence failure hidden")
		}
		if wait && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err=%v", err)
		}
		// A failed write must not strand the session lease.
		_, err = ag.Chat(context.Background(), "s", "retry")
		if errors.Is(err, core.ErrSessionBusy) {
			t.Fatal("session remained busy")
		}
	}
}

type loadingStore struct {
	store.Store
	entered, release chan struct{}
}

func (s loadingStore) Load(ctx context.Context, id string) (*store.SessionSnapshot, error) {
	if id == "slow" {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.Store.Load(ctx, id)
}
func TestSlowLoadDoesNotBlockOtherSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st := loadingStore{Store: store.NewMemoryStore(), entered: make(chan struct{}), release: make(chan struct{})}
	ag, _ := New(Config{Model: &scriptedModel{}, Store: st})
	done := make(chan error, 1)
	go func() { _, err := ag.Session(ctx, "slow"); done <- err }()
	select {
	case <-st.entered:
	case <-ctx.Done():
		close(st.release)
		t.Fatal(ctx.Err())
	}
	otherCtx, stop := context.WithTimeout(ctx, 200*time.Millisecond)
	defer stop()
	if _, err := ag.Session(otherCtx, "fast"); err != nil {
		close(st.release)
		t.Fatalf("unrelated session blocked: %v", err)
	}
	close(st.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunEndIsDeliveredAfterPersistence(t *testing.T) {
	st := store.NewMemoryStore()
	ag, _ := New(Config{Model: &scriptedModel{}, Store: st})
	for ev, err := range ag.Run(context.Background(), "s", "go") {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Type == core.EventRunEnd {
			snap, err := st.Load(context.Background(), "s")
			if err != nil || len(snap.Messages) != 2 {
				t.Fatalf("end delivered before commit: %+v %v", snap, err)
			}
		}
	}
}

func TestUnloadSessionPreservesStoreAndFreesCapacity(t *testing.T) {
	st := store.NewMemoryStore()
	ag, _ := New(Config{Model: &scriptedModel{}, Store: st, MaxCachedSessions: 1})
	if _, err := ag.Chat(context.Background(), "a", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Session(context.Background(), "b"); err == nil {
		t.Fatal("cache capacity not enforced")
	}
	if err := ag.UnloadSession(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Session(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if err := ag.UnloadSession(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	s, err := ag.Session(context.Background(), "a")
	if err != nil || s.Len() != 2 {
		t.Fatalf("history lost: session=%v err=%v", s, err)
	}
}

func TestUnloadSessionRejectsPersistenceInProgress(t *testing.T) {
	st := &blockingStore{Store: store.NewMemoryStore(), entered: make(chan struct{}), release: make(chan struct{})}
	ag, _ := New(Config{Model: &scriptedModel{}, Store: st})
	done := make(chan error, 1)
	go func() { _, err := ag.Chat(context.Background(), "s", "go"); done <- err }()
	<-st.entered
	if err := ag.UnloadSession(context.Background(), "s"); !errors.Is(err, core.ErrSessionBusy) {
		close(st.release)
		t.Fatalf("err=%v", err)
	}
	close(st.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTimeoutIncludesSessionLoad(t *testing.T) {
	st := loadingStore{Store: store.NewMemoryStore(), entered: make(chan struct{}), release: make(chan struct{})}
	ag, _ := New(Config{Model: &scriptedModel{}, Store: st, Timeout: 20 * time.Millisecond})
	_, err := ag.Chat(context.Background(), "slow", "go")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
