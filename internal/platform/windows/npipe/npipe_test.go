//go:build windows

package npipe

import (
	"encoding/json"
	"testing"
	"time"

	"ncepupan/hdd/internal/ipc"
)

func testPipe(t *testing.T) string {
	t.Helper()
	return `\\.\pipe\huadian-drive-test-` + t.Name()
}

func TestPingServerClient(t *testing.T) {
	path := testPipe(t)
	srv := NewServer(path, func(req ipc.Request) ipc.Response {
		return ipc.Response{Type: "pong", ID: req.ID}
	})

	go srv.Serve()
	defer srv.Shutdown()
	time.Sleep(50 * time.Millisecond)

	conn, err := Dial(path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	resp, err := conn.Call(ipc.Request{Type: "ping", ID: "1"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Type != "pong" {
		t.Errorf("type = %q, want %q", resp.Type, "pong")
	}
}

func TestStatus(t *testing.T) {
	path := testPipe(t)
	srv := NewServer(path, func(req ipc.Request) ipc.Response {
		switch req.Type {
		case "status":
			data, _ := json.Marshal(ipc.StatusData{Provider: "mock"})
			return ipc.Response{Type: "status", ID: req.ID, Data: data}
		default:
			return ipc.Response{Type: "pong", ID: req.ID}
		}
	})

	go srv.Serve()
	defer srv.Shutdown()
	time.Sleep(50 * time.Millisecond)

	conn, err := Dial(path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	resp, err := conn.Call(ipc.Request{Type: "status", ID: "2"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var status ipc.StatusData
	if err := json.Unmarshal(resp.Data, &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.Provider != "mock" {
		t.Errorf("provider = %q, want %q", status.Provider, "mock")
	}
}

func TestShutdown(t *testing.T) {
	path := testPipe(t)
	srv := NewServer(path, func(req ipc.Request) ipc.Response {
		return ipc.Response{Type: "shutdown", ID: req.ID}
	})

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve()
	}()
	time.Sleep(50 * time.Millisecond)

	conn, err := Dial(path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_, err = conn.Call(ipc.Request{Type: "shutdown", ID: "3"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestInvalidJSON(t *testing.T) {
	path := testPipe(t)
	srv := NewServer(path, func(req ipc.Request) ipc.Response {
		return ipc.Response{Type: "pong", ID: req.ID}
	})

	go srv.Serve()
	defer srv.Shutdown()
	time.Sleep(50 * time.Millisecond)

	conn, err := Dial(path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	resp, err := conn.Call(ipc.Request{Type: "ping", ID: "ok"})
	if err != nil {
		t.Fatalf("initial call: %v", err)
	}
	if resp.ID != "ok" {
		t.Errorf("id = %q", resp.ID)
	}
}

func TestIllegalLength(t *testing.T) {
	path := testPipe(t)
	srv := NewServer(path, func(req ipc.Request) ipc.Response {
		return ipc.Response{Type: "pong", ID: req.ID}
	})

	go srv.Serve()
	defer srv.Shutdown()
	time.Sleep(50 * time.Millisecond)

	conn, err := Dial(path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	header := []byte{0x00, 0x10, 0x00, 0x01} // > MaxMessageSize
	conn.pf.Write(header)

	_, err = conn.Call(ipc.Request{Type: "ping", ID: "x"})
	if err == nil {
		t.Error("expected error after illegal length")
	}
}

func TestDialTimeout(t *testing.T) {
	_, err := Dial(`\\.\pipe\huadian-drive-nonexistent-`+t.Name(), 500*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestResponsePropagation(t *testing.T) {
	path := testPipe(t)
	srv := NewServer(path, func(req ipc.Request) ipc.Response {
		return ipc.Response{
			Type:  "error",
			ID:    req.ID,
			Error: "something went wrong",
		}
	})

	go srv.Serve()
	defer srv.Shutdown()
	time.Sleep(50 * time.Millisecond)

	conn, err := Dial(path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	resp, err := conn.Call(ipc.Request{Type: "ping", ID: "err1"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Error != "something went wrong" {
		t.Errorf("error = %q", resp.Error)
	}
}

func TestClosedConnRead(t *testing.T) {
	path := testPipe(t)
	srv := NewServer(path, func(req ipc.Request) ipc.Response {
		return ipc.Response{Type: "pong", ID: req.ID}
	})

	go srv.Serve()
	defer srv.Shutdown()
	time.Sleep(50 * time.Millisecond)

	conn, err := Dial(path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()

	_, err = conn.Call(ipc.Request{Type: "ping", ID: "x"})
	if err == nil {
		t.Error("expected error on closed connection")
	}
}
