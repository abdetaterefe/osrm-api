package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ethiopia_places.tsv.gz
var ethiopiaPlacesGZ []byte

type localPlace struct {
	ID          string
	Name        string
	NameLower   string
	AltNames    []string
	AltLower    []string
	Region      string
	RegionLower string
	Category    string
	Type        string
	Lon         float64
	Lat         float64
	Pop         int
	Class       string
}

var (
	placesOnce sync.Once
	placesList []localPlace
)

func loadPlaces() []localPlace {
	placesOnce.Do(func() {
		gr, err := gzip.NewReader(bytes.NewReader(ethiopiaPlacesGZ))
		if err != nil {
			return
		}
		defer gr.Close()

		scanner := bufio.NewScanner(gr)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		list := make([]localPlace, 0, 15000)

		for scanner.Scan() {
			cols := strings.Split(scanner.Text(), "\t")
			if len(cols) < 9 {
				continue
			}
			lon, err1 := strconv.ParseFloat(cols[6], 64)
			lat, err2 := strconv.ParseFloat(cols[7], 64)
			if err1 != nil || err2 != nil {
				continue
			}
			pop, _ := strconv.Atoi(cols[8])
			var alts, altsLower []string
			if cols[2] != "" {
				for _, a := range strings.Split(cols[2], "|") {
					a = strings.TrimSpace(a)
					if a != "" {
						alts = append(alts, a)
						altsLower = append(altsLower, strings.ToLower(a))
					}
				}
			}
			cat, typ := mapGeoCategory(cols[4], cols[5])
			list = append(list, localPlace{
				ID:          "gn-" + cols[0],
				Name:        cols[1],
				NameLower:   strings.ToLower(cols[1]),
				AltNames:    alts,
				AltLower:    altsLower,
				Region:      cols[3],
				RegionLower: strings.ToLower(cols[3]),
				Category:    cat,
				Type:        typ,
				Lon:         roundFloat(lon, 5),
				Lat:         roundFloat(lat, 5),
				Pop:         pop,
				Class:       cols[4],
			})
		}
		placesList = list
	})
	return placesList
}

func mapGeoCategory(fclass, fcode string) (string, string) {
	switch fclass {
	case "P":
		switch fcode {
		case "PPLC":
			return "place", "capital"
		case "PPLA", "PPLA2":
			return "place", "city"
		case "PPLX":
			return "place", "suburb"
		default:
			return "place", "town"
		}
	case "A":
		return "boundary", "administrative"
	case "S":
		switch {
		case strings.HasPrefix(fcode, "HSP") || fcode == "CLN":
			return "amenity", "hospital"
		case strings.HasPrefix(fcode, "SCH") || fcode == "UNIV":
			return "amenity", "school"
		case fcode == "HTL":
			return "tourism", "hotel"
		case fcode == "MKT":
			return "amenity", "marketplace"
		case fcode == "AIRP" || fcode == "AIRF":
			return "aeroway", "aerodrome"
		case fcode == "CH" || fcode == "MSQE":
			return "amenity", "place_of_worship"
		default:
			return "amenity", strings.ToLower(fcode)
		}
	case "L":
		return "landuse", strings.ToLower(fcode)
	default:
		return "place", strings.ToLower(fcode)
	}
}

func (p localPlace) toPlace(streetPrefix string) Place {
	parts := make([]string, 0, 4)
	if streetPrefix != "" && !strings.EqualFold(streetPrefix, p.Name) {
		parts = append(parts, streetPrefix)
	}
	parts = append(parts, p.Name)
	if p.Region != "" && !strings.EqualFold(p.Region, p.Name) {
		parts = append(parts, p.Region)
	}
	parts = append(parts, "Ethiopia")

	name := p.Name
	if streetPrefix != "" {
		name = streetPrefix
	}
	return Place{
		ID:          p.ID,
		Name:        name,
		DisplayName: strings.Join(parts, ", "),
		Category:    p.Category,
		Type:        p.Type,
		Location:    [2]float64{p.Lon, p.Lat},
	}
}

// fastDistKm computes a fast equirectangular distance in km using a precomputed
// cosLat = cos(lat1 * pi / 180). Accurate to <0.1% within Ethiopia and avoids
// per-point trig calls in tight loops.
func fastDistKm(lon1, lat1, cosLat, lon2, lat2 float64) float64 {
	const degToKm = 111.32
	dx := (lon2 - lon1) * cosLat
	dy := lat2 - lat1
	return math.Sqrt(dx*dx+dy*dy) * degToKm
}

