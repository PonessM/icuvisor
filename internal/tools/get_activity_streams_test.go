package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ricardocabral/icuvisor/internal/config"
	"github.com/ricardocabral/icuvisor/internal/intervals"
)

func (f *fakeActivityReadClient) GetActivityStreams(ctx context.Context, params intervals.ActivityStreamsParams) ([]intervals.ActivityStream, error) {
	f.streamCalls++
	f.streamParams = params
	f.streamParamHistory = append(f.streamParamHistory, params)
	key := strings.Join(params.Types, ",")
	if f.streamErrors != nil {
		if err, ok := f.streamErrors[key]; ok {
			return nil, err
		}
	}
	if f.streamResponses != nil {
		if rows, ok := f.streamResponses[key]; ok {
			return rows, nil
		}
	}
	return f.streams, f.streamErr
}

func TestGetActivityStreamsUnavailableReasons(t *testing.T) {
	t.Parallel()

	tests := activityReadUnavailableCases()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := &fakeActivityReadClient{activity: tc.fallbackActivity, activityErr: tc.fallbackErr, streamErr: tc.upstreamErr}
			tool := newGetActivityStreamsTool(client, client, "test", false)

			result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"stub1"}`)})
			if err != nil {
				t.Fatalf("Handler() error = %v, want structured unavailable", err)
			}
			assertUnavailableReason(t, resultMap(t, result), tc.reason)
		})
	}
}

func TestGetActivityStreamsCanonicalizesKeysAndRequiresSamplesOptIn(t *testing.T) {
	t.Parallel()

	client := &fakeActivityReadClient{streams: decodeStreamFixtures(t,
		`{"type":"Power","name":"Power","data":[250,260]}`,
		`{"type":"CustomThing","name":"CustomThing","data":[1]}`,
	)}
	tool := newGetActivityStreamsTool(client, client, "test", false)

	defaultResult, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"a1"}`)})
	if err != nil {
		t.Fatalf("default Handler() error = %v", err)
	}
	defaultStream := resultMap(t, defaultResult)["streams"].(map[string]any)["watts"].(map[string]any)
	if _, ok := defaultStream["samples"]; ok {
		t.Fatalf("default stream = %#v, want no samples without keys/include_full", defaultStream)
	}

	keyedResult, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"a1","keys":["Power","unknownThing"]}`)})
	if err != nil {
		t.Fatalf("keyed Handler() error = %v", err)
	}
	payload := resultMap(t, keyedResult)
	streamsMap := payload["streams"].(map[string]any)
	if _, ok := streamsMap["watts"].(map[string]any)["samples"]; ok {
		t.Fatalf("streams = %#v, want watts metadata without samples for explicit key", streamsMap)
	}
	if got := payload["_meta"].(map[string]any)["samples_included"]; got != false {
		t.Fatalf("_meta.samples_included = %#v, want false", got)
	}
	unknown := payload["_meta"].(map[string]any)["unknown_stream_keys"].([]any)
	if len(unknown) == 0 {
		t.Fatalf("_meta = %#v, want unknown stream keys", payload["_meta"])
	}

	fullResult, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"a1","keys":["Power"],"include_full":true}`)})
	if err != nil {
		t.Fatalf("full Handler() error = %v", err)
	}
	fullPayload := resultMap(t, fullResult)
	fullStreams := fullPayload["streams"].(map[string]any)
	if got := fullStreams["watts"].(map[string]any)["samples"]; !equalFloatSlices(got.([]any), []float64{250, 260}) {
		t.Fatalf("streams = %#v, want watts samples [250 260]", fullStreams)
	}
	if got := fullPayload["_meta"].(map[string]any)["samples_included"]; got != true {
		t.Fatalf("_meta.samples_included = %#v, want true", got)
	}

	sampledClient := &fakeActivityReadClient{streams: decodeStreamFixtures(t, `{"type":"time","data":[0,10,20,30,40]}`)}
	sampledTool := newGetActivityStreamsTool(sampledClient, sampledClient, "test", false)
	sampledResult, err := sampledTool.Handler(context.Background(), Request{Name: sampledTool.Name, Arguments: json.RawMessage(`{"activity_id":"a1","include_full":true,"max_points":3}`)})
	if err != nil {
		t.Fatalf("sampled Handler() error = %v", err)
	}
	sampledStreams := resultMap(t, sampledResult)["streams"].(map[string]any)
	sampled := sampledStreams["time"].(map[string]any)
	if got := sampled["samples"]; !equalFloatSlices(got.([]any), []float64{0, 20, 40}) {
		t.Fatalf("samples = %#v, want [0 20 40]", got)
	}
	if got := sampled["sample_count"]; got != float64(5) {
		t.Fatalf("sample_count = %#v, want 5", got)
	}
	if got := sampled["returned_sample_count"]; got != float64(3) {
		t.Fatalf("returned_sample_count = %#v, want 3", got)
	}
	if got := sampled["sampling_method"]; got != "uniform_index" {
		t.Fatalf("sampling_method = %#v, want uniform_index", got)
	}
	if got := sampled["full"].(map[string]any)["data"]; !equalFloatSlices(got.([]any), []float64{0, 20, 40}) {
		t.Fatalf("full.data = %#v, want [0 20 40]", got)
	}
}

