package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseCoords(t *testing.T) {
	coords, err := parseCoords("38.7577,9.0128")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if coords[0] != 38.7577 || coords[1] != 9.0128 {
		t.Fatalf("unexpected coords: %v", coords)
	}

	_, err = parseCoords("invalid")
	if err == nil {
		t.Fatal("expected error for invalid coords")
	}

	_, err = parseCoords("12.34,invalid")
	if err == nil {
		t.Fatal("expected error for non-float coords")
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
}

func TestHealthCheck(t *testing.T) {
	// 1. When backend is down
	origURL := osrmURL
	osrmURL = "http://127.0.0.1:59999" // unreachable port
	defer func() { osrmURL = origURL }()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handleHealth(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}

	var degradedResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &degradedResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if degradedResp["backend_status"] != "offline" {
		t.Fatalf("expected backend_status offline, got %v", degradedResp["backend_status"])
	}

	// 2. When backend is up
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
	var okResp map[string]interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &okResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if okResp["backend_status"] != "online" {
		t.Fatalf("expected backend_status online, got %v", okResp["backend_status"])
	}
}

func TestDistanceEndpoint(t *testing.T) {
	mockBackend := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		osrmRes := OsrmResponse{
			Code: "Ok",
			Routes: []OsrmRoute{
				{
					Distance: 4804.6,
					Duration: 298.4,
				},
			},
		}
		_ = json.NewEncoder(rw).Encode(osrmRes)
	}))
	defer mockBackend.Close()

	origURL := osrmURL
	osrmURL = mockBackend.URL
	defer func() { osrmURL = origURL }()

	req := httptest.NewRequest("GET", "/distance?from=38.7577,9.0128&to=38.7891,9.0054&vehicle=bicycle", nil)
	w := httptest.NewRecorder()
	handleDistance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var dist FormattedDistance
	if err := json.Unmarshal(w.Body.Bytes(), &dist); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if dist.Vehicle != "bicycle" || dist.DistanceKm != 4.8 || dist.DurationMinutes != 5.0 {
		t.Fatalf("unexpected distance result: %+v", dist)
	}
}

func TestMatrixEndpoint(t *testing.T) {
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

	body := `{"coordinates": [[38.7577, 9.0128], [38.7891, 9.0054]], "vehicle": "bicycle"}`
	req := httptest.NewRequest("POST", "/matrix", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleMatrix(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
