package main

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	osrmURL      = getEnv("OSRM_URL", getEnv("OSRM_BICYCLE", "http://127.0.0.1:5000"))
	port         = getEnv("PORT", "3000")
	strictBounds = getEnv("STRICT_BOUNDS", "true") == "true"
)

const (
	profileName = "bicycle"

	// Ethiopia bounding box
	MinLon = 32.0
	MaxLon = 48.5
	MinLat = 3.0
	MaxLat = 15.5
)

// LRU Cache with TTL
type cacheEntry struct {
	key       string
	value     []byte
	expiresAt time.Time
}

type LRUCache struct {
	mu        sync.RWMutex
	capacity  int
	ttl       time.Duration
	items     map[string]*list.Element
	evictList *list.List
}

func NewLRUCache(capacity int, ttl time.Duration) *LRUCache {
	return &LRUCache{
		capacity:  capacity,
		ttl:       ttl,
		items:     make(map[string]*list.Element),
		evictList: list.New(),
	}
}

func (c *LRUCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, exists := c.items[key]
	if !exists {
		return nil, false
	}

	entry := element.Value.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.evictList.Remove(element)
		delete(c.items, key)
		return nil, false
	}

	c.evictList.MoveToFront(element)
	return entry.value, true
}

func (c *LRUCache) Set(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if element, exists := c.items[key]; exists {
		c.evictList.MoveToFront(element)
		entry := element.Value.(*cacheEntry)
		entry.value = value
		entry.expiresAt = time.Now().Add(c.ttl)
		return
	}

	for len(c.items) >= c.capacity && c.evictList.Len() > 0 {
		oldest := c.evictList.Back()
		if oldest != nil {
			c.evictList.Remove(oldest)
			oldEntry := oldest.Value.(*cacheEntry)
			delete(c.items, oldEntry.key)
		}
	}

	entry := &cacheEntry{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	elem := c.evictList.PushFront(entry)
	c.items[key] = elem
}

func (c *LRUCache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element)
	c.evictList.Init()
}