// fastDistSqDeg computes squared degree distance (for nearest-neighbor comparison
// without sqrt).
func fastDistSqDeg(lon1, lat1, cosLat, lon2, lat2 float64) float64 {
	dx := (lon2 - lon1) * cosLat
	dy := lat2 - lat1
	return dx*dx + dy*dy
}

// levenshtein computes bounded edit distance with zero heap allocations for
// words up to 63 bytes, returning maxDist+1 if exceeded.
func levenshtein(a, b string, maxDist int) int {
	la, lb := len(a), len(b)
	if la-lb > maxDist || lb-la > maxDist {
		return maxDist + 1
	}
	if la > 63 || lb > 63 {
		return maxDist + 1
	}
	var prev, curr [64]int
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		minRow := curr[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := prev[j] + 1
			if curr[j-1]+1 < v {
				v = curr[j-1] + 1
			}
			if prev[j-1]+cost < v {
				v = prev[j-1] + cost
			}
			curr[j] = v
			if v < minRow {
				minRow = v
			}
		}
		if minRow > maxDist {
			return maxDist + 1
		}
		prev = curr
	}
	return prev[lb]
}

type areaFilter struct {
	regionLower string
	centerLon   float64
	centerLat   float64
	radiusKm    float64
}

func resolveLocalArea(area string, places []localPlace) (*areaFilter, error) {
	q := strings.ToLower(strings.TrimSpace(area))
	if q == "" {
		return nil, nil
	}

	// Find the best matching city/town/admin area first (gives accurate center+radius).
	var best *localPlace
	bestScore := -1.0
	for i := range places {
		p := &places[i]
		if p.Class != "P" && p.Class != "A" {
			continue
		}
		s := 0.0
		if p.NameLower == q {
			s = 1000
		} else {
			for _, alt := range p.AltLower {
				if alt == q {
					s = 900
					break
				}
			}
		}
		if s == 0 && strings.HasPrefix(p.NameLower, q) {
			s = 500
		}
		if s == 0 {
			continue
		}
		s += math.Log10(float64(p.Pop+1)) * 20
		if p.Class == "P" {
			s += 30
		}
		if s > bestScore {
			bestScore = s
			best = p
		}
	}
	if best != nil {
		radius := 30.0
		if best.Class == "A" && best.Pop == 0 {
			radius = 150.0
		}
		return &areaFilter{
			regionLower: best.RegionLower,
			centerLon:   best.Lon,
			centerLat:   best.Lat,
			radiusKm:    radius,
		}, nil
	}

	// Fallback: check if it matches an admin1 region name directly.
	for _, p := range places {
		if p.RegionLower == q {
			return &areaFilter{regionLower: q}, nil
		}
	}

	return nil, fmt.Errorf("no bounds found for area %q", area)
}

func scorePlaceMatch(p *localPlace, q string, tokens []string) float64 {
	best := 0.0
	checkText := func(txt string, weight float64) {
		if txt == "" {
			return
		}
		if txt == q {
			if 1000*weight > best {
				best = 1000 * weight
			}
			return
		}
		if strings.HasPrefix(txt, q) {
			if 600*weight > best {
				best = 600 * weight
			}
			return
		}
		// Word-prefix check without allocating a slice.
		for idx := 0; idx < len(txt); {
			for idx < len(txt) && txt[idx] == ' ' {
				idx++
			}
			if idx < len(txt) && strings.HasPrefix(txt[idx:], q) {
				if 420*weight > best {
					best = 420 * weight
				}
				return
			}
			for idx < len(txt) && txt[idx] != ' ' {
				idx++
			}
		}
		if strings.Contains(txt, q) {
			if 260*weight > best {
				best = 260 * weight
			}
			return
		}
		if len(tokens) > 1 {
			matched := 0
			for _, tok := range tokens {
				if strings.Contains(txt, tok) {
					matched++
				}
			}
			if matched == len(tokens) {
				if 220*weight > best {
					best = 220 * weight
				}
				return
			}
		}
		if len(q) >= 4 {
			maxDist := 1
			if len(q) >= 7 {
				maxDist = 2
			}
			for idx := 0; idx < len(txt); {
				for idx < len(txt) && txt[idx] == ' ' {
					idx++
				}
				end := idx
				for end < len(txt) && txt[end] != ' ' {
					end++
				}
				if end > idx {
					if d := levenshtein(q, txt[idx:end], maxDist); d <= maxDist {
						score := (160 - float64(d*40)) * weight
						if score > best {
							best = score
						}
					}
				}
				idx = end
			}
		}
	}

	checkText(p.NameLower, 1.0)
	for _, alt := range p.AltLower {
		checkText(alt, 0.9)
	}
	return best
}

