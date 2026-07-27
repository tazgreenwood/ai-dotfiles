package main

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ── GET /projects/{name}/events ─────────────────────────────────────────────
//
// SSE endpoint (DOTFILES-25): streams `data: changed` frames whenever the
// project's registry data changes. These tests are red until events.go wires
// up the route in newRouter() and implements the push mechanism.

func TestGetEvents_ContentTypeIsEventStream(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/events")
	if err != nil {
		t.Fatalf("GET /projects/existing/events: %v", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("want Content-Type text/event-stream, got %q (status %d)", ct, resp.StatusCode)
	}
}

func TestGetEvents_ReceivesChangedFrameAfterPlanUpdate(t *testing.T) {
	dir, cleanup := setupFixtureDir(t)
	defer cleanup()
	setupProjectFixture(t, dir, "existing")

	ts := newTestServer(t, dir)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/projects/existing/events")
	if err != nil {
		t.Fatalf("GET /projects/existing/events: %v", err)
	}
	defer resp.Body.Close()

	// Simulate the registry MCP server updating a plan step: the change
	// should be observed by the running events handler and pushed as a
	// `data: changed` frame to the connected client.
	seedPlan(t, dir, "existing", "TICKET-1", map[string]any{
		"ticket":     "TICKET-1",
		"summary":    "Test plan",
		"plan_steps": []map[string]any{{"step": 1, "status": "in_progress"}},
	})

	frames := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				frames <- line
				return
			}
		}
	}()

	select {
	case frame := <-frames:
		if !strings.Contains(frame, "changed") {
			t.Errorf("want frame containing %q, got %q", "changed", frame)
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for `data: changed` SSE frame")
	}
}
