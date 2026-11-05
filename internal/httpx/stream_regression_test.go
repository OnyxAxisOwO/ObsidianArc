package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type flushFailure struct {
	*httptest.ResponseRecorder
	err   error
	calls int
	after int
}

func (w *flushFailure) FlushError() error {
	w.calls++
	if w.calls > w.after {
		return w.err
	}
	return nil
}

func TestCompressorPreservesFlushErrorThroughLogger(t *testing.T) {
	broken := errors.New("broken pipe")
	for _, contentType := range []string{"text/event-stream", "application/json"} {
		writer := &flushFailure{ResponseRecorder: httptest.NewRecorder(), err: broken}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			if _, err := w.Write([]byte("test")); err != nil {
				t.Fatal(err)
			}
			if err := http.NewResponseController(w).Flush(); !errors.Is(err, broken) {
				t.Fatalf("type=%s err=%v", contentType, err)
			}
		})
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Accept-Encoding", "gzip")
		Chain(handler, RequestID(), Logger(), Recover(), Compress()).ServeHTTP(writer, request)
	}
}

func TestHeartbeatFailureCancelsUpstreamAndStopsWriting(t *testing.T) {
	broken := errors.New("closed")
	writer := &flushFailure{ResponseRecorder: httptest.NewRecorder(), err: broken, after: 1}
	sse, err := NewSSE(writer)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := sse.keepAlive(context.Background(), time.Millisecond)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not cancel the upstream")
	}
	if err := stop(); !errors.Is(err, broken) {
		t.Fatalf("stop=%v", err)
	}
	calls := writer.calls
	if err := sse.Event("done", nil); !errors.Is(err, broken) {
		t.Fatalf("sticky error=%v", err)
	}
	if writer.calls != calls {
		t.Fatal("wrote after terminal write failure")
	}
}

func TestHeartbeatAndContentWritesAreSerialized(t *testing.T) {
	writer := httptest.NewRecorder()
	sse, err := NewSSE(writer)
	if err != nil {
		t.Fatal(err)
	}
	_, stop := sse.keepAlive(context.Background(), time.Microsecond)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			for j := 0; j < 50; j++ {
				_ = sse.Event("delta", map[string]string{"text": "hi"})
			}
		})
	}
	wg.Wait()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if len(writer.Body.Bytes()) == 0 {
		t.Fatal("no events")
	}
}
