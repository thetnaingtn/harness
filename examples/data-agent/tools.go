// data.gov.sg tools — five concrete tool.Tool implementations covering
// the real-time environment APIs (api-open.data.gov.sg/v2) and the
// CKAN-style dataset catalog (data.gov.sg/api/action).
//
// All endpoints are public — no API key needed. Each tool returns the
// trimmed JSON the agent actually needs, not the raw upstream payload,
// so the model spends fewer tokens parsing wrapper envelopes.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sausheong/harness/tool"
)

const (
	realtimeBase = "https://api-open.data.gov.sg/v2/real-time/api"
	ckanBase     = "https://data.gov.sg/api/action"
	datasetsBase = "https://api-production.data.gov.sg/v2/public/api"
)

// httpGet issues a plain GET and returns the body. Centralised so each
// tool's Execute is just "build the URL + interpret the body".
func httpGet(ctx context.Context, raw string, q url.Values) ([]byte, error) {
	u := raw
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("user-agent", "harness-data-agent/0.1")

	client := &http.Client{Timeout: 20 * time.Second}
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
		return nil, fmt.Errorf("data.gov.sg %s returned %d: %s", raw, resp.StatusCode, preview)
	}
	return body, nil
}

func errResult(err error) tool.ToolResult { return tool.ToolResult{Error: err.Error()} }

func jsonOut(v any) tool.ToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(err)
	}
	return tool.ToolResult{Output: string(b)}
}

// =====================================================================
// Weather forecast (2-hour nowcast across 47 named areas)
// =====================================================================

type WeatherForecastTool struct{}

func (*WeatherForecastTool) Name() string { return "datagov_weather" }
func (*WeatherForecastTool) Description() string {
	return "Get Singapore's 2-hour weather forecast from data.gov.sg (NEA). " +
		"Returns the forecast string (e.g. 'Partly Cloudy', 'Light Rain', 'Thundery Showers') " +
		"per named area. Optionally pass `area` (e.g. 'Bedok', 'Ang Mo Kio') to filter " +
		"to a single match — case-insensitive substring. Omit `area` to get the full " +
		"island view (47 areas)."
}
func (*WeatherForecastTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"area": {
				"type": "string",
				"description": "Substring of an area name (e.g. 'Bedok', 'Bukit'). Omit for all areas."
			}
		}
	}`)
}
func (*WeatherForecastTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *WeatherForecastTool) Execute(ctx context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		Area string `json:"area"`
	}
	_ = json.Unmarshal(input, &in)

	body, err := httpGet(ctx, realtimeBase+"/two-hr-forecast", nil)
	if err != nil {
		return errResult(err), nil
	}

	// NEA weather endpoint uses snake_case at the item level
	// (`update_timestamp`, `valid_period`) but the wrapping envelope
	// (`code`, `errorMsg`) is camelCase. Field tags reflect that.
	var raw struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				UpdateTimestamp string `json:"update_timestamp"`
				Timestamp       string `json:"timestamp"`
				ValidPeriod     struct {
					Start string `json:"start"`
					End   string `json:"end"`
					Text  string `json:"text"`
				} `json:"valid_period"`
				Forecasts []struct {
					Area     string `json:"area"`
					Forecast string `json:"forecast"`
				} `json:"forecasts"`
			} `json:"items"`
		} `json:"data"`
		ErrorMsg string `json:"errorMsg"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return errResult(fmt.Errorf("parse: %w", err)), nil
	}
	if raw.ErrorMsg != "" {
		return errResult(fmt.Errorf("upstream: %s", raw.ErrorMsg)), nil
	}
	if len(raw.Data.Items) == 0 {
		return errResult(fmt.Errorf("no forecast items returned")), nil
	}

	item := raw.Data.Items[0]
	needle := strings.ToLower(strings.TrimSpace(in.Area))
	type pair struct {
		Area     string `json:"area"`
		Forecast string `json:"forecast"`
	}
	matches := make([]pair, 0, len(item.Forecasts))
	for _, f := range item.Forecasts {
		if needle == "" || strings.Contains(strings.ToLower(f.Area), needle) {
			matches = append(matches, pair{f.Area, f.Forecast})
		}
	}
	return jsonOut(map[string]any{
		"valid_from":    item.ValidPeriod.Start,
		"valid_until":   item.ValidPeriod.End,
		"valid_text":    item.ValidPeriod.Text,
		"updated_at":    item.UpdateTimestamp,
		"area_filter":   in.Area,
		"matched_count": len(matches),
		"forecasts":     matches,
	}), nil
}

