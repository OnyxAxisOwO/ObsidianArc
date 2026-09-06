package admin

import (
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// What this instance is costing the machine it runs on: the files its users
// are storing, the memory the process holds, and how busy it has been.
//
// Deliberately not here: the size of the database file. Reading that means
// asking SQLite for its page count or Postgres for pg_database_size, and
// internal/database exists on the rule that no module branches on which
// database it is. The attachments below are the part users actually grow, and
// they are countable in one portable query.

// How many accounts the storage table lists. Long enough to find whoever is
// filling the disk, short enough that the page stays a page.
const storageRows = 20

// cpuSampler turns a counter into a rate.
//
// The process gives out CPU time used since it started, which on its own says
// almost nothing: a week of uptime divided into an hour of work reads as an
// idle instance. Two readings and the interval between them is a rate, so the
// sampler keeps the last reading and every request answers for the window
// since the previous one.
type cpuSampler struct {
	mu   sync.Mutex
	at   time.Time
	used time.Duration
}

// rate reports the percentage of one core this process used since the last
// call, and how long that window was. The first call has nothing to compare
// against and says so.
func (s *cpuSampler) rate(now time.Time, used time.Duration) (percent, window float64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	previousAt, previousUsed := s.at, s.used
	s.at, s.used = now, used

	if previousAt.IsZero() {
		return 0, 0, false
	}
	elapsed := now.Sub(previousAt)
	// A clock that went backwards, or two requests inside the same instant.
	if elapsed <= 0 {
		return 0, 0, false
	}
	// Can exceed 100: this is one core's worth, and the page is told how many
	// cores there are so it can say so.
	return float64(used-previousUsed) / float64(elapsed) * 100, elapsed.Seconds(), true
}

func (h *Handlers) resources(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	heldCount, heldBytes, err := h.conversations.Held(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	discarded, err := h.conversations.Discarded(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	byUser, err := h.conversations.HeldByUser(ctx, storageRows)
	if err != nil {
		return httpx.Internal(err)
	}

	// ReadMemStats stops the world for the length of the read. It is
	// microseconds on a heap this size, and this is the only place that asks.
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)

	cpu := map[string]any{
		"cores":      runtime.NumCPU(),
		"gomaxprocs": runtime.GOMAXPROCS(0),
	}
	if used, ok := processCPU(); ok {
		cpu["process_sec"] = used.Seconds()
		if percent, window, sampled := h.cpu.rate(time.Now(), used); sampled {
			cpu["percent"] = percent
			cpu["window_sec"] = window
		}
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"storage": map[string]any{
			"held_bytes":      heldBytes,
			"held_count":      heldCount,
			"discarded_count": discarded,
			"by_user":         byUser,
		},
		"memory": map[string]any{
			// In use, reserved for the heap, and taken from the operating
			// system altogether. The third is the one a container's limit is
			// measured against; the first is the one that answers "is this
			// leaking".
			"heap_bytes":     memory.HeapAlloc,
			"heap_sys_bytes": memory.HeapSys,
			"sys_bytes":      memory.Sys,
			"gc_count":       memory.NumGC,
			"gc_pause_ms":    float64(memory.PauseNs[(memory.NumGC+255)%256]) / 1e6,
			"goroutines":     runtime.NumGoroutine(),
		},
		"cpu":        cpu,
		"sampled_at": time.Now().UnixMilli(),
	})
}