func TestGetActivityStreamsRejectsInvalidMaxPoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args string
	}{
		{name: "explicit zero", args: `{"activity_id":"a1","include_full":true,"max_points":0}`},
		{name: "below minimum", args: `{"activity_id":"a1","include_full":true,"max_points":1}`},
		{name: "above maximum", args: `{"activity_id":"a1","include_full":true,"max_points":5001}`},
		{name: "without include full", args: `{"activity_id":"a1","max_points":3}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := &fakeActivityReadClient{}
			tool := newGetActivityStreamsTool(client, client, "test", false)
			_, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(tc.args)})
			if _, ok := PublicErrorMessage(err); !ok {
				t.Fatalf("PublicErrorMessage(%v) = _, false, want NewUserError", err)
			}
			if client.streamCalls != 0 {
				t.Fatalf("GetActivityStreams calls = %d, want 0", client.streamCalls)
			}
		})
	}
}

func TestGetActivityStreamsReportsMissingHeartRateGuidance(t *testing.T) {
	t.Parallel()

	client := &fakeActivityReadClient{streams: decodeStreamFixtures(t, `{"type":"time","data":[0,1]}`)}
	tool := newGetActivityStreamsTool(client, client, "test", false)

	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"a1","keys":["heart_rate"]}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	meta := resultMap(t, result)["_meta"].(map[string]any)
	diagnostics := meta["data_availability"].([]any)
	if len(diagnostics) != 1 {
		t.Fatalf("data_availability = %#v, want one missing-stream diagnostic", diagnostics)
	}
	diagnostic := diagnostics[0].(map[string]any)
	if diagnostic["reason"] != "missing_stream" || !strings.Contains(diagnostic["message"].(string), "max-heart-rate") || !strings.Contains(diagnostic["workaround"].(string), "re-import") {
		t.Fatalf("diagnostic = %#v, want max-HR stream guidance", diagnostic)
	}
}

func TestGetActivityStreamsUnavailableIncludesRestrictedSourceDiagnostic(t *testing.T) {
	t.Parallel()

	client := &fakeActivityReadClient{activity: decodeExtendedMetricsActivity(t, `{"id":"stub1","source":"Strava","_note":"hidden"}`), streamErr: intervals.ErrNotFound}
	tool := newGetActivityStreamsTool(client, client, "test", false)

	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"stub1"}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v, want structured unavailable", err)
	}
	payload := resultMap(t, result)
	assertUnavailableReason(t, payload, "strava_blocked")
	diagnostics := payload["_meta"].(map[string]any)["data_availability"].([]any)
	diagnostic := diagnostics[0].(map[string]any)
	if diagnostic["reason"] != "restricted_source" || !strings.Contains(diagnostic["message"].(string), "max-heart-rate") {
		t.Fatalf("diagnostic = %#v, want restricted source data guidance", diagnostic)
	}
}

func TestGetActivitySplitsUnavailableReasons(t *testing.T) {
	t.Parallel()

	tests := activityReadUnavailableCases()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := &fakeActivityReadClient{fakeProfileClient: fakeProfileClient{profile: intervals.AthleteWithSportSettings{PreferredUnits: "metric"}}, activity: tc.fallbackActivity, activityErr: tc.fallbackErr, intervalErr: intervals.ErrNotFound, streamErr: tc.upstreamErr}
			tool := newGetActivitySplitsTool(client, client, client, client, "test", false)

			result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"stub1"}`)})
			if err != nil {
				t.Fatalf("Handler() error = %v, want structured unavailable", err)
			}
			assertUnavailableReason(t, resultMap(t, result), tc.reason)
		})
	}
}

func TestGetActivitySplitsComputesVirtualMetricAndImperial(t *testing.T) {
	t.Parallel()

	metricClient := &fakeActivityReadClient{fakeProfileClient: fakeProfileClient{profile: intervals.AthleteWithSportSettings{PreferredUnits: "metric"}}, streams: decodeStreamFixtures(t,
		`{"type":"distance","data":[0,1000,2000]}`,
		`{"type":"time","data":[0,300,620]}`,
	)}
	metricTool := newGetActivitySplitsTool(metricClient, metricClient, metricClient, metricClient, "test", false)
	metricResult, err := metricTool.Handler(context.Background(), Request{Name: metricTool.Name, Arguments: json.RawMessage(`{"activity_id":"a1"}`)})
	if err != nil {
		t.Fatalf("metric Handler() error = %v", err)
	}
	metricPayload := resultMap(t, metricResult)
	if metricPayload["split_unit"] != "km" || len(metricPayload["splits"].([]any)) != 2 {
		t.Fatalf("metric payload = %#v, want two km splits", metricPayload)
	}

	imperialClient := &fakeActivityReadClient{fakeProfileClient: fakeProfileClient{profile: intervals.AthleteWithSportSettings{PreferredUnits: "miles"}}, streams: decodeStreamFixtures(t,
		`{"type":"distance","data":[0,1609.344]}`,
		`{"type":"time","data":[0,480]}`,
	)}
	imperialTool := newGetActivitySplitsTool(imperialClient, imperialClient, imperialClient, imperialClient, "test", false)
	imperialResult, err := imperialTool.Handler(context.Background(), Request{Name: imperialTool.Name, Arguments: json.RawMessage(`{"activity_id":"a1"}`)})
	if err != nil {
		t.Fatalf("imperial Handler() error = %v", err)
	}
	imperialPayload := resultMap(t, imperialResult)
	if imperialPayload["split_unit"] != "mi" || len(imperialPayload["splits"].([]any)) != 1 {
		t.Fatalf("imperial payload = %#v, want one mile split", imperialPayload)
	}
}

