# Power-Zone Semantics Correction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct every power-zone read, analyzer, and write surface so intervals.icu percentage-of-FTP upper ceilings are represented truthfully and converted once through a shared contract.

**Architecture:** Rename the upstream transport field and normalize it in `internal/intervals`, returning typed profile ceilings and analyzer lower boundaries with stable failure codes. Profile shaping and both watt analyzers consume that function. The destructive writer uses presence-aware MCP decoding and kind-specific internal fields so a fractional power ceiling cannot reach JSON encoding.

**Tech Stack:** Go 1.25, standard library JSON/HTTP, `github.com/modelcontextprotocol/go-sdk`, table-driven Go tests, existing repository generators and Make targets.

**Spec:** `docs/superpowers/specs/2026-08-24-power-zone-semantics-design.md`

## Global Constraints

- Preserve the clean-room implementation rule: use only public API behavior and black-box observations; do not use GPL/copyleft source.
- Add no dependency; keep new implementation under `internal/`.
- Do not change HR-zone or pace-zone public behavior.
- Do not add compatibility units, value heuristics, or a terminal-999 requirement.
- Keep the pure histogram and zone-energy algorithms watt-based and independent of intervals.icu transport types.
- Preserve the legacy `icuvisor://analysis-formulas#power_zone_mechanical_work` text; add v2 instead of changing it.
- Never log or commit `.env-dev` credentials. The live smoke may mutate only the disposable test athlete and must restore the complete original zone arrays.
- Follow red-green-refactor, run focused tests after each change, and use Conventional Commits without amending existing commits.

---

### Task 1: Canonical Power-Zone Normalization

**Files:**
- Create: `internal/intervals/power_zones.go`
- Create: `internal/intervals/power_zones_test.go`
- Modify: `internal/intervals/types.go`
- Modify: all Go fixtures and call sites returned by `rg -l 'PowerZones' --glob '*.go'`

**Interfaces:**
- Consumes: `ftpWatts int`, upstream `[]int` percentage ceilings, optional `[]string` names.
- Produces:

```go
type PowerZoneValidationCode string

const (
	PowerZoneValid             PowerZoneValidationCode = ""
	PowerZoneMissingZones      PowerZoneValidationCode = "missing_power_zones"
	PowerZoneMissingFTP        PowerZoneValidationCode = "missing_power_ftp"
	PowerZoneInvalidCeilings   PowerZoneValidationCode = "invalid_power_zone_ceilings"
	PowerZoneMismatchedNames   PowerZoneValidationCode = "mismatched_power_zone_names"
)

type NormalizedPowerZones struct {
	UpperBoundsPercentOfFTP   []int
	UpperBoundsWatts          []float64
	AnalyzerLowerBoundsWatts  []float64
	AnalyzerNames             []string
}

func NormalizePowerZones(ftpWatts int, upperBoundsPercentOfFTP []int, names []string) (NormalizedPowerZones, PowerZoneValidationCode)
```

Validation precedence is empty ceilings, malformed ceilings, mismatched nonempty names, then missing/nonpositive FTP. The result always copies raw percentage ceilings. Successful analyzer arrays have `len(ceilings)+1` entries: zero plus every converted ceiling, and configured or generic names plus `Above <last>`.

- [ ] **Step 1: Add failing derivation and validation tests**

Add table-driven tests that assert the live vector exactly:

```go
got, code := NormalizePowerZones(228, []int{55, 75, 90, 105, 120, 150, 999}, []string{
	"Active Recovery", "Endurance", "Tempo", "Threshold", "VO2 Max", "Anaerobic", "Neuromuscular",
})
// code == PowerZoneValid
// got.UpperBoundsWatts == []float64{125.4, 171, 205.2, 239.4, 273.6, 342, 2277.72}
// got.AnalyzerLowerBoundsWatts == []float64{0, 125.4, 171, 205.2, 239.4, 273.6, 342, 2277.72}
// got.AnalyzerNames final value == "Above Neuromuscular"
```

