package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSSEBroadcastOnEdit connects an SSE client, performs an entry edit through
// the API, and asserts the client receives an entry.updated event.
func TestSSEBroadcastOnEdit(t *testing.T) {
	ts, token := setup(t)

	// Open the SSE stream with the token as a query param (EventSource style).
	req, _ := http.NewRequest("GET", ts.URL+"/sse?token="+token, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("sse connect status %d", resp.StatusCode)
	}

	// Read events in a goroutine until we see entry.updated or time out.
	got := make(chan map[string]any, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		var event string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if event == "entry.updated" {
					var data map[string]any
					_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data)
					got <- data
					return
				}
			}
		}
	}()

	// Give the client a moment to register before broadcasting.
	time.Sleep(100 * time.Millisecond)

	body, _ := json.Marshal(map[string]string{
		"category": "cards", "field": "prefix", "key": "こんにちは",
		"text": "你好（已校对）", "source": "human",
	})
	editReq, _ := http.NewRequest("PUT", ts.URL+"/api/entry", bytes.NewReader(body))
	editReq.Header.Set("Authorization", "Bearer "+token)
	editResp, err := http.DefaultClient.Do(editReq)
	if err != nil {
		t.Fatal(err)
	}
	editResp.Body.Close()

	select {
	case data := <-got:
		if data["key"] != "こんにちは" || data["text"] != "你好（已校对）" {
			t.Errorf("unexpected event payload: %+v", data)
		}
		if data["user"] != "alice" {
			t.Errorf("expected user alice, got %v", data["user"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for entry.updated SSE event")
	}
}

// TestSSERequiresAuth verifies the stream rejects unauthenticated clients.
func TestSSERequiresAuth(t *testing.T) {
	ts, _ := setup(t)
	resp, err := http.Get(ts.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}
