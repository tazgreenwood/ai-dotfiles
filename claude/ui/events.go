package main

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// ── SSE hub (DOTFILES-25, global subscriber DOTFILES-32) ──────────────────
//
// Per-project pub/sub over stdlib http.Flusher. A background goroutine polls
// registry.db's mtime every 500ms and broadcasts to all connected projects'
// channels on change — a simple diff, since SQLite is single-writer WAL and
// any write touches the file.
//
// globalSubKey is a reserved subscriber key (no project is ever named "*")
// used by the project-less global dashboard pages (Kanban/Reviews/Audits/
// Issues/Deploy Checks): they subscribe under this key instead of a project
// name, and broadcastAll notifies it on every change alongside every
// per-project subscriber.
const globalSubKey = "*"

type eventHub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: make(map[string]map[chan struct{}]struct{})}
}

func (h *eventHub) subscribe(project string) chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[project] == nil {
		h.subs[project] = make(map[chan struct{}]struct{})
	}
	h.subs[project][ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *eventHub) unsubscribe(project string, ch chan struct{}) {
	h.mu.Lock()
	delete(h.subs[project], ch)
	if len(h.subs[project]) == 0 {
		delete(h.subs, project)
	}
	h.mu.Unlock()
}

// broadcastAll notifies every subscribed channel across all projects. Any
// write to registry.db touches the file's mtime regardless of which
// project's data changed, so all connected clients are notified.
func (h *eventHub) broadcastAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, chans := range h.subs {
		for ch := range chans {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

var hub = newEventHub()

var watcherOnce sync.Once

// dbModTime returns the newer of registry.db's and registry.db-wal's mtimes.
//
// SQLite in WAL mode buffers writes in the -wal file and only flushes them
// into the main database file on checkpoint. Stat'ing registry.db alone is
// therefore blind to any write that hasn't been checkpointed yet — the
// watcher would miss live updates until something forced a checkpoint (e.g.
// a process restart). Considering both files' mtimes closes that gap.
func dbModTime() time.Time {
	var latest time.Time
	if info, err := os.Stat(dbPath()); err == nil {
		latest = info.ModTime()
	}
	if info, err := os.Stat(dbPath() + "-wal"); err == nil {
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

// startWatcher launches (once) a background goroutine that polls dbModTime()
// every 500ms and broadcasts to the hub on change.
func startWatcher() {
	watcherOnce.Do(func() {
		go func() {
			lastMod := dbModTime()
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for range ticker.C {
				mod := dbModTime()
				if mod.After(lastMod) {
					lastMod = mod
					hub.broadcastAll()
				}
			}
		}()
	})
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := ReadProject(name); err != nil {
		http.NotFound(w, r)
		return
	}
	streamEvents(w, r, name)
}

// handleGlobalEvents is the project-less counterpart to handleEvents, used
// by the 5 global dashboard pages: it subscribes under globalSubKey instead
// of a project name, so it's notified on every broadcastAll regardless of
// which project's data changed.
func handleGlobalEvents(w http.ResponseWriter, r *http.Request) {
	streamEvents(w, r, globalSubKey)
}

// streamEvents runs the shared SSE loop for a given subscriber key (a
// project name, or globalSubKey for the project-less global pages).
func streamEvents(w http.ResponseWriter, r *http.Request, subKey string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	startWatcher()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := hub.subscribe(subKey)
	defer hub.unsubscribe(subKey, ch)

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if _, err := fmt.Fprint(w, "data: changed\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
