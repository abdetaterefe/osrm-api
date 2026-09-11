package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseCoords(t *testing.T) {
	// Standard lon,lat
	coords, err := parseCoords("38.7577123,9.0128456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if coords[0] != 38.75771 || coords[1] != 9.01285 {
		t.Fatalf("unexpected rounded coords: %v", coords)
	}

	// Swapped lat,lon auto-correction
	coordsSwapped, err := parseCoords("9.01285,38.75771")
	if err != nil {
		t.Fatalf("unexpected error for swapped coords: %v", err)
	}
	if coordsSwapped[0] != 38.75771 || coordsSwapped[1] != 9.01285 {
		t.Fatalf("expected auto-swapped coords [38.75771, 9.01285], got %v", coordsSwapped)
	}

	// Invalid format
	_, err = parseCoords("invalid")
	if err == nil {
		t.Fatal("expected error for invalid coords")
	}

	// Out of bounds coordinate
	_, err = parseCoords("0.0,0.0")
	if err == nil {
		t.Fatal("expected error for out of bounds coords")
	}
}

func TestRoundFloat(t *testing.T) {
	if got := roundFloat(4.8046, 2); got != 4.8 {
		t.Fatalf("expected 4.8, got %v", got)
	}
	if got := roundFloat(4.856, 2); got != 4.86 {
		t.Fatalf("expected 4.86, got %v", got)
	}
	if got := roundFloat(4.96, 1); got != 5.0 {
		t.Fatalf("expected 5.0, got %v", got)
	}
	if got := roundFloat(38.757712345, 5); got != 38.75771 {
		t.Fatalf("expected 38.75771, got %v", got)
	}
}

func TestLRUCache(t *testing.T) {
	cache := NewLRUCache(2, 50*time.Millisecond)

	cache.Set("k1", []byte("val1"))
	cache.Set("k2", []byte("val2"))

	v, ok := cache.Get("k1")
	if !ok || string(v) != "val1" {
		t.Fatalf("expected val1, got %s", string(v))
	}

	cache.Set("k3", []byte("val3"))

	if _, ok := cache.Get("k2"); ok {
		t.Fatal("expected k2 to be evicted")
	}
	if _, ok := cache.Get("k1"); !ok {
		t.Fatal("expected k1 to be present")
	}
	if _, ok := cache.Get("k3"); !ok {
		t.Fatal("expected k3 to be present")
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := cache.Get("k1"); ok {
		t.Fatal("expected k1 to have expired")
	}
}

func TestHealthCheck(t *testing.T) {
	origURL := osrmURL
	osrmURL = "http://127.0.0.1:59999"
	defer func() { osrmURL = origURL }()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handleHealth(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}

	var degradedResp HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &degradedResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if degradedResp.Backend != "offline" || degradedResp.Status != "degraded" {
		t.Fatalf("unexpected degraded response: %+v", degradedResp)
	}

	mockBackend := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(`{"code":"Ok"}`))
	}))
	defer mockBackend.Close()

	osrmURL = mockBackend.URL
	w2 := httptest.NewRecorder()
	handleHealth(w2, req)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w2.Code)
	}
	var okResp HealthResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &okResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if okResp.Backend != "online" || okResp.Status != "ok" {
		t.Fatalf("unexpected ok response: %+v", okResp)
	}
}

