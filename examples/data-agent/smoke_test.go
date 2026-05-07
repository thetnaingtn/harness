// Live integration smoke test against data.gov.sg endpoints.
// Skipped by default; run with: go test -tags=live ./examples/data-agent/
//go:build live

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWeather_Live(t *testing.T) {
	ctx := context.Background()
	out, err := (&WeatherForecastTool{}).Execute(ctx, json.RawMessage(`{"area":"Bedok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "" {
		t.Fatalf("tool error: %s", out.Error)
	}
	if !strings.Contains(out.Output, "Bedok") {
		t.Fatalf("expected Bedok in output, got: %s", out.Output)
	}
}

func TestAirQuality_Live(t *testing.T) {
	ctx := context.Background()
	out, err := (&AirQualityTool{}).Execute(ctx, json.RawMessage(`{"region":"west"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "" {
		t.Fatalf("tool error: %s", out.Error)
	}
	if !strings.Contains(out.Output, "west") || !strings.Contains(out.Output, "psi_24hr") {
		t.Fatalf("missing expected keys in output: %s", out.Output)
	}
}

func TestEnvironment_StationReading_Live(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"air-temperature", "relative-humidity", "rainfall", "wind-speed", "wind-direction"} {
		t.Run(kind, func(t *testing.T) {
			in, _ := json.Marshal(map[string]string{"kind": kind})
			out, err := (&EnvironmentReadingTool{}).Execute(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if out.Error != "" {
				t.Fatalf("tool error: %s", out.Error)
			}
			if !strings.Contains(out.Output, "station_id") {
				t.Fatalf("expected station_id in output for %s, got: %s", kind, out.Output)
			}
		})
	}
}

func TestEnvironment_UV_Live(t *testing.T) {
	ctx := context.Background()
	out, err := (&EnvironmentReadingTool{}).Execute(context.Background(), json.RawMessage(`{"kind":"uv"}`))
	_ = ctx
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "" {
		t.Fatalf("tool error: %s", out.Error)
	}
	if !strings.Contains(out.Output, "hourly") {
		t.Fatalf("expected 'hourly' in UV output, got: %s", out.Output)
	}
}

func TestDatasetSearch_Live(t *testing.T) {
	out, err := (&DatasetSearchTool{}).Execute(context.Background(), json.RawMessage(`{"query":"hdb resale"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "" {
		t.Fatalf("tool error: %s", out.Error)
	}
	if !strings.Contains(out.Output, "dataset_id") {
		t.Fatalf("expected dataset_id in search output: %s", out.Output)
	}
}

func TestDatasetQuery_Live(t *testing.T) {
	// Stable HDB resale dataset id used by data.gov.sg docs/examples.
	out, err := (&DatasetQueryTool{}).Execute(context.Background(),
		json.RawMessage(`{"dataset_id":"d_8b84c4ee58e3cfc0ece0d773c8ca6abc","limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "" {
		t.Fatalf("tool error: %s", out.Error)
	}
	if !strings.Contains(out.Output, "records") || !strings.Contains(out.Output, "fields") {
		t.Fatalf("expected records+fields in output: %s", out.Output)
	}
}
