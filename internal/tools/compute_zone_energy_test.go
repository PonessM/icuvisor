package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ricardocabral/icuvisor/internal/intervals"
)

type zoneEnergyTestClient struct {
	activities []intervals.Activity
	profile    intervals.AthleteWithSportSettings
	streams    map[string][]intervals.ActivityStream
	streamErrs map[string]error
	listErr    error
	profileErr error
	listParams intervals.ListActivitiesParams
	streamIDs  []string
}

func (c *zoneEnergyTestClient) ListActivities(_ context.Context, params intervals.ListActivitiesParams) ([]intervals.Activity, error) {
	c.listParams = params
	return append([]intervals.Activity(nil), c.activities...), c.listErr
}

func (c *zoneEnergyTestClient) GetAthleteProfile(context.Context) (intervals.AthleteWithSportSettings, error) {
	return c.profile, c.profileErr
}

func (c *zoneEnergyTestClient) GetActivityStreams(_ context.Context, params intervals.ActivityStreamsParams) ([]intervals.ActivityStream, error) {
	c.streamIDs = append(c.streamIDs, params.ActivityID)
	if err := c.streamErrs[params.ActivityID]; err != nil {
		return nil, err
	}
	return c.streams[params.ActivityID], nil
}

func TestComputeZoneEnergyRegistrationAndSchema(t *testing.T) {
	t.Parallel()

	tool := newComputeZoneEnergyTool(nil, nil, nil, "test", "UTC", false)
	if tool.Name != computeZoneEnergyName || tool.Requirement.effective() != RequirementRead || tool.EffectiveToolset().String() != "full" {
		t.Fatalf("tool registration = %#v, want full/read compute_zone_energy", tool)
	}
	properties := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"start_date", "end_date", "sport", "include_full"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("input schema missing %s: %#v", name, tool.InputSchema)
		}
	}
	if _, ok := properties["athlete_id"]; ok {
		t.Fatalf("base input schema unexpectedly contains coach-injected athlete_id: %#v", tool.InputSchema)
	}
	if !strings.Contains(tool.Description, "do not fetch get_activity_streams") || !strings.Contains(tool.Description, "Mechanical work is not metabolic energy or calories") {
		t.Fatalf("description = %q, want activation, stream-avoidance, and interpretation hints", tool.Description)
	}
}

