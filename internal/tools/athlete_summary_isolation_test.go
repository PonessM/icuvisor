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

func TestAthleteSummaryToolsCannotMixFollowedAthletes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tool   func(*intervals.Client, ProfileClient) Tool
		args   json.RawMessage
		assert func(*testing.T, map[string]any)
	}{
		{
			name: "get_fitness include_full",
			tool: func(client *intervals.Client, profile ProfileClient) Tool {
				return newGetFitnessTool(client, profile, "test", "UTC", false)
			},
			args: json.RawMessage(`{"start_date":"2026-05-01","end_date":"2026-05-01","include_full":true}`),
			assert: func(t *testing.T, got map[string]any) {
				t.Helper()
				rows := got["fitness"].([]any)
				if len(rows) != 1 || rows[0].(map[string]any)["ctl"] != float64(70) {
					t.Fatalf("fitness rows = %#v, want only target athlete", rows)
				}
			},
		},
		{
			name: "get_training_summary include_full",
			tool: func(client *intervals.Client, profile ProfileClient) Tool {
				return newGetTrainingSummaryTool(client, profile, "test", "UTC", false)
			},
			args: json.RawMessage(`{"start_date":"2026-05-01","end_date":"2026-05-01","include_full":true}`),
			assert: func(t *testing.T, got map[string]any) {
				t.Helper()
				summary := got["summary"].(map[string]any)
				if summary["count"] != float64(1) || summary["training_load"] != float64(50) {
					t.Fatalf("training summary = %#v, want only target athlete", summary)
				}
				if full := got["full"].([]any); len(full) != 1 {
					t.Fatalf("full rows = %#v, want only target athlete", full)
				}
			},
		},
		{
			name: "compute_zone_time",
			tool: func(client *intervals.Client, profile ProfileClient) Tool {
				return newComputeZoneTimeTool(client, nil, nil, profile, "test", "UTC", false)
			},
			args: json.RawMessage(`{"start_date":"2026-05-01","end_date":"2026-05-01","zone_metric":"power","include_full":true}`),
			assert: func(t *testing.T, got map[string]any) {
				t.Helper()
				result := got["result"].(map[string]any)
				if result["total_seconds"] != float64(175) {
					t.Fatalf("zone-time result = %#v, want only target athlete", result)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/athlete/i12345/athlete-summary.json" {
					t.Fatalf("path = %q, want athlete summary path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[
					{"athlete_id":"i99999","athlete_name":"Followed Rider","athlete_email":"followed@example.test","date":"2026-05-01","count":10,"training_load":200,"fitness":99,"fatigue":100,"form":-1,"timeInZones":[1000,500,250],"timeInZonesTot":1750},
					{"athlete_id":"i88888","Athlete_ID":"i12345","athlete_name":"Case Variant Rider","athlete_email":"variant@example.test","date":"2026-05-01","count":20,"training_load":300,"fitness":120,"fatigue":130,"form":-10,"timeInZones":[2000,1000,500],"timeInZonesTot":3500},
					{"athlete_id":"i12345","date":"2026-05-01","count":1,"training_load":50,"fitness":70,"fatigue":80,"form":-10,"timeInZones":[100,50,25],"timeInZonesTot":175}
				]`))
			}))
			defer server.Close()

			client, err := intervals.NewClient(intervals.Options{
				Config: config.Config{
					APIKey:      "test-key",
					AthleteID:   "i12345",
					APIBaseURL:  server.URL,
					HTTPTimeout: time.Second,
				},
				Version:    "test",
				HTTPClient: server.Client(),
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
			text := resultText(t, result)
			for _, secret := range []string{"i99999", "Followed Rider", "followed@example.test", "i88888", "Case Variant Rider", "variant@example.test"} {
				if strings.Contains(text, secret) {
					t.Fatalf("tool response leaked followed-athlete data %q: %s", secret, text)
				}
			}
			tc.assert(t, resultMap(t, result))
		})
	}
}
