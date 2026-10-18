package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/checkin"
)

func noopRoute(http.ResponseWriter, *http.Request) error { return nil }

// A plugin route mounted after Routes has read the table is accepted and then
// never served, and nothing would ever say so. The mistake is refused where it
// is made instead.
func TestMountAfterRoutesIsRefused(t *testing.T) {
	h := &Handlers{}
	h.Routes(http.NewServeMux())

	defer func() {
		if recover() == nil {
			t.Fatal("a route mounted after Routes was accepted")
		}
	}()
	h.Mount(Route{Pattern: "GET /api/admin/late", Handler: noopRoute})
}

func TestMountBeforeRoutesIsServed(t *testing.T) {
	h := &Handlers{}
	h.Mount(Route{Pattern: "GET /api/admin/early", Handler: noopRoute})
	mux := http.NewServeMux()
	h.Routes(mux)

	// Anonymous, so the route answers the session check rather than 404: that is
	// how a mounted route is told apart from one that never made it into the table.
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/early", nil))
	if response.Code == http.StatusNotFound {
		t.Fatal("a route mounted before Routes was not served")
	}
}

// The check-in handlers call straight into their service, so their routes exist
// only where a service does, as the bonus routes do. Without one the request is
// a 404 rather than a nil dereference.
func TestCheckinRoutesExistOnlyWithAService(t *testing.T) {
	answer := func(h *Handlers) int {
		mux := http.NewServeMux()
		h.Routes(mux)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/checkin", nil))
		return response.Code
	}

	if code := answer(&Handlers{}); code != http.StatusNotFound {
		t.Errorf("without a check-in service the route answered %d, want 404", code)
	}
	if code := answer(&Handlers{Checkin: &checkin.Service{}}); code != http.StatusUnauthorized {
		t.Errorf("with a check-in service an anonymous request answered %d, want 401", code)
	}
}
