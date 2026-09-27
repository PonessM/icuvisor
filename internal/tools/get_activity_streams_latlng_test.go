package tools

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/ricardocabral/icuvisor/internal/intervals"
)

func latlngResult(t *testing.T, rows []string, args string) map[string]any {
	t.Helper()
	client := &fakeActivityReadClient{streams: decodeStreamFixtures(t, rows...)}
	tool := newGetActivityStreamsTool(client, client, "test", false)
	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	return resultMap(t, result)
}

func latlngStream(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	return payload["streams"].(map[string]any)["latlng"].(map[string]any)
}

func assertLatlngAxes(t *testing.T, row map[string]any) {
	t.Helper()
	if row["samples_axis"] != "latitude_degrees" || row["data2_axis"] != "longitude_degrees" {
		t.Fatalf("latlng axes = %#v, want latitude and longitude degrees", row)
	}
}

func TestGetActivityStreamsLatlngFullAndTerse(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("testdata/activity_streams/latlng_response.json")
	if err != nil {
		t.Fatalf("read latlng response fixture: %v", err)
	}
	var rawRows []json.RawMessage
	if err := json.Unmarshal(fixture, &rawRows); err != nil {
		t.Fatalf("decode latlng response fixture: %v", err)
	}
	rows := make([]string, len(rawRows))
	for i, raw := range rawRows {
		rows[i] = string(raw)
	}
	for _, tc := range []struct {
		name string
		args string
		full bool
	}{
		{name: "terse", args: `{"activity_id":"a1","keys":["LatLng"]}`},
		{name: "full", args: `{"activity_id":"a1","keys":["LatLng"],"include_full":true}`, full: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := latlngResult(t, rows, tc.args)
			row := latlngStream(t, payload)
			assertLatlngAxes(t, row)
			if got := payload["_meta"].(map[string]any)["samples_included"]; got != tc.full {
				t.Fatalf("samples_included = %#v, want %t", got, tc.full)
			}
			if !tc.full {
				for _, key := range []string{"samples", "data2", "full"} {
					if _, ok := row[key]; ok {
						t.Fatalf("terse row includes %s: %#v", key, row)
					}
				}
				return
			}
			if !equalFloatSlices(row["samples"].([]any), []float64{-23.5, -23.6}) || !equalFloatSlices(row["data2"].([]any), []float64{-46.6, -46.7}) {
				t.Fatalf("coordinate channels = %#v", row)
			}
			full := row["full"].(map[string]any)
			if full["source"] != "gps" || !equalFloatSlices(full["data"].([]any), []float64{-23.5, -23.6}) || !equalFloatSlices(full["data2"].([]any), []float64{-46.6, -46.7}) {
				t.Fatalf("full upstream evidence = %#v", full)
			}
		})
	}
}

