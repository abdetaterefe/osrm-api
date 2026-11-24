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

func TestSearchEndpoint(t *testing.T) {
	searchCache.Purge()
	origURL := geocoderURL
	origInterval := geocoderLimiter.interval
	geocoderURL = "http://127.0.0.1:59998"
	geocoderLimiter.interval = 0
	defer func() {
		geocoderURL = origURL
		geocoderLimiter.interval = origInterval
	}()

	calls := 0
	mock := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/search" {
			t.Errorf("expected upstream /search, got %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("countrycodes"); got != "et" {
			t.Errorf("expected default Ethiopia country code, got %q", got)
		}
		if got := r.Header.Get("User-Agent"); !strings.Contains(got, "amana-osrm-api") {
			t.Errorf("expected custom User-Agent, got %q", got)
		}
		_, _ = rw.Write([]byte(`[
			{"place_id":123,"osm_type":"node","osm_id":456,"name":"Posta","display_name":"Posta, Adama, Oromia, Ethiopia","lat":"8.54000","lon":"39.27000","class":"amenity","type":"post_office","address":{"city":"Adama","amenity":"post office"}},
			{"place_id":124,"osm_type":"way","osm_id":789,"display_name":"ASTU Gate 1, Adama, Ethiopia","lat":"8.55000","lon":"39.28000","class":"highway","type":"residential","address":{"road":"ASTU Gate 1"}}
		]`))
	}))
	defer mock.Close()
	geocoderURL = mock.URL

	req := httptest.NewRequest("GET", "/search?q=posta&near=39.27,8.54", nil)
	w := httptest.NewRecorder()
	handleSearch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("expected X-Cache MISS, got %s", w.Header().Get("X-Cache"))
	}
	var resp SearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode search response: %v", err)
	}
	if resp.Query != "posta" || len(resp.Results) != 2 {
		t.Fatalf("unexpected search response: %+v", resp)
	}
	if resp.Results[0].Name != "Posta" || resp.Results[0].Location[0] != 39.27 || resp.Results[0].Location[1] != 8.54 {
		t.Fatalf("unexpected first result: %+v", resp.Results[0])
	}
	if resp.Results[1].Name != "ASTU Gate 1" {
		t.Fatalf("expected name from address.road, got %+v", resp.Results[1])
	}

	// Cached repeat
	w2 := httptest.NewRecorder()
	handleSearch(w2, httptest.NewRequest("GET", "/search?q=posta&near=39.27,8.54", nil))
	if w2.Header().Get("X-Cache") != "HIT" || calls != 1 {
		t.Fatalf("expected cache hit with 1 upstream call, got header=%s calls=%d", w2.Header().Get("X-Cache"), calls)
	}

	// Validation
	w3 := httptest.NewRecorder()
	handleSearch(w3, httptest.NewRequest("GET", "/search", nil))
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing q, got %d", w3.Code)
	}
	w4 := httptest.NewRecorder()
	handleSearch(w4, httptest.NewRequest("GET", "/search?q=x&limit=99", nil))
	if w4.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid limit, got %d", w4.Code)
	}
}

func TestSearchWithAreaScope(t *testing.T) {
	searchCache.Purge()
	origURL := geocoderURL
	origInterval := geocoderLimiter.interval
	geocoderLimiter.interval = 0
	defer func() {
		geocoderURL = origURL
		geocoderLimiter.interval = origInterval
	}()

	searchCalls := 0
	mock := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "Adama" {
			// Area resolution request.
			_, _ = rw.Write([]byte(`[{"display_name":"Adama, Ethiopia","lat":"8.541","lon":"39.270","boundingbox":["8.49","8.59","39.24","39.30"]}]`))
			return
		}
		if q == "Nowhere" {
			_, _ = rw.Write([]byte(`[]`))
			return
		}
		searchCalls++
		if got := r.URL.Query().Get("bounded"); got != "1" {
			t.Errorf("expected bounded=1 for scoped search, got %q", got)
		}
		if got := r.URL.Query().Get("viewbox"); got != "39.240000,8.590000,39.300000,8.490000" {
			t.Errorf("unexpected viewbox: %q", got)
		}
		// Second result is farther from near=39.27,8.54; it must be ranked last.
		_, _ = rw.Write([]byte(`[
			{"name":"Far Place","display_name":"Far Place, Addis Ababa, Ethiopia","lat":"9.01","lon":"38.75"},
			{"name":"Near Place","display_name":"Near Place, Adama, Ethiopia","lat":"8.54","lon":"39.27"}
		]`))
	}))
	defer mock.Close()
	geocoderURL = mock.URL

	req := httptest.NewRequest("GET", "/search?q=place&city=Adama&near=39.27,8.54", nil)
	w := httptest.NewRecorder()
	handleSearch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp SearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Results) != 2 || resp.Results[0].Name != "Near Place" {
		t.Fatalf("expected near result first, got %+v", resp.Results)
	}
	if searchCalls != 1 {
		t.Fatalf("expected 1 place search call, got %d", searchCalls)
	}

	// Unknown area should fail validation instead of silently broadening the scope.
	w2 := httptest.NewRecorder()
	handleSearch(w2, httptest.NewRequest("GET", "/search?q=place&area=Nowhere", nil))
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown area, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestReverseEndpoint(t *testing.T) {
	searchCache.Purge()
	origURL := geocoderURL
	origInterval := geocoderLimiter.interval
	geocoderURL = "http://127.0.0.1:59997"
	geocoderLimiter.interval = 0
	defer func() {
		geocoderURL = origURL
		geocoderLimiter.interval = origInterval
	}()

	mock := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reverse" {
			t.Errorf("expected upstream /reverse, got %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("lat"); got != "8.54000" {
			t.Errorf("expected lat 8.54000, got %q", got)
		}
		_, _ = rw.Write([]byte(`{"place_id":42,"osm_type":"way","osm_id":99,"display_name":"Bole Road, Adama, Oromia, Ethiopia","lat":"8.54001","lon":"39.27001","class":"highway","type":"primary","address":{"road":"Bole Road","city":"Adama"}}`))
	}))
	defer mock.Close()
	geocoderURL = mock.URL

	req := httptest.NewRequest("GET", "/reverse?lon=39.27&lat=8.54", nil)
	w := httptest.NewRecorder()
	handleReverse(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ReverseResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode reverse response: %v", err)
	}
	if resp.Place == nil || resp.Place.Name != "Bole Road" || resp.Location[0] != 39.27 || resp.Location[1] != 8.54 {
		t.Fatalf("unexpected reverse response: %+v", resp)
	}

	w2 := httptest.NewRecorder()
	handleReverse(w2, httptest.NewRequest("GET", "/reverse?lon=39.27&lat=8.54", nil))
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("expected cache hit, got %s", w2.Header().Get("X-Cache"))
	}

	w3 := httptest.NewRecorder()
	handleReverse(w3, httptest.NewRequest("GET", "/reverse?lat=8.54", nil))
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing lon, got %d", w3.Code)
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
