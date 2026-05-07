// LTA Datamall tools — five concrete tool.Tool implementations exposing
// the live transport endpoints at http://datamall2.mytransport.sg/ltaodataservice.
//
// Each tool is independent (no shared state beyond the per-instance HTTP
// client + AccountKey). The bus-stops tool caches the master list once
// to avoid re-fetching ~6,000 stops on every search.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sausheong/harness/tool"
)

const ltaBase = "http://datamall2.mytransport.sg/ltaodataservice"

// httpDo sends a GET to the LTA endpoint with the AccountKey header and
// returns the raw response body. Centralised so each tool's Execute is
// just "build the URL + interpret the body".
func httpDo(ctx context.Context, accountKey, path string, q url.Values) ([]byte, error) {
	u := ltaBase + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("AccountKey", accountKey)
	req.Header.Set("accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		return nil, fmt.Errorf("LTA API %s returned %d: %s", path, resp.StatusCode, preview)
	}
	return body, nil
}

// errResult wraps an error as a non-fatal tool result so the agent sees
// the message and can decide whether to retry / pivot.
func errResult(err error) tool.ToolResult {
	return tool.ToolResult{Error: err.Error()}
}

// =====================================================================
// Bus arrival
// =====================================================================

type BusArrivalTool struct{ AccountKey string }

func (*BusArrivalTool) Name() string { return "lta_bus_arrival" }
func (*BusArrivalTool) Description() string {
	return "Get real-time bus arrival info from LTA Datamall for a Singapore bus stop. " +
		"Returns the next 1–3 arriving buses per service with estimated arrival time, " +
		"crowding level (Load: SEA=Seats Available, SDA=Standing, LSD=Limited), " +
		"and accessibility (Feature: WAB=wheelchair-accessible). " +
		"Use lta_bus_stops first to look up the 5-digit BusStopCode for a location."
}
func (*BusArrivalTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"bus_stop_code": {
				"type": "string",
				"description": "5-digit bus stop code (e.g. '83139'). Find it via lta_bus_stops."
			},
			"service_no": {
				"type": "string",
				"description": "Optional: filter to a specific bus service number (e.g. '15', 'NR1')."
			}
		},
		"required": ["bus_stop_code"]
	}`)
}
func (*BusArrivalTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *BusArrivalTool) Execute(ctx context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		BusStopCode string `json:"bus_stop_code"`
		ServiceNo   string `json:"service_no"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult(fmt.Errorf("invalid input: %w", err)), nil
	}
	if in.BusStopCode == "" {
		return errResult(fmt.Errorf("bus_stop_code is required")), nil
	}

	q := url.Values{"BusStopCode": {in.BusStopCode}}
	if in.ServiceNo != "" {
		q.Set("ServiceNo", in.ServiceNo)
	}
	body, err := httpDo(ctx, t.AccountKey, "/v3/BusArrival", q)
	if err != nil {
		return errResult(err), nil
	}
	return tool.ToolResult{Output: string(body)}, nil
}

// =====================================================================
// Traffic incidents
// =====================================================================

type TrafficIncidentsTool struct{ AccountKey string }

func (*TrafficIncidentsTool) Name() string { return "lta_traffic_incidents" }
func (*TrafficIncidentsTool) Description() string {
	return "Get current traffic incidents reported by LTA (accidents, road blocks, " +
		"vehicle breakdowns, weather hazards). Returns location (lat/long), type, " +
		"and free-text message. Updated every 2 minutes by LTA. No parameters."
}
func (*TrafficIncidentsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}
func (*TrafficIncidentsTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *TrafficIncidentsTool) Execute(ctx context.Context, _ json.RawMessage) (tool.ToolResult, error) {
	body, err := httpDo(ctx, t.AccountKey, "/TrafficIncidents", nil)
	if err != nil {
		return errResult(err), nil
	}
	return tool.ToolResult{Output: string(body)}, nil
}

// =====================================================================
// Carpark availability
// =====================================================================

type CarparkAvailabilityTool struct {
	AccountKey string

	mu         sync.Mutex
	cached     []byte
	cachedAt   time.Time
	cacheTTL   time.Duration // refresh window (0 = always fresh)
}

func (*CarparkAvailabilityTool) Name() string { return "lta_carpark_availability" }
func (*CarparkAvailabilityTool) Description() string {
	return "Look up real-time public carpark availability across Singapore (HDB, LTA, URA). " +
		"Returns lots-available counts for each carpark. Optionally filter by name " +
		"(e.g. 'Bugis', 'Marina') — returns matching carparks only. With no filter, " +
		"returns the first 50 carparks for context."
}
func (*CarparkAvailabilityTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name_contains": {
				"type": "string",
				"description": "Case-insensitive substring match on carpark Development name. Omit to get the first 50 entries."
			}
		}
	}`)
}
func (*CarparkAvailabilityTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *CarparkAvailabilityTool) Execute(ctx context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		NameContains string `json:"name_contains"`
	}
	_ = json.Unmarshal(input, &in)

	body, err := t.fetch(ctx)
	if err != nil {
		return errResult(err), nil
	}

	var raw struct {
		Value []map[string]any `json:"value"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return errResult(fmt.Errorf("parse: %w", err)), nil
	}

	needle := strings.ToLower(strings.TrimSpace(in.NameContains))
	var matches []map[string]any
	for _, cp := range raw.Value {
		if needle == "" {
			matches = append(matches, cp)
			if len(matches) >= 50 {
				break
			}
			continue
		}
		dev, _ := cp["Development"].(string)
		if strings.Contains(strings.ToLower(dev), needle) {
			matches = append(matches, cp)
		}
	}

	out, _ := json.MarshalIndent(map[string]any{
		"matched_count": len(matches),
		"total_records": len(raw.Value),
		"filter":        in.NameContains,
		"results":       matches,
	}, "", "  ")
	return tool.ToolResult{Output: string(out)}, nil
}