func (c *LRUCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Global cache: 10,000 entries, 24-hour TTL
var routeCache = NewLRUCache(10000, 24*time.Hour)

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func roundFloat(val float64, decimals int) float64 {
	pow := math.Pow10(decimals)
	return math.Round(val*pow) / pow
}

// normalizeAndValidate rounds coordinates to 5 decimal places (~1.1m precision),
// detects swapped [lat, lon] coordinates and auto-corrects them to [lon, lat],
// and verifies they fall within the coverage area of Ethiopia.
func normalizeAndValidate(p1, p2 float64) (float64, float64, error) {
	// Auto-correct: user provided [lat, lon]
	if p1 >= MinLat && p1 <= MaxLat && p2 >= MinLon && p2 <= MaxLon {
		return roundFloat(p2, 5), roundFloat(p1, 5), nil
	}

	// Standard format: user provided [lon, lat]
	if p1 >= MinLon && p1 <= MaxLon && p2 >= MinLat && p2 <= MaxLat {
		return roundFloat(p1, 5), roundFloat(p2, 5), nil
	}

	if !strictBounds {
		if p1 >= -180 && p1 <= 180 && p2 >= -90 && p2 <= 90 {
			return roundFloat(p1, 5), roundFloat(p2, 5), nil
		}
	}

	return 0, 0, fmt.Errorf("coordinates [%.5f, %.5f] are outside the Ethiopia coverage area (lon: %.1f-%.1f, lat: %.1f-%.1f)",
		p1, p2, MinLon, MaxLon, MinLat, MaxLat)
}

func parseCoords(s string) ([2]float64, error) {
	var c [2]float64
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return c, fmt.Errorf("coords must be formatted as lon,lat or lat,lon")
	}
	p1, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	p2, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return c, fmt.Errorf("coords must be valid numbers")
	}

	lon, lat, err := normalizeAndValidate(p1, p2)
	if err != nil {
		return c, err
	}
	return [2]float64{lon, lat}, nil
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeCachedJSON(w http.ResponseWriter, status int, hit bool, data []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400, stale-while-revalidate=3600")
	if hit {
		w.Header().Set("X-Cache", "HIT")
	} else {
		w.Header().Set("X-Cache", "MISS")
	}
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// OSRM raw response models
type OsrmManeuver struct {
	Type     string `json:"type,omitempty"`
	Modifier string `json:"modifier,omitempty"`
}

type OsrmStep struct {
	Distance float64                `json:"distance"`
	Duration float64                `json:"duration"`
	Maneuver *OsrmManeuver          `json:"maneuver,omitempty"`
	Name     string                 `json:"name"`
	Geometry map[string]interface{} `json:"geometry"`
}

type OsrmLeg struct {
	Distance float64    `json:"distance"`
	Duration float64    `json:"duration"`
	Steps    []OsrmStep `json:"steps,omitempty"`
}

type OsrmRoute struct {
	Distance float64                `json:"distance"`
	Duration float64                `json:"duration"`
	Geometry map[string]interface{} `json:"geometry"`
	Legs     []OsrmLeg              `json:"legs"`
}

type OsrmWaypoint struct {
	Location [2]float64 `json:"location"`
	Name     string     `json:"name"`
}

type OsrmResponse struct {
	Code      string         `json:"code"`
	Message   string         `json:"message,omitempty"`
	Routes    []OsrmRoute    `json:"routes,omitempty"`
	Waypoints []OsrmWaypoint `json:"waypoints,omitempty"`
	Distances [][]float64    `json:"distances,omitempty"`
	Durations [][]float64    `json:"durations,omitempty"`
}

// Formatted response models
type FormattedStep struct {
	DistanceMeters  float64       `json:"distance_meters"`
	DurationSeconds int           `json:"duration_seconds"`
	Instruction     *OsrmManeuver `json:"instruction"`
	Name            string        `json:"name"`
	Geometry        interface{}   `json:"geometry"`
}

type FormattedLeg struct {
	DistanceMeters  float64         `json:"distance_meters"`
	DistanceKm      float64         `json:"distance_km"`
	DurationSeconds int             `json:"duration_seconds"`
	DurationMinutes float64         `json:"duration_minutes"`
	Steps           []FormattedStep `json:"steps,omitempty"`
}

type FormattedRoute struct {
	DistanceMeters  float64        `json:"distance_meters"`
	DistanceKm      float64        `json:"distance_km"`
	DurationSeconds int            `json:"duration_seconds"`
	DurationMinutes float64        `json:"duration_minutes"`
	Geometry        interface{}    `json:"geometry"`
	Legs            []FormattedLeg `json:"legs"`
}

type FormattedDistance struct {
	Vehicle         string  `json:"vehicle"`
	DistanceMeters  float64 `json:"distance_meters"`
	DistanceKm      float64 `json:"distance_km"`
	DurationSeconds int     `json:"duration_seconds"`
	DurationMinutes float64 `json:"duration_minutes"`
}

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func osrmGet(reqPath string) (*OsrmResponse, error) {
	targetURL := strings.TrimRight(osrmURL, "/") + reqPath
	resp, err := httpClient.Get(targetURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var osrmResp OsrmResponse
	if err := json.Unmarshal(body, &osrmResp); err != nil {
		return nil, fmt.Errorf("invalid OSRM response: %w", err)
	}
	return &osrmResp, nil
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	// Active health check: verify OSRM backend connectivity
	testURL := strings.TrimRight(osrmURL, "/") + "/route/v1/driving/38.7577,9.0128;38.7578,9.0129?overview=false"
	checkClient := http.Client{Timeout: 2 * time.Second}
	resp, err := checkClient.Get(testURL)

	if err != nil || resp.StatusCode >= 500 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":         "degraded",
			"vehicle":        profileName,
			"profiles":       []string{profileName},
			"backend_status": "offline",
			"cached_routes":  routeCache.Len(),
			"error":          "OSRM bicycle backend is unreachable",
		})
		return
	}
	defer resp.Body.Close()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "ok",
		"vehicle":        profileName,
		"profiles":       []string{profileName},
		"backend_status": "online",
		"cached_routes":  routeCache.Len(),
	})
}

