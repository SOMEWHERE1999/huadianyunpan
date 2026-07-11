package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ncepupan/hdd/internal/store/sqlite"
)

func TestWatcherDetectsNewFile(t *testing.T) {
	dir := t.TempDir()
	storeDir := t.TempDir()

	store, err := sqlite.Open(storeDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	events := make(chan [2]string, 10)
	w := New(store, func(local, remote string) {
		events <- [2]string{local, remote}
	})
	w.SetPollInterval(100 * time.Millisecond)
	w.SetDebounce(200 * time.Millisecond)
	w.AddRoot(dir, "/remote")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)
	f, _ := os.Create(filepath.Join(dir, "test.txt"))
	f.WriteString("hello")
	f.Close()

	select {
	case e := <-events:
		if e[0] != filepath.Join(dir, "test.txt") {
			t.Errorf("local = %q", e[0])
		}
		if e[1] != "/remote/test.txt" {
			t.Errorf("remote = %q", e[1])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for file event")
	}
}

func TestWatcherDebounce(t *testing.T) {
	dir := t.TempDir()
	storeDir := t.TempDir()

	store, _ := sqlite.Open(storeDir)
	defer store.Close()

	events := make(chan [2]string, 10)
	w := New(store, func(local, remote string) {
		events <- [2]string{local, remote}
	})
	w.SetPollInterval(100 * time.Millisecond)
	w.SetDebounce(500 * time.Millisecond)
	w.AddRoot(dir, "/remote")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	defer w.Stop()

	path := filepath.Join(dir, "rapid.txt")
	for i := 0; i < 5; i++ {
		f, _ := os.Create(path)
		f.WriteString("write")
		f.Close()
		time.Sleep(80 * time.Millisecond)
	}

	time.Sleep(800 * time.Millisecond)

	count := 0
	for {
		select {
		case <-events:
			count++
		default:
			goto done
		}
	}
done:
	if count != 1 {
		t.Errorf("expected 1 event after debounce, got %d", count)
	}
}

func TestWatcherRemotePathMapping(t *testing.T) {
	w := New(nil, nil)
	w.AddRoot("C:\\local\\dir", "/remote/root")
	mapped := w.remotePathFor(
		syncRoot{LocalPath: "C:\\local\\dir", RemotePath: "/remote/root"},
		"C:\\local\\dir\\sub\\file.txt",
	)
	if mapped != "/remote/root/sub/file.txt" {
		t.Errorf("mapped = %q, want /remote/root/sub/file.txt", mapped)
	}
}

func TestWatcherDebounceResets(t *testing.T) {
	dir := t.TempDir()
	storeDir := t.TempDir()
	store, _ := sqlite.Open(storeDir)
	defer store.Close()

	events := make(chan [2]string, 10)
	w := New(store, func(local, remote string) {
		events <- [2]string{local, remote}
	})
	w.SetPollInterval(50 * time.Millisecond)
	w.SetDebounce(300 * time.Millisecond)
	w.AddRoot(dir, "/remote")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	defer w.Stop()

	path := filepath.Join(dir, "continuous.txt")
	// Write continuously for 1 second (should NOT fire during active writes).
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			f, _ := os.Create(path)
			f.WriteString("write")
			f.Close()
			time.Sleep(50 * time.Millisecond)
		}
	}()
	<-done

	// After writes stop, wait for debounce + poll.
	time.Sleep(600 * time.Millisecond)

	count := 0
	for {
		select {
		case <-events:
			count++
		default:
			goto check
		}
	}
check:
	if count != 1 {
		t.Errorf("expected 1 event after continuous writes stop, got %d", count)
	}
}

func TestWatcherDetectsDeletion(t *testing.T) {
	dir := t.TempDir()
	storeDir := t.TempDir()
	store, _ := sqlite.Open(storeDir)
	defer store.Close()

	path := filepath.Join(dir, "delete-me.txt")
	os.WriteFile(path, []byte("hello"), 0600)

	uploadEvents := make(chan [2]string, 10)
	deleteEvents := make(chan [2]string, 10)
	w := New(store, func(local, remote string) {
		uploadEvents <- [2]string{local, remote}
	})
	w.SetDeleteFunc(func(local, remote string) {
		deleteEvents <- [2]string{local, remote}
	})
	w.SetPollInterval(100 * time.Millisecond)
	w.SetDebounce(100 * time.Millisecond)
	w.AddRoot(dir, "/remote")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	defer w.Stop()

	// Let the initial upload event fire.
	time.Sleep(400 * time.Millisecond)
	select {
	case <-uploadEvents:
	default:
	}

	// Delete the file.
	os.Remove(path)
	time.Sleep(600 * time.Millisecond)

	select {
	case e := <-deleteEvents:
		if e[0] != path {
			t.Errorf("delete local = %q, want %q", e[0], path)
		}
		if e[1] != "/remote/delete-me.txt" {
			t.Errorf("delete remote = %q", e[1])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for delete event")
	}
}
