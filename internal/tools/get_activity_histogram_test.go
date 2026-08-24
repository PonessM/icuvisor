package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ricardocabral/icuvisor/internal/intervals"
)

func TestGetActivityHistogramConfiguredZoneResponse(t *testing.T) {
	t.Parallel()

	client := &fakeActivityReadClient{
		fakeProfileClient: fakeProfileClient{profile: intervals.AthleteWithSportSettings{PreferredUnits: "metric", SportSettings: []intervals.SportSettings{{
			ID:                               7,
			Type:                             "Ride",
			FTP:                              228,
			PowerZoneUpperBoundsPercentOfFTP: []int{55, 75, 90, 105, 120, 150, 999},
			PowerZoneNames:                   []string{"Active Recovery", "Endurance", "Tempo", "Threshold", "VO2 Max", "Anaerobic", "Neuromuscular"},
		}}}},
		activity: decodeActivityFixture(t, `{"id":"a1","type":"Ride"}`),
		streams: decodeStreamFixtures(t,
			`{"type":"watts","data":[0,125.4,200,2277.72,2277.72]}`,
			`{"type":"time","data":[0,10,20,30,40]}`,
		),
	}
	tool := newGetActivityHistogramTool(client, client, client, "test", false)

	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"a1","metric":"power"}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	text := resultText(t, result)
	if strings.Contains(text, "samples") || strings.Contains(text, "\"data\"") {
		t.Fatalf("response contains raw stream samples: %s", text)
	}
	payload := resultMap(t, result)
	if payload["metric"] != "power_watts" {
		t.Fatalf("metric = %v", payload["metric"])
	}
	assertStringSlice(t, client.streamParams.Types, []string{"watts", "time"})
	if client.streamParams.IncludeDefaults {
		t.Fatal("IncludeDefaults = true, want false")
	}
	buckets := payload["buckets"].([]any)
	if len(buckets) != 8 {
		t.Fatalf("buckets = %d, want 8", len(buckets))
	}
	first := buckets[0].(map[string]any)
	if first["label"] != "Active Recovery" || first["seconds"] != float64(10) {
		t.Fatalf("first bucket = %#v, want zero watts in the first zone", first)
	}
	endurance := buckets[1].(map[string]any)
	if endurance["label"] != "Endurance" || endurance["seconds"] != float64(10) {
		t.Fatalf("endurance bucket = %#v, want 125.4 W in lower-inclusive next zone", endurance)
	}
	tempo := buckets[2].(map[string]any)
	if tempo["label"] != "Tempo" || tempo["lower"] != float64(171) || tempo["upper"] != float64(205.2) || tempo["seconds"] != float64(10) || tempo["percentage"] != float64(25) {
		t.Fatalf("tempo bucket = %#v, want 200 W classified entirely as Tempo", tempo)
	}
	above := buckets[7].(map[string]any)
	if above["label"] != "Above Neuromuscular" || above["lower"] != float64(2277.7) || above["seconds"] != float64(10) {
		t.Fatalf("overflow bucket = %#v, want 2277.72 W in Above Neuromuscular", above)
	}
	meta := payload["_meta"].(map[string]any)
	if meta["method"] != "activity_stream_histogram" || meta["bucket_method"] != "configured_zones" || meta["n"] != float64(4) {
		t.Fatalf("meta = %#v", meta)
	}
	assertSourceTools(t, meta["source_tools"], []string{"get_activity_details", "get_activity_streams", "get_athlete_profile"})
	zone := meta["zone_source"].(map[string]any)
	if zone["sport"] != "Ride" || zone["sport_setting_id"] != float64(7) || zone["boundary_unit"] != "W" {
		t.Fatalf("zone_source = %#v", zone)
	}
	boundaries := zone["boundaries"].([]any)
	wantBoundaries := []float64{0, 125.4, 171, 205.2, 239.4, 273.6, 342, 2277.72}
	if len(boundaries) != len(wantBoundaries) {
		t.Fatalf("zone boundaries = %#v, want %#v", boundaries, wantBoundaries)
	}
	for i, want := range wantBoundaries {
		if boundaries[i] != want {
			t.Fatalf("zone boundary %d = %v, want %v", i, boundaries[i], want)
		}
	}
}

