package tools

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/ricardocabral/icuvisor/internal/intervals"
)

const (
	getFitnessName        = "get_fitness"
	getFitnessDescription = "Get upstream weekly CTL, ATL, and TSB buckets by inclusive athlete-local anchor date; exact partial-week values cannot be derived. Optional per-sport trends are reported unavailable because weekly category load cannot be distributed across days without an authorized model."
	fetchFitnessMessage   = "could not fetch fitness data; check intervals.icu credentials, athlete ID, and date range"

	perSportLoadTrendWarmupDays = 84
)

type getFitnessRequest struct {
	StartDate                 string `json:"start_date"`
	EndDate                   string `json:"end_date"`
	IncludeFull               bool   `json:"include_full,omitempty"`
	IncludePerSportLoadTrends bool   `json:"include_per_sport_load_trends,omitempty"`
}

type fitnessResponse struct {
	Rows []fitnessRow `json:"fitness"`
	Meta fitnessMeta  `json:"_meta"`
}

type fitnessRow struct {
	Date string         `json:"date"`
	CTL  *float64       `json:"ctl,omitempty"`
	ATL  *float64       `json:"atl,omitempty"`
	TSB  *float64       `json:"tsb,omitempty"`
	Full map[string]any `json:"full,omitempty"`
}

type fitnessMeta struct {
	ServerVersion      string                       `json:"server_version"`
	StartDate          string                       `json:"start_date"`
	EndDate            string                       `json:"end_date"`
	Timezone           string                       `json:"timezone"`
	Count              int                          `json:"count"`
	IncludeFull        bool                         `json:"include_full"`
	LoadDiagnostics    []dataAvailabilityDiagnostic `json:"load_diagnostics,omitempty"`
	SummaryGrain       string                       `json:"summary_grain"`
	WindowPolicy       string                       `json:"window_policy"`
	Caveats            []string                     `json:"caveats"`
	PerSportLoadTrends *perSportLoadTrendMeta       `json:"per_sport_load_trends,omitempty"`
}

type perSportLoadTrendMeta struct {
	Status                 string   `json:"status,omitempty"`
	Reason                 string   `json:"reason,omitempty"`
	SourceEndpoint         string   `json:"source_endpoint"`
	SourceField            string   `json:"source_field"`
	SummaryGrain           string   `json:"summary_grain"`
	WindowPolicy           string   `json:"window_policy"`
	WarmupDaysRequested    int      `json:"warmup_days_requested"`
	WarmupAnchorsAvailable int      `json:"warmup_summary_anchors_available"`
	Caveats                []string `json:"caveats"`
}

func newGetFitnessTool(client FitnessClient, profileClient ProfileClient, version string, timezoneFallback string, debugMetadata bool, shaping ...responseShaping) Tool {
	shapeCfg := responseShapingOrDefault(shaping)
	return coreTool(Tool{Name: getFitnessName, Description: getFitnessDescription, InputSchema: getFitnessInputSchema(), OutputSchema: genericOutputSchema("Weekly-anchor fitness rows with CTL, ATL, and TSB plus partial-week caveats; unsupported per-sport daily trends are omitted."), Handler: getFitnessHandler(client, profileClient, version, timezoneFallback, debugMetadata, shapeCfg)})
}

func getFitnessHandler(client FitnessClient, profileClient ProfileClient, version string, timezoneFallback string, debugMetadata bool, shapeCfg responseShaping) Handler {
	return func(ctx context.Context, req Request) (Result, error) {
		args, err := decodeGetFitnessRequest(req.Arguments)
		if err != nil {
			return Result{}, NewUserError(invalidFitnessArgumentsMessage, err)
		}
		unitSystem, timezone, err := toolProfile(ctx, profileClient, timezoneFallback)
		if err != nil {
			return Result{}, NewUserError(fetchFitnessMessage, err)
		}
		fetchStart := args.StartDate
		if args.IncludePerSportLoadTrends {
			fetchStart = perSportLoadTrendLookbackStart(args.StartDate)
		}
		rows, err := client.ListAthleteSummary(ctx, intervals.AthleteSummaryParams{Start: fetchStart, End: args.EndDate})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return Result{}, err
			}
			return Result{}, NewUserError(fetchFitnessMessage, err)
		}
		requestedRows := filterFitnessRowsByDate(rows, args.StartDate, args.EndDate)
		payload := fitnessResponse{Rows: shapeFitnessRows(requestedRows, args.IncludeFull), Meta: fitnessMeta{ServerVersion: normalizeVersion(version), StartDate: args.StartDate, EndDate: args.EndDate, Timezone: timezone, Count: len(requestedRows), IncludeFull: args.IncludeFull, LoadDiagnostics: loadDiagnostics(requestedRows), SummaryGrain: athleteSummaryGrain, WindowPolicy: athleteSummaryWindowPolicy, Caveats: []string{athleteSummaryPartialWeekNote}}}
		if args.IncludePerSportLoadTrends {
			meta := unavailablePerSportLoadTrendMeta(rows, args.StartDate)
			payload.Meta.PerSportLoadTrends = &meta
		}
		return encodeShaped(payload, args.IncludeFull, []string{"fitness"}, version, debugMetadata, getFitnessName, unitSystem, shapeCfg)
	}
}

