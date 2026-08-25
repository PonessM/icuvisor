package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ricardocabral/icuvisor/internal/intervals"
)

const (
	runningDynamicsActivityFixture  = "../../testdata/extended-metrics/activity-running-dynamics.json"
	runningDynamicsIntervalsFixture = "../../testdata/extended-metrics/activity-intervals-running-dynamics.json"
)

var documentedRunningDynamics = []struct {
	responseField string
	sourceField   string
	unit          string
	activityValue float64
	intervalValue float64
}{
	{responseField: "average_stance_time", sourceField: "average_stance_time", unit: "ms", activityValue: 0, intervalValue: 246},
	{responseField: "average_vertical_oscillation", sourceField: "average_vertical_oscillation", unit: "mm", activityValue: 82, intervalValue: 79},
	{responseField: "average_vertical_ratio", sourceField: "average_vertical_ratio", unit: "PERCENT", activityValue: 7.4, intervalValue: 7.1},
	{responseField: "average_step_length", sourceField: "average_step_length", unit: "mm", activityValue: 1180, intervalValue: 1210},
	{responseField: "average_stance_time_percent", sourceField: "average_stance_time_percent", unit: "PERCENT", activityValue: 41.2, intervalValue: 40.4},
	{responseField: "average_stance_time_balance", sourceField: "average_stance_time_balance", unit: "PERCENT", activityValue: 50.1, intervalValue: 49.8},
	{responseField: "average_vertical_speed", sourceField: "average_vertical_speed", unit: "m/s", activityValue: 1.21, intervalValue: 1.19},
	{responseField: "average_leg_spring_stiffness", sourceField: "average_leg_spring_stiffness", unit: "kN/m", activityValue: 10.5, intervalValue: 11.2},
}

func TestExtendedMetricsReturnsDocumentedRunningDynamicsAtActivityAndIntervalScope(t *testing.T) {
	t.Parallel()

	client := newFakeExtendedMetricsClient(t)
	client.activity = decodeActivityFileFixture(t, runningDynamicsActivityFixture)
	client.intervals = decodeIntervalsFileFixture(t, runningDynamicsIntervalsFixture)
	tool := newGetExtendedMetricsTool(client, client, "test", "UTC", false)

	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"activity-running-dynamics-fixture"}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	payload := resultMap(t, result)
	metrics := payload["metrics"].(map[string]any)
	intervalsOut, ok := payload["intervals"].([]any)
	if !ok || len(intervalsOut) != 1 {
		t.Fatalf("terse response intervals = %#v, want one running-dynamics interval", payload["intervals"])
	}
	interval := intervalsOut[0].(map[string]any)
	meta := payload["_meta"].(map[string]any)
	units := meta["extended_metric_units"].(map[string]any)
	provenance := meta["metric_provenance"].(map[string]any)

	for _, tc := range documentedRunningDynamics {
		if got := metrics[tc.responseField]; got != tc.activityValue {
			t.Fatalf("activity metrics[%s] = %v, want %v in %#v", tc.responseField, got, tc.activityValue, metrics)
		}
		if got := interval[tc.responseField]; got != tc.intervalValue {
			t.Fatalf("interval[%s] = %v, want %v in %#v", tc.responseField, got, tc.intervalValue, interval)
		}
		if got := units[tc.responseField]; got != tc.unit {
			t.Fatalf("extended_metric_units[%s] = %v, want %q", tc.responseField, got, tc.unit)
		}
		entry := provenance[tc.responseField].(map[string]any)
		if entry["source_field"] != tc.sourceField || entry["response_field"] != tc.responseField || entry["unit"] != tc.unit || entry["scope"] != "activity_and_interval" || entry["source_endpoint"] != "GET /api/v1/activity/{id}; GET /api/v1/activity/{id}/intervals" || entry["availability"] != "conditional" {
			t.Fatalf("metric_provenance[%s] = %#v", tc.responseField, entry)
		}
	}
	for _, metricsScope := range []map[string]any{metrics, interval} {
		if _, ok := metricsScope["average_impact_loading_rate"]; ok {
			t.Fatalf("unit-unverified impact loading rate was surfaced: %#v", metricsScope)
		}
	}
	if _, ok := units["average_impact_loading_rate"]; ok {
		t.Fatalf("extended_metric_units included unit-unverified impact loading rate: %#v", units)
	}
	if _, ok := provenance["average_impact_loading_rate"]; ok {
		t.Fatalf("metric_provenance included unit-unverified impact loading rate: %#v", provenance)
	}
	if !containsAnyString(meta["dropped_fields"].([]any), "average_impact_loading_rate") {
		t.Fatalf("dropped_fields did not identify unit-unverified impact loading rate: %#v", meta["dropped_fields"])
	}
}