Cover absent names generating `Zone 1` through `Zone N` plus `Above Zone N`; empty, zero, negative, duplicate, and descending ceilings; mismatched nonempty names; zero FTP; input/result slice non-aliasing; and the validation precedence above.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test ./internal/intervals -run 'TestNormalizePowerZones' -count=1`

Expected: FAIL because `NormalizePowerZones` and its types do not exist.

- [ ] **Step 3: Implement the normalizer**

Implement only the public package surface shown above. Use `float64(ftpWatts)*float64(percent)/100`; never round derived watts. Build analyzer labels without mutating caller slices. Return immediately on the first validation failure according to the stated precedence.

- [ ] **Step 4: Rename the upstream transport field**

In `SportSettings`, replace:

```go
PowerZones []int `json:"power_zones"`
```

with:

```go
PowerZoneUpperBoundsPercentOfFTP []int `json:"power_zones"`
```

Mechanically update every Go call site and fixture to the new identifier without changing behavior yet. Do not change HR or pace fields.

- [ ] **Step 5: Run focused and repository tests**

Run: `go test ./internal/intervals ./internal/athleteprofile ./internal/tools ./internal/resources -count=1`

Expected: PASS. If an assertion still describes watts for the raw transport field, update only its internal fixture wording; public profile semantics change in Task 2.

- [ ] **Step 6: Format and commit**

Run: `gofmt -w internal/intervals/power_zones.go internal/intervals/power_zones_test.go internal/intervals/types.go`

Commit:

```bash
git add internal
git commit -m "fix(power-zones): centralize upstream normalization"
```

---

### Task 2: Truthful Athlete Profile Read Contract

**Files:**
- Modify: `internal/athleteprofile/profile.go`
- Modify: `internal/tools/get_athlete_profile_test.go`
- Modify: `internal/resources/athlete_profile_test.go`
- Modify: `internal/tools/get_data_quality_report_test.go`
- Modify: athlete-profile fixtures under `internal/intervals/testdata/` and their matching tests as required by changed public fields

**Interfaces:**
- Consumes: `intervals.NormalizePowerZones` from Task 1.
- Produces these `athleteprofile.Sport` fields:

```go
PowerZonesPercentOfFTP []int     `json:"power_zones_percent_of_ftp,omitempty"`
PowerZonesWatts        []float64 `json:"power_zones_watts,omitempty"`
```

- [ ] **Step 1: Write failing profile contract tests**

Use FTP 228 and the live percentage vector. Assert exact raw percentages, exact derived float watts, unchanged names, and tool/resource parity. Add cases for missing FTP, malformed ceilings, mismatched names, and absent names. Assert warning codes and fields:

```text
missing_power_threshold -> ftp_watts
invalid_power_zone_ceilings -> power_zones_percent_of_ftp
mismatched_power_zone_names -> power_zone_names
```

For invalid ceilings or FTP, assert raw percentages remain present and `power_zones_watts` is omitted.

- [ ] **Step 2: Run profile tests and verify RED**

Run: `go test ./internal/athleteprofile ./internal/tools ./internal/resources -run 'AthleteProfile|Readiness|DataQuality' -count=1`

Expected: FAIL because the old profile exposes raw integers as watts.

- [ ] **Step 3: Update profile shaping**

Set `PowerZonesPercentOfFTP` from the upstream copy. Call `NormalizePowerZones` once per sport setting and populate derived watts only when its code is empty. Generate readiness warnings from the stable code, while preserving the existing `missing_power_threshold` warning for nonpositive FTP. Do not hide invalid raw arrays.

Replace `Meta.ZoneBoundaryConvention` with text stating that both power arrays are upper ceilings, percentages are exact upstream integers, watts are FTP-derived floats, and names align only when present with equal length.

- [ ] **Step 4: Update profile/resource/data-quality fixtures**

Change happy-path percentage fixtures to valid positive increasing values such as `[]int{55, 75, 90, 105, 120, 150, 999}`. Reclassify zero-ceiling fixtures as invalid cases. Preserve every HR, pace, threshold, unit, and cache assertion.

- [ ] **Step 5: Run focused tests and format**

Run: `go test ./internal/athleteprofile ./internal/tools ./internal/resources ./internal/intervals -count=1`

Expected: PASS.

Run: `gofmt -w internal/athleteprofile/profile.go internal/tools/get_athlete_profile_test.go internal/resources/athlete_profile_test.go internal/tools/get_data_quality_report_test.go`

- [ ] **Step 6: Commit**

```bash
git add internal/athleteprofile internal/tools internal/resources internal/intervals
git commit -m "fix(profile): expose canonical power-zone ceilings"
```

---

### Task 3: Shared Histogram and Zone-Energy Attribution

**Files:**
- Modify: `internal/tools/get_activity_histogram.go`
- Modify: `internal/tools/get_activity_histogram_test.go`
- Modify: `internal/tools/compute_zone_energy.go`
- Modify: `internal/tools/compute_zone_energy_test.go`
- Modify: `internal/analysis/zone_energy.go`
- Modify: `internal/analysis/zone_energy_test.go`

**Interfaces:**
- Consumes: `intervals.NormalizePowerZones` and its stable codes.
- Produces:

```go
func histogramZoneConfig(metric analysis.HistogramMetric, emittedUnit string, activity intervals.Activity, profile intervals.AthleteWithSportSettings, profileAvailable bool) (*analysis.HistogramZoneConfig, intervals.PowerZoneValidationCode)

