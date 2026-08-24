package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ricardocabral/icuvisor/internal/analysis"
	"github.com/ricardocabral/icuvisor/internal/intervals"
	"github.com/ricardocabral/icuvisor/internal/response"
	"github.com/ricardocabral/icuvisor/internal/streams"
)

const (
	computeZoneEnergyName        = "compute_zone_energy"
	computeZoneEnergyDescription = "Use when the prompt asks for timestamp-weighted mechanical work or kJ by configured power zone across activities in a bounded date range; do not fetch get_activity_streams and reduce them in chat. Returns terse aggregate zone rows; include_full adds per-activity coverage diagnostics, never raw streams. Mechanical work is not metabolic energy or calories."
	maxZoneEnergyCandidates      = 201
	maxZoneEnergyActivities      = 200
	maxZoneEnergyRangeDays       = 366
)

type zoneEnergyActivitiesClient interface {
	ListActivities(context.Context, intervals.ListActivitiesParams) ([]intervals.Activity, error)
}

type computeZoneEnergyRequest struct {
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	Sport       string `json:"sport,omitempty"`
	IncludeFull bool   `json:"include_full,omitempty"`
}

type computeZoneEnergyPayload struct {
	Result computeZoneEnergyResult `json:"result"`
	Series []zoneEnergyActivityRow `json:"series,omitempty"`
	Meta   zoneEnergyMeta          `json:"_meta"`
}

type computeZoneEnergyResult struct {
	Status                      string          `json:"status"`
	InsufficientReason          string          `json:"insufficient_reason,omitempty"`
	StartDate                   string          `json:"start_date"`
	EndDate                     string          `json:"end_date"`
	Sport                       string          `json:"sport,omitempty"`
	ActivityCount               int             `json:"activity_count"`
	UsableActivityCount         int             `json:"usable_activity_count"`
	SkippedActivityCount        int             `json:"skipped_activity_count"`
	InvalidIntervalCount        int             `json:"invalid_interval_count"`
	TruncatedActivityCandidates bool            `json:"truncated_activity_candidates"`
	TotalSeconds                float64         `json:"total_seconds"`
	TotalKJ                     float64         `json:"total_kj"`
	Zones                       []zoneEnergyRow `json:"zones"`
	Interpretation              string          `json:"interpretation"`
}

type zoneEnergyRow struct {
	ZoneKey        string   `json:"zone_key"`
	SportSettingID int      `json:"sport_setting_id"`
	Sport          string   `json:"sport"`
	Zone           int      `json:"zone"`
	Name           string   `json:"name"`
	LowerWatts     float64  `json:"lower_watts"`
	UpperWatts     *float64 `json:"upper_watts,omitempty"`
	Seconds        float64  `json:"seconds"`
	KJ             float64  `json:"kj"`
	TimeShare      float64  `json:"time_share"`
	EnergyShare    float64  `json:"energy_share"`
}

type zoneEnergyActivityRow struct {
	ActivityID       string                         `json:"activity_id"`
	Date             string                         `json:"date"`
	Sport            string                         `json:"sport"`
	Status           string                         `json:"status"`
	Reason           string                         `json:"reason,omitempty"`
	SportSettingID   int                            `json:"sport_setting_id,omitempty"`
	TotalSeconds     float64                        `json:"total_seconds"`
	TotalKJ          float64                        `json:"total_kj"`
	UsableIntervals  int                            `json:"usable_intervals"`
	SkippedIntervals int                            `json:"skipped_intervals"`
	Diagnostics      analysis.ZoneEnergyDiagnostics `json:"diagnostics"`
}

type zoneEnergyMeta struct {
	analysis.AnalyzerMeta
	AnalysisUnits map[string]string      `json:"analysis_units"`
	ZoneSources   []zoneEnergyZoneSource `json:"zone_sources"`
	Coverage      zoneEnergyCoverage     `json:"coverage"`
}

type zoneEnergyZoneSource struct {
	ZoneKey         string    `json:"zone_key"`
	SportSettingID  int       `json:"sport_setting_id"`
	Sport           string    `json:"sport"`
	BoundariesWatts []float64 `json:"boundaries_watts"`
	Names           []string  `json:"names"`
}