func handleRoute(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	alternatives := r.URL.Query().Get("alternatives") == "true"
	steps := r.URL.Query().Get("steps") == "true"

	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query params required")
		return
	}

	fromCoords, err1 := parseCoords(from)
	if err1 != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'from' coords: %v", err1))
		return
	}
	toCoords, err2 := parseCoords(to)
	if err2 != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'to' coords: %v", err2))
		return
	}

	cacheKey := fmt.Sprintf("route:%.5f,%.5f->%.5f,%.5f:alt=%t:steps=%t",
		fromCoords[0], fromCoords[1], toCoords[0], toCoords[1], alternatives, steps)

	if cached, hit := routeCache.Get(cacheKey); hit {
		writeCachedJSON(w, http.StatusOK, true, cached)
		return
	}

	params := url.Values{}
	params.Set("overview", "full")
	params.Set("alternatives", strconv.FormatBool(alternatives))
	params.Set("steps", strconv.FormatBool(steps))
	params.Set("geometries", "geojson")

	path := fmt.Sprintf("/route/v1/driving/%.5f,%.5f;%.5f,%.5f?%s",
		fromCoords[0], fromCoords[1], toCoords[0], toCoords[1], params.Encode())

	result, err := osrmGet(path)
	if err != nil {
		log.Printf("Route error: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to calculate route")
		return
	}

	if result.Code != "Ok" || len(result.Routes) == 0 {
		writeError(w, http.StatusNotFound, "No route found")
		return
	}

	formattedRoutes := make([]FormattedRoute, len(result.Routes))
	for i, route := range result.Routes {
		legs := make([]FormattedLeg, len(route.Legs))
		for j, leg := range route.Legs {
			var formattedSteps []FormattedStep
			if len(leg.Steps) > 0 {
				formattedSteps = make([]FormattedStep, len(leg.Steps))
				for k, step := range leg.Steps {
					formattedSteps[k] = FormattedStep{
						DistanceMeters:  step.Distance,
						DurationSeconds: int(math.Round(step.Duration)),
						Instruction:     step.Maneuver,
						Name:            step.Name,
						Geometry:        step.Geometry,
					}
				}
			}
			legs[j] = FormattedLeg{
				DistanceMeters:  leg.Distance,
				DistanceKm:      roundFloat(leg.Distance/1000.0, 2),
				DurationSeconds: int(math.Round(leg.Duration)),
				DurationMinutes: roundFloat(leg.Duration/60.0, 1),
				Steps:           formattedSteps,
			}
		}

		formattedRoutes[i] = FormattedRoute{
			DistanceMeters:  route.Distance,
			DistanceKm:      roundFloat(route.Distance/1000.0, 2),
			DurationSeconds: int(math.Round(route.Duration)),
			DurationMinutes: roundFloat(route.Duration/60.0, 1),
			Geometry:        route.Geometry,
			Legs:            legs,
		}
	}

	respData := map[string]interface{}{
		"vehicle":   profileName,
		"routes":    formattedRoutes,
		"waypoints": result.Waypoints,
	}

	jsonBytes, err := json.Marshal(respData)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to serialize route response")
		return
	}

	routeCache.Set(cacheKey, jsonBytes)
	writeCachedJSON(w, http.StatusOK, false, jsonBytes)
}

func handleDistance(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query params required")
		return
	}

	fromCoords, err1 := parseCoords(from)
	if err1 != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'from' coords: %v", err1))
		return
	}
	toCoords, err2 := parseCoords(to)
	if err2 != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid 'to' coords: %v", err2))
		return
	}

	cacheKey := fmt.Sprintf("dist:%.5f,%.5f->%.5f,%.5f",
		fromCoords[0], fromCoords[1], toCoords[0], toCoords[1])

	if cached, hit := routeCache.Get(cacheKey); hit {
		writeCachedJSON(w, http.StatusOK, true, cached)
		return
	}

	path := fmt.Sprintf("/route/v1/driving/%.5f,%.5f;%.5f,%.5f?overview=false",
		fromCoords[0], fromCoords[1], toCoords[0], toCoords[1])

	result, err := osrmGet(path)
	if err != nil {
		log.Printf("Distance error: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to calculate distance")
		return
	}

	if result.Code != "Ok" || len(result.Routes) == 0 {
		writeError(w, http.StatusNotFound, "No route found")
		return
	}

	route := result.Routes[0]
	respData := FormattedDistance{
		Vehicle:         profileName,
		DistanceMeters:  route.Distance,
		DistanceKm:      roundFloat(route.Distance/1000.0, 2),
		DurationSeconds: int(math.Round(route.Duration)),
		DurationMinutes: roundFloat(route.Duration/60.0, 1),
	}

	jsonBytes, err := json.Marshal(respData)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to serialize distance response")
		return
	}

	routeCache.Set(cacheKey, jsonBytes)
	writeCachedJSON(w, http.StatusOK, false, jsonBytes)
}

type MatrixRequest struct {
	Coordinates [][2]float64 `json:"coordinates"`
	Vehicle     string       `json:"vehicle,omitempty"`
}