func TestGetActivityHistogramPowerConfigurationFallbackWarnings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setting intervals.SportSettings
		warning string
	}{
		{
			name:    "malformed ceilings",
			setting: intervals.SportSettings{ID: 7, Type: "Ride", FTP: 228, PowerZoneUpperBoundsPercentOfFTP: []int{55, 55}, PowerZoneNames: []string{"Active Recovery", "Endurance"}},
			warning: string(intervals.PowerZoneInvalidCeilings),
		},
		{
			name:    "missing FTP",
			setting: intervals.SportSettings{ID: 7, Type: "Ride", PowerZoneUpperBoundsPercentOfFTP: []int{55, 75}, PowerZoneNames: []string{"Active Recovery", "Endurance"}},
			warning: string(intervals.PowerZoneMissingFTP),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeActivityReadClient{
				fakeProfileClient: fakeProfileClient{profile: intervals.AthleteWithSportSettings{SportSettings: []intervals.SportSettings{tc.setting}}},
				activity:          decodeActivityFixture(t, `{"id":"a1","type":"Ride"}`),
				streams: decodeStreamFixtures(t,
					`{"type":"watts","data":[100,200]}`,
					`{"type":"time","data":[0,10]}`,
				),
			}

			result, err := newGetActivityHistogramTool(client, client, client, "test", false).Handler(context.Background(), Request{Name: getActivityHistogramName, Arguments: json.RawMessage(`{"activity_id":"a1","metric":"power"}`)})
			if err != nil {
				t.Fatalf("Handler() error = %v", err)
			}
			payload := resultMap(t, result)
			meta := payload["_meta"].(map[string]any)
			if meta["bucket_method"] != "fixed_width" {
				t.Fatalf("bucket method = %#v, want fixed-width fallback", meta)
			}
			warnings := meta["warnings"].([]any)
			if len(warnings) != 1 || warnings[0] != tc.warning {
				t.Fatalf("warnings = %#v, want exact stable power-zone code %q", warnings, tc.warning)
			}
		})
	}
}

func TestGetActivityHistogramRequestsOnlyMetricStreams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		metric  string
		streams []intervals.ActivityStream
		want    []string
	}{
		{name: "power", metric: "power_watts", streams: decodeStreamFixtures(t, `{"type":"watts","data":[200,210]}`, `{"type":"time","data":[0,1]}`), want: []string{"watts", "time"}},
		{name: "heart rate", metric: "heart_rate_bpm", streams: decodeStreamFixtures(t, `{"type":"heart_rate","data":[140,142]}`, `{"type":"time","data":[0,1]}`), want: []string{"heart_rate", "time"}},
		{name: "pace", metric: "pace_seconds_per_km", streams: decodeStreamFixtures(t, `{"type":"distance","data":[0,1000]}`, `{"type":"time","data":[0,300]}`), want: []string{"distance", "time"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeActivityReadClient{streams: tc.streams}
			tool := newGetActivityHistogramTool(client, nil, nil, "test", false)
			_, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"a1","metric":"` + tc.metric + `","include_full":true}`)})
			if err != nil {
				t.Fatalf("Handler() error = %v", err)
			}
			assertStringSlice(t, client.streamParams.Types, tc.want)
			if client.streamParams.IncludeDefaults {
				t.Fatal("IncludeDefaults = true, want false")
			}
		})
	}
}

func TestGetActivityHistogramConvertsPaceZonePercentagesFromMPS(t *testing.T) {
	t.Parallel()

	client := &fakeActivityReadClient{
		fakeProfileClient: fakeProfileClient{profile: intervals.AthleteWithSportSettings{PreferredUnits: "metric", SportSettings: []intervals.SportSettings{{
			ID:            9,
			Type:          "Run",
			ThresholdPace: 3.5714285,
			PaceUnits:     "MINS_KM",
			PaceZones:     []float64{77.5, 100},
			PaceZoneNames: []string{"Easy", "Threshold"},
		}}}},
		activity: decodeActivityFixture(t, `{"id":"run-percent-zones","type":"Run"}`),
		streams: decodeStreamFixtures(t,
			`{"type":"distance","data":[0,1000,2000]}`,
			`{"type":"time","data":[0,300,700]}`,
		),
	}
	tool := newGetActivityHistogramTool(client, client, client, "test", false)

	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"run-percent-zones","metric":"pace"}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	payload := resultMap(t, result)
	meta := payload["_meta"].(map[string]any)
	if meta["bucket_method"] != "configured_zones" || meta["emitted_unit"] != "seconds_per_km" {
		t.Fatalf("meta = %#v, want configured pace zones in seconds_per_km", meta)
	}
	zone := meta["zone_source"].(map[string]any)
	boundaries := zone["boundaries"].([]any)
	if len(boundaries) != 2 || boundaries[0].(float64) < 279.9 || boundaries[0].(float64) > 280.1 || boundaries[1].(float64) < 361.2 || boundaries[1].(float64) > 361.4 || zone["boundary_unit"] != "seconds_per_km" {
		t.Fatalf("zone source = %#v, want duration boundaries derived from 77.5/100 percentages", zone)
	}
	buckets := payload["buckets"].([]any)
	if len(buckets) != 3 || buckets[1].(map[string]any)["label"] != "Threshold" || buckets[1].(map[string]any)["seconds"] != float64(300) || buckets[2].(map[string]any)["label"] != "Easy" || buckets[2].(map[string]any)["seconds"] != float64(400) {
		t.Fatalf("pace buckets = %#v, want samples classified by derived pace durations", buckets)
	}
}

func TestGetActivityHistogramPaceImperialFallbackForUnknownPaceUnits(t *testing.T) {
	t.Parallel()

	client := &fakeActivityReadClient{
		fakeProfileClient: fakeProfileClient{profile: intervals.AthleteWithSportSettings{PreferredUnits: "imperial", SportSettings: []intervals.SportSettings{{ID: 9, Type: "Run", PaceUnits: "", PaceZones: []float64{77.5, 100}, PaceZoneNames: []string{"Easy", "Threshold"}}}}},
		activity:          decodeActivityFixture(t, `{"id":"run1","type":"Run"}`),
		streams: decodeStreamFixtures(t,
			`{"type":"distance","data":[0,1609.344,3218.688]}`,
			`{"type":"time","data":[0,480,1020]}`,
		),
	}
	tool := newGetActivityHistogramTool(client, client, client, "test", false)

	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"run1","metric":"pace"}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	payload := resultMap(t, result)
	meta := payload["_meta"].(map[string]any)
	if meta["emitted_unit"] != "seconds_per_mile" || meta["bucket_method"] != "fixed_width" {
		t.Fatalf("meta = %#v", meta)
	}
	if _, ok := meta["zone_source"]; ok {
		t.Fatalf("zone_source present for unknown pace units: %#v", meta["zone_source"])
	}
}

func TestGetActivityHistogramUnavailableResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		streams []intervals.ActivityStream
		reason  string
	}{
		{name: "missing stream", streams: decodeStreamFixtures(t, `{"type":"time","data":[0,1]}`), reason: "missing_stream"},
		{name: "length mismatch", streams: decodeStreamFixtures(t, `{"type":"watts","data":[1,2,3]}`, `{"type":"time","data":[0,1]}`), reason: "missing_stream"},
		{name: "no positive intervals", streams: decodeStreamFixtures(t, `{"type":"watts","data":[1,2]}`, `{"type":"time","data":[0,0]}`), reason: "insufficient_sample"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeActivityReadClient{streams: tc.streams}
			tool := newGetActivityHistogramTool(client, nil, nil, "test", false)
			result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"a1","metric":"power_watts"}`)})
			if err != nil {
				t.Fatalf("Handler() error = %v", err)
			}
			payload := resultMap(t, result)
			if len(payload["buckets"].([]any)) != 0 {
				t.Fatalf("buckets = %#v, want empty", payload["buckets"])
			}
			unavailable := payload["unavailable"].(map[string]any)
			if unavailable["reason"] != tc.reason || unavailable["message"] == "" {
				t.Fatalf("unavailable = %#v", unavailable)
			}
			meta := payload["_meta"].(map[string]any)
			if _, ok := meta["bucket_method"]; ok {
				t.Fatalf("bucket_method present in unavailable meta: %#v", meta)
			}
			if meta["insufficient_sample"] != true || meta["n"] != float64(0) || meta["missing_days"] != float64(0) || meta["missing_action"] != "skip" {
				t.Fatalf("meta = %#v", meta)
			}
			assertSourceTools(t, meta["source_tools"], []string{"get_activity_streams"})
		})
	}
}