type zoneEnergyCoverage struct {
	FetchedCandidateCount     int `json:"fetched_candidate_count"`
	RetainedCandidateCount    int `json:"retained_candidate_count"`
	SportMatchedActivityCount int `json:"sport_matched_activity_count"`
	UsableActivityCount       int `json:"usable_activity_count"`
	SkippedActivityCount      int `json:"skipped_activity_count"`
}

type zoneEnergyGroup struct {
	config  analysis.PowerZoneConfig
	zoneKey string
	rows    []zoneEnergyRow
}

func newComputeZoneEnergyTool(activitiesClient zoneEnergyActivitiesClient, streamsClient ActivityStreamsClient, profileClient ProfileClient, version string, timezoneFallback string, debugMetadata bool, shaping ...responseShaping) Tool {
	shapeCfg := responseShapingOrDefault(shaping)
	return fullTool(Tool{
		Name:         computeZoneEnergyName,
		Description:  computeZoneEnergyDescription,
		InputSchema:  computeZoneEnergyInputSchema(),
		OutputSchema: genericOutputSchema("Timestamp-weighted mechanical work in seconds and kJ by configured power zone, with analyzer metadata and optional per-activity audit rows."),
		Requirement:  RequirementRead,
		Handler:      computeZoneEnergyHandler(activitiesClient, streamsClient, profileClient, version, timezoneFallback, debugMetadata, shapeCfg),
	})
}

func computeZoneEnergyHandler(activitiesClient zoneEnergyActivitiesClient, streamsClient ActivityStreamsClient, profileClient ProfileClient, version string, timezoneFallback string, debugMetadata bool, shapeCfg responseShaping) Handler {
	return func(ctx context.Context, req Request) (Result, error) {
		args, err := decodeComputeZoneEnergyRequest(req.Arguments)
		if err != nil {
			return Result{}, NewUserError("invalid compute_zone_energy arguments; use an inclusive 1..366 day athlete-local range", err)
		}
		if activitiesClient == nil || streamsClient == nil || profileClient == nil {
			return Result{}, NewUserError("could not compute zone energy", errors.New("missing required read client"))
		}
		profile, unitSystem, _, err := toolProfileDetails(ctx, profileClient, timezoneFallback)
		if err != nil {
			return Result{}, NewUserError("could not compute zone energy; check athlete profile access", err)
		}
		activities, err := activitiesClient.ListActivities(ctx, intervals.ListActivitiesParams{Oldest: args.StartDate, Newest: args.EndDate, Limit: maxZoneEnergyCandidates})
		if err != nil {
			return Result{}, NewUserError("could not compute zone energy; check activity access and date range", err)
		}
		payload, err := collectZoneEnergy(ctx, args, activities, profile, streamsClient)
		if err != nil {
			return Result{}, NewUserError("could not compute zone energy; required activity streams were unavailable", err)
		}
		if !args.IncludeFull {
			payload.Series = nil
		}
		return encodeZoneEnergyResponse(payload, args.IncludeFull, version, debugMetadata, unitSystem, shapeCfg)
	}
}

func decodeComputeZoneEnergyRequest(raw json.RawMessage) (computeZoneEnergyRequest, error) {
	args, err := DecodeStrict[computeZoneEnergyRequest](raw)
	if err != nil {
		return args, err
	}
	args.StartDate = strings.TrimSpace(args.StartDate)
	args.EndDate = strings.TrimSpace(args.EndDate)
	args.Sport = strings.TrimSpace(args.Sport)
	start, err := time.Parse(time.DateOnly, args.StartDate)
	if err != nil {
		return args, errors.New("start_date must be YYYY-MM-DD")
	}
	end, err := time.Parse(time.DateOnly, args.EndDate)
	if err != nil {
		return args, errors.New("end_date must be YYYY-MM-DD")
	}
	days := int(end.Sub(start).Hours()/24) + 1
	if days < 1 || days > maxZoneEnergyRangeDays {
		return args, fmt.Errorf("inclusive date range must contain 1..%d days", maxZoneEnergyRangeDays)
	}
	return args, nil
}