func zoneEnergyPowerZoneConfig(setting intervals.SportSettings, matchedSport string) (analysis.PowerZoneConfig, intervals.PowerZoneValidationCode)
```

Non-power histogram paths return an empty validation code. Power analyzer config uses every normalized lower watt boundary and every normalized analyzer name.

- [ ] **Step 1: Finish failing histogram behavior tests**

Retain the existing issue #59 regression test with FTP 228 and the live vector. Assert 200 W is `Tempo`, zero watts is the first zone, 125.4 W moves to the next lower-inclusive bucket, and 2277.72 W enters `Above Neuromuscular`. Assert malformed ceilings or missing FTP use fixed-width buckets and append the exact stable code string to `_meta.warnings`.

- [ ] **Step 2: Add failing zone-energy integration tests**

Use aligned time/power streams containing 0, 125.4, 200, and 2277.72 W. Assert the same labels as histogram, unchanged total seconds/kJ, and that invalid power configuration skips before any `GetActivityStreams` call. Cover all four reason codes and `mixed_power_zone_config_errors` aggregation.

- [ ] **Step 3: Run analyzer tests and verify RED**

Run: `go test ./internal/tools ./internal/analysis -run 'Histogram|ZoneEnergy' -count=1`

Expected: FAIL because zone energy still compares recorded watts to raw percentages and histogram does not yet expose all shared failure codes/overflow semantics.

- [ ] **Step 4: Replace local conversions with the shared normalizer**

Delete `powerZoneLowerBoundariesWatts`. Make `histogramZoneConfig` return the config and code; append a nonempty power code to warnings before the fixed-width fallback. In zone energy, normalize immediately after selecting the sport setting and before checking stream availability or fetching streams. Convert the normalized arrays directly to `analysis.PowerZoneConfig`.

When no activities are usable, keep a sole shared configuration reason verbatim; return `mixed_power_zone_config_errors` only when distinct configuration codes occurred. Preserve existing nonconfiguration reasons such as missing streams.

- [ ] **Step 5: Keep pure algorithms generic and update their fixtures**

Do not import `internal/intervals` into `internal/analysis`. Its `PowerZoneConfig` remains lower-watt based. Update analysis fixtures to explicitly include the zero lower boundary and final overflow lower boundary provided by the adapter. Keep total work and timestamp-integration behavior unchanged.

- [ ] **Step 6: Run focused tests and format**

Run: `go test ./internal/analysis ./internal/tools -run 'Histogram|ZoneEnergy' -count=1`

Expected: PASS.

Run: `gofmt -w internal/tools/get_activity_histogram.go internal/tools/get_activity_histogram_test.go internal/tools/compute_zone_energy.go internal/tools/compute_zone_energy_test.go internal/analysis/zone_energy.go internal/analysis/zone_energy_test.go`

- [ ] **Step 7: Commit**

```bash
git add internal/tools/get_activity_histogram.go internal/tools/get_activity_histogram_test.go internal/tools/compute_zone_energy.go internal/tools/compute_zone_energy_test.go internal/analysis/zone_energy.go internal/analysis/zone_energy_test.go
git commit -m "fix(analyzers): normalize power-zone attribution"
```

---

### Task 4: Integer-Safe Power-Zone Writes

**Files:**
- Modify: `internal/tools/update_sport_settings.go`
- Modify: `internal/tools/update_sport_settings_zones.go`
- Modify: `internal/tools/update_sport_settings_test.go`
- Modify: `internal/tools/update_sport_settings_zones_test.go`
- Modify: `internal/intervals/sport_settings.go`
- Modify: `internal/intervals/sport_settings_test.go`
- Modify: schema/catalog/protocol tests that snapshot `update_sport_settings`

**Interfaces:**
- Consumes: current profile `SportSettings` to validate omitted-name preservation.
- Produces a kind-specific internal write DTO:

```go
type SportSettingsZoneDefinition struct {
	Kind                               string
	PowerUpperBoundsPercentOfFTP       []int
	HRBoundariesBPM                    []int
	PaceBoundariesPercentOfThreshold   []float64
	Names                              []string
}
```

`updateSportSettingsZoneRequest` retains `Boundaries []float64` for the JSON union and adds unexported `namesProvided bool`. A raw-object presence pass marks omission separately and rejects explicit `null`; normal validation rejects a provided empty array.

- [ ] **Step 1: Write failing MCP decoder and handler tests**

Add table cases for power `[55,75,90,105,120,150,999]` succeeding and these failing before writer invocation: `55.5`, NaN, Inf, zero, negative, duplicate, descending, mismatched names, `names:null`, and `names:[]`. JSON cannot encode NaN/Inf, so exercise those values through `validateSportSettingsZones` directly.

Assert omitted names succeed when existing names are absent or have matching length, and fail when existing nonempty names have another length. Assert supplied matching names are forwarded unchanged.

- [ ] **Step 2: Write failing direct intervals-writer tests**

Assert exact request JSON contains:

```json
{"power_zones":[55,75,90,105,120,150,999]}
```

Add malformed direct DTO cases (empty, nonpositive, duplicate, descending, wrong populated kind field) and assert `UpdateSportSettings` returns before HTTP. Preserve current HR integer JSON and pace decimal JSON assertions.

- [ ] **Step 3: Run writer tests and verify RED**

Run: `go test ./internal/tools ./internal/intervals -run 'SportSettings|Zone' -count=1`

Expected: FAIL because power values remain generic floats and the client truncates them.

- [ ] **Step 4: Implement presence-aware tool decoding and exact conversion**

Inspect each raw `zones` element alongside strict typed decoding. Set `namesProvided` only when the key exists; return a validation error when its raw value is `null`. For power, require `math.IsNaN/IsInf == false`, `value > 0`, `value == math.Trunc(value)`, and strict increase, then convert to `[]int`. Apply the existing public behavior for HR and pace while filling their kind-specific fields.

Before building params, if a power request omits names, allow it only when existing `PowerZoneNames` is empty or its length equals the new ceiling count. Leave names absent in the outgoing body so upstream preserves them.

- [ ] **Step 5: Replace truncating writer encoding with typed validation**

Delete `roundedZoneBoundaries`. Validate each definition immediately in `writeSportSettingsBody`: exactly the field matching `Kind` must be nonempty, power must be positive and strictly increasing, and names when present must match the active array. Encode power/HR integer slices and pace float slices without numeric conversion.

- [ ] **Step 6: Correct MCP schema and examples**

Describe power as integer percent-of-FTP upper ceilings, HR as bpm, and pace as percentages. Because the array schema is shared by kind, retain JSON `number` items and state the power integer constraint in the property description; runtime validation is authoritative. Update the Ride example to the live percentage vector and matching names.

- [ ] **Step 7: Run focused tests and format**

Run: `go test ./internal/tools ./internal/intervals ./internal/mcp ./internal/toolchecks -run 'SportSettings|Schema|Catalog|Protocol' -count=1`

Expected: PASS.

Run: `gofmt -w internal/tools/update_sport_settings.go internal/tools/update_sport_settings_zones.go internal/tools/update_sport_settings_test.go internal/tools/update_sport_settings_zones_test.go internal/intervals/sport_settings.go internal/intervals/sport_settings_test.go`

- [ ] **Step 8: Commit**

```bash
git add internal
git commit -m "fix(sport-settings): write power-zone percentages safely"
```

---

### Task 5: Formula, Product Contract, and Generated Documentation

**Files:**
- Modify: `internal/resources/analysis_formulas.go`
- Modify: `internal/resources/analysis_formulas_test.go`
- Modify: `internal/resources/testdata/analysis_formulas.md`
- Modify: `internal/analysis/zone_energy.go`
- Modify: `internal/analysis/zone_energy_test.go`
- Modify: `docs/prd/PRD-icuvisor.md`
- Modify: `ROADMAP.md`
- Modify: `docs/upstream-gaps/sport-settings-write-contract.md`
- Modify: `docs/dogfood/v0.3-prompts.md`
- Modify: generated tool/schema artifacts produced by `make docs-tools`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: final public contracts from Tasks 1–4.
- Produces:

```go
const AnalysisFormulaRefPowerZoneMechanicalWorkV2 = AnalysisFormulasURI + "#power_zone_mechanical_work_v2"
const ZoneEnergyFormulaRef = "icuvisor://analysis-formulas#power_zone_mechanical_work_v2"
```

- [ ] **Step 1: Write failing formula registry tests**

Assert the legacy ref and exact legacy paragraph remain present and unchanged. Assert v2 exists, describes percentage upper-ceiling normalization, `[lower, upper)` attribution, explicit above-final bucket, and the unchanged left-endpoint watt integration. Assert zone-energy metadata points to v2.

- [ ] **Step 2: Run formula tests and verify RED**

Run: `go test ./internal/resources ./internal/analysis ./internal/tools -run 'Formula|ZoneEnergy' -count=1`

Expected: FAIL because v2 is absent.

- [ ] **Step 3: Add v2 without modifying legacy text**

Append the new formula registry entry; do not edit the legacy paragraph. Change only the analyzer adapter constant to v2 and update the formula golden.

- [ ] **Step 4: Update authoritative and operational documentation**

Document exact profile field types, power-write percentages, overflow bucket behavior, stable failure codes, and intentional write break in PRD §7.2.C. Update the roadmap zone-energy row, sport-settings gap, and W-09 dogfood prompt. Add an `[Unreleased]` changelog entry that tells cached-schema users to start a new conversation after upgrading.

- [ ] **Step 5: Regenerate tool docs and validate the diff**

Run: `make docs-tools`

Run: `git diff --check`

Inspect the generated diff and confirm only power-zone/profile/formula/schema contracts changed.

- [ ] **Step 6: Run documentation and catalog tests**

Run: `go test ./internal/resources ./internal/analysis ./internal/tools ./internal/toolchecks ./internal/toolcatalog -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add CHANGELOG.md ROADMAP.md docs internal/analysis internal/resources internal/tools internal/toolchecks internal/toolcatalog
git commit -m "docs(power-zones): publish corrected contracts"
```

---

### Task 6: Full Verification and Reversible Live Smoke

**Files:**
- Modify only if verification exposes a defect in an earlier task; commit each focused correction separately.

**Interfaces:**
- Consumes: the complete implementation and `.env-dev` disposable test-account configuration.
- Produces: fresh local test evidence plus verified restoration of upstream power ceilings and names.

- [ ] **Step 1: Run formatting, focused tests, and full project gate**

Run:

```bash
make fmt
go test ./internal/intervals ./internal/athleteprofile ./internal/analysis ./internal/tools ./internal/resources -count=1
make check
```

Expected: every command exits zero. Review `git diff --check` and `git status --short` after formatting.

- [ ] **Step 2: Run the serial live test-account smoke**

Load `.env-dev` without printing values and launch the built binary with full delete mode/full toolset. In one cleanup-guarded script or test harness:

1. read the Ride setting and keep complete original `power_zones` and `power_zone_names` arrays in memory;
2. choose a first-ceiling integer mutation that remains positive and below the second ceiling;
3. call `update_sport_settings` using percent-of-FTP boundaries and matching names;
4. re-read with the uncached profile tool and assert exact percentage values plus `FTP*percent/100` watt ceilings;
5. verify histogram and zone-energy classify a known watt sample consistently when suitable test-account activity data exists;
6. restore both original arrays in a deferred/guaranteed cleanup path; and
7. re-read and compare both restored arrays exactly before exiting.

If suitable stream data does not exist, report analyzer live verification as unavailable; unit/integration tests remain authoritative. A failed restore is a hard blocker and must be reported immediately without further writes.

- [ ] **Step 3: Review final scope**

Run:

```bash
git diff 19d3cff..HEAD --stat
git log --oneline 19d3cff..HEAD
git status --short
```

Confirm there are no credentials, unrelated refactors, dependencies, or changes to HR/pace behavior.

- [ ] **Step 4: Record verification-only corrections if needed**

For each defect, add a reproducing test, make the smallest fix, rerun the focused test and `make check`, then commit with `fix(power-zones): correct verified regression`. If multiple unrelated defects appear, use one focused commit per reproducer and name the affected surface in place of `verified regression`. If no defect is found, do not create an empty commit.