func searchLocalPlaces(query, area string, near *[2]float64, limit int) ([]Place, error) {
	places := loadPlaces()
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, fmt.Errorf("empty query")
	}
	tokens := strings.Fields(q)

	af, err := resolveLocalArea(area, places)
	if err != nil {
		return nil, err
	}

	type scored struct {
		place localPlace
		score float64
	}
	matches := make([]scored, 0, limit*2)

	var areaCosLat float64
	if af != nil && af.radiusKm > 0 {
		areaCosLat = math.Cos(af.centerLat * math.Pi / 180.0)
	}
	var nearCosLat float64
	if near != nil {
		nearCosLat = math.Cos(near[1] * math.Pi / 180.0)
	}

	for i := range places {
		p := &places[i]
		if af != nil {
			inRegion := af.regionLower != "" && p.RegionLower == af.regionLower
			inRadius := af.radiusKm > 0 && fastDistKm(af.centerLon, af.centerLat, areaCosLat, p.Lon, p.Lat) <= af.radiusKm
			if !inRegion && !inRadius {
				continue
			}
		}

		s := scorePlaceMatch(p, q, tokens)
		if s <= 0 {
			continue
		}

		s += math.Log10(float64(p.Pop+1)) * 12
		switch p.Class {
		case "P":
			s += 20
		case "S":
			s += 15
		case "A":
			s += 10
		}
		if near != nil {
			d := fastDistKm(near[0], near[1], nearCosLat, p.Lon, p.Lat)
			if d < 50 {
				s += (50 - d) * 2.5
			}
		}
		matches = append(matches, scored{place: *p, score: s})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].place.Pop > matches[j].place.Pop
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}

	results := make([]Place, len(matches))
	for i, m := range matches {
		pl := m.place.toPlace("")
		// If the primary name doesn't contain the query, check if an alternate name does
		// (e.g., searching "Adama" matches "Nazrēt" whose altname is "Adama").
		if !strings.Contains(m.place.NameLower, q) {
			for j, altLow := range m.place.AltLower {
				if strings.Contains(altLow, q) {
					alt := m.place.AltNames[j]
					pl.Name = alt
					pl.DisplayName = strings.Replace(pl.DisplayName, m.place.Name, alt+" ("+m.place.Name+")", 1)
					break
				}
			}
		}
		results[i] = pl
	}
	return results, nil
}

// valhallaLocateStreet queries the local Valhalla /locate endpoint for the
// nearest road name. Returns "" if Valhalla is unavailable or the edge is unnamed.
func valhallaLocateStreet(lon, lat float64) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"locations": []map[string]float64{{"lon": lon, "lat": lat}},
		"verbose":   true,
	})
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Post(
		strings.TrimRight(valhallaURL, "/")+"/locate",
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return ""
	}
	var located []struct {
		Edges []struct {
			Names []string `json:"names"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(body, &located); err != nil || len(located) == 0 {
		return ""
	}
	for _, edge := range located[0].Edges {
		for _, n := range edge.Names {
			n = strings.TrimSpace(n)
			if n != "" {
				return n
			}
		}
	}
	return ""
}

func reverseLocalPlace(lon, lat float64) (*Place, error) {
	places := loadPlaces()
	if len(places) == 0 {
		return nil, fmt.Errorf("no places loaded")
	}

	bestIdx := -1
	bestDistSq := math.MaxFloat64
	cosLat := math.Cos(lat * math.Pi / 180.0)
	for i := range places {
		d := fastDistSqDeg(lon, lat, cosLat, places[i].Lon, places[i].Lat)
		if d < bestDistSq {
			bestDistSq = d
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return nil, fmt.Errorf("no place found")
	}

	street := valhallaLocateStreet(lon, lat)
	place := places[bestIdx].toPlace(street)
	return &place, nil
}