func collectZoneEnergy(ctx context.Context, args computeZoneEnergyRequest, activities []intervals.Activity, profile intervals.AthleteWithSportSettings, streamsClient ActivityStreamsClient) (computeZoneEnergyPayload, error) {
	sort.SliceStable(activities, func(i, j int) bool {
		left := zoneEnergyActivitySortTime(activities[i])
		right := zoneEnergyActivitySortTime(activities[j])
		if left != right {
			return left < right
		}
		return activities[i].ID < activities[j].ID
	})
	fetchedCount := len(activities)
	truncated := fetchedCount >= maxZoneEnergyCandidates
	if len(activities) > maxZoneEnergyActivities {
		activities = activities[:maxZoneEnergyActivities]
	}

	groups := map[string]*zoneEnergyGroup{}
	series := make([]zoneEnergyActivityRow, 0, len(activities))
	reasons := make([]string, 0, len(activities))
	matched := 0
	usable := 0
	invalidIntervals := 0

	for _, activity := range activities {
		if !zoneEnergySportMatches(args.Sport, activity) {
			continue
		}
		matched++
		audit := zoneEnergyActivityRow{ActivityID: activity.ID, Date: zoneEnergyActivityDate(activity), Sport: zoneEnergyActivitySport(activity), Status: "skipped"}
		setting, matchedSport, ok := selectZoneEnergySportSetting(activity, profile.SportSettings)
		if !ok {
			audit.Reason = string(intervals.PowerZoneMissingZones)
			series = append(series, audit)
			reasons = append(reasons, audit.Reason)
			continue
		}
		config, powerZoneCode := zoneEnergyPowerZoneConfig(setting, matchedSport)
		audit.SportSettingID = config.SportSettingID
		if powerZoneCode != intervals.PowerZoneValid {
			audit.Reason = string(powerZoneCode)
			series = append(series, audit)
			reasons = append(reasons, audit.Reason)
			continue
		}
		if !zoneEnergyStreamsAdvertised(activity.StreamTypes) {
			audit.Reason = "required_streams_not_advertised"
			series = append(series, audit)
			reasons = append(reasons, audit.Reason)
			continue
		}

		streamRows, err := streamsClient.GetActivityStreams(ctx, intervals.ActivityStreamsParams{ActivityID: activity.ID, Types: []string{"watts", "time"}, IncludeDefaults: false})
		if err != nil {
			if errors.Is(err, intervals.ErrNotFound) {
				audit.Reason = "streams_not_found"
				series = append(series, audit)
				reasons = append(reasons, audit.Reason)
				continue
			}
			return computeZoneEnergyPayload{}, fmt.Errorf("fetching activity streams: %w", err)
		}
		streamData := canonicalActivityStreamData(streamRows)
		power, powerOK := streamData["watts"]
		timestamps, timeOK := streamData["time"]
		switch {
		case !powerOK || len(power) == 0:
			audit.Reason = "missing_power_stream"
		case !timeOK || len(timestamps) == 0:
			audit.Reason = "missing_time_stream"
		case len(power) != len(timestamps):
			audit.Reason = "misaligned_streams"
		case len(power) < 2:
			audit.Reason = "insufficient_stream_samples"
		}
		if audit.Reason != "" {
			series = append(series, audit)
			reasons = append(reasons, audit.Reason)
			continue
		}

		calculated, err := analysis.ComputeZoneEnergy(analysis.ZoneEnergyInput{TimestampsSeconds: timestamps, PowerWatts: power, ZoneConfig: config})
		if err != nil {
			return computeZoneEnergyPayload{}, fmt.Errorf("computing activity zone energy: %w", err)
		}
		audit.TotalSeconds = calculated.TotalSeconds
		audit.TotalKJ = calculated.TotalKJ
		audit.UsableIntervals = calculated.Diagnostics.UsableIntervals
		audit.SkippedIntervals = calculated.Diagnostics.SkippedIntervals
		audit.Diagnostics = calculated.Diagnostics
		invalidIntervals += calculated.Diagnostics.SkippedIntervals
		if calculated.Diagnostics.UsableIntervals == 0 {
			audit.Reason = "no_usable_intervals"
			series = append(series, audit)
			reasons = append(reasons, audit.Reason)
			continue
		}
		audit.Status = "usable"
		if calculated.Diagnostics.SkippedIntervals > 0 {
			audit.Status = "partial"
			audit.Reason = "invalid_intervals_skipped"
		}
		usable++
		series = append(series, audit)

		zoneKey := zoneEnergyConfigKey(config)
		group := groups[zoneKey]
		if group == nil {
			group = &zoneEnergyGroup{config: config, zoneKey: zoneKey, rows: zoneEnergyRows(zoneKey, config, calculated.Zones)}
			groups[zoneKey] = group
		}
		for i := range calculated.Zones {
			group.rows[i].Seconds = roundZoneEnergyValue(group.rows[i].Seconds+calculated.Zones[i].Seconds, 3)
			group.rows[i].KJ = roundZoneEnergyValue(group.rows[i].KJ+calculated.Zones[i].KJ, 3)
		}
	}

	zones, sources := orderedZoneEnergyGroups(groups)
	totalSeconds, totalKJ := finalizeZoneEnergyRows(zones)
	skipped := matched - usable
	status := "ok"
	insufficientReason := ""
	if usable == 0 {
		status = "insufficient"
		insufficientReason = zoneEnergyInsufficientReason(matched, reasons)
	} else if skipped > 0 || invalidIntervals > 0 || truncated {
		status = "partial"
	}
	result := computeZoneEnergyResult{
		Status:                      status,
		InsufficientReason:          insufficientReason,
		StartDate:                   args.StartDate,
		EndDate:                     args.EndDate,
		Sport:                       args.Sport,
		ActivityCount:               matched,
		UsableActivityCount:         usable,
		SkippedActivityCount:        skipped,
		InvalidIntervalCount:        invalidIntervals,
		TruncatedActivityCandidates: truncated,
		TotalSeconds:                totalSeconds,
		TotalKJ:                     totalKJ,
		Zones:                       zones,
		Interpretation:              analysis.ZoneEnergyInterpretation,
	}
	meta := zoneEnergyMeta{
		AnalyzerMeta: analysis.NewAnalyzerMeta(analysis.AnalyzerMetaInput{
			Method:        analysis.ZoneEnergyMethod,
			SourceTools:   []string{getActivitiesName, getActivityStreamsName, getAthleteProfileName},
			N:             usable,
			MissingDays:   0,
			MissingAction: analysis.MissingActionSkip,
			MinSamples:    1,
			FormulaRef:    analysis.ZoneEnergyFormulaRef,
			Assumptions: map[string]any{
				"integration_rule":              "left_endpoint",
				"timestamp_unit":                "s",
				"final_sample_duration_seconds": 0,
				"max_interval_seconds":          analysis.ZoneEnergyMaxIntervalSeconds,
				"activity_cap":                  maxZoneEnergyActivities,
				"candidate_fetch_limit":         maxZoneEnergyCandidates,
				"interpolation":                 false,
			},
			Boundaries: analysis.ZoneEnergyBoundaries,
		}),
		AnalysisUnits: map[string]string{"power": "W", "time": "s", "integration_work": "J", "work": "kJ"},
		ZoneSources:   sources,
		Coverage: zoneEnergyCoverage{
			FetchedCandidateCount:     fetchedCount,
			RetainedCandidateCount:    len(activities),
			SportMatchedActivityCount: matched,
			UsableActivityCount:       usable,
			SkippedActivityCount:      skipped,
		},
	}
	return computeZoneEnergyPayload{Result: result, Series: series, Meta: meta}, nil
}

