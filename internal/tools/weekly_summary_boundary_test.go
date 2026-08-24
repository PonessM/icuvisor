package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ricardocabral/icuvisor/internal/config"
	"github.com/ricardocabral/icuvisor/internal/intervals"
)

func TestSummaryToolDescriptionsDiscloseWeeklyAnchorContract(t *testing.T) {
	tools := []Tool{
		newGetFitnessTool(nil, nil, "test", "UTC", false),
		newGetTrainingSummaryTool(nil, nil, "test", "UTC", false),
		newComputeZoneTimeTool(nil, nil, nil, nil, "test", "UTC", false),
		newComputeLoadBalanceTool(nil, nil, nil, nil, "test", "UTC", false),
	}
	for _, tool := range tools {
		t.Run(tool.Name, func(t *testing.T) {
			description := strings.ToLower(tool.Description)
			for _, term := range []string{"weekly", "anchor", "partial-week"} {
				if !strings.Contains(description, term) {
					t.Fatalf("description %q does not disclose %q", tool.Description, term)
				}
			}
		})
	}
	for _, tool := range tools[:2] {
		properties := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
		for _, field := range []string{"start_date", "end_date"} {
			description := strings.ToLower(properties[field].(map[string]any)["description"].(string))
			for _, term := range []string{"inclusive", "weekly", "anchor"} {
				if !strings.Contains(description, term) {
					t.Fatalf("%s %s description %q does not disclose %q", tool.Name, field, description, term)
				}
			}
		}
	}
}

func TestGetFitnessBaseMetadataDisclosesWeeklyAnchorContract(t *testing.T) {
	client := &fakeFitnessMetricsClient{summaries: []intervals.SummaryWithCats{{Date: "2026-05-04", Fitness: 50}}}
	tool := newGetFitnessTool(client, client, "test", "UTC", false)
	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"start_date":"2026-05-04","end_date":"2026-05-10"}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	assertWeeklySummaryBoundaryMeta(t, resultMap(t, result)["_meta"].(map[string]any))
}

