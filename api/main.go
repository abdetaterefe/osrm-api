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
	// Ethiopia geographic bounding box
	MinLon = 32.0
	MaxLon = 48.5
	MinLat = 3.0
	MaxLat = 15.5
)

// LRUCache implements a thread-safe in-memory LRU cache with TTL
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

// OSRM Internal Structures
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

// Clean Public API Models
type WaypointInfo struct {
	Name     string     `json:"name"`
	Location [2]float64 `json:"location"`
}

type RouteStep struct {
	Instruction     string `json:"instruction"`
	StreetName      string `json:"street_name,omitempty"`
	DistanceMeters  int    `json:"distance_meters"`
	DurationSeconds int    `json:"duration_seconds"`
	Type            string `json:"type,omitempty"`
	Modifier        string `json:"modifier,omitempty"`
}

type DistanceResponse struct {
	DistanceKm      float64       `json:"distance_km"`
	DurationMinutes float64       `json:"duration_minutes"`
	DistanceMeters  int           `json:"distance_meters"`
	DurationSeconds int           `json:"duration_seconds"`
	Origin          *WaypointInfo `json:"origin,omitempty"`
	Destination     *WaypointInfo `json:"destination,omitempty"`
}

type RouteResponse struct {
	DistanceKm      float64       `json:"distance_km"`
	DurationMinutes float64       `json:"duration_minutes"`
	DistanceMeters  int           `json:"distance_meters"`
	DurationSeconds int           `json:"duration_seconds"`
	Origin          *WaypointInfo `json:"origin,omitempty"`
	Destination     *WaypointInfo `json:"destination,omitempty"`
	Geometry        interface{}   `json:"geometry"`
	Steps           []RouteStep   `json:"steps,omitempty"`
}

type MatrixResponse struct {
	DistancesKm      [][]float64    `json:"distances_km"`
	DurationsMinutes [][]float64    `json:"durations_minutes"`
	DistancesMeters  [][]int        `json:"distances_meters"`
	DurationsSeconds [][]int        `json:"durations_seconds"`
	Waypoints        []WaypointInfo `json:"waypoints"`
}

type HealthResponse struct {
	Status       string `json:"status"`
	Backend      string `json:"backend"`
	CachedRoutes int    `json:"cached_routes"`
}

func formatInstruction(m *OsrmManeuver, streetName string) string {
	target := streetName
	if target == "" {
		target = "road"
	}

	if m == nil {
		return fmt.Sprintf("Continue onto %s", target)
	}

	mod := m.Modifier
	switch m.Type {
	case "depart":
		if mod != "" && mod != "straight" {
			return fmt.Sprintf("Head %s on %s", mod, target)
		}
		return fmt.Sprintf("Head out on %s", target)
	case "arrive":
		return "Arrive at destination"
	case "turn":
		if mod != "" {
			return fmt.Sprintf("Turn %s onto %s", mod, target)
		}
		return fmt.Sprintf("Turn onto %s", target)
	case "continue", "new name":
		return fmt.Sprintf("Continue onto %s", target)
	case "end of road":
		if mod != "" {
			return fmt.Sprintf("At the end of the road, turn %s onto %s", mod, target)
		}
		return fmt.Sprintf("At the end of the road, turn onto %s", target)
	case "fork":
		if mod != "" {
			return fmt.Sprintf("Take the %s fork onto %s", mod, target)
		}
		return fmt.Sprintf("Take the fork onto %s", target)
	case "roundabout":
		return fmt.Sprintf("Enter the roundabout and take exit onto %s", target)
	case "merge":
		if mod != "" {
			return fmt.Sprintf("Merge %s onto %s", mod, target)
		}
		return fmt.Sprintf("Merge onto %s", target)
	default:
		if mod != "" {
			return fmt.Sprintf("Proceed %s onto %s", mod, target)
		}
		return fmt.Sprintf("Proceed onto %s", target)
	}
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
	testURL := strings.TrimRight(osrmURL, "/") + "/route/v1/driving/38.7577,9.0128;38.7578,9.0129?overview=false"
	checkClient := http.Client{Timeout: 2 * time.Second}
	resp, err := checkClient.Get(testURL)

	if err != nil || resp.StatusCode >= 500 {
		writeJSON(w, http.StatusServiceUnavailable, HealthResponse{
			Status:       "degraded",
			Backend:      "offline",
			CachedRoutes: routeCache.Len(),
		})
		return
	}
	defer resp.Body.Close()

	writeJSON(w, http.StatusOK, HealthResponse{
		Status:       "ok",
		Backend:      "online",
		CachedRoutes: routeCache.Len(),
	})
}