func zoneEnergyPowerZoneConfig(setting intervals.SportSettings, matchedSport string) (analysis.PowerZoneConfig, intervals.PowerZoneValidationCode) {
	sport := strings.TrimSpace(setting.Type)
	if sport == "" {
		sport = strings.TrimSpace(matchedSport)
	}
	normalized, code := intervals.NormalizePowerZones(setting.FTP, setting.PowerZoneUpperBoundsPercentOfFTP, setting.PowerZoneNames)
	config := analysis.PowerZoneConfig{Sport: sport, SportSettingID: setting.ID}
	if code != intervals.PowerZoneValid {
		return config, code
	}
	config.BoundariesWatts = normalized.AnalyzerLowerBoundsWatts
	config.Names = normalized.AnalyzerNames
	return config, intervals.PowerZoneValid
}

func selectZoneEnergySportSetting(activity intervals.Activity, settings []intervals.SportSettings) (intervals.SportSettings, string, bool) {
	candidates := []string{stringValue(activity.Type), stringValue(activity.SubType)}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		for _, setting := range settings {
			if strings.EqualFold(strings.TrimSpace(setting.Type), candidate) {
				return setting, candidate, true
			}
			for _, settingType := range setting.Types {
				if strings.EqualFold(strings.TrimSpace(settingType), candidate) {
					return setting, strings.TrimSpace(settingType), true
				}
			}
		}
	}
	return intervals.SportSettings{}, "", false
}