func decodeStreamFixtures(t *testing.T, raws ...string) []intervals.ActivityStream {
	t.Helper()
	out := make([]intervals.ActivityStream, 0, len(raws))
	for _, raw := range raws {
		var stream intervals.ActivityStream
		if err := json.Unmarshal([]byte(raw), &stream); err != nil {
			t.Fatalf("decode stream fixture: %v", err)
		}
		out = append(out, stream)
	}
	return out
}

func equalFloatSlices(got []any, want []float64) bool {
	if len(got) != len(want) {
		return false
	}
	for i, wantValue := range want {
		if gotValue, ok := got[i].(float64); !ok || gotValue != wantValue {
			return false
		}
	}
	return true
}

func TestActivityStreamToolsWithUpstreamAnomaliesAndHeartRateNames(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("testdata/activity_streams_anomalies.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(fixture, &rows); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/athlete/i12345":
			_, _ = w.Write([]byte(`{"id":"i12345","preferred_units":"metric"}`))
		case "/activity/run-1":
			_, _ = w.Write([]byte(`{"id":"run-1","type":"Run"}`))
		case "/activity/run-1/intervals":
			_, _ = w.Write([]byte(`{"icu_intervals":[]}`))
		case "/activity/run-1/streams":
			types := r.URL.Query().Get("types")
			selected := strings.Split(types, ",")
			for _, key := range selected {
				if key == "heart_rate" {
					http.Error(w, "unknown stream type", http.StatusBadRequest)
					return
				}
			}
			var out []json.RawMessage
			for _, row := range rows {
				var channel struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(row, &channel); err != nil {
					t.Error(err)
					return
				}
				include := types == "" || r.URL.Query().Get("includeDefaults") == "true"
				for _, key := range selected {
					if key == channel.Type {
						include = true
					}
				}
				if include {
					out = append(out, row)
				}
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := intervals.NewClient(intervals.Options{Config: config.Config{APIKey: "test-key", AthleteID: "i12345", APIBaseURL: server.URL}, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		tool Tool
		args string
	}{
		{"segment control", newComputeActivitySegmentStatsTool(client, "test", false), `{"activity_id":"run-1","stat":"mean","metric":"heart_rate","start_seconds":0,"end_seconds":240}`},
		{"streams", newGetActivityStreamsTool(client, client, "test", false), `{"activity_id":"run-1"}`},
		{"filtered streams", newGetActivityStreamsTool(client, client, "test", false), `{"activity_id":"run-1","keys":["heart_rate"],"include_full":true}`},
		{"histogram", newGetActivityHistogramTool(client, client, client, "test", false), `{"activity_id":"run-1","metric":"heart_rate_bpm"}`},
		{"splits", newGetActivitySplitsTool(client, client, client, client, "test", false), `{"activity_id":"run-1","split_unit":"km"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.tool.Handler(context.Background(), Request{Arguments: json.RawMessage(tc.args)})
			if err != nil {
				t.Fatal(err)
			}
			payload := resultMap(t, result)
			if payload["unavailable"] != nil {
				t.Fatalf("unexpected unavailable: %s", resultText(t, result))
			}
			switch tc.name {
			case "segment control":
				if payload["result"].(map[string]any)["value"] != float64(120) {
					t.Fatalf("mean = %#v", payload)
				}
			case "streams", "filtered streams":
				hr := payload["streams"].(map[string]any)["heart_rate"].(map[string]any)
				if hr["type"] != "heartrate" {
					t.Fatalf("HR metadata = %#v", hr)
				}
				if tc.name == "filtered streams" {
					if !equalFloatSlices(hr["samples"].([]any), []float64{100, 110, 130, 140}) {
						t.Fatalf("HR samples = %#v", hr)
					}
				} else if _, ok := hr["samples"]; ok {
					t.Fatalf("terse response includes samples: %#v", hr)
				}
			case "histogram":
				if len(payload["buckets"].([]any)) == 0 {
					t.Fatalf("empty histogram: %#v", payload)
				}
			case "splits":
				splits := payload["splits"].([]any)
				if len(splits) != 2 || splits[0].(map[string]any)["average_heart_rate_bpm"] != float64(110) {
					t.Fatalf("splits = %#v", splits)
				}
			}
		})
	}
}