func TestAthleteSummaryToolsHonorInclusiveDateWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tool   func(*intervals.Client, ProfileClient) Tool
		args   json.RawMessage
		assert func(*testing.T, map[string]any)
	}{
		{
			name: "training summary excludes buckets before non Monday start in full response",
			tool: func(client *intervals.Client, profile ProfileClient) Tool {
				return newGetTrainingSummaryTool(client, profile, "test", "UTC", false)
			},
			args: json.RawMessage(`{"start_date":"2026-08-11","end_date":"2026-08-24","include_full":true}`),
			assert: func(t *testing.T, got map[string]any) {
				t.Helper()
				summary := got["summary"].(map[string]any)
				if summary["count"] != float64(2) || summary["training_load"] != float64(50) {
					t.Fatalf("summary = %#v, want only 2026-08-17 and 2026-08-24 buckets", summary)
				}
				assertSummaryFullDates(t, got, []string{"2026-08-17", "2026-08-24"})
				assertWeeklySummaryBoundaryMeta(t, got["_meta"].(map[string]any))
			},
		},
		{
			name: "training summary excludes bucket after end boundary",
			tool: func(client *intervals.Client, profile ProfileClient) Tool {
				return newGetTrainingSummaryTool(client, profile, "test", "UTC", false)
			},
			args: json.RawMessage(`{"start_date":"2026-08-11","end_date":"2026-08-23","include_full":true}`),
			assert: func(t *testing.T, got map[string]any) {
				t.Helper()
				summary := got["summary"].(map[string]any)
				if summary["count"] != float64(1) || summary["training_load"] != float64(20) {
					t.Fatalf("summary = %#v, want only 2026-08-17 bucket", summary)
				}
				assertSummaryFullDates(t, got, []string{"2026-08-17"})
			},
		},
		{
			name: "zone analyzer excludes out of range buckets",
			tool: func(client *intervals.Client, profile ProfileClient) Tool {
				return newComputeZoneTimeTool(client, nil, nil, profile, "test", "UTC", false)
			},
			args: json.RawMessage(`{"start_date":"2026-08-11","end_date":"2026-08-24","zone_metric":"power","include_full":true}`),
			assert: func(t *testing.T, got map[string]any) {
				t.Helper()
				result := got["result"].(map[string]any)
				if result["total_seconds"] != float64(75) {
					t.Fatalf("zone result = %#v, want only in-range bucket totals", result)
				}
				assertSeriesDates(t, got["series"].([]any), []string{"2026-08-17", "2026-08-24"})
				boundaries := stringSliceFromAny(got["_meta"].(map[string]any)["boundaries"])
				if !stringSliceContains(boundaries, "upstream weekly summary buckets are selected by anchor date") {
					t.Fatalf("zone boundaries = %v, want weekly bucket caveat", boundaries)
				}
			},
		},
		{
			name: "trend analyzer excludes out of range samples",
			tool: func(client *intervals.Client, profile ProfileClient) Tool {
				return newAnalyzeTrendTool(client, nil, nil, profile, "test", "UTC", false)
			},
			args: json.RawMessage(`{"metric":"ctl","window":{"start_date":"2026-08-11","end_date":"2026-08-24"},"baseline_window":{"start_date":"2026-07-28","end_date":"2026-08-10"},"rolling_window_days":7,"include_full":true}`),
			assert: func(t *testing.T, got map[string]any) {
				t.Helper()
				meta := got["_meta"].(map[string]any)
				if meta["n"] != float64(2) || meta["missing_days"] != float64(0) {
					t.Fatalf("trend metadata = %#v, want two current-window samples", meta)
				}
				result := got["result"].(map[string]any)
				if result["sample_grain"] != "weekly" {
					t.Fatalf("trend result = %#v, want weekly sample grain", result)
				}
				assumptions := meta["assumptions"].(map[string]any)
				if assumptions["expected_weekly_anchors"] != float64(2) || assumptions["missing_weekly_anchors"] != float64(0) {
					t.Fatalf("trend assumptions = %#v", assumptions)
				}
				assertSeriesDates(t, got["series"].([]any), []string{"2026-08-17", "2026-08-24"})
			},
		},
		{
			name: "fitness trend keeps lookback but refuses daily distribution",
			tool: func(client *intervals.Client, profile ProfileClient) Tool {
				return newGetFitnessTool(client, profile, "test", "UTC", false)
			},
			args: json.RawMessage(`{"start_date":"2026-08-11","end_date":"2026-08-24","include_per_sport_load_trends":true,"include_full":true}`),
			assert: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertSeriesDates(t, got["fitness"].([]any), []string{"2026-08-17", "2026-08-24"})
				if _, ok := got["per_sport_load_trends"]; ok {
					t.Fatalf("fitness response invented daily per-sport rows: %#v", got["per_sport_load_trends"])
				}
				trendMeta := got["_meta"].(map[string]any)["per_sport_load_trends"].(map[string]any)
				if trendMeta["status"] != "unavailable" || trendMeta["warmup_summary_anchors_available"] != float64(2) {
					t.Fatalf("per-sport trend metadata = %#v, want unavailable with two retained warmup anchors", trendMeta)
				}
				assertWeeklySummaryBoundaryMeta(t, trendMeta)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[
					{"athlete_id":"i12345","date":"2026-08-03","count":10,"training_load":90,"fitness":50,"fatigue":60,"form":-10,"timeInZones":[90,45],"timeInZonesTot":135,"byCategory":[{"category":"Ride","training_load":90}]},
					{"athlete_id":"i12345","date":"2026-08-10","count":10,"training_load":100,"fitness":60,"fatigue":70,"form":-10,"timeInZones":[100,50],"timeInZonesTot":150,"byCategory":[{"category":"Ride","training_load":100}]},
					{"athlete_id":"i12345","date":"2026-08-17","count":1,"training_load":20,"fitness":70,"fatigue":75,"form":-5,"timeInZones":[20,10],"timeInZonesTot":30,"byCategory":[{"category":"Ride","training_load":20}]},
					{"athlete_id":"i12345","date":"2026-08-24","count":1,"training_load":30,"fitness":80,"fatigue":82,"form":-2,"timeInZones":[30,15],"timeInZonesTot":45,"byCategory":[{"category":"Ride","training_load":30}]},
					{"athlete_id":"i12345","date":"2026-08-25","count":10,"training_load":400,"fitness":90,"fatigue":100,"form":-10,"timeInZones":[400,200],"timeInZonesTot":600,"byCategory":[{"category":"Ride","training_load":400}]}
				]`))
			}))
			defer server.Close()

			client, err := intervals.NewClient(intervals.Options{
				Config:  config.Config{APIKey: "test-key", AthleteID: "i12345", APIBaseURL: server.URL, HTTPTimeout: time.Second},
				Version: "test", HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			profile := &fakeProfileClient{profile: intervals.AthleteWithSportSettings{ID: "i12345", PreferredUnits: "metric", Timezone: "UTC"}}
			tool := tc.tool(client, profile)
			result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: tc.args})
			if err != nil {
				t.Fatalf("Handler() error = %v", err)
			}
			tc.assert(t, resultMap(t, result))
		})
	}
}

func assertWeeklySummaryBoundaryMeta(t *testing.T, meta map[string]any) {
	t.Helper()
	if meta["summary_grain"] != "upstream_weekly_bucket" || meta["window_policy"] != "include_bucket_when_anchor_date_is_within_inclusive_window" {
		t.Fatalf("summary boundary metadata = %#v", meta)
	}
	caveats := stringSliceFromAny(meta["caveats"])
	if !stringSliceContains(caveats, "exact partial-week totals cannot be derived from athlete-summary buckets") {
		t.Fatalf("summary caveats = %v, want partial-week limitation", caveats)
	}
}

func assertSummaryFullDates(t *testing.T, got map[string]any, want []string) {
	t.Helper()
	assertSeriesDates(t, got["full"].([]any), want)
}

func assertSeriesDates(t *testing.T, rows []any, want []string) {
	t.Helper()
	dates := make([]string, 0, len(rows))
	for _, row := range rows {
		dates = append(dates, row.(map[string]any)["date"].(string))
	}
	if len(dates) != len(want) {
		t.Fatalf("series dates = %v, want %v", dates, want)
	}
	for index := range want {
		if dates[index] != want[index] {
			t.Fatalf("series dates = %v, want %v", dates, want)
		}
	}
}