func zoneEnergyConfigKey(config analysis.PowerZoneConfig) string {
	canonical := struct {
		Sport           string    `json:"sport"`
		BoundariesWatts []float64 `json:"boundaries_watts"`
		Names           []string  `json:"names"`
	}{Sport: config.Sport, BoundariesWatts: config.BoundariesWatts, Names: config.Names}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(digest[:])[:12]
	if config.SportSettingID > 0 {
		return fmt.Sprintf("setting-%d-%s", config.SportSettingID, fingerprint)
	}
	return "config-" + fingerprint
}

func zoneEnergyRows(zoneKey string, config analysis.PowerZoneConfig, calculated []analysis.ZoneEnergyZone) []zoneEnergyRow {
	rows := make([]zoneEnergyRow, len(calculated))
	for i, zone := range calculated {
		rows[i] = zoneEnergyRow{
			ZoneKey:        zoneKey,
			SportSettingID: config.SportSettingID,
			Sport:          config.Sport,
			Zone:           zone.Zone,
			Name:           zone.Name,
			LowerWatts:     zone.LowerWatts,
			UpperWatts:     zone.UpperWatts,
		}
	}
	return rows
}

func orderedZoneEnergyGroups(groups map[string]*zoneEnergyGroup) ([]zoneEnergyRow, []zoneEnergyZoneSource) {
	ordered := make([]*zoneEnergyGroup, 0, len(groups))
	for _, group := range groups {
		ordered = append(ordered, group)
	}
	sort.Slice(ordered, func(i, j int) bool {
		leftSport := strings.ToLower(ordered[i].config.Sport)
		rightSport := strings.ToLower(ordered[j].config.Sport)
		if leftSport != rightSport {
			return leftSport < rightSport
		}
		if ordered[i].config.SportSettingID != ordered[j].config.SportSettingID {
			return ordered[i].config.SportSettingID < ordered[j].config.SportSettingID
		}
		return ordered[i].zoneKey < ordered[j].zoneKey
	})
	rowCount := 0
	for _, group := range ordered {
		rowCount += len(group.rows)
	}
	rows := make([]zoneEnergyRow, 0, rowCount)
	sources := make([]zoneEnergyZoneSource, 0, len(ordered))
	for _, group := range ordered {
		rows = append(rows, group.rows...)
		sources = append(sources, zoneEnergyZoneSource{
			ZoneKey:         group.zoneKey,
			SportSettingID:  group.config.SportSettingID,
			Sport:           group.config.Sport,
			BoundariesWatts: append([]float64(nil), group.config.BoundariesWatts...),
			Names:           append([]string(nil), group.config.Names...),
		})
	}
	return rows, sources
}

func finalizeZoneEnergyRows(rows []zoneEnergyRow) (float64, float64) {
	totalSeconds := 0.0
	totalKJ := 0.0
	for i := range rows {
		rows[i].Seconds = roundZoneEnergyValue(rows[i].Seconds, 3)
		rows[i].KJ = roundZoneEnergyValue(rows[i].KJ, 3)
		totalSeconds += rows[i].Seconds
		totalKJ += rows[i].KJ
	}
	totalSeconds = roundZoneEnergyValue(totalSeconds, 3)
	totalKJ = roundZoneEnergyValue(totalKJ, 3)
	setZoneEnergyRowShares(rows, totalSeconds, true)
	setZoneEnergyRowShares(rows, totalKJ, false)
	return totalSeconds, totalKJ
}

