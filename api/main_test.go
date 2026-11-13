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

func TestResolveProfile(t *testing.T) {
	// Defaults
	p, err := resolveProfile("")
	if err != nil || p.Name != "bicycle" {
		t.Fatalf("expected bicycle default, got %+v (err: %v)", p, err)
	}

	// Direct names
	cases := []struct {
		input   string
		name    string
		costing string
	}{
		{"car", "car", "auto"},
		{"bicycle", "bicycle", "bicycle"},
		{"motorcycle", "motorcycle", "motorcycle"},
		{"pedestrian", "pedestrian", "pedestrian"},
		{"auto", "car", "auto"},
		{"driving", "car", "auto"},
		{"moto", "motorcycle", "motorcycle"},
		{"bike", "bicycle", "bicycle"},
		{"cycling", "bicycle", "bicycle"},
		{"walk", "pedestrian", "pedestrian"},
		{"foot", "pedestrian", "pedestrian"},
	}

	for _, tc := range cases {
		prof, err := resolveProfile(tc.input)
		if err != nil {
			t.Fatalf("unexpected error resolving %s: %v", tc.input, err)
		}
		if prof.Name != tc.name || prof.Costing != tc.costing {
			t.Fatalf("for input %s: expected name=%s costing=%s, got name=%s costing=%s",
				tc.input, tc.name, tc.costing, prof.Name, prof.Costing)
		}
	}

	// Invalid profile
	_, err = resolveProfile("spaceship")
	if err == nil {
		t.Fatal("expected error for invalid profile 'spaceship'")
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
	origURL := valhallaURL
	valhallaURL = "http://127.0.0.1:59999"
	defer func() { valhallaURL = origURL }()

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
		_, _ = rw.Write([]byte(`{"version":"3.5.1"}`))
	}))
	defer mockBackend.Close()

	valhallaURL = mockBackend.URL
	w2 := httptest.NewRecorder()
	handleHealth(w2, req)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w2.Code)
	}
	var okResp HealthResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &okResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if okResp.Backend != "online" || okResp.Status != "ok" || okResp.Engine != "valhalla" {
		t.Fatalf("unexpected ok response: %+v", okResp)
	}
	if len(okResp.Profiles) != 4 {
		t.Fatalf("expected 4 supported profiles in health check, got %d", len(okResp.Profiles))
	}
}

func TestProfilesEndpoint(t *testing.T) {
	req := httptest.NewRequest("GET", "/profiles", nil)
	w := httptest.NewRecorder()
	handleProfiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp ProfilesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode profiles response: %v", err)
	}

	if len(resp.Profiles) != 4 {
		t.Fatalf("expected 4 profiles, got %d", len(resp.Profiles))
	}
}

func TestDistanceEndpointWithProfileAndCaching(t *testing.T) {
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

	origURL := valhallaURL
	valhallaURL = mockBackend.URL
	defer func() { valhallaURL = origURL }()

	// 1. Car Request (MISS)
	req1 := httptest.NewRequest("GET", "/distance?from=38.7577,9.0128&to=38.7891,9.0054&profile=car", nil)
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
	if dist.Profile != "car" || dist.DistanceKm != 4.8 || dist.DurationMinutes != 5.0 {
		t.Fatalf("unexpected distance result: %+v", dist)
	}

	// 2. Car Request (HIT)
	req2 := httptest.NewRequest("GET", "/distance?from=9.0128,38.7577&to=9.0054,38.7891&profile=auto", nil)
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

	// 3. Bicycle Request (different profile => Cache MISS)
	req3 := httptest.NewRequest("GET", "/distance?from=38.7577,9.0128&to=38.7891,9.0054&profile=bicycle", nil)
	w3 := httptest.NewRecorder()
	handleDistance(w3, req3)

	if w3.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("expected X-Cache MISS for distinct profile, got %s", w3.Header().Get("X-Cache"))
	}
	if backendCalls != 2 {
		t.Fatalf("expected 2 backend calls, got %d", backendCalls)
	}
}

func TestRouteEndpointWithProfileAndSteps(t *testing.T) {
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
									Distance:    1200.0,
									Duration:    180.0,
									Instruction: "Head north on Main St",
									Name:        "Main St",
									Maneuver:    &OsrmManeuver{Type: "depart", Modifier: "straight"},
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

	origURL := valhallaURL
	valhallaURL = mockBackend.URL
	defer func() { valhallaURL = origURL }()

	req := httptest.NewRequest("GET", "/route?from=38.7577,9.0128&to=38.7891,9.0054&profile=motorcycle&steps=true", nil)
	w := httptest.NewRecorder()
	handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var route RouteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &route); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if route.Profile != "motorcycle" || route.DistanceKm != 1.2 || len(route.Steps) != 1 || route.Steps[0].Instruction != "Head north on Main St" {
		t.Fatalf("unexpected route response: %+v", route)
	}
}

func TestMatrixEndpointWithProfile(t *testing.T) {
	routeCache.Purge()

	mockBackend := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		matRes := ValhallaMatrixResponse{
			SourcesToTargets: [][]ValhallaMatrixElement{
				{
					{Distance: 0.0, Time: 0.0, FromIndex: 0, ToIndex: 0},
					{Distance: 1.5, Time: 180.0, FromIndex: 0, ToIndex: 1},
				},
				{
					{Distance: 1.5, Time: 180.0, FromIndex: 1, ToIndex: 0},
					{Distance: 0.0, Time: 0.0, FromIndex: 1, ToIndex: 1},
				},
			},
		}
		_ = json.NewEncoder(rw).Encode(matRes)
	}))
	defer mockBackend.Close()

	origURL := valhallaURL
	valhallaURL = mockBackend.URL
	defer func() { valhallaURL = origURL }()

	body := `{"profile": "pedestrian", "coordinates": [[9.0128, 38.7577], [9.0054, 38.7891]]}`
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
	if matrix.Profile != "pedestrian" {
		t.Fatalf("expected pedestrian profile, got %s", matrix.Profile)
	}
	if matrix.DistancesKm[0][1] != 1.5 || matrix.DurationsMinutes[0][1] != 3.0 {
		t.Fatalf("unexpected matrix response: %+v", matrix)
	}
	if matrix.DistancesMeters[0][1] != 1500 || matrix.DurationsSeconds[0][1] != 180 {
		t.Fatalf("unexpected meters/seconds matrix: %+v", matrix)
	}
}
