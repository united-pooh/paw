package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

func TestJSONRPCSessionMatchesConcurrentResponsesByID(t *testing.T) {
	clientToPeerReader, clientToPeerWriter := io.Pipe()
	peerToClientReader, peerToClientWriter := io.Pipe()
	session := newJSONRPCSession(peerToClientReader, clientToPeerWriter)
	t.Cleanup(func() {
		_ = session.Close(context.Background())
		_ = clientToPeerReader.Close()
		_ = peerToClientWriter.Close()
	})

	peerDone := make(chan error, 1)
	go func() {
		defer close(peerDone)
		decoder := json.NewDecoder(bufio.NewReader(clientToPeerReader))
		requests := make([]rpcRequest, 0, 2)
		for len(requests) < 2 {
			var request rpcRequest
			if err := decoder.Decode(&request); err != nil {
				peerDone <- err
				return
			}
			requests = append(requests, request)
		}
		for i := len(requests) - 1; i >= 0; i-- {
			request := requests[i]
			response := rpcResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result:  json.RawMessage(fmt.Sprintf(`{"request_id":%d}`, request.ID)),
			}
			if err := json.NewEncoder(peerToClientWriter).Encode(response); err != nil {
				peerDone <- err
				return
			}
		}
		peerDone <- nil
	}()

	var wg sync.WaitGroup
	results := make(chan map[string]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var result struct {
				RequestID int `json:"request_id"`
			}
			if err := session.Call(context.Background(), "tools/list", nil, &result); err != nil {
				t.Errorf("Call() error = %v", err)
				return
			}
			results <- map[string]int{"request_id": result.RequestID}
		}()
	}
	wg.Wait()
	close(results)

	if got := len(results); got != 2 {
		t.Fatalf("completed calls=%d, want 2", got)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("peer error: %v", err)
	}
}

func TestJSONRPCSessionDeliversNotifications(t *testing.T) {
	peerToClientReader, peerToClientWriter := io.Pipe()
	clientToPeerReader, clientToPeerWriter := io.Pipe()
	session := newJSONRPCSession(peerToClientReader, clientToPeerWriter)
	t.Cleanup(func() {
		_ = session.Close(context.Background())
		_ = clientToPeerReader.Close()
		_ = peerToClientWriter.Close()
	})

	go func() {
		_, _ = io.Copy(io.Discard, clientToPeerReader)
	}()
	notification := rpcNotification{
		JSONRPC: "2.0",
		Method:  "notifications/tools/list_changed",
		Params:  json.RawMessage(`{"changed":true}`),
	}
	if err := json.NewEncoder(peerToClientWriter).Encode(notification); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-session.Notifications():
		if got.Method != notification.Method || string(got.Params) != string(notification.Params) {
			t.Fatalf("notification=%#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification")
	}
}

func TestJSONRPCSessionFailsPendingCallOnMalformedOutput(t *testing.T) {
	peerToClientReader, peerToClientWriter := io.Pipe()
	clientToPeerReader, clientToPeerWriter := io.Pipe()
	session := newJSONRPCSession(peerToClientReader, clientToPeerWriter)
	t.Cleanup(func() {
		_ = session.Close(context.Background())
		_ = clientToPeerReader.Close()
		_ = peerToClientWriter.Close()
	})

	go func() {
		_, _ = bufio.NewReader(clientToPeerReader).ReadString('\n')
		_, _ = io.WriteString(peerToClientWriter, "not-json\n")
	}()

	var result map[string]any
	err := session.Call(context.Background(), "tools/list", nil, &result)
	if err == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestJSONRPCSessionCallHonorsContextCancellation(t *testing.T) {
	peerToClientReader, peerToClientWriter := io.Pipe()
	clientToPeerReader, clientToPeerWriter := io.Pipe()
	session := newJSONRPCSession(peerToClientReader, clientToPeerWriter)
	t.Cleanup(func() {
		_ = session.Close(context.Background())
		_ = clientToPeerReader.Close()
		_ = peerToClientWriter.Close()
	})

	go func() {
		_, _ = io.Copy(io.Discard, clientToPeerReader)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var result map[string]any
	err := session.Call(ctx, "tools/list", nil, &result)
	if err == nil {
		t.Fatal("expected context cancellation")
	}
	if ctx.Err() == nil {
		t.Fatal("context did not expire")
	}
}