func unavailablePerSportLoadTrendMeta(rows []intervals.SummaryWithCats, startDate string) perSportLoadTrendMeta {
	warmupAnchors := map[string]bool{}
	for _, row := range rows {
		if validDate(row.Date) && row.Date < startDate {
			warmupAnchors[row.Date] = true
		}
	}
	return perSportLoadTrendMeta{
		Status:                 "unavailable",
		Reason:                 "weekly_summary_cannot_be_distributed_daily",
		SourceEndpoint:         "athlete-summary.json",
		SourceField:            "byCategory[].training_load",
		SummaryGrain:           athleteSummaryGrain,
		WindowPolicy:           athleteSummaryWindowPolicy,
		WarmupDaysRequested:    perSportLoadTrendWarmupDays,
		WarmupAnchorsAvailable: len(warmupAnchors),
		Caveats: []string{
			athleteSummaryPartialWeekNote,
			"upstream byCategory training_load is a weekly bucket total, not a daily load series",
			"No product-authorized weekly-to-daily distribution model is available; per-sport CTL/ATL/TSB estimates are omitted.",
		},
	}
}

func decodeGetFitnessRequest(raw json.RawMessage) (getFitnessRequest, error) {
	var args getFitnessRequest
	if strings.TrimSpace(string(raw)) == "" {
		return args, errors.New("arguments must be a JSON object")
	}
	decoded, err := DecodeStrict[getFitnessRequest](raw)
	if err != nil {
		return args, err
	}
	args = decoded
	args.StartDate = strings.TrimSpace(args.StartDate)
	args.EndDate = strings.TrimSpace(args.EndDate)
	if !validDate(args.StartDate) || !validDate(args.EndDate) {
		return args, errors.New("start_date and end_date must be YYYY-MM-DD")
	}
	if args.EndDate < args.StartDate {
		return args, errors.New("end_date must be on or after start_date")
	}
	return args, nil
}

func getFitnessInputSchema() map[string]any {
	schema := athleteSummaryDateRangeInputSchema()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return schema
	}
	properties["include_per_sport_load_trends"] = map[string]any{"type": "boolean", "default": false, "description": "When true, fetch an 84-day weekly-summary lookback and return explicit unavailable metadata for per-sport CTL/ATL/TSB; weekly category load is not distributed into invented daily samples."}
	return schema
}

func shapeFitnessRows(rows []intervals.SummaryWithCats, includeFull bool) []fitnessRow {
	out := make([]fitnessRow, 0, len(rows))
	for _, row := range rows {
		ctl, atl, tsb := rawFloatPtr(row.Raw, "fitness", row.Fitness), rawFloatPtr(row.Raw, "fatigue", row.Fatigue), rawFloatPtr(row.Raw, "form", row.Form)
		shaped := fitnessRow{Date: row.Date, CTL: ctl, ATL: atl, TSB: tsb}
		if includeFull {
			shaped.Full = row.Raw
		}
		out = append(out, shaped)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func filterFitnessRowsByDate(rows []intervals.SummaryWithCats, startDate string, endDate string) []intervals.SummaryWithCats {
	out := make([]intervals.SummaryWithCats, 0, len(rows))
	for _, row := range rows {
		if row.Date >= startDate && row.Date <= endDate {
			out = append(out, row)
		}
	}
	return out
}

func perSportLoadTrendLookbackStart(startDate string) string {
	start, _ := time.Parse(time.DateOnly, startDate)
	return start.AddDate(0, 0, -perSportLoadTrendWarmupDays).Format(time.DateOnly)
}