func TestExtendedMetricsOmitsUnavailableDocumentedRunningDynamicsButKeepsThemRawWhenRequested(t *testing.T) {
	t.Parallel()

	client := newFakeExtendedMetricsClient(t)
	client.activity = decodeActivityFileFixture(t, runningDynamicsActivityFixture)
	client.intervals = decodeIntervalsFileFixture(t, runningDynamicsIntervalsFixture)
	setDocumentedRunningDynamicsUnavailable(t, &client.activity.Raw, &client.intervals)
	tool := newGetExtendedMetricsTool(client, client, "test", "UTC", false)

	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"activity_id":"activity-running-dynamics-fixture","include_full":true}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	payload := resultMap(t, result)
	metrics := payload["metrics"].(map[string]any)
	for _, tc := range documentedRunningDynamics {
		if _, ok := metrics[tc.responseField]; ok {
			t.Fatalf("activity metrics included unavailable %s: %#v", tc.responseField, metrics)
		}
	}
	if intervalsOut, ok := payload["intervals"].([]any); ok {
		for _, row := range intervalsOut {
			interval := row.(map[string]any)
			for _, tc := range documentedRunningDynamics {
				if _, ok := interval[tc.responseField]; ok {
					t.Fatalf("interval metrics included unavailable %s: %#v", tc.responseField, interval)
				}
			}
		}
	}
	full := payload["full"].(map[string]any)
	activityRaw := full["activity"].(map[string]any)
	intervalRaw := full["intervals"].(map[string]any)["icu_intervals"].([]any)[0].(map[string]any)
	if activityRaw["average_stance_time"] != nil || intervalRaw["average_stance_time"] != "malformed" {
		t.Fatalf("full raw running dynamics = activity %#v interval %#v", activityRaw, intervalRaw)
	}
}

func setDocumentedRunningDynamicsUnavailable(t *testing.T, activity *map[string]any, dto *intervals.IntervalsDTO) {
	t.Helper()
	intervalsRaw := dto.Raw["icu_intervals"].([]any)
	intervalRaw := intervalsRaw[0].(map[string]any)
	for index, tc := range documentedRunningDynamics {
		switch index % 3 {
		case 0:
			(*activity)[tc.sourceField] = nil
			intervalRaw[tc.sourceField] = "malformed"
			dto.ICUIntervals[0].Raw[tc.sourceField] = "malformed"
		case 1:
			delete(*activity, tc.sourceField)
			delete(intervalRaw, tc.sourceField)
			delete(dto.ICUIntervals[0].Raw, tc.sourceField)
		case 2:
			(*activity)[tc.sourceField] = "malformed"
			intervalRaw[tc.sourceField] = nil
			dto.ICUIntervals[0].Raw[tc.sourceField] = nil
		}
	}
}

func TestExtendedMetricsDefersUnitUnverifiedRunningCadence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		includeFull bool
		cadenceMode string
	}{
		{name: "numeric terse", cadenceMode: "numeric"},
		{name: "numeric full", includeFull: true, cadenceMode: "numeric"},
		{name: "null full", includeFull: true, cadenceMode: "null"},
		{name: "absent full", includeFull: true, cadenceMode: "absent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := newFakeExtendedMetricsClient(t)
			client.activity = decodeActivityFileFixture(t, runningDynamicsActivityFixture)
			client.intervals = decodeIntervalsFileFixture(t, runningDynamicsIntervalsFixture)
			setRunningCadenceFixtureMode(t, &client.activity.Raw, &client.intervals, tc.cadenceMode)
			tool := newGetExtendedMetricsTool(client, client, "test", "UTC", false)

			arguments := `{"activity_id":"activity-running-dynamics-fixture"}`
			if tc.includeFull {
				arguments = `{"activity_id":"activity-running-dynamics-fixture","include_full":true}`
			}
			result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(arguments)})
			if err != nil {
				t.Fatalf("Handler() error = %v", err)
			}
			payload := resultMap(t, result)
			assertRunningCadenceIsTerseAbsent(t, payload)

			if !tc.includeFull {
				if _, ok := payload["full"]; ok {
					t.Fatalf("full present in terse response: %#v", payload)
				}
				return
			}
			assertRunningCadenceRawMode(t, payload, tc.cadenceMode)
		})
	}
}