func handleMatrix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read body")
		return
	}

	var req MatrixRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	if len(req.Coordinates) < 2 {
		writeError(w, http.StatusBadRequest, "coordinates array with at least 2 coordinate pairs required")
		return
	}

	normalizedCoords := make([][2]float64, len(req.Coordinates))
	coordStrs := make([]string, len(req.Coordinates))
	indices := make([]string, len(req.Coordinates))

	for i, c := range req.Coordinates {
		lon, lat, err := normalizeAndValidate(c[0], c[1])
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid coordinate at index %d: %v", i, err))
			return
		}
		normalizedCoords[i] = [2]float64{lon, lat}
		coordStrs[i] = fmt.Sprintf("%.5f,%.5f", lon, lat)
		indices[i] = strconv.Itoa(i)
	}

	cacheKey := fmt.Sprintf("matrix:%s", strings.Join(coordStrs, ";"))
	if cached, hit := routeCache.Get(cacheKey); hit {
		writeCachedJSON(w, http.StatusOK, true, cached)
		return
	}

	idxJoined := strings.Join(indices, ";")
	path := fmt.Sprintf("/table/v1/driving/%s?sources=%s&destinations=%s&annotations=distance,duration",
		strings.Join(coordStrs, ";"), idxJoined, idxJoined)

	result, err := osrmGet(path)
	if err != nil {
		log.Printf("Matrix error: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to calculate matrix")
		return
	}

	if result.Code != "Ok" {
		writeError(w, http.StatusNotFound, "Could not compute matrix")
		return
	}

	waypoints := normalizedCoords
	if len(result.Waypoints) > 0 {
		waypoints = make([][2]float64, len(result.Waypoints))
		for i, wp := range result.Waypoints {
			waypoints[i] = wp.Location
		}
	}

	respData := map[string]interface{}{
		"vehicle":     profileName,
		"distances":   result.Distances,
		"durations":   result.Durations,
		"coordinates": waypoints,
	}

	jsonBytes, err := json.Marshal(respData)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to serialize matrix response")
		return
	}

	routeCache.Set(cacheKey, jsonBytes)
	writeCachedJSON(w, http.StatusOK, false, jsonBytes)
}

// corsMiddleware adds standard CORS headers
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// findMapPath locates the pre-compiled OSRM map dataset
func findMapPath() string {
	if custom := os.Getenv("OSRM_MAP_PATH"); custom != "" {
		return custom
	}
	candidates := []string{
		"/data/ethiopia-bicycle.osrm",
		"/opt/osrm-data/ethiopia-bicycle.osrm",
		"./ethiopia-bicycle.osrm",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path + ".mldgr"); err == nil {
			return path
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// startSupervisor checks if osrm-routed and map data exist, and if so launches
// osrm-routed with memory mapping enabled (--mmap) in a managed child process.
func startSupervisor() *exec.Cmd {
	osrmBin, err := exec.LookPath("osrm-routed")
	if err != nil {
		log.Printf("[Supervisor] osrm-routed binary not found in PATH; operating in standalone API mode")
		return nil
	}

	mapPath := findMapPath()
	if mapPath == "" {
		log.Printf("[Supervisor] No OSRM map dataset found; operating in standalone API mode")
		return nil
	}

	// Adjust permissions if mounted volume requires it
	_ = exec.Command("chmod", "-R", "777", "/opt/osrm-data", "/data").Run()

	log.Printf("[Supervisor] Map found: %s", mapPath)
	log.Printf("[Supervisor] Starting osrm-routed with --mmap on 127.0.0.1:5000...")

	cmd := exec.Command(osrmBin,
		"--algorithm", "mld",
		"--mmap",
		"--max-table-size", "100",
		"--ip", "127.0.0.1",
		"--port", "5000",
		mapPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Printf("[Supervisor] Failed to launch osrm-routed: %v", err)
		return nil
	}

	log.Printf("[Supervisor] osrm-routed running with PID %d", cmd.Process.Pid)

	// Wait for OSRM to bind to port 5000 (up to 30 seconds)
	ready := false
	for i := 0; i < 60; i++ {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:5000", 250*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if ready {
		log.Printf("[Supervisor] OSRM backend is healthy and listening on 127.0.0.1:5000")
	} else {
		log.Printf("[Supervisor] WARNING: OSRM backend did not bind to port 5000 within 30s")
	}

	return cmd
}

func main() {
	cmd := startSupervisor()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/route", handleRoute)
	mux.HandleFunc("/distance", handleDistance)
	mux.HandleFunc("/matrix", handleMatrix)

	handler := corsMiddleware(mux)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}

	// Channel to capture shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("OSRM Bicycle Routing Service (Go) listening on port %s", port)
		log.Printf("OSRM Backend URL: %s", osrmURL)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-sigChan
	log.Printf("Shutting down service...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	if cmd != nil && cmd.Process != nil {
		log.Printf("[Supervisor] Terminating osrm-routed (PID %d)...", cmd.Process.Pid)
		_ = cmd.Process.Signal(syscall.SIGTERM)

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		select {
		case <-time.After(4 * time.Second):
			log.Printf("[Supervisor] osrm-routed did not stop in time; killing...")
			_ = cmd.Process.Kill()
		case err := <-done:
			log.Printf("[Supervisor] osrm-routed exited: %v", err)
		}
	}

	log.Printf("Shutdown complete.")
}