// fetch caches the carpark list for 30s — the LTA endpoint refreshes
// every minute upstream, so caching saves bandwidth on burst queries
// (e.g. agent issues two filter queries in one turn).
func (t *CarparkAvailabilityTool) fetch(ctx context.Context) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ttl := t.cacheTTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	if t.cached != nil && time.Since(t.cachedAt) < ttl {
		return t.cached, nil
	}
	body, err := httpDo(ctx, t.AccountKey, "/CarParkAvailabilityv2", nil)
	if err != nil {
		return nil, err
	}
	t.cached = body
	t.cachedAt = time.Now()
	return body, nil
}

// =====================================================================
// Train service alerts
// =====================================================================

type TrainAlertsTool struct{ AccountKey string }

func (*TrainAlertsTool) Name() string { return "lta_train_alerts" }
func (*TrainAlertsTool) Description() string {
	return "Get current MRT/LRT service disruptions or shuttle service alerts. " +
		"Returns Status (1=Normal, 2=Disruption), affected train lines, and " +
		"free-text message. Empty when service is normal across all lines."
}
func (*TrainAlertsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}
func (*TrainAlertsTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *TrainAlertsTool) Execute(ctx context.Context, _ json.RawMessage) (tool.ToolResult, error) {
	body, err := httpDo(ctx, t.AccountKey, "/TrainServiceAlerts", nil)
	if err != nil {
		return errResult(err), nil
	}
	return tool.ToolResult{Output: string(body)}, nil
}

// =====================================================================
// Bus stops search (with on-disk persistent cache)
// =====================================================================

type BusStopsSearchTool struct {
	AccountKey string

	mu     sync.Mutex
	stops  []map[string]any
	loaded bool
}

func (*BusStopsSearchTool) Name() string { return "lta_bus_stops" }
func (*BusStopsSearchTool) Description() string {
	return "Search Singapore bus stops by description, road name, or stop code. " +
		"Returns BusStopCode, Description, RoadName, and lat/long. " +
		"Use the 5-digit BusStopCode with lta_bus_arrival to get arrival times. " +
		"On first call, downloads the full LTA bus stop registry (~6,000 stops, " +
		"may take 15-30s); subsequent calls hit the in-memory cache."
}
func (*BusStopsSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Substring to match against Description, RoadName, or BusStopCode (case-insensitive). Returns up to 20 matches."
			}
		},
		"required": ["query"]
	}`)
}
func (*BusStopsSearchTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *BusStopsSearchTool) Execute(ctx context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult(fmt.Errorf("invalid input: %w", err)), nil
	}
	if strings.TrimSpace(in.Query) == "" {
		return errResult(fmt.Errorf("query is required")), nil
	}

	if err := t.ensureLoaded(ctx); err != nil {
		return errResult(err), nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	needle := strings.ToLower(in.Query)
	var matches []map[string]any
	for _, s := range t.stops {
		desc, _ := s["Description"].(string)
		road, _ := s["RoadName"].(string)
		code, _ := s["BusStopCode"].(string)
		if strings.Contains(strings.ToLower(desc), needle) ||
			strings.Contains(strings.ToLower(road), needle) ||
			strings.Contains(strings.ToLower(code), needle) {
			matches = append(matches, s)
			if len(matches) >= 20 {
				break
			}
		}
	}

	out, _ := json.MarshalIndent(map[string]any{
		"matched_count":   len(matches),
		"registry_size":   len(t.stops),
		"query":           in.Query,
		"results":         matches,
	}, "", "  ")
	return tool.ToolResult{Output: string(out)}, nil
}

// ensureLoaded fetches all bus stops once and caches them in memory.
// LTA's BusStops endpoint paginates 500/page; the registry is ~6,000
// stops so we issue ~12 requests. Done lazily so the tool registers
// fast and only pays the cost on first use.
func (t *BusStopsSearchTool) ensureLoaded(ctx context.Context) error {
	t.mu.Lock()
	if t.loaded {
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()

	var all []map[string]any
	for skip := 0; ; skip += 500 {
		body, err := httpDo(ctx, t.AccountKey, "/BusStops", url.Values{
			"$skip": {fmt.Sprintf("%d", skip)},
		})
		if err != nil {
			return err
		}
		var page struct {
			Value []map[string]any `json:"value"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("parse page %d: %w", skip, err)
		}
		if len(page.Value) == 0 {
			break
		}
		all = append(all, page.Value...)
		if len(page.Value) < 500 {
			break
		}
	}

	t.mu.Lock()
	t.stops = all
	t.loaded = true
	t.mu.Unlock()
	return nil
}
