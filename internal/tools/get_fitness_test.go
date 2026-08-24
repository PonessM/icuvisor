package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGetFitnessToolShapes(t *testing.T) {
	t.Parallel()

	client := newFakeFitnessMetricsClient(t)
	tool := newGetFitnessTool(client, client, "test", "UTC", false)
	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"start_date":"2026-05-01","end_date":"2026-05-02"}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	got := resultMap(t, result)
	rows := got["fitness"].([]any)
	if rows[0].(map[string]any)["date"] != "2026-05-01" || rows[1].(map[string]any)["date"] != "2026-05-02" {
		t.Fatalf("fitness row order = %#v", rows)
	}
	meta := got["_meta"].(map[string]any)
	if meta["timezone"] != "America/Sao_Paulo" || meta["server_version"] != "test" {
		t.Fatalf("meta = %#v", meta)
	}
	if _, ok := got["per_sport_load_trends"]; ok {
		t.Fatalf("default response unexpectedly included per_sport_load_trends: %#v", got)
	}
	if len(client.summaryCalls) != 1 || client.summaryCalls[0].Start != "2026-05-01" || client.summaryCalls[0].End != "2026-05-02" {
		t.Fatalf("summary calls = %#v", client.summaryCalls)
	}
}

func TestGetFitnessPerSportLoadTrends(t *testing.T) {
	t.Parallel()

	client := newFakeFitnessMetricsClient(t)
	client.summaries = decodeSummaries(t, `[
		{"date":"2026-05-02","fitness":72,"fatigue":80,"form":-8,"training_load":135,"byCategory":[{"category":"Indoor Cycling","training_load":90},{"category":"MTB","training_load":10},{"category":"Open Water Swim","training_load":35}]},
		{"date":"2026-05-01","fitness":70,"fatigue":78,"form":-8,"training_load":55,"byCategory":[{"category":"Trail Run","training_load":40},{"category":"Strength","training_load":15}]}
	]`)
	tool := newGetFitnessTool(client, client, "test", "UTC", false)

	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"start_date":"2026-05-01","end_date":"2026-05-02","include_per_sport_load_trends":true,"include_full":true}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	got := resultMap(t, result)
	if len(client.summaryCalls) != 1 || client.summaryCalls[0].Start != "2026-02-06" || client.summaryCalls[0].End != "2026-05-02" {
		t.Fatalf("summary calls = %#v", client.summaryCalls)
	}
	if _, ok := got["per_sport_load_trends"]; ok {
		t.Fatalf("response invented per-sport daily trend rows: %#v", got["per_sport_load_trends"])
	}
	meta := got["_meta"].(map[string]any)["per_sport_load_trends"].(map[string]any)
	if meta["status"] != "unavailable" || meta["reason"] != "weekly_summary_cannot_be_distributed_daily" || meta["warmup_summary_anchors_available"] != float64(0) {
		t.Fatalf("per-sport meta = %#v, want explicit unavailable result", meta)
	}
	if !strings.Contains(joinedStrings(meta["caveats"].([]any)), "No product-authorized weekly-to-daily distribution model") {
		t.Fatalf("per-sport caveats = %#v", meta["caveats"])
	}
}

func TestGetFitnessPerSportLoadTrendCaveatsAndDateGaps(t *testing.T) {
	t.Parallel()

	client := newFakeFitnessMetricsClient(t)
	client.summaries = decodeSummaries(t, `[
		{"date":"2026-05-01","fitness":70,"fatigue":78,"form":-8,"training_load":50,"byCategory":[]},
		{"date":"2026-05-03","fitness":72,"fatigue":80,"form":-8,"training_load":60,"byCategory":[{"category":"Run"}]}
	]`)
	tool := newGetFitnessTool(client, client, "test", "UTC", false)

	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"start_date":"2026-05-01","end_date":"2026-05-03","include_per_sport_load_trends":true}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	got := resultMap(t, result)
	if _, ok := got["per_sport_load_trends"]; ok {
		t.Fatalf("response invented per-sport daily trend rows: %#v", got["per_sport_load_trends"])
	}
	meta := got["_meta"].(map[string]any)["per_sport_load_trends"].(map[string]any)
	caveats := joinedStrings(meta["caveats"].([]any))
	for _, want := range []string{"weekly bucket", "No product-authorized weekly-to-daily distribution model"} {
		if !strings.Contains(caveats, want) {
			t.Fatalf("caveats %q missing %q", caveats, want)
		}
	}
}

func TestGetFitnessPreservesTRIMPLoadAndOmitsMissingFitnessFields(t *testing.T) {
	t.Parallel()

	client := newFakeFitnessMetricsClient(t)
	client.summaries = decodeSummaries(t, `[
		{"date":"2026-05-01","trimp":42,"byCategory":[{"category":"Run","trimp":42}]},
		{"date":"2026-05-02","fitness":0,"fatigue":0,"form":0,"training_load":0,"byCategory":[{"category":"Run","training_load":0}]}
	]`)
	tool := newGetFitnessTool(client, client, "test", "UTC", false)

	result, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"start_date":"2026-05-01","end_date":"2026-05-02","include_per_sport_load_trends":true}`)})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	got := resultMap(t, result)
	rows := got["fitness"].([]any)
	first := rows[0].(map[string]any)
	if _, ok := first["ctl"]; ok {
		t.Fatalf("first fitness row = %#v, want missing CTL omitted", first)
	}
	second := rows[1].(map[string]any)
	if second["ctl"] != float64(0) || second["atl"] != float64(0) || second["tsb"] != float64(0) {
		t.Fatalf("second fitness row = %#v, want explicit zero fitness values preserved", second)
	}
	meta := got["_meta"].(map[string]any)
	diagnostics := meta["load_diagnostics"].([]any)
	if !diagnosticReasonsContain(diagnostics, "trimp_or_hr_load_available") || !diagnosticReasonsContain(diagnostics, "fitness_fields_missing") {
		t.Fatalf("load_diagnostics = %#v, want TRIMP and missing-fitness diagnostics", diagnostics)
	}
	trendMeta := meta["per_sport_load_trends"].(map[string]any)
	if trendMeta["status"] != "unavailable" || trendMeta["reason"] != "weekly_summary_cannot_be_distributed_daily" {
		t.Fatalf("per-sport metadata = %#v, want unavailable weekly summary", trendMeta)
	}
}

func joinedStrings(values []any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, value.(string))
	}
	return strings.Join(parts, "\n")
}

func diagnosticReasonsContain(values []any, want string) bool {
	for _, value := range values {
		row := value.(map[string]any)
		if row["reason"] == want {
			return true
		}
	}
	return false
}