func TestGetActivityHistogramBestEffortLookupFailures(t *testing.T) {
	t.Parallel()

	client := &fakeActivityReadClient{
		fakeProfileClient: fakeProfileClient{err: errors.New("profile down")},
		activityErr:       errors.New("details down"),
		streams: decodeStreamFixtures(t,
			`{"type":"distance","data":[0,1000]}`,
			`{"type":"time","data":[0,300]}`,
		),
	}
	tool := newGetActivityHistogramTool(client, client, client, "test", false)
	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"a1","metric":"pace"}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	payload := resultMap(t, result)
	meta := payload["_meta"].(map[string]any)
	if meta["emitted_unit"] != "seconds_per_km" || meta["bucket_method"] != "fixed_width" {
		t.Fatalf("meta = %#v", meta)
	}
	warnings := meta["warnings"].([]any)
	if len(warnings) != 2 {
		t.Fatalf("warnings = %#v, want details/profile warnings", warnings)
	}
}

func TestGetActivityHistogramContextErrorsPropagate(t *testing.T) {
	t.Parallel()

	client := &fakeActivityReadClient{activityErr: context.Canceled}
	tool := newGetActivityHistogramTool(client, client, nil, "test", false)
	_, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"a1","metric":"power_watts"}`)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Handler() error = %v, want context canceled", err)
	}
}

func assertStringSlice(t *testing.T, got []string, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slice = %#v, want %#v", got, want)
	}
}

func assertSourceTools(t *testing.T, raw any, want []string) {
	t.Helper()
	values := raw.([]any)
	got := make([]string, 0, len(values))
	for _, value := range values {
		got = append(got, value.(string))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source_tools = %#v, want %#v", got, want)
	}
}