func TestComputeZoneEnergyUsesNormalizedPowerZones(t *testing.T) {
	t.Parallel()

	client := &zoneEnergyTestClient{
		activities: []intervals.Activity{zoneEnergyActivityFixture(t, `{"id":"a1","type":"Ride","start_date_local":"2026-01-01T08:00:00","stream_types":["watts","time"]}`)},
		profile: intervals.AthleteWithSportSettings{SportSettings: []intervals.SportSettings{{
			ID:                               7,
			Type:                             "Ride",
			FTP:                              228,
			PowerZoneUpperBoundsPercentOfFTP: []int{55, 75, 90, 105, 120, 150, 999},
			PowerZoneNames:                   []string{"Active Recovery", "Endurance", "Tempo", "Threshold", "VO2 Max", "Anaerobic", "Neuromuscular"},
		}}},
		streams: map[string][]intervals.ActivityStream{
			"a1": zoneEnergyStreamFixtures(t,
				`{"type":"watts","data":[0,125.4,200,2277.72,2277.72]}`,
				`{"type":"time","data":[0,10,20,30,40]}`,
			),
		},
		streamErrs: map[string]error{},
	}

	payload, err := collectZoneEnergy(context.Background(), computeZoneEnergyRequest{StartDate: "2026-01-01", EndDate: "2026-01-01"}, client.activities, client.profile, client)
	if err != nil {
		t.Fatalf("collectZoneEnergy() error = %v", err)
	}
	if payload.Result.TotalSeconds != 40 || payload.Result.TotalKJ != 26.031 {
		t.Fatalf("totals = (%v s, %v kJ), want (40 s, 26.031 kJ)", payload.Result.TotalSeconds, payload.Result.TotalKJ)
	}
	want := map[string]struct{ seconds, kj float64 }{
		"Active Recovery":     {seconds: 10, kj: 0},
		"Endurance":           {seconds: 10, kj: 1.254},
		"Tempo":               {seconds: 10, kj: 2},
		"Above Neuromuscular": {seconds: 10, kj: 22.777},
	}
	for _, zone := range payload.Result.Zones {
		if expected, ok := want[zone.Name]; ok {
			if zone.Seconds != expected.seconds || zone.KJ != expected.kj {
				t.Fatalf("zone %q = %#v, want %v s and %v kJ", zone.Name, zone, expected.seconds, expected.kj)
			}
			delete(want, zone.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing normalized zone rows: %#v", want)
	}
}

func TestComputeZoneEnergySkipsInvalidPowerConfigurationBeforeStreams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setting  intervals.SportSettings
		wantCode intervals.PowerZoneValidationCode
	}{
		{name: "missing zones", setting: intervals.SportSettings{ID: 7, Type: "Ride", FTP: 228}, wantCode: intervals.PowerZoneMissingZones},
		{name: "missing FTP", setting: intervals.SportSettings{ID: 7, Type: "Ride", PowerZoneUpperBoundsPercentOfFTP: []int{55}, PowerZoneNames: []string{"Active Recovery"}}, wantCode: intervals.PowerZoneMissingFTP},
		{name: "invalid ceilings", setting: intervals.SportSettings{ID: 7, Type: "Ride", FTP: 228, PowerZoneUpperBoundsPercentOfFTP: []int{55, 55}, PowerZoneNames: []string{"Active Recovery", "Endurance"}}, wantCode: intervals.PowerZoneInvalidCeilings},
		{name: "mismatched names", setting: intervals.SportSettings{ID: 7, Type: "Ride", FTP: 228, PowerZoneUpperBoundsPercentOfFTP: []int{55, 75}, PowerZoneNames: []string{"Active Recovery"}}, wantCode: intervals.PowerZoneMismatchedNames},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &zoneEnergyTestClient{
				activities: []intervals.Activity{zoneEnergyActivityFixture(t, `{"id":"a1","type":"Ride","start_date_local":"2026-01-01T08:00:00","stream_types":["watts","time"]}`)},
				profile:    intervals.AthleteWithSportSettings{SportSettings: []intervals.SportSettings{tc.setting}},
				streams:    map[string][]intervals.ActivityStream{},
				streamErrs: map[string]error{},
			}
			payload, err := collectZoneEnergy(context.Background(), computeZoneEnergyRequest{StartDate: "2026-01-01", EndDate: "2026-01-01"}, client.activities, client.profile, client)
			if err != nil {
				t.Fatalf("collectZoneEnergy() error = %v", err)
			}
			if payload.Result.InsufficientReason != string(tc.wantCode) || payload.Series[0].Reason != string(tc.wantCode) {
				t.Fatalf("power configuration result = %#v, want stable code %q", payload, tc.wantCode)
			}
			if len(client.streamIDs) != 0 {
				t.Fatalf("GetActivityStreams called for invalid configuration: %#v", client.streamIDs)
			}
		})
	}
}

