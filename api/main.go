package main

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
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
	valhallaURL    = getEnv("VALHALLA_URL", getEnv("OSRM_URL", "http://127.0.0.1:8002"))
	port           = getEnv("PORT", "3000")
	defaultProfile = getEnv("DEFAULT_PROFILE", "bicycle")
	strictBounds   = getEnv("STRICT_BOUNDS", "true") == "true"
)

const (
	// Ethiopia geographic bounding box
	MinLon = 32.0
	MaxLon = 48.5
	MinLat = 3.0
	MaxLat = 15.5
)

// ProfileInfo describes a routing profile and its Valhalla costing model
type ProfileInfo struct {
	Name        string   `json:"name"`
	Costing     string   `json:"costing"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
}

var SupportedProfiles = map[string]ProfileInfo{
	"bicycle": {
		Name:        "bicycle",
		Costing:     "bicycle",
		Aliases:     []string{"bike", "cycling"},
		Description: "Bicycle routing prioritizing cycling paths, tracks, and quiet roads",
	},
	"car": {
		Name:        "car",
		Costing:     "auto",
		Aliases:     []string{"auto", "driving", "automobile"},
		Description: "Automobile routing based on standard Ethiopian road classification and speed limits",
	},
	"motorcycle": {
		Name:        "motorcycle",
		Costing:     "motorcycle",
		Aliases:     []string{"moto", "motorbike"},
		Description: "Motorcycle routing with agile traversal and flexible road permissions",
	},
	"pedestrian": {
		Name:        "pedestrian",
		Costing:     "pedestrian",
		Aliases:     []string{"walk", "foot", "walking"},
		Description: "Pedestrian walking routes utilizing sidewalks, crosswalks, and footways",
	},
}

func resolveProfile(raw string) (ProfileInfo, error) {
	if raw == "" {
		raw = defaultProfile
	}
	clean := strings.ToLower(strings.TrimSpace(raw))

	if info, ok := SupportedProfiles[clean]; ok {
		return info, nil
	}

	for _, info := range SupportedProfiles {
		for _, alias := range info.Aliases {
			if clean == alias {
				return info, nil
			}
		}
	}

	return ProfileInfo{}, fmt.Errorf("unsupported profile '%s'. Supported: bicycle, car, motorcycle, pedestrian", raw)
}

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

// OSRM compatibility structures (returned by Valhalla with format=osrm)
type OsrmManeuver struct {
	Type     string `json:"type,omitempty"`
	Modifier string `json:"modifier,omitempty"`
}

type OsrmStep struct {
	Distance    float64                `json:"distance"`
	Duration    float64                `json:"duration"`
	Instruction string                 `json:"instruction,omitempty"`
	Maneuver    *OsrmManeuver          `json:"maneuver,omitempty"`
	Name        string                 `json:"name"`
	Geometry    map[string]interface{} `json:"geometry"`
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

// Valhalla native matrix structures
type ValhallaMatrixElement struct {
	Distance  float64 `json:"distance"` // kilometers
	Time      float64 `json:"time"`     // seconds
	FromIndex int     `json:"from_index"`
	ToIndex   int     `json:"to_index"`
}

type ValhallaMatrixResponse struct {
	SourcesToTargets [][]ValhallaMatrixElement `json:"sources_to_targets"`
	Error            string                    `json:"error,omitempty"`
	ErrorCode        int                       `json:"error_code,omitempty"`
}

// Clean Public API Response Models
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
	Profile         string        `json:"profile"`
	DistanceKm      float64       `json:"distance_km"`
	DurationMinutes float64       `json:"duration_minutes"`
	DistanceMeters  int           `json:"distance_meters"`
	DurationSeconds int           `json:"duration_seconds"`
	Origin          *WaypointInfo `json:"origin,omitempty"`
	Destination     *WaypointInfo `json:"destination,omitempty"`
}

type RouteResponse struct {
	Profile         string        `json:"profile"`
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
	Profile          string         `json:"profile"`
	DistancesKm      [][]float64    `json:"distances_km"`
	DurationsMinutes [][]float64    `json:"durations_minutes"`
	DistancesMeters  [][]int        `json:"distances_meters"`
	DurationsSeconds [][]int        `json:"durations_seconds"`
	Waypoints        []WaypointInfo `json:"waypoints"`
}

type HealthResponse struct {
	Status         string   `json:"status"`
	Backend        string   `json:"backend"`
	Engine         string   `json:"engine"`
	DefaultProfile string   `json:"default_profile"`
	Profiles       []string `json:"profiles"`
	CachedRoutes   int      `json:"cached_routes"`
}

type ProfilesResponse struct {
	DefaultProfile string        `json:"default_profile"`
	Profiles       []ProfileInfo `json:"profiles"`
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

// Dedicated connection-pooled HTTP client
var httpClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression: true,
	},
}

// valhallaRoute queries Valhalla /route in OSRM-compatible format
func valhallaRoute(from, to [2]float64, costing string, steps bool, shapeFormat string) (*OsrmResponse, error) {
	reqBody := map[string]interface{}{
		"locations": []map[string]float64{
			{"lon": from[0], "lat": from[1]},
			{"lon": to[0], "lat": to[1]},
		},
		"costing":             costing,
		"format":              "osrm",
		"shape_format":        shapeFormat,
		"banner_instructions": false,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	targetURL := strings.TrimRight(valhallaURL, "/") + "/route"
	resp, err := httpClient.Post(targetURL, "application/json", bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("valhalla error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var osrmResp OsrmResponse
	if err := json.Unmarshal(body, &osrmResp); err != nil {
		return nil, fmt.Errorf("invalid Valhalla route response: %w", err)
	}

	return &osrmResp, nil
}

// valhallaMatrix queries Valhalla /sources_to_targets
func valhallaMatrix(coords [][2]float64, costing string) (*ValhallaMatrixResponse, error) {
	locs := make([]map[string]float64, len(coords))
	for i, c := range coords {
		locs[i] = map[string]float64{
			"lon": c[0],
			"lat": c[1],
		}
	}

	reqBody := map[string]interface{}{
		"sources": locs,
		"targets": locs,
		"costing": costing,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	targetURL := strings.TrimRight(valhallaURL, "/") + "/sources_to_targets"
	resp, err := httpClient.Post(targetURL, "application/json", bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("valhalla matrix error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var matResp ValhallaMatrixResponse
	if err := json.Unmarshal(body, &matResp); err != nil {
		return nil, fmt.Errorf("invalid Valhalla matrix response: %w", err)
	}

	return &matResp, nil
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	testURL := strings.TrimRight(valhallaURL, "/") + "/status"
	checkClient := http.Client{Timeout: 2 * time.Second}
	resp, err := checkClient.Get(testURL)

	profilesList := make([]string, 0, len(SupportedProfiles))
	for name := range SupportedProfiles {
		profilesList = append(profilesList, name)
	}

	if err != nil || resp.StatusCode >= 500 {
		writeJSON(w, http.StatusServiceUnavailable, HealthResponse{
			Status:         "degraded",
			Backend:        "offline",
			Engine:         "valhalla",
			DefaultProfile: defaultProfile,
			Profiles:       profilesList,
			CachedRoutes:   routeCache.Len(),
		})
		return
	}
	defer resp.Body.Close()

	writeJSON(w, http.StatusOK, HealthResponse{
		Status:         "ok",
		Backend:        "online",
		Engine:         "valhalla",
		DefaultProfile: defaultProfile,
		Profiles:       profilesList,
		CachedRoutes:   routeCache.Len(),
	})
}

func handleProfiles(w http.ResponseWriter, r *http.Request) {
	profiles := make([]ProfileInfo, 0, len(SupportedProfiles))
	for _, p := range SupportedProfiles {
		profiles = append(profiles, p)
	}
	writeJSON(w, http.StatusOK, ProfilesResponse{
		DefaultProfile: defaultProfile,
		Profiles:       profiles,
	})
}

func handleRoute(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	profileParam := r.URL.Query().Get("profile")
	if profileParam == "" {
		profileParam = r.URL.Query().Get("mode")
	}
	steps := r.URL.Query().Get("steps") == "true"

	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query parameters required")
		return
	}

	profile, err := resolveProfile(profileParam)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	cacheKey := fmt.Sprintf("route:%s:%.5f,%.5f->%.5f,%.5f:steps=%t",
		profile.Name, fromCoords[0], fromCoords[1], toCoords[0], toCoords[1], steps)

	if cached, hit := routeCache.Get(cacheKey); hit {
		writeCachedJSON(w, http.StatusOK, true, cached)
		return
	}

	result, err := valhallaRoute(fromCoords, toCoords, profile.Costing, steps, "geojson")
	if err != nil {
		log.Printf("Route calculation error [%s]: %v", profile.Name, err)
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
				instruction := step.Instruction
				if instruction == "" {
					instruction = formatInstruction(step.Maneuver, step.Name)
				}
				routeSteps = append(routeSteps, RouteStep{
					Instruction:     instruction,
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
		Profile:         profile.Name,
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
	profileParam := r.URL.Query().Get("profile")
	if profileParam == "" {
		profileParam = r.URL.Query().Get("mode")
	}

	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query parameters required")
		return
	}

	profile, err := resolveProfile(profileParam)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	cacheKey := fmt.Sprintf("dist:%s:%.5f,%.5f->%.5f,%.5f",
		profile.Name, fromCoords[0], fromCoords[1], toCoords[0], toCoords[1])

	if cached, hit := routeCache.Get(cacheKey); hit {
		writeCachedJSON(w, http.StatusOK, true, cached)
		return
	}

	result, err := valhallaRoute(fromCoords, toCoords, profile.Costing, false, "no_shape")
	if err != nil {
		log.Printf("Distance error [%s]: %v", profile.Name, err)
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
		Profile:         profile.Name,
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
	Profile     string       `json:"profile,omitempty"`
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

	profileParam := req.Profile
	if profileParam == "" {
		profileParam = r.URL.Query().Get("profile")
	}
	if profileParam == "" {
		profileParam = r.URL.Query().Get("mode")
	}

	profile, err := resolveProfile(profileParam)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if len(req.Coordinates) < 2 {
		writeError(w, http.StatusBadRequest, "coordinates array with at least 2 coordinate pairs required")
		return
	}

	normalizedCoords := make([][2]float64, len(req.Coordinates))
	coordStrs := make([]string, len(req.Coordinates))

	for i, c := range req.Coordinates {
		lon, lat, err := normalizeAndValidate(c[0], c[1])
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid coordinate at index %d: %v", i, err))
			return
		}
		normalizedCoords[i] = [2]float64{lon, lat}
		coordStrs[i] = fmt.Sprintf("%.5f,%.5f", lon, lat)
	}

	h := sha256.New()
	h.Write([]byte(strings.Join(coordStrs, ";")))
	hashStr := hex.EncodeToString(h.Sum(nil))
	cacheKey := fmt.Sprintf("matrix:%s:%s", profile.Name, hashStr)

	if cached, hit := routeCache.Get(cacheKey); hit {
		writeCachedJSON(w, http.StatusOK, true, cached)
		return
	}

	result, err := valhallaMatrix(normalizedCoords, profile.Costing)
	if err != nil {
		log.Printf("Matrix error [%s]: %v", profile.Name, err)
		writeError(w, http.StatusInternalServerError, "Failed to calculate matrix")
		return
	}

	if len(result.SourcesToTargets) == 0 {
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
			if i < len(result.SourcesToTargets) && j < len(result.SourcesToTargets[i]) {
				elem := result.SourcesToTargets[i][j]
				distancesKm[i][j] = roundFloat(elem.Distance, 2)
				distancesMeters[i][j] = int(math.Round(elem.Distance * 1000.0))
				durationsMinutes[i][j] = roundFloat(elem.Time/60.0, 1)
				durationsSeconds[i][j] = int(math.Round(elem.Time))
			}
		}
	}

	waypoints := make([]WaypointInfo, len(normalizedCoords))
	for i, c := range normalizedCoords {
		waypoints[i] = WaypointInfo{
			Location: c,
		}
	}

	resp := MatrixResponse{
		Profile:          profile.Name,
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

func findValhallaConfig() string {
	if custom := os.Getenv("VALHALLA_CONFIG"); custom != "" {
		return custom
	}
	candidates := []string{
		"/custom_files/valhalla.json",
		"/data/valhalla/valhalla.json",
		"/opt/valhalla/valhalla.json",
		"./valhalla.json",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func startSupervisor() *exec.Cmd {
	valhallaBin, err := exec.LookPath("valhalla_service")
	if err != nil {
		log.Printf("[Supervisor] valhalla_service binary not found in PATH; operating in standalone API mode")
		return nil
	}

	configPath := findValhallaConfig()
	if configPath == "" {
		log.Printf("[Supervisor] No Valhalla config (valhalla.json) found; operating in standalone API mode")
		return nil
	}

	log.Printf("[Supervisor] Valhalla config found: %s", configPath)
	threads := getEnv("VALHALLA_THREADS", "1")
	log.Printf("[Supervisor] Starting valhalla_service with %s thread(s)...", threads)

	cmd := exec.Command(valhallaBin, configPath, threads)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Printf("[Supervisor] Failed to launch valhalla_service: %v", err)
		return nil
	}

	log.Printf("[Supervisor] valhalla_service running with PID %d", cmd.Process.Pid)

	ready := false
	statusURL := strings.TrimRight(valhallaURL, "/") + "/status"
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for i := 0; i < 60; i++ {
		resp, err := client.Get(statusURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			ready = true
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}

	if ready {
		log.Printf("[Supervisor] Valhalla backend is healthy and listening on %s", valhallaURL)
	} else {
		log.Printf("[Supervisor] WARNING: Valhalla backend did not report ready within 30s")
	}

	return cmd
}

func main() {
	cmd := startSupervisor()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/profiles", handleProfiles)
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
		log.Printf("Multi-Profile Navigation API listening on port %s", port)
		log.Printf("Valhalla Backend URL: %s", valhallaURL)
		log.Printf("Default Profile: %s", defaultProfile)
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
		log.Printf("[Supervisor] Terminating valhalla_service (PID %d)...", cmd.Process.Pid)
		_ = cmd.Process.Signal(syscall.SIGTERM)

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		select {
		case <-time.After(4 * time.Second):
			log.Printf("[Supervisor] valhalla_service did not stop in time; killing...")
			_ = cmd.Process.Kill()
		case err := <-done:
			log.Printf("[Supervisor] valhalla_service exited: %v", err)
		}
	}

	log.Printf("Shutdown complete.")
}