func setRunningCadenceFixtureMode(t *testing.T, activity *map[string]any, dto *intervals.IntervalsDTO, mode string) {
	t.Helper()
	intervalsRaw, ok := dto.Raw["icu_intervals"].([]any)
	if !ok || len(intervalsRaw) != 1 {
		t.Fatalf("fixture intervals = %#v, want one raw interval", dto.Raw)
	}
	intervalRaw, ok := intervalsRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("fixture interval = %#v, want object", intervalsRaw[0])
	}
	if len(dto.ICUIntervals) != 1 {
		t.Fatalf("decoded intervals = %#v, want one interval", dto.ICUIntervals)
	}

	applyCadenceMode(*activity, []string{"average_cadence"}, mode)
	applyCadenceMode(intervalRaw, runningCadenceKeys, mode)
	applyCadenceMode(dto.ICUIntervals[0].Raw, runningCadenceKeys, mode)
	for _, metric := range documentedRunningDynamics {
		delete(*activity, metric.sourceField)
		delete(intervalRaw, metric.sourceField)
		delete(dto.ICUIntervals[0].Raw, metric.sourceField)
	}
}

var runningCadenceKeys = []string{"average_cadence", "min_cadence", "max_cadence"}

func applyCadenceMode(raw map[string]any, keys []string, mode string) {
	for _, key := range keys {
		switch mode {
		case "numeric":
		case "null":
			raw[key] = nil
		case "absent":
			delete(raw, key)
		default:
			panic("unknown cadence fixture mode")
		}
	}
}

func assertRunningCadenceIsTerseAbsent(t *testing.T, payload map[string]any) {
	t.Helper()
	metrics := payload["metrics"].(map[string]any)
	for _, key := range runningCadenceKeys {
		if _, ok := metrics[key]; ok {
			t.Fatalf("terse metrics included unit-unverified %s: %#v", key, metrics)
		}
	}
	if _, ok := payload["intervals"]; ok {
		t.Fatalf("cadence-only interval was surfaced in terse response: %#v", payload["intervals"])
	}

	meta := payload["_meta"].(map[string]any)
	units := meta["extended_metric_units"].(map[string]any)
	for _, key := range runningCadenceKeys {
		if _, ok := units[key]; ok {
			t.Fatalf("extended_metric_units included unit-unverified %s: %#v", key, units)
		}
	}
	dropped := meta["dropped_fields"].([]any)
	for _, key := range droppedExtendedMetricFields {
		if !containsAnyString(dropped, key) {
			t.Fatalf("dropped_fields missing %s: %#v", key, dropped)
		}
		if _, ok := metrics[key]; ok {
			t.Fatalf("dropped field %s was surfaced: %#v", key, metrics)
		}
	}
}

func assertRunningCadenceRawMode(t *testing.T, payload map[string]any, mode string) {
	t.Helper()
	full := payload["full"].(map[string]any)
	activity := full["activity"].(map[string]any)
	intervalsRaw := full["intervals"].(map[string]any)
	rows := intervalsRaw["icu_intervals"].([]any)
	interval := rows[0].(map[string]any)

	assertCadenceRawValue(t, activity, "average_cadence", mode, float64(176))
	assertCadenceRawValue(t, interval, "average_cadence", mode, float64(176))
	assertCadenceRawValue(t, interval, "min_cadence", mode, float64(164))
	assertCadenceRawValue(t, interval, "max_cadence", mode, float64(188))
}

func assertCadenceRawValue(t *testing.T, raw map[string]any, key, mode string, want float64) {
	t.Helper()
	value, present := raw[key]
	switch mode {
	case "numeric":
		if !present || value != want {
			t.Fatalf("raw %s = (%v, %t), want (%v, true)", key, value, present, want)
		}
	case "null":
		if !present || value != nil {
			t.Fatalf("raw %s = (%v, %t), want (nil, true)", key, value, present)
		}
	case "absent":
		if present {
			t.Fatalf("raw %s = %v, want absent", key, value)
		}
	default:
		t.Fatalf("unknown cadence fixture mode %q", mode)
	}
}

func containsAnyString(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