func setZoneEnergyRowShares(rows []zoneEnergyRow, total float64, timeShare bool) {
	if total == 0 {
		return
	}
	lastPositive := -1
	sum := 0.0
	for i := range rows {
		value := rows[i].KJ
		if timeShare {
			value = rows[i].Seconds
		}
		share := roundZoneEnergyValue(value/total, 4)
		if timeShare {
			rows[i].TimeShare = share
		} else {
			rows[i].EnergyShare = share
		}
		if value > 0 {
			lastPositive = i
		}
		sum += share
	}
	if lastPositive < 0 {
		return
	}
	adjustment := roundZoneEnergyValue(1-sum, 4)
	if timeShare {
		rows[lastPositive].TimeShare = roundZoneEnergyValue(rows[lastPositive].TimeShare+adjustment, 4)
	} else {
		rows[lastPositive].EnergyShare = roundZoneEnergyValue(rows[lastPositive].EnergyShare+adjustment, 4)
	}
}

func roundZoneEnergyValue(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}

func zoneEnergyInsufficientReason(activityCount int, reasons []string) string {
	if activityCount == 0 {
		return "no_activities"
	}
	configurationCodes := map[string]struct{}{}
	for _, reason := range reasons {
		if !isPowerZoneConfigurationCode(reason) {
			return "no_usable_power_streams"
		}
		configurationCodes[reason] = struct{}{}
	}
	if len(reasons) != activityCount || len(configurationCodes) == 0 {
		return "no_usable_power_streams"
	}
	if len(configurationCodes) == 1 {
		for code := range configurationCodes {
			return code
		}
	}
	return "mixed_power_zone_config_errors"
}

func isPowerZoneConfigurationCode(reason string) bool {
	switch intervals.PowerZoneValidationCode(reason) {
	case intervals.PowerZoneMissingZones, intervals.PowerZoneMissingFTP, intervals.PowerZoneInvalidCeilings, intervals.PowerZoneMismatchedNames:
		return true
	default:
		return false
	}
}

func zoneEnergyStreamsAdvertised(streamTypes []string) bool {
	if len(streamTypes) == 0 {
		return true
	}
	seen := map[string]bool{}
	for _, streamType := range streamTypes {
		key, _ := streams.CanonicalKey(streamType)
		seen[key] = true
	}
	return seen["watts"] && seen["time"]
}

func zoneEnergySportMatches(filter string, activity intervals.Activity) bool {
	if filter == "" {
		return true
	}
	return strings.EqualFold(filter, strings.TrimSpace(stringValue(activity.Type))) || strings.EqualFold(filter, strings.TrimSpace(stringValue(activity.SubType)))
}

func zoneEnergyActivitySortTime(activity intervals.Activity) string {
	if local := strings.TrimSpace(stringValue(activity.StartDateLocal)); local != "" {
		return local
	}
	return strings.TrimSpace(stringValue(activity.StartDate))
}

func zoneEnergyActivityDate(activity intervals.Activity) string {
	return localDatePrefix(zoneEnergyActivitySortTime(activity))
}

func zoneEnergyActivitySport(activity intervals.Activity) string {
	if sport := strings.TrimSpace(stringValue(activity.Type)); sport != "" {
		return sport
	}
	return strings.TrimSpace(stringValue(activity.SubType))
}

func computeZoneEnergyInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"start_date", "end_date"},
		"properties": map[string]any{
			"start_date":   map[string]any{"type": "string", "description": "Inclusive start date as YYYY-MM-DD in the athlete timezone; the range may contain at most 366 days."},
			"end_date":     map[string]any{"type": "string", "description": "Inclusive end date as YYYY-MM-DD in the athlete timezone; must be on or after start_date."},
			"sport":        map[string]any{"type": "string", "description": "Optional exact case-insensitive activity type or subtype filter."},
			"include_full": map[string]any{"type": "boolean", "default": false, "description": "When true, include bounded per-activity audit rows and diagnostics. Raw stream samples are never returned."},
		},
	}
}

func encodeZoneEnergyResponse(payload computeZoneEnergyPayload, includeFull bool, version string, debugMetadata bool, unitSystem response.UnitSystem, shapeCfg responseShaping) (Result, error) {
	shaped, err := response.Shape(payload, shapeCfg.options(includeFull, []string{"series"}, version, debugMetadata, computeZoneEnergyName, unitSystem))
	if err != nil {
		return Result{}, fmt.Errorf("shaping %s response: %w", computeZoneEnergyName, err)
	}
	return TextResult(shaped), nil
}