func handleRoute(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	steps := r.URL.Query().Get("steps") == "true"

	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query parameters required")
		return
	}

	fromCoords, err1 := parseCoords(from)
	if err1 != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid from coords: %v", err1))
		return
	}
	toCoords, err2 := parseCoords(to)
	if err2 != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid to coords: %v", err2))
		return
	}

	cacheKey := fmt.Sprintf("route:%.5f,%.5f->%.5f,%.5f:steps=%t",
		fromCoords[0], fromCoords[1], toCoords[0], toCoords[1], steps)

	if cached, hit := routeCache.Get(cacheKey); hit {
		writeCachedJSON(w, http.StatusOK, true, cached)
		return
	}

	params := url.Values{}
	params.Set("overview", "full")
	params.Set("alternatives", "false")
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

	route := result.Routes[0]

	var originInfo, destInfo *WaypointInfo
	if len(result.Waypoints) >= 2 {
		originInfo = &WaypointInfo{
			Name:     result.Waypoints[0].Name,
			Location: [2]float64{roundFloat(result.Waypoints[0].Location[0], 5), roundFloat(result.Waypoints[0].Location[1], 5)},
		}
		destInfo = &WaypointInfo{
			Name:     result.Waypoints[1].Name,
			Location: [2]float64{roundFloat(result.Waypoints[1].Location[0], 5), roundFloat(result.Waypoints[1].Location[1], 5)},
		}
	}

	var routeSteps []RouteStep
	if steps && len(route.Legs) > 0 {
		for _, leg := range route.Legs {
			for _, step := range leg.Steps {
				maneuverType := ""
				maneuverMod := ""
				if step.Maneuver != nil {
					maneuverType = step.Maneuver.Type
					maneuverMod = step.Maneuver.Modifier
				}
				routeSteps = append(routeSteps, RouteStep{
					Instruction:     formatInstruction(step.Maneuver, step.Name),
					StreetName:      step.Name,
					DistanceMeters:  int(math.Round(step.Distance)),
					DurationSeconds: int(math.Round(step.Duration)),
					Type:            maneuverType,
					Modifier:        maneuverMod,
				})
			}
		}
	}

	resp := RouteResponse{
		DistanceKm:      roundFloat(route.Distance/1000.0, 2),
		DurationMinutes: roundFloat(route.Duration/60.0, 1),
		DistanceMeters:  int(math.Round(route.Distance)),
		DurationSeconds: int(math.Round(route.Duration)),
		Origin:          originInfo,
		Destination:     destInfo,
		Geometry:        route.Geometry,
		Steps:           routeSteps,
	}

	jsonBytes, err := json.Marshal(resp)
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
		writeError(w, http.StatusBadRequest, "from and to query parameters required")
		return
	}

	fromCoords, err1 := parseCoords(from)
	if err1 != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid from coords: %v", err1))
		return
	}
	toCoords, err2 := parseCoords(to)
	if err2 != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid to coords: %v", err2))
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

	var originInfo, destInfo *WaypointInfo
	if len(result.Waypoints) >= 2 {
		originInfo = &WaypointInfo{
			Name:     result.Waypoints[0].Name,
			Location: [2]float64{roundFloat(result.Waypoints[0].Location[0], 5), roundFloat(result.Waypoints[0].Location[1], 5)},
		}
		destInfo = &WaypointInfo{
			Name:     result.Waypoints[1].Name,
			Location: [2]float64{roundFloat(result.Waypoints[1].Location[0], 5), roundFloat(result.Waypoints[1].Location[1], 5)},
		}
	}

	resp := DistanceResponse{
		DistanceKm:      roundFloat(route.Distance/1000.0, 2),
		DurationMinutes: roundFloat(route.Duration/60.0, 1),
		DistanceMeters:  int(math.Round(route.Distance)),
		DurationSeconds: int(math.Round(route.Duration)),
		Origin:          originInfo,
		Destination:     destInfo,
	}

	jsonBytes, err := json.Marshal(resp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to serialize distance response")
		return
	}

	routeCache.Set(cacheKey, jsonBytes)
	writeCachedJSON(w, http.StatusOK, false, jsonBytes)
}

type MatrixRequest struct {
	Coordinates [][2]float64 `json:"coordinates"`
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

	n := len(req.Coordinates)
	distancesKm := make([][]float64, n)
	durationsMinutes := make([][]float64, n)
	distancesMeters := make([][]int, n)
	durationsSeconds := make([][]int, n)

	for i := 0; i < n; i++ {
		distancesKm[i] = make([]float64, n)
		durationsMinutes[i] = make([]float64, n)
		distancesMeters[i] = make([]int, n)
		durationsSeconds[i] = make([]int, n)
		for j := 0; j < n; j++ {
			if i < len(result.Distances) && j < len(result.Distances[i]) {
				rawDist := result.Distances[i][j]
				distancesKm[i][j] = roundFloat(rawDist/1000.0, 2)
				distancesMeters[i][j] = int(math.Round(rawDist))
			}
			if i < len(result.Durations) && j < len(result.Durations[i]) {
				rawDur := result.Durations[i][j]
				durationsMinutes[i][j] = roundFloat(rawDur/60.0, 1)
				durationsSeconds[i][j] = int(math.Round(rawDur))
			}
		}
	}

	waypoints := make([]WaypointInfo, len(normalizedCoords))
	for i, c := range normalizedCoords {
		name := ""
		if i < len(result.Waypoints) {
			name = result.Waypoints[i].Name
		}
		waypoints[i] = WaypointInfo{
			Name:     name,
			Location: c,
		}
	}

	resp := MatrixResponse{
		DistancesKm:      distancesKm,
		DurationsMinutes: durationsMinutes,
		DistancesMeters:  distancesMeters,
		DurationsSeconds: durationsSeconds,
		Waypoints:        waypoints,
	}

	jsonBytes, err := json.Marshal(resp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to serialize matrix response")
		return
	}

	routeCache.Set(cacheKey, jsonBytes)
	writeCachedJSON(w, http.StatusOK, false, jsonBytes)
}

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

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("OSRM Bicycle Routing Service listening on port %s", port)
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