// =====================================================================
// Air quality (PSI + PM2.5) by region
// =====================================================================

type AirQualityTool struct{}

func (*AirQualityTool) Name() string { return "datagov_air_quality" }
func (*AirQualityTool) Description() string {
	return "Get current air-quality readings (PSI 24-hour and PM2.5 1-hour) for Singapore. " +
		"Regions: north, south, east, west, central, national. Pass `region` to filter; " +
		"omit it to get all six. PSI bands: 0-50 Good, 51-100 Moderate, 101-200 Unhealthy, " +
		"201-300 Very Unhealthy, 301+ Hazardous."
}
func (*AirQualityTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"region": {
				"type": "string",
				"enum": ["north", "south", "east", "west", "central", "national"],
				"description": "One of the six regions. Omit to get all."
			}
		}
	}`)
}
func (*AirQualityTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *AirQualityTool) Execute(ctx context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		Region string `json:"region"`
	}
	_ = json.Unmarshal(input, &in)
	region := strings.ToLower(strings.TrimSpace(in.Region))

	psi, err := fetchReadings(ctx, realtimeBase+"/psi", "psi_twenty_four_hourly")
	if err != nil {
		return errResult(fmt.Errorf("psi: %w", err)), nil
	}
	pm, err := fetchReadings(ctx, realtimeBase+"/pm25", "pm25_one_hourly")
	if err != nil {
		return errResult(fmt.Errorf("pm25: %w", err)), nil
	}

	out := map[string]any{
		"updated_at":          psi.timestamp,
		"region_filter":       in.Region,
		"psi_24hr":            filterRegion(psi.values, region),
		"pm25_1hr_micrograms": filterRegion(pm.values, region),
		"psi_band_legend":     "0-50 Good · 51-100 Moderate · 101-200 Unhealthy · 201-300 Very Unhealthy · 301+ Hazardous",
	}
	return jsonOut(out), nil
}

type readings struct {
	timestamp string
	values    map[string]float64 // region → value
}

// fetchReadings parses the PSI / PM2.5 v2 envelope into a flat
// region→value map for the specified reading key.
func fetchReadings(ctx context.Context, endpoint, readingKey string) (readings, error) {
	body, err := httpGet(ctx, endpoint, nil)
	if err != nil {
		return readings{}, err
	}
	var raw struct {
		Data struct {
			Items []struct {
				Date      string                        `json:"date"`
				Timestamp string                        `json:"timestamp"`
				Updated   string                        `json:"updatedTimestamp"`
				Readings  map[string]map[string]float64 `json:"readings"`
			} `json:"items"`
		} `json:"data"`
		ErrorMsg string `json:"errorMsg"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return readings{}, fmt.Errorf("parse: %w", err)
	}
	if raw.ErrorMsg != "" {
		return readings{}, fmt.Errorf("upstream: %s", raw.ErrorMsg)
	}
	if len(raw.Data.Items) == 0 {
		return readings{}, fmt.Errorf("no items")
	}
	it := raw.Data.Items[0]
	stamp := it.Updated
	if stamp == "" {
		stamp = it.Timestamp
	}
	r, ok := it.Readings[readingKey]
	if !ok {
		return readings{}, fmt.Errorf("reading %q not present", readingKey)
	}
	return readings{timestamp: stamp, values: r}, nil
}

func filterRegion(m map[string]float64, region string) map[string]float64 {
	if region == "" {
		return m
	}
	if v, ok := m[region]; ok {
		return map[string]float64{region: v}
	}
	return map[string]float64{}
}