func TestComputeZoneEnergyAggregatesDistinctPowerConfigurationCodes(t *testing.T) {
	t.Parallel()

	client := &zoneEnergyTestClient{
		activities: []intervals.Activity{
			zoneEnergyActivityFixture(t, `{"id":"missing-ftp","type":"Ride","start_date_local":"2026-01-01T08:00:00","stream_types":["watts","time"]}`),
			zoneEnergyActivityFixture(t, `{"id":"bad-ceilings","type":"Run","start_date_local":"2026-01-02T08:00:00","stream_types":["watts","time"]}`),
		},
		profile: intervals.AthleteWithSportSettings{SportSettings: []intervals.SportSettings{
			{ID: 7, Type: "Ride", PowerZoneUpperBoundsPercentOfFTP: []int{55}, PowerZoneNames: []string{"Active Recovery"}},
			{ID: 8, Type: "Run", FTP: 228, PowerZoneUpperBoundsPercentOfFTP: []int{55, 55}, PowerZoneNames: []string{"Active Recovery", "Endurance"}},
		}},
		streams:    map[string][]intervals.ActivityStream{},
		streamErrs: map[string]error{},
	}

	payload, err := collectZoneEnergy(context.Background(), computeZoneEnergyRequest{StartDate: "2026-01-01", EndDate: "2026-01-02"}, client.activities, client.profile, client)
	if err != nil {
		t.Fatalf("collectZoneEnergy() error = %v", err)
	}
	if payload.Result.InsufficientReason != "mixed_power_zone_config_errors" || len(client.streamIDs) != 0 {
		t.Fatalf("result = %#v, stream calls = %#v", payload.Result, client.streamIDs)
	}
}

