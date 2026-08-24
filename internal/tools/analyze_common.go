package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ricardocabral/icuvisor/internal/analysis"
	"github.com/ricardocabral/icuvisor/internal/intervals"
	"github.com/ricardocabral/icuvisor/internal/response"
)

const (
	maxAnalyzerWindowDays = 366
)

type analyzerWindowRequest struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

func (w analyzerWindowRequest) analysisWindow() analysis.Window {
	return analysis.Window{StartDate: strings.TrimSpace(w.StartDate), EndDate: strings.TrimSpace(w.EndDate)}
}

type analyzerClients struct {
	fitness          FitnessClient
	wellness         WellnessClient
	activities       ActivitiesClient
	customFields     ActivityCustomFieldClient
	customFieldCache *customFieldCache
}

func newAnalyzerClients(fitness FitnessClient, wellness WellnessClient, activities ActivitiesClient) analyzerClients {
	customFieldClient, _ := activities.(ActivityCustomFieldClient)
	return analyzerClients{fitness: fitness, wellness: wellness, activities: activities, customFields: customFieldClient, customFieldCache: newCustomFieldCache()}
}

func decodeAnalyzerStrict[T any](raw json.RawMessage) (T, error) {
	var zero T
	if strings.TrimSpace(string(raw)) == "" {
		return zero, errors.New("arguments must be a JSON object")
	}
	return DecodeStrict[T](raw)
}

// analyzerFetchUserError keeps unsupported-metric explanations visible instead
// of collapsing them into the generic fetch-failure guess list.
func analyzerFetchUserError(genericMessage string, err error) error {
	var unsupported *unsupportedAnalyzerMetricError
	if errors.As(err, &unsupported) {
		return NewUserError(unsupported.Error(), err)
	}
	return NewUserError(genericMessage, err)
}

func loadAnalyzerSeries(ctx context.Context, clients analyzerClients, metric analysis.Metric, window analysis.ParsedWindow, grain analysis.SampleGrain, sport string, unitSystem response.UnitSystem, customFieldCodes []string, allowWeekly bool) (analyzerSampleSeries, error) {
	selection, err := selectAnalyzerMetricSource(metric, grain, allowWeekly)
	if err != nil {
		return analyzerSampleSeries{}, err
	}
	series := analyzerSampleSeries{Metric: metric, Unit: selection.Source.UnitLabel, ScaleLabel: selection.Source.ScaleLabel, SourceTools: []string{selection.Source.Tool}, Assumptions: map[string]any{"sample_grain": string(grain), "unit": selection.Source.UnitLabel}}
	if selection.Source.ScaleLabel != "" {
		series.Assumptions["scale_label"] = selection.Source.ScaleLabel
	}
	switch selection.Source.Family {
	case analysis.SourceFitnessWeekly, analysis.SourceTrainingSummary:
		if clients.fitness == nil {
			return series, errors.New("missing fitness client")
		}
		rows, err := clients.fitness.ListAthleteSummary(ctx, intervals.AthleteSummaryParams{Start: window.StartDate, End: window.EndDate})
		if err != nil {
			return series, err
		}
		var missingAnchors int
		series.Samples, missingAnchors = weeklySummarySamples(rows, metric, window, unitSystem)
		series.MissingDays = 0
		series.Assumptions["sample_grain"] = string(analysis.SampleGrainWeekly)
		series.Assumptions["aggregation"] = "upstream_weekly_anchor_value"
		series.Assumptions["expected_weekly_anchors"] = len(expectedWeeklyAnchorDates(window))
		series.Assumptions["missing_weekly_anchors"] = missingAnchors
		series.Assumptions["missing_days_applicable"] = false
	case analysis.SourceWellnessDaily:
		if clients.wellness == nil {
			return series, errors.New("missing wellness client")
		}
		rows, err := clients.wellness.ListWellness(ctx, intervals.WellnessParams{Oldest: window.StartDate, Newest: window.EndDate, Fields: analyzerWellnessFields(metric, selection.Source.Field)})
		if err != nil {
			return series, err
		}
		seen := map[string]bool{}
		freshness := newWellnessFreshnessTracker(metric, window.StartDate, window.EndDate)
		for _, row := range rows {
			date := strings.TrimSpace(stringValue(row.ID))
			if value, ok := wellnessMetricValue(row, metric); ok && date != "" {
				series.Samples = append(series.Samples, analysis.NumericSample{Key: date, Date: date, Value: value})
				seen[date] = true
				freshness.record(row, date)
			}
		}
		series.Samples = sortedSamples(series.Samples)
		series.MissingDays = analysis.MissingSamples(window.Days, len(seen))
		series.WellnessFreshness = freshness.trendSummary(series.MissingDays)
		series.Assumptions["aggregation"] = "native_daily"
	case analysis.SourceActivityRow:
		if clients.activities == nil {
			return series, errors.New("missing activities client")
		}
		rows, err := loadAllAnalyzerActivities(ctx, clients.activities, window.StartDate, window.EndDate, customFieldCodes)
		if err != nil {
			return series, err
		}
		rows = filterAnalyzerSport(rows, sport)
		if grain == analysis.SampleGrainActivity {
			for _, row := range rows {
				if value, ok := activityMetricValue(row, metric, unitSystem); ok {
					date := localActivityDate(row)
					series.Samples = append(series.Samples, analysis.NumericSample{Key: row.ID, Date: date, ActivityID: row.ID, Value: value})
				}
			}
			series.MissingDays = 0
			series.Assumptions["missing_days_applicable"] = false
			series.Assumptions["sample_grain"] = string(analysis.SampleGrainActivity)
			series.Samples = sortedSamples(series.Samples)
			return series, nil
		}
		byDate := groupActivitiesByLocalDate(rows)
		for date, dateRows := range byDate {
			if sample, ok, assumptions := aggregateActivityDay(date, dateRows, metric, unitSystem); ok {
				series.Samples = append(series.Samples, sample)
				for key, value := range assumptions {
					series.Assumptions[key] = value
				}
			}
		}
		series.Samples = sortedSamples(series.Samples)
		series.MissingDays = analysis.MissingSamples(window.Days, len(series.Samples))
	}
	return series, nil
}