// =====================================================================
// Environment readings (temperature, humidity, rainfall, UV, wind)
// =====================================================================

type EnvironmentReadingTool struct{}

var envEndpoints = map[string]string{
	"air-temperature":   "/air-temperature",
	"relative-humidity": "/relative-humidity",
	"rainfall":          "/rainfall",
	"uv":                "/uv",
	"wind-speed":        "/wind-speed",
	"wind-direction":    "/wind-direction",
}

func (*EnvironmentReadingTool) Name() string { return "datagov_environment" }
func (*EnvironmentReadingTool) Description() string {
	return "Get a real-time environment reading from NEA stations across Singapore. " +
		"Choose `kind`: air-temperature (°C), relative-humidity (%), rainfall (mm in last 5 min), " +
		"uv (UV index), wind-speed (knots), wind-direction (degrees from N). " +
		"Returns per-station readings with station id and lat/long. " +
		"For an island-wide summary, the agent should average the station values."
}
func (*EnvironmentReadingTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"kind": {
				"type": "string",
				"enum": ["air-temperature", "relative-humidity", "rainfall", "uv", "wind-speed", "wind-direction"],
				"description": "Which reading to fetch."
			}
		},
		"required": ["kind"]
	}`)
}
func (*EnvironmentReadingTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *EnvironmentReadingTool) Execute(ctx context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult(fmt.Errorf("invalid input: %w", err)), nil
	}
	path, ok := envEndpoints[in.Kind]
	if !ok {
		return errResult(fmt.Errorf("unknown kind %q", in.Kind)), nil
	}
	body, err := httpGet(ctx, realtimeBase+path, nil)
	if err != nil {
		return errResult(err), nil
	}

	// UV uses a separate envelope (data.records[].index[]); the other
	// readings share data.stations[] + data.readings[].data[stationId,value].
	if in.Kind == "uv" {
		return parseUV(body)
	}
	return parseStationReading(body, in.Kind)
}

// parseStationReading handles the air-temperature / humidity / rainfall /
// wind-speed / wind-direction shape: stations in one array, readings in
// another, joined here on stationId for the agent's convenience.
func parseStationReading(body []byte, kind string) (tool.ToolResult, error) {
	var raw struct {
		Data struct {
			Stations []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Location struct {
					Latitude  float64 `json:"latitude"`
					Longitude float64 `json:"longitude"`
				} `json:"location"`
			} `json:"stations"`
			Readings []struct {
				Timestamp string `json:"timestamp"`
				Data      []struct {
					StationID string  `json:"stationId"`
					Value     float64 `json:"value"`
				} `json:"data"`
			} `json:"readings"`
			ReadingType string `json:"readingType"`
			ReadingUnit string `json:"readingUnit"`
		} `json:"data"`
		ErrorMsg string `json:"errorMsg"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return errResult(fmt.Errorf("parse: %w", err)), nil
	}
	if raw.ErrorMsg != "" {
		return errResult(fmt.Errorf("upstream: %s", raw.ErrorMsg)), nil
	}
	if len(raw.Data.Readings) == 0 {
		return errResult(fmt.Errorf("no readings returned")), nil
	}

	type stationMeta struct {
		Name string
		Lat  float64
		Lng  float64
	}
	stations := make(map[string]stationMeta, len(raw.Data.Stations))
	for _, s := range raw.Data.Stations {
		stations[s.ID] = stationMeta{s.Name, s.Location.Latitude, s.Location.Longitude}
	}

	type row struct {
		StationID string  `json:"station_id"`
		Name      string  `json:"name"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Value     float64 `json:"value"`
	}
	r0 := raw.Data.Readings[0]
	out := make([]row, 0, len(r0.Data))
	for _, d := range r0.Data {
		s := stations[d.StationID]
		out = append(out, row{d.StationID, s.Name, s.Lat, s.Lng, d.Value})
	}

	return jsonOut(map[string]any{
		"kind":         kind,
		"reading_type": raw.Data.ReadingType,
		"unit":         raw.Data.ReadingUnit,
		"timestamp":    r0.Timestamp,
		"count":        len(out),
		"readings":     out,
	}), nil
}

// parseUV handles the UV endpoint's records[].index[] shape — hourly
// readings rather than per-station.
func parseUV(body []byte) (tool.ToolResult, error) {
	var raw struct {
		Data struct {
			Records []struct {
				Date             string `json:"date"`
				Timestamp        string `json:"timestamp"`
				UpdatedTimestamp string `json:"updatedTimestamp"`
				Index            []struct {
					Hour  string  `json:"hour"`
					Value float64 `json:"value"`
				} `json:"index"`
			} `json:"records"`
		} `json:"data"`
		ErrorMsg string `json:"errorMsg"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return errResult(fmt.Errorf("parse: %w", err)), nil
	}
	if raw.ErrorMsg != "" {
		return errResult(fmt.Errorf("upstream: %s", raw.ErrorMsg)), nil
	}
	if len(raw.Data.Records) == 0 {
		return errResult(fmt.Errorf("no uv records returned")), nil
	}
	r := raw.Data.Records[0]
	return jsonOut(map[string]any{
		"kind":       "uv",
		"date":       r.Date,
		"timestamp":  r.Timestamp,
		"updated_at": r.UpdatedTimestamp,
		"hourly":     r.Index,
	}), nil
}