func TestGetActivityStreamsLatlngWindowIgnoresNonFiniteOutsideSelection(t *testing.T) {
	t.Parallel()
	rows := []intervals.ActivityStream{
		{Type: "time", Data: []float64{0, 10, 20, 30}},
		{Type: "LatLng", Data: []float64{math.NaN(), 1, 2, 3}, Data2: []float64{4, 5, 6, math.Inf(1)}},
	}
	for _, tc := range []struct {
		name string
		args string
	}{
		{name: "windowed safe pairs", args: `{"activity_id":"a1","keys":["latlng"],"include_full":true,"time_window":{"start":10,"end":20}}`},
		{name: "unwindowed whole channel", args: `{"activity_id":"a1","keys":["latlng"],"include_full":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeActivityReadClient{streams: rows}
			tool := newGetActivityStreamsTool(client, client, "test", false)
			result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(tc.args)})
			if err != nil {
				t.Fatalf("Handler() error = %v", err)
			}
			payload := resultMap(t, result)
			row := latlngStream(t, payload)
			if tc.name == "windowed safe pairs" {
				lat, latOK := row["samples"].([]any)
				lon, lonOK := row["data2"].([]any)
				if !latOK || !lonOK || !equalFloatSlices(lat, []float64{1, 2}) || !equalFloatSlices(lon, []float64{5, 6}) {
					t.Fatalf("windowed pairs = %#v", row)
				}
				full := row["full"].(map[string]any)
				if !equalFloatSlices(full["data"].([]any), []float64{1, 2}) || !equalFloatSlices(full["data2"].([]any), []float64{5, 6}) {
					t.Fatalf("windowed full pairs = %#v", full)
				}
				if _, ok := payload["_meta"].(map[string]any)["data_availability"]; ok {
					t.Fatalf("safe selected pairs have diagnostic: %#v", payload["_meta"])
				}
				return
			}
			for _, key := range []string{"samples", "data2", "full"} {
				if _, ok := row[key]; ok {
					t.Fatalf("unwindowed %s exposed: %#v", key, row)
				}
			}
			diagnostics := payload["_meta"].(map[string]any)["data_availability"].([]any)
			if len(diagnostics) != 1 || diagnostics[0].(map[string]any)["reason"] != "channel_non_finite" {
				t.Fatalf("unwindowed diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestGetActivityStreamsLatlngMaxPointsAndWindowKeepPairs(t *testing.T) {
	t.Parallel()
	rows := []string{
		`{"type":"time","data":[0,10,20,30,40,50,60]}`,
		`{"type":"LatLng","data":[10,11,12,13,14,15,16],"data2":[-10,-11,-12,-13,-14,-15,-16]}`,
	}
	for _, tc := range []struct {
		name string
		args string
		lat  []float64
		lon  []float64
	}{
		{name: "unwindowed", args: `{"activity_id":"a1","keys":["latlng"],"include_full":true,"max_points":3}`, lat: []float64{10, 13, 16}, lon: []float64{-10, -13, -16}},
		{name: "windowed", args: `{"activity_id":"a1","keys":["latlng"],"include_full":true,"time_window":{"start":10,"end":50},"max_points":3}`, lat: []float64{11, 13, 15}, lon: []float64{-11, -13, -15}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := latlngStream(t, latlngResult(t, rows, tc.args))
			assertLatlngAxes(t, row)
			if !equalFloatSlices(row["samples"].([]any), tc.lat) || !equalFloatSlices(row["data2"].([]any), tc.lon) {
				t.Fatalf("paired samples = %#v, want latitude %v longitude %v", row, tc.lat, tc.lon)
			}
			full := row["full"].(map[string]any)
			if !equalFloatSlices(full["data"].([]any), tc.lat) || !equalFloatSlices(full["data2"].([]any), tc.lon) {
				t.Fatalf("bounded full pairs = %#v", full)
			}
			if row["returned_sample_count"] != float64(3) || row["sampling_method"] != "uniform_index" {
				t.Fatalf("sampling provenance = %#v", row)
			}
		})
	}
}

func TestGetActivityStreamsLatlngAbsentAndUnsafeData2(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		row    string
		reason string
	}{
		{name: "absent", row: `{"type":"LatLng","data":[1,2,3]}`},
		{name: "null channel", row: `{"type":"LatLng","data":[1,2,3],"data2":null,"source":"gps"}`, reason: "channel_null"},
		{name: "null element", row: `{"type":"LatLng","data":[1,2,3],"data2":[4,null,6],"source":"gps"}`, reason: "channel_null"},
		{name: "short longitude", row: `{"type":"LatLng","data":[1,2,3],"data2":[4,5],"source":"gps"}`, reason: "channel_length_mismatch"},
		{name: "long longitude", row: `{"type":"LatLng","data":[1,2,3],"data2":[4,5,6,7],"source":"gps"}`, reason: "channel_length_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, windowed := range []bool{false, true} {
				name := "unwindowed"
				args := `{"activity_id":"a1","include_full":true,"max_points":2}`
				rows := []string{tc.row}
				if windowed {
					name = "windowed"
					args = `{"activity_id":"a1","include_full":true,"time_window":{"start":0,"end":20},"max_points":2}`
					rows = append([]string{`{"type":"time","data":[0,10,20]}`}, rows...)
				}
				t.Run(name, func(t *testing.T) {
					payload := latlngResult(t, rows, args)
					row := latlngStream(t, payload)
					assertLatlngAxes(t, row)
					if tc.reason == "" {
						if !equalFloatSlices(row["samples"].([]any), []float64{1, 3}) {
							t.Fatalf("latitude-only samples = %#v", row)
						}
						if _, ok := row["data2"]; ok {
							t.Fatalf("absent data2 unexpectedly present: %#v", row)
						}
						return
					}
					for _, key := range []string{"samples", "data2", "full"} {
						if _, ok := row[key]; ok {
							t.Fatalf("unsafe %s exposed: %#v", key, row)
						}
					}
					want := tc.reason
					if windowed {
						want = "window_" + want
					}
					diagnostics := payload["_meta"].(map[string]any)["data_availability"].([]any)
					if len(diagnostics) != 1 || diagnostics[0].(map[string]any)["reason"] != want {
						t.Fatalf("data_availability = %#v, want %q", diagnostics, want)
					}
					if message, ok := diagnostics[0].(map[string]any)["message"].(string); !ok || !strings.Contains(message, "withheld") {
						t.Fatalf("diagnostic needs actionable withholding message: %#v", diagnostics)
					}
				})
			}
		})
	}
}

func TestGetActivityStreamsLatlngLargeSourceHasBoundedPairs(t *testing.T) {
	t.Parallel()
	const sourceCount = 20000
	latitudes := make([]float64, sourceCount)
	longitudes := make([]float64, sourceCount)
	for i := range latitudes {
		latitudes[i] = float64(i)
		longitudes[i] = -float64(i)
	}
	fixture, err := json.Marshal(map[string]any{"type": "LatLng", "data": latitudes, "data2": longitudes})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	payload := latlngResult(t, []string{string(fixture)}, `{"activity_id":"a1","include_full":true,"max_points":5}`)
	row := latlngStream(t, payload)
	if row["sample_count"] != float64(sourceCount) || row["returned_sample_count"] != float64(5) {
		t.Fatalf("sample counts = %#v", row)
	}
	lat := row["samples"].([]any)
	lon := row["data2"].([]any)
	if len(lat) != 5 || len(lon) != 5 || lat[0] != float64(0) || lon[0] != float64(0) || lat[4] != float64(sourceCount-1) || lon[4] != -float64(sourceCount-1) {
		t.Fatalf("bounded endpoint pairs = %#v / %#v", lat, lon)
	}
	for i := range lat {
		if lat[i].(float64) != -lon[i].(float64) {
			t.Fatalf("pair %d misaligned: %v / %v", i, lat[i], lon[i])
		}
	}
	if got := len(row["full"].(map[string]any)["data"].([]any)); got != 5 {
		t.Fatalf("full data length = %d, want 5", got)
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > 2500 {
		t.Fatalf("bounded response size = %d bytes, marshal error = %v", len(encoded), err)
	}
}

func TestGetActivityStreamsLatlngNonFiniteCoordinatesAreWithheld(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{name: "nan", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "negative infinity", value: math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, windowed := range []bool{false, true} {
				name := "unwindowed"
				args := `{"activity_id":"a1","include_full":true}`
				rows := []intervals.ActivityStream{{Type: "LatLng", Data: []float64{1, 2, 3}, Data2: []float64{4, tc.value, 6}}}
				want := "channel_non_finite"
				if windowed {
					name = "windowed"
					args = `{"activity_id":"a1","include_full":true,"time_window":{"start":0,"end":20}}`
					rows = append(decodeStreamFixtures(t, `{"type":"time","data":[0,10,20]}`), rows...)
					want = "window_" + want
				}
				t.Run(name, func(t *testing.T) {
					client := &fakeActivityReadClient{streams: rows}
					tool := newGetActivityStreamsTool(client, client, "test", false)
					result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(args)})
					if err != nil {
						t.Fatalf("Handler() error = %v, want diagnostic", err)
					}
					payload := resultMap(t, result)
					row := latlngStream(t, payload)
					for _, key := range []string{"samples", "data2", "full"} {
						if _, ok := row[key]; ok {
							t.Fatalf("non-finite %s exposed: %#v", key, row)
						}
					}
					diagnostics := payload["_meta"].(map[string]any)["data_availability"].([]any)
					if len(diagnostics) != 1 || diagnostics[0].(map[string]any)["reason"] != want {
						t.Fatalf("data_availability = %#v, want %q", diagnostics, want)
					}
				})
			}
		})
	}
}