func filterAnalyzerSport(rows []intervals.Activity, sport string) []intervals.Activity {
	trimmed := strings.ToLower(strings.TrimSpace(sport))
	if trimmed == "" {
		return rows
	}
	out := make([]intervals.Activity, 0, len(rows))
	for _, row := range rows {
		if strings.ToLower(strings.TrimSpace(stringValue(row.Type))) == trimmed || strings.ToLower(strings.TrimSpace(stringValue(row.SubType))) == trimmed {
			out = append(out, row)
		}
	}
	return out
}

func sampleMapByDate(samples []analysis.NumericSample) map[string]analysis.NumericSample {
	out := map[string]analysis.NumericSample{}
	for _, sample := range samples {
		out[sample.Date] = sample
	}
	return out
}

func pairDailySamples(xSeries analyzerSampleSeries, ySeries analyzerSampleSeries, window analysis.ParsedWindow, lagDays int) []analysis.PairedSample {
	yByDate := sampleMapByDate(ySeries.Samples)
	pairs := []analysis.PairedSample{}
	for _, x := range xSeries.Samples {
		date, err := time.Parse(time.DateOnly, x.Date)
		if err != nil || date.Before(window.Start) || date.After(window.End) {
			continue
		}
		yDate := date.AddDate(0, 0, lagDays).Format(time.DateOnly)
		if y, ok := yByDate[yDate]; ok {
			pairs = append(pairs, analysis.PairedSample{Key: x.Date, Date: x.Date, X: x.Value, Y: y.Value})
		}
	}
	return pairs
}

func pairActivitySamples(xSeries analyzerSampleSeries, ySeries analyzerSampleSeries) []analysis.PairedSample {
	yByID := map[string]analysis.NumericSample{}
	for _, y := range ySeries.Samples {
		yByID[y.ActivityID] = y
	}
	pairs := []analysis.PairedSample{}
	for _, x := range xSeries.Samples {
		if y, ok := yByID[x.ActivityID]; ok {
			pairs = append(pairs, analysis.PairedSample{Key: x.ActivityID, Date: x.Date, X: x.Value, Y: y.Value})
		}
	}
	return pairs
}

func shiftedLookupWindow(window analysis.ParsedWindow, lagDays int) analysis.Window {
	if lagDays > 0 {
		return analysis.Window{StartDate: window.StartDate, EndDate: window.End.AddDate(0, 0, lagDays).Format(time.DateOnly)}
	}
	if lagDays < 0 {
		return analysis.Window{StartDate: window.Start.AddDate(0, 0, lagDays).Format(time.DateOnly), EndDate: window.EndDate}
	}
	return window.Window
}

func weeklySummarySamples(rows []intervals.SummaryWithCats, metric analysis.Metric, window analysis.ParsedWindow, unitSystem response.UnitSystem) ([]analysis.NumericSample, int) {
	expected := expectedWeeklyAnchorDates(window)
	anchorIndex := make(map[string]int, len(expected))
	for index, date := range expected {
		anchorIndex[date] = index
	}
	byDate := map[string]analysis.NumericSample{}
	for _, row := range rows {
		bucket, ok := anchorIndex[row.Date]
		if !ok {
			continue
		}
		if value, ok := summaryMetricValue(row, metric, unitSystem); ok {
			byDate[row.Date] = analysis.NumericSample{Key: row.Date, Date: row.Date, Bucket: bucket, Value: value}
		}
	}
	samples := make([]analysis.NumericSample, 0, len(byDate))
	for _, date := range expected {
		if sample, ok := byDate[date]; ok {
			samples = append(samples, sample)
		}
	}
	return samples, len(expected) - len(samples)
}

func expectedWeeklyAnchorDates(window analysis.ParsedWindow) []string {
	first := window.Start
	for first.Weekday() != time.Monday {
		first = first.AddDate(0, 0, 1)
	}
	anchors := []string{}
	for date := first; !date.After(window.End); date = date.AddDate(0, 0, 7) {
		anchors = append(anchors, date.Format(time.DateOnly))
	}
	return anchors
}

func analyzerMetaAssumptions(base map[string]any, window analysis.Window, includeFull bool) map[string]any {
	out := map[string]any{"window": window, "include_full": includeFull}
	for key, value := range base {
		out[key] = value
	}
	return out
}

func mergeSourceTools(series ...analyzerSampleSeries) []string {
	count := 0
	for _, item := range series {
		count += len(item.SourceTools)
	}
	tools := make([]string, 0, count)
	for _, item := range series {
		tools = append(tools, item.SourceTools...)
	}
	return analysis.NormalizeSourceTools(tools)
}

func parseMetricArgument(value string) (analysis.Metric, error) {
	metric, err := analysis.ParseMetric(value)
	if err != nil {
		return "", err
	}
	return metric, nil
}

func analyzerMetricProperty(description string) map[string]any {
	property := analysis.MetricSchemaProperty()
	if description != "" {
		property["description"] = description + " " + fmt.Sprint(property["description"])
	}
	return property
}

func sortedFloatCopy(values []float64) []float64 {
	out := append([]float64(nil), values...)
	sort.Float64s(out)
	return out
}