func TestComputeZoneEnergyReportsPartialActivityCoverageOnlyInFullResponse(t *testing.T) {
	t.Parallel()

	client := &zoneEnergyTestClient{
		activities: []intervals.Activity{
			zoneEnergyActivityFixture(t, `{"id":"missing","type":"Ride","start_date_local":"2026-01-02T08:00:00","stream_types":["time"]}`),
			zoneEnergyActivityFixture(t, `{"id":"usable","type":"Ride","start_date_local":"2026-01-01T08:00:00","stream_types":["watts","time"]}`),
		},
		profile: intervals.AthleteWithSportSettings{PreferredUnits: "metric", Timezone: "UTC", SportSettings: []intervals.SportSettings{{ID: 7, Type: "Ride", FTP: 200, PowerZoneUpperBoundsPercentOfFTP: []int{100, 150}, PowerZoneNames: []string{"Easy", "Hard"}}}},
		streams: map[string][]intervals.ActivityStream{
			"usable": zoneEnergyStreamFixtures(t, `{"type":"watts","data":[100,200,200]}`, `{"type":"time","data":[0,10,20]}`),
		},
		streamErrs: map[string]error{},
	}
	tool := newComputeZoneEnergyTool(client, client, client, "test", "UTC", false)

	full, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"start_date":"2026-01-01","end_date":"2026-01-02","include_full":true}`)})
	if err != nil {
		t.Fatalf("full Handler() error = %v", err)
	}
	fullText := resultText(t, full)
	if strings.Contains(fullText, `"data"`) || strings.Contains(fullText, `"watts"`) {
		t.Fatalf("full response leaked raw streams: %s", fullText)
	}
	payload := resultMap(t, full)
	result := payload["result"].(map[string]any)
	if result["status"] != "partial" || result["activity_count"] != float64(2) || result["usable_activity_count"] != float64(1) || result["skipped_activity_count"] != float64(1) {
		t.Fatalf("result coverage = %#v", result)
	}
	series := payload["series"].([]any)
	if len(series) != 2 {
		t.Fatalf("series count = %d, want 2", len(series))
	}
	if first := series[0].(map[string]any); first["activity_id"] != "usable" || first["status"] != "usable" {
		t.Fatalf("first audit row = %#v", first)
	}
	if second := series[1].(map[string]any); second["activity_id"] != "missing" || second["status"] != "skipped" || second["reason"] != "required_streams_not_advertised" {
		t.Fatalf("second audit row = %#v", second)
	}
	meta := payload["_meta"].(map[string]any)
	coverage := meta["coverage"].(map[string]any)
	if coverage["fetched_candidate_count"] != float64(2) || coverage["retained_candidate_count"] != float64(2) || coverage["sport_matched_activity_count"] != float64(2) {
		t.Fatalf("coverage metadata = %#v", coverage)
	}

	terse, err := tool.Handler(context.Background(), Request{Name: tool.Name, Arguments: json.RawMessage(`{"start_date":"2026-01-01","end_date":"2026-01-02"}`)})
	if err != nil {
		t.Fatalf("terse Handler() error = %v", err)
	}
	terseText := resultText(t, terse)
	if strings.Contains(terseText, `"series"`) || strings.Contains(terseText, `"activity_id"`) || strings.Contains(terseText, "required_streams_not_advertised") {
		t.Fatalf("terse response leaked activity audit details: %s", terseText)
	}
	if client.listParams.Limit != maxZoneEnergyCandidates || client.listParams.Oldest != "2026-01-01" || client.listParams.Newest != "2026-01-02" {
		t.Fatalf("list params = %#v", client.listParams)
	}
}

func TestComputeZoneEnergyAggregatesDisplayedActivityValuesAndReconcilesShares(t *testing.T) {
	t.Parallel()

	client := &zoneEnergyTestClient{
		activities: []intervals.Activity{
			zoneEnergyActivityFixture(t, `{"id":"ride-2","type":"Ride","start_date_local":"2026-01-02T08:00:00","stream_types":["watts","time"]}`),
			zoneEnergyActivityFixture(t, `{"id":"run-1","type":"Run","start_date_local":"2026-01-03T08:00:00","stream_types":["watts","time"]}`),
			zoneEnergyActivityFixture(t, `{"id":"ride-1","type":"Ride","start_date_local":"2026-01-01T08:00:00","stream_types":["watts","time"]}`),
		},
		profile: intervals.AthleteWithSportSettings{SportSettings: []intervals.SportSettings{
			{ID: 7, Type: "Ride", FTP: 1000, PowerZoneUpperBoundsPercentOfFTP: []int{100}, PowerZoneNames: []string{"Ride work"}},
			{ID: 8, Type: "Run", FTP: 2000, PowerZoneUpperBoundsPercentOfFTP: []int{100}, PowerZoneNames: []string{"Run work"}},
		}},
		streams: map[string][]intervals.ActivityStream{
			"ride-1": zoneEnergyStreamFixtures(t, `{"type":"watts","data":[1000,1000]}`, `{"type":"time","data":[0,0.0006]}`),
			"ride-2": zoneEnergyStreamFixtures(t, `{"type":"watts","data":[1000,1000]}`, `{"type":"time","data":[0,0.0006]}`),
			"run-1":  zoneEnergyStreamFixtures(t, `{"type":"watts","data":[2000,2000]}`, `{"type":"time","data":[0,0.0006]}`),
		},
		streamErrs: map[string]error{},
	}

	payload, err := collectZoneEnergy(context.Background(), computeZoneEnergyRequest{StartDate: "2026-01-01", EndDate: "2026-01-03", IncludeFull: true}, client.activities, client.profile, client)
	if err != nil {
		t.Fatalf("collectZoneEnergy() error = %v", err)
	}
	if payload.Result.TotalSeconds != 0.003 || payload.Result.TotalKJ != 0.003 {
		t.Fatalf("headline totals = (%v s, %v kJ), want sums of displayed activity values (0.003, 0.003)", payload.Result.TotalSeconds, payload.Result.TotalKJ)
	}
	if len(payload.Result.Zones) != 4 {
		t.Fatalf("zone rows = %d, want each configured zone and overflow row", len(payload.Result.Zones))
	}
	rows := map[string]zoneEnergyRow{}
	for _, row := range payload.Result.Zones {
		rows[row.Name] = row
	}
	ride := rows["Above Ride work"]
	if ride.Sport != "Ride" || ride.Seconds != 0.002 || ride.KJ != 0.002 || ride.TimeShare != 0.6667 || ride.EnergyShare != 0.6667 {
		t.Fatalf("ride aggregate = %#v", ride)
	}
	run := rows["Above Run work"]
	if run.Sport != "Run" || run.Seconds != 0.001 || run.KJ != 0.001 || run.TimeShare != 0.3333 || run.EnergyShare != 0.3333 {
		t.Fatalf("run aggregate = %#v", run)
	}
	seriesSeconds, seriesKJ := 0.0, 0.0
	for _, row := range payload.Series {
		seriesSeconds = roundZoneEnergyValue(seriesSeconds+row.TotalSeconds, 3)
		seriesKJ = roundZoneEnergyValue(seriesKJ+row.TotalKJ, 3)
	}
	if seriesSeconds != payload.Result.TotalSeconds || seriesKJ != payload.Result.TotalKJ {
		t.Fatalf("audit sums = (%v, %v), headline = (%v, %v)", seriesSeconds, seriesKJ, payload.Result.TotalSeconds, payload.Result.TotalKJ)
	}
	if roundZoneEnergyValue(ride.TimeShare+run.TimeShare, 4) != 1 || roundZoneEnergyValue(ride.EnergyShare+run.EnergyShare, 4) != 1 {
		t.Fatalf("response-wide shares do not reconcile: ride=%#v run=%#v", ride, run)
	}
}

func TestComputeZoneEnergyStreamErrorsClassifiedAsCoverageOrOperational(t *testing.T) {
	t.Parallel()

	operational := []struct {
		name string
		err  error
	}{
		{name: "unauthorized", err: intervals.ErrUnauthorized},
		{name: "rate limited", err: intervals.ErrRateLimited},
		{name: "upstream", err: intervals.ErrUpstream},
		{name: "unknown decode", err: errors.New("decode failed")},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
	}
	for _, tc := range operational {
		t.Run(tc.name, func(t *testing.T) {
			client := zoneEnergySingleActivityClient(t)
			client.streamErrs["a1"] = tc.err
			_, err := collectZoneEnergy(context.Background(), computeZoneEnergyRequest{StartDate: "2026-01-01", EndDate: "2026-01-01"}, client.activities, client.profile, client)
			if !errors.Is(err, tc.err) {
				t.Fatalf("collectZoneEnergy() error = %v, want %v", err, tc.err)
			}
		})
	}

	client := zoneEnergySingleActivityClient(t)
	client.streamErrs["a1"] = intervals.ErrNotFound
	payload, err := collectZoneEnergy(context.Background(), computeZoneEnergyRequest{StartDate: "2026-01-01", EndDate: "2026-01-01"}, client.activities, client.profile, client)
	if err != nil {
		t.Fatalf("collectZoneEnergy(not found) error = %v", err)
	}
	if payload.Result.Status != "insufficient" || payload.Result.InsufficientReason != "no_usable_power_streams" || payload.Series[0].Reason != "streams_not_found" {
		t.Fatalf("not-found payload = %#v", payload)
	}
}

func zoneEnergySingleActivityClient(t *testing.T) *zoneEnergyTestClient {
	t.Helper()
	return &zoneEnergyTestClient{
		activities: []intervals.Activity{zoneEnergyActivityFixture(t, `{"id":"a1","type":"Ride","start_date_local":"2026-01-01T08:00:00","stream_types":["watts","time"]}`)},
		profile:    intervals.AthleteWithSportSettings{PreferredUnits: "metric", Timezone: "UTC", SportSettings: []intervals.SportSettings{{ID: 7, Type: "Ride", FTP: 200, PowerZoneUpperBoundsPercentOfFTP: []int{100, 150}, PowerZoneNames: []string{"Easy", "Hard"}}}},
		streams:    map[string][]intervals.ActivityStream{"a1": zoneEnergyStreamFixtures(t, `{"type":"watts","data":[100,200]}`, `{"type":"time","data":[0,10]}`)},
		streamErrs: map[string]error{},
	}
}

func zoneEnergyActivityFixture(t *testing.T, raw string) intervals.Activity {
	t.Helper()
	var activity intervals.Activity
	if err := json.Unmarshal([]byte(raw), &activity); err != nil {
		t.Fatalf("unmarshal activity fixture: %v", err)
	}
	return activity
}

func zoneEnergyStreamFixtures(t *testing.T, raws ...string) []intervals.ActivityStream {
	t.Helper()
	rows := make([]intervals.ActivityStream, 0, len(raws))
	for _, raw := range raws {
		var row intervals.ActivityStream
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatalf("unmarshal stream fixture: %v", err)
		}
		rows = append(rows, row)
	}
	return rows
}
