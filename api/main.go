package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	osrmURL = getEnv("OSRM_URL", getEnv("OSRM_BICYCLE", "http://127.0.0.1:5000"))
	port    = getEnv("PORT", "3000")
)

const profileName = "bicycle"

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

func parseCoords(s string) ([2]float64, error) {
	var c [2]float64
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return c, fmt.Errorf("coords must be lon,lat numbers")
	}
	lon, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lat, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return c, fmt.Errorf("coords must be lon,lat numbers")
	}
	return [2]float64{lon, lat}, nil
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
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
	DistanceMeters  float64      `json:"distance_meters"`
	DurationSeconds int          `json:"duration_seconds"`
	Instruction     *OsrmManeuver `json:"instruction"`
	Name            string       `json:"name"`
	Geometry        interface{}  `json:"geometry"`
}

type FormattedLeg struct {
	DistanceMeters  float64         `json:"distance_meters"`
	DistanceKm      float64         `json:"distance_km"`
	DurationSeconds int             `json:"duration_seconds"`
	DurationMinutes float64         `json:"duration_minutes"`
	Steps           []FormattedStep `json:"steps,omitempty"`
}

type FormattedRoute struct {
	DistanceMeters  float64         `json:"distance_meters"`
	DistanceKm      float64         `json:"distance_km"`
	DurationSeconds int             `json:"duration_seconds"`
	DurationMinutes float64         `json:"duration_minutes"`
	Geometry        interface{}     `json:"geometry"`
	Legs            []FormattedLeg  `json:"legs"`
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
			"profiles":       []string{profileName},
			"backend_status": "offline",
			"error":          "OSRM bicycle backend is unreachable",
		})
		return
	}
	defer resp.Body.Close()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "ok",
		"profiles":       []string{profileName},
		"backend_status": "online",
	})
}

func handleRoute(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	vehicle := r.URL.Query().Get("vehicle")
	if vehicle == "" {
		vehicle = profileName
	}
	alternatives := r.URL.Query().Get("alternatives") == "true"
	steps := r.URL.Query().Get("steps") == "true"

	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query params required")
		return
	}

	if vehicle != profileName {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown vehicle '%s'. supported: %s", vehicle, profileName))
		return
	}

	fromCoords, err1 := parseCoords(from)
	toCoords, err2 := parseCoords(to)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, "coords must be lon,lat numbers")
		return
	}

	params := url.Values{}
	params.Set("overview", "full")
	params.Set("alternatives", strconv.FormatBool(alternatives))
	params.Set("steps", strconv.FormatBool(steps))
	params.Set("geometries", "geojson")

	path := fmt.Sprintf("/route/v1/driving/%f,%f;%f,%f?%s",
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"vehicle":   vehicle,
		"routes":    formattedRoutes,
		"waypoints": result.Waypoints,
	})
}

func handleDistance(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	vehicle := r.URL.Query().Get("vehicle")
	if vehicle == "" {
		vehicle = profileName
	}

	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query params required")
		return
	}

	if vehicle != profileName {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown vehicle '%s'. supported: %s", vehicle, profileName))
		return
	}

	fromCoords, err1 := parseCoords(from)
	toCoords, err2 := parseCoords(to)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, "coords must be lon,lat numbers")
		return
	}

	path := fmt.Sprintf("/route/v1/driving/%f,%f;%f,%f?overview=false",
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
	writeJSON(w, http.StatusOK, FormattedDistance{
		Vehicle:         vehicle,
		DistanceMeters:  route.Distance,
		DistanceKm:      roundFloat(route.Distance/1000.0, 2),
		DurationSeconds: int(math.Round(route.Duration)),
		DurationMinutes: roundFloat(route.Duration/60.0, 1),
	})
}

func handleCompare(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query params required")
		return
	}

	fromCoords, err1 := parseCoords(from)
	toCoords, err2 := parseCoords(to)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, "coords must be lon,lat numbers")
		return
	}

	path := fmt.Sprintf("/route/v1/driving/%f,%f;%f,%f?overview=false",
		fromCoords[0], fromCoords[1], toCoords[0], toCoords[1])

	results := make(map[string]interface{})
	result, err := osrmGet(path)
	if err != nil || result.Code != "Ok" || len(result.Routes) == 0 {
		results[profileName] = map[string]string{"error": "No route found"}
	} else {
		route := result.Routes[0]
		results[profileName] = map[string]interface{}{
			"distance_meters":  route.Distance,
			"distance_km":      roundFloat(route.Distance/1000.0, 2),
			"duration_seconds": int(math.Round(route.Duration)),
			"duration_minutes": roundFloat(route.Duration/60.0, 1),
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"from":    fromCoords,
		"to":      toCoords,
		"results": results,
	})
}

type MatrixRequest struct {
	Coordinates [][2]float64 `json:"coordinates"`
	Vehicle     string       `json:"vehicle"`
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
		writeError(w, http.StatusBadRequest, "coordinates array with at least 2 [lon,lat] pairs required")
		return
	}

	vehicle := req.Vehicle
	if vehicle == "" {
		vehicle = profileName
	}
	if vehicle != profileName {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown vehicle '%s'. supported: %s", vehicle, profileName))
		return
	}

	coordStrs := make([]string, len(req.Coordinates))
	indices := make([]string, len(req.Coordinates))
	for i, c := range req.Coordinates {
		coordStrs[i] = fmt.Sprintf("%f,%f", c[0], c[1])
		indices[i] = strconv.Itoa(i)
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

	waypoints := req.Coordinates
	if len(result.Waypoints) > 0 {
		waypoints = make([][2]float64, len(result.Waypoints))
		for i, wp := range result.Waypoints {
			waypoints[i] = wp.Location
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"vehicle":     vehicle,
		"distances":   result.Distances,
		"durations":   result.Durations,
		"coordinates": waypoints,
	})
}

// corsMiddleware adds basic CORS headers
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

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/route", handleRoute)
	mux.HandleFunc("/distance", handleDistance)
	mux.HandleFunc("/compare", handleCompare)
	mux.HandleFunc("/matrix", handleMatrix)

	handler := corsMiddleware(mux)

	addr := ":" + port
	log.Printf("OSRM API (Go) starting on port %s", port)
	log.Printf("Active Profile: %s", profileName)
	log.Printf("OSRM Backend: %s", osrmURL)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