func TestDistanceEndpointAndCaching(t *testing.T) {
	routeCache.Purge()

	backendCalls := 0
	mockBackend := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		backendCalls++
		osrmRes := OsrmResponse{
			Code: "Ok",
			Routes: []OsrmRoute{
				{
					Distance: 4804.6,
					Duration: 298.4,
				},
			},
			Waypoints: []OsrmWaypoint{
				{Location: [2]float64{38.7577, 9.0128}, Name: "Origin St"},
				{Location: [2]float64{38.7891, 9.0054}, Name: "Dest St"},
			},
		}
		_ = json.NewEncoder(rw).Encode(osrmRes)
	}))
	defer mockBackend.Close()

	origURL := osrmURL
	osrmURL = mockBackend.URL
	defer func() { osrmURL = origURL }()

	// Cache MISS
	req1 := httptest.NewRequest("GET", "/distance?from=38.7577,9.0128&to=38.7891,9.0054", nil)
	w1 := httptest.NewRecorder()
	handleDistance(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w1.Code, w1.Body.String())
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("expected X-Cache MISS, got %s", w1.Header().Get("X-Cache"))
	}

	var dist DistanceResponse
	if err := json.Unmarshal(w1.Body.Bytes(), &dist); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if dist.DistanceKm != 4.8 || dist.DurationMinutes != 5.0 || dist.Origin.Name != "Origin St" {
		t.Fatalf("unexpected distance result: %+v", dist)
	}

	// Cache HIT (with swapped lat,lon)
	req2 := httptest.NewRequest("GET", "/distance?from=9.0128,38.7577&to=9.0054,38.7891", nil)
	w2 := httptest.NewRecorder()
	handleDistance(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("expected X-Cache HIT, got %s", w2.Header().Get("X-Cache"))
	}
	if backendCalls != 1 {
		t.Fatalf("expected 1 backend call due to cache, got %d", backendCalls)
	}
}

func TestRouteEndpoint(t *testing.T) {
	routeCache.Purge()

	mockBackend := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		osrmRes := OsrmResponse{
			Code: "Ok",
			Routes: []OsrmRoute{
				{
					Distance: 1200.0,
					Duration: 180.0,
					Geometry: map[string]interface{}{"type": "LineString"},
					Legs: []OsrmLeg{
						{
							Distance: 1200.0,
							Duration: 180.0,
							Steps: []OsrmStep{
								{
									Distance: 1200.0,
									Duration: 180.0,
									Name:     "Main St",
									Maneuver: &OsrmManeuver{Type: "depart", Modifier: "straight"},
								},
							},
						},
					},
				},
			},
			Waypoints: []OsrmWaypoint{
				{Location: [2]float64{38.7577, 9.0128}, Name: "Point 1"},
				{Location: [2]float64{38.7891, 9.0054}, Name: "Point 2"},
			},
		}
		_ = json.NewEncoder(rw).Encode(osrmRes)
	}))
	defer mockBackend.Close()

	origURL := osrmURL
	osrmURL = mockBackend.URL
	defer func() { osrmURL = origURL }()

	req := httptest.NewRequest("GET", "/route?from=38.7577,9.0128&to=38.7891,9.0054&steps=true", nil)
	w := httptest.NewRecorder()
	handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var route RouteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &route); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if route.DistanceKm != 1.2 || len(route.Steps) != 1 || route.Steps[0].Instruction != "Head out on Main St" {
		t.Fatalf("unexpected route response: %+v", route)
	}
}

func TestMatrixEndpoint(t *testing.T) {
	routeCache.Purge()

	mockBackend := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		osrmRes := OsrmResponse{
			Code: "Ok",
			Distances: [][]float64{
				{0, 1500},
				{1500, 0},
			},
			Durations: [][]float64{
				{0, 180},
				{180, 0},
			},
			Waypoints: []OsrmWaypoint{
				{Location: [2]float64{38.7577, 9.0128}, Name: "Point 1"},
				{Location: [2]float64{38.7891, 9.0054}, Name: "Point 2"},
			},
		}
		_ = json.NewEncoder(rw).Encode(osrmRes)
	}))
	defer mockBackend.Close()

	origURL := osrmURL
	osrmURL = mockBackend.URL
	defer func() { osrmURL = origURL }()

	body := `{"coordinates": [[9.0128, 38.7577], [9.0054, 38.7891]]}`
	req := httptest.NewRequest("POST", "/matrix", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleMatrix(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var matrix MatrixResponse
	if err := json.Unmarshal(w.Body.Bytes(), &matrix); err != nil {
		t.Fatalf("failed to decode matrix response: %v", err)
	}
	if matrix.DistancesKm[0][1] != 1.5 || matrix.DurationsMinutes[0][1] != 3.0 {
		t.Fatalf("unexpected matrix response: %+v", matrix)
	}
}
