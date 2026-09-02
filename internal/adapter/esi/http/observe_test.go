package http

import (
	nhttp "net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func TestObserverSeesHashParamPattern(t *testing.T) {
	t.Parallel()
	c, obs := observedClient(t, nhttp.StatusOK, `{"ok":true}`)
	obs.EXPECT().Request(nhttp.MethodGet, nhttp.StatusOK, "/killmails/{id}/{id}", gomock.Any())
	if _, err := c.Get(t.Context(), esi.Path("killmails", esi.ID(1), esi.Param("deadbeef")), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestObserverSeesTemplatedPath(t *testing.T) {
	t.Parallel()
	c, obs := observedClient(t, nhttp.StatusOK, `{"ok":true}`)
	obs.EXPECT().Request(nhttp.MethodGet, nhttp.StatusOK, "/characters/{id}/skills", gomock.Any())
	if _, err := c.Get(t.Context(), esi.Path("characters", esi.ID(7), "skills"), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestObserverSeesErrorStatus(t *testing.T) {
	t.Parallel()
	c, obs := observedClient(t, nhttp.StatusNotFound, `{"error":"no"}`)
	obs.EXPECT().Request(nhttp.MethodGet, nhttp.StatusNotFound, "/characters/{id}/skills", gomock.Any())
	if _, err := c.Get(t.Context(), esi.Path("characters", esi.ID(7), "skills"), nil, nil, nil); err == nil {
		t.Fatal("want ESI error")
	}
}

func TestCacheHitDoesNotObserve(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, _ *nhttp.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		if _, err := w.Write([]byte(`{"players":1}`)); err != nil {
			t.Fatal(err)
		}
	}))
	t.Cleanup(srv.Close)
	ctrl := gomock.NewController(t)
	obs := mocks.NewMockESIObserver(ctrl)
	obs.EXPECT().Request(nhttp.MethodGet, nhttp.StatusOK, "/fw/stats", gomock.Any()).Times(1)
	opts := testOptions(srv.URL)
	opts.Observe = obs
	c := mustClient(t, opts, srv.Client())
	if _, err := c.Get(t.Context(), esi.Path("/fw/stats"), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(t.Context(), esi.Path("/fw/stats"), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("ESI hits %d", hits)
	}
}

func TestObserverSeesNetworkFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(nhttp.HandlerFunc(func(nhttp.ResponseWriter, *nhttp.Request) {}))
	client := srv.Client()
	srv.Close()
	ctrl := gomock.NewController(t)
	obs := mocks.NewMockESIObserver(ctrl)
	obs.EXPECT().Request(nhttp.MethodGet, 0, "/fw/stats", gomock.Any()).MinTimes(1)
	opts := testOptions(srv.URL)
	opts.Observe = obs
	c := mustClient(t, opts, client)
	if _, err := c.Get(t.Context(), esi.Path("/fw/stats"), nil, nil, nil); err == nil {
		t.Fatal("want network error")
	}
}

func observedClient(t *testing.T, status int, body string) (*Client, *mocks.MockESIObserver) {
	t.Helper()
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, _ *nhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}))
	t.Cleanup(srv.Close)
	ctrl := gomock.NewController(t)
	obs := mocks.NewMockESIObserver(ctrl)

	opts := testOptions(srv.URL)
	opts.Observe = obs

	return mustClient(t, opts, srv.Client()), obs
}