// =====================================================================
// Dataset catalog search (data.gov.sg v2 public datasets API)
// =====================================================================

type DatasetSearchTool struct{}

func (*DatasetSearchTool) Name() string { return "datagov_search_datasets" }
func (*DatasetSearchTool) Description() string {
	return "Search the data.gov.sg dataset catalog by free-text query. " +
		"Returns matching datasets with their `dataset_id` (pass to datagov_query_dataset " +
		"to pull rows), name, description (truncated), agency, and update history. " +
		"Use `page` (default 1) for pagination — each page returns ~10 datasets."
}
func (*DatasetSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Free-text query (e.g. 'HDB resale', 'COE bidding', 'population')."
			},
			"page": {
				"type": "integer",
				"description": "Page number, starting at 1 (default 1)."
			}
		},
		"required": ["query"]
	}`)
}
func (*DatasetSearchTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *DatasetSearchTool) Execute(ctx context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		Query string `json:"query"`
		Page  int    `json:"page"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult(fmt.Errorf("invalid input: %w", err)), nil
	}
	if strings.TrimSpace(in.Query) == "" {
		return errResult(fmt.Errorf("query is required")), nil
	}
	if in.Page <= 0 {
		in.Page = 1
	}

	body, err := httpGet(ctx, datasetsBase+"/datasets", url.Values{
		"q":    {in.Query},
		"page": {fmt.Sprintf("%d", in.Page)},
	})
	if err != nil {
		return errResult(err), nil
	}

	var raw struct {
		Code int `json:"code"`
		Data struct {
			Datasets []struct {
				DatasetID            string `json:"datasetId"`
				Name                 string `json:"name"`
				Description          string `json:"description"`
				Status               string `json:"status"`
				Format               string `json:"format"`
				ManagedByAgencyName  string `json:"managedByAgencyName"`
				CreatedAt            string `json:"createdAt"`
				LastUpdatedAt        string `json:"lastUpdatedAt"`
				CoverageStart        string `json:"coverageStart"`
				CoverageEnd          string `json:"coverageEnd"`
			} `json:"datasets"`
			Pages         int `json:"pages"`
			RowCount      int `json:"rowCount"`
			TotalRowCount int `json:"totalRowCount"`
		} `json:"data"`
		ErrorMsg string `json:"errorMsg"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return errResult(fmt.Errorf("parse: %w", err)), nil
	}
	if raw.ErrorMsg != "" {
		return errResult(fmt.Errorf("upstream: %s", raw.ErrorMsg)), nil
	}

	type ds struct {
		DatasetID     string `json:"dataset_id"`
		Name          string `json:"name"`
		Description   string `json:"description,omitempty"`
		Agency        string `json:"agency,omitempty"`
		Format        string `json:"format,omitempty"`
		LastUpdated   string `json:"last_updated,omitempty"`
		CoverageStart string `json:"coverage_start,omitempty"`
		CoverageEnd   string `json:"coverage_end,omitempty"`
	}
	results := make([]ds, 0, len(raw.Data.Datasets))
	for _, d := range raw.Data.Datasets {
		desc := d.Description
		if len(desc) > 400 {
			desc = desc[:400] + "..."
		}
		results = append(results, ds{
			DatasetID:     d.DatasetID,
			Name:          d.Name,
			Description:   desc,
			Agency:        d.ManagedByAgencyName,
			Format:        d.Format,
			LastUpdated:   d.LastUpdatedAt,
			CoverageStart: d.CoverageStart,
			CoverageEnd:   d.CoverageEnd,
		})
	}

	return jsonOut(map[string]any{
		"query":           in.Query,
		"page":            in.Page,
		"total_pages":     raw.Data.Pages,
		"total_matches":   raw.Data.TotalRowCount,
		"returned":        len(results),
		"datasets":        results,
	}), nil
}

// =====================================================================
// Dataset query (CKAN datastore_search)
// =====================================================================

type DatasetQueryTool struct{}

func (*DatasetQueryTool) Name() string { return "datagov_query_dataset" }
func (*DatasetQueryTool) Description() string {
	return "Pull rows from a specific data.gov.sg dataset (CKAN datastore_search). " +
		"Requires a `dataset_id` from datagov_search_datasets (the prefixed id like " +
		"'d_8b84c4ee58e3cfc0ece0d773c8ca6abc'). Optional `q` does free-text matching " +
		"across all string fields. `limit` defaults to 20 (max 100). " +
		"Returns the matching records plus the field schema so you can interpret columns."
}
func (*DatasetQueryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"dataset_id": {
				"type": "string",
				"description": "Dataset id from datagov_search_datasets (e.g. 'd_8b84c4ee58e3cfc0ece0d773c8ca6abc')."
			},
			"q": {
				"type": "string",
				"description": "Optional free-text filter across all string fields."
			},
			"limit": {
				"type": "integer",
				"description": "Max records to return (1-100, default 20)."
			}
		},
		"required": ["dataset_id"]
	}`)
}
func (*DatasetQueryTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *DatasetQueryTool) Execute(ctx context.Context, input json.RawMessage) (tool.ToolResult, error) {
	// Accept either `dataset_id` (preferred — matches what search returns)
	// or `resource_id` (legacy alias) so the agent can recover if it slips.
	var in struct {
		DatasetID  string `json:"dataset_id"`
		ResourceID string `json:"resource_id"`
		Q          string `json:"q"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult(fmt.Errorf("invalid input: %w", err)), nil
	}
	id := strings.TrimSpace(in.DatasetID)
	if id == "" {
		id = strings.TrimSpace(in.ResourceID)
	}
	if id == "" {
		return errResult(fmt.Errorf("dataset_id is required")), nil
	}
	if in.Limit <= 0 || in.Limit > 100 {
		in.Limit = 20
	}

	q := url.Values{
		"resource_id": {id},
		"limit":       {fmt.Sprintf("%d", in.Limit)},
	}
	if in.Q != "" {
		q.Set("q", in.Q)
	}
	body, err := httpGet(ctx, ckanBase+"/datastore_search", q)
	if err != nil {
		return errResult(err), nil
	}

	var raw struct {
		Success bool `json:"success"`
		Result  struct {
			ResourceID string           `json:"resource_id"`
			Total      int              `json:"total"`
			Fields     []map[string]any `json:"fields"`
			Records    []map[string]any `json:"records"`
		} `json:"result"`
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return errResult(fmt.Errorf("parse: %w", err)), nil
	}
	if !raw.Success {
		return errResult(fmt.Errorf("ckan error: %v", raw.Error)), nil
	}

	return jsonOut(map[string]any{
		"dataset_id":    raw.Result.ResourceID,
		"total_records": raw.Result.Total,
		"returned":      len(raw.Result.Records),
		"fields":        raw.Result.Fields,
		"records":       raw.Result.Records,
	}), nil
}
