# Power-zone semantics correction

## Status

Approved design for correcting intervals.icu power-zone reads, analysis, and writes.

## Problem

intervals.icu represents `SportSettings.power_zones` as ordered integer upper ceilings expressed as percentages of FTP. Icuvisor currently treats the same values as lower boundaries expressed in watts in several surfaces:

- `get_athlete_profile` publishes the raw percentages as `power_zones_watts`.
- `compute_zone_energy` compares watt samples directly with percentage ceilings and shifts zone names.
- `update_sport_settings` documents power boundaries as watts and forwards them unchanged to the upstream percentage field.

The issue #59 histogram fix corrected the symptom locally, but retaining separate conversions would allow the contracts to drift again.

## Evidence

A live authenticated read from the `.env-dev` test athlete returned FTP 228, percentage ceilings `[55,75,90,105,120,150,999]`, and names `[Active Recovery, Endurance, Tempo, Threshold, VO2 Max, Anaerobic, Neuromuscular]`.

A controlled write changed the first ceiling from 55 to 54. intervals.icu returned 54 unchanged on the next read. The original 55 value was then restored and verified. This confirms integer percentage-ceiling round-tripping for that athlete. It does not establish boundary inclusivity, name-omission behavior, or a required terminal ceiling.

## Goals

- Give the upstream field one accurate internal meaning.
- Return truthful percentage and watt representations from athlete-profile surfaces.
- Make histogram and zone-energy attribution use the same derived watt boundaries.
- Make power-zone writes accept the upstream-native representation directly.
- Fail closed when percentage ceilings or FTP cannot be used safely.
- Preserve HR-zone and pace-zone behavior.

## Non-goals

- Supporting the previous incorrect implicit-watt power-zone write behavior.
- Adding unit-selection, compatibility conversion, or value heuristics.
- Adding a new MCP tool.
- Changing FTP or indoor-FTP selection rules.
- Reworking unrelated histogram or zone-energy statistics and integration behavior.

## Public contract

### Athlete profile

Each power-zone definition exposes two same-index upper-ceiling arrays paired with `power_zone_names`:

- `power_zones_percent_of_ftp`: exact upstream percentages as `[]int`.
- `power_zones_watts`: `FTP * percent / 100` as `[]float64`.

For FTP 228, these are `[55,75,90,105,120,150,999]` and `[125.4,171,205.2,239.4,273.6,342,2277.72]`.

If FTP is absent or nonpositive, the percentage array remains available and the derived watt array is omitted. Invalid raw ceilings remain visible for diagnosis but do not produce derived watts. Profile metadata states that both arrays contain upper ceilings and that names align by index only when the optional name array is present and has the same length.

An absent name array is valid and analyzers generate `Zone 1`, `Zone 2`, and so on. A nonempty name array whose length differs from the ceiling count produces a `mismatched_power_zone_names` readiness warning; analyzers do not use that configuration.

### Power-zone writes

For `update_sport_settings`, `zones[].boundaries` with `kind: "power"` means ordered integer percent-of-FTP upper ceilings. The server sends those integers unchanged as upstream `power_zones`.

The values must be finite, positive, strictly increasing integers. Fractional values are rejected instead of truncated. Optional names must match the number of ceilings when supplied.

When names are omitted, the server preserves existing upstream names only if they are absent or their count matches the replacement ceiling count. If existing names are nonempty and their count differs, the write is rejected before HTTP and the caller must supply a complete matching name array. Presence-aware decoding distinguishes omission from an explicitly supplied value: `names: null` and `names: []` are rejected for a nonempty boundary array, while an omitted `names` field alone requests preservation. The server does not require or synthesize a terminal `999` value.

This deliberately replaces the incorrect documented watt-input behavior. There is no compatibility unit or heuristic.

### Analyzer boundaries

Watt analyzers consume lower boundaries and labels derived from every upper ceiling:

```text
upper percentages: [55, 75, 90, 105, 120, 150, 999]
lower watts:       [0, FTP*0.55, FTP*0.75, FTP*0.90, FTP*1.05, FTP*1.20, FTP*1.50, FTP*9.99]
labels:            [Active Recovery, Endurance, Tempo, Threshold, VO2 Max, Anaerobic, Neuromuscular, Above Neuromuscular]
```

Intervals are lower-inclusive and upper-exclusive: `[lower, upper)`. Zero watts belongs to the first named zone; a sample exactly on a derived boundary belongs to the following zone. The final configured ceiling becomes the lower bound of an explicit open-ended `Above <last name>` bucket, so no caller-supplied ceiling is discarded. If names are absent, the overflow label is `Above Zone N`.

## Internal design

Rename the raw `intervals.SportSettings` field to identify it as percentage upper ceilings while retaining JSON tag `power_zones`. Replace the generic intervals writer boundary representation with kind-specific typed fields: power percent upper ceilings and HR bpm boundaries are `[]int`, while pace percentage boundaries remain `[]float64`. The MCP request may keep its JSON number array, but it converts power values to integers only after exact integer validation; no `float64` power boundary reaches the intervals writer. The writer validates the kind-specific invariant again immediately before encoding.

Add a focused normalization module under `internal/intervals` with a typed result containing raw percentage ceilings, derived upper watt ceilings, analyzer lower watt boundaries, analyzer names, and a stable validation code. It:

1. validates a non-empty, positive, strictly increasing integer ceiling array;
2. derives same-index upper watt ceilings when FTP is positive; and
3. derives analyzer lower watt boundaries by prepending zero and converting every ceiling, adding an explicit above-final label.

`internal/athleteprofile`, `get_activity_histogram`, and `compute_zone_energy` consume this shared normalization instead of copying or independently converting the raw field. Readiness and data-quality checks continue to test zone availability but use corrected field names in warnings.

The pure histogram and zone-energy algorithms remain watt-based and independent of intervals.icu transport types.

## Error handling

- Empty, zero, negative, duplicate, or descending percentage ceilings are invalid.
- Missing or nonpositive FTP prevents watt derivation.
- Athlete-profile reads preserve raw percentages, omit derived watts for invalid ceilings or missing FTP, and emit `invalid_power_zone_ceilings` or the existing `missing_power_threshold` readiness warning as applicable.
- A nonempty mismatched name array emits `mismatched_power_zone_names`; an absent name array remains valid.
- `compute_zone_energy` skips invalid configurations before fetching streams. Per-activity reasons are `missing_power_zones`, `missing_power_ftp`, `invalid_power_zone_ceilings`, or `mismatched_power_zone_names`. If no activity is usable, the aggregate reason is the single shared code or `mixed_power_zone_config_errors` when skipped activities have different configuration failures.
- `get_activity_histogram` uses its existing fixed-width fallback when configured watt zones cannot be derived and includes the corresponding stable reason code in `_meta.warnings`.
- Power-zone writes reject fractional, malformed, or unordered ceilings before any HTTP request. Presence-aware decoding also rejects explicitly null or empty name arrays while preserving the distinct omitted-name case.
- Upstream write failures continue through the existing short, sanitized error path.

## Testing

Implementation follows red-green-refactor and adds behavior-first coverage for:

- the live FTP 228 vector and exact upper/lower watt derivations;
- correct profile tool/resource parity for percentage and watt arrays;
- omitted derived watts when FTP is unavailable;
- 200 W classified as Tempo in both histogram and zone-energy paths, plus 0 W and exact-boundary cases proving `[lower, upper)` behavior;
- preservation of the final ceiling through an explicit above-final bucket, with and without configured names;
- unchanged mechanical-work totals with corrected zone attribution;
- malformed ceiling arrays and mismatched nonempty names failing closed with the specified reason codes;
- exact integer percentage JSON emitted by `update_sport_settings`, with `55.5`, NaN/Inf, zero, duplicate, and descending values making no write call at both the MCP and direct intervals-client boundaries;
- omitted write names preserving an existing same-length or absent array, rejecting an existing mismatched array, and explicit `null` or `[]` names making no write call;
- unchanged HR and pace zone behavior;
- schema snapshots and generated tool documentation; and
- a final manual `.env-dev` test-account percentage write/read/restore smoke test.

Existing fixture families that encode watt/lower-bound assumptions are migrated explicitly: intervals athlete-profile fixtures and client tests; profile tool/resource tests; readiness/data-quality fixtures; histogram tests; zone-energy analysis/tool tests; direct intervals writer tests; MCP catalog/protocol schema tests; and generated documentation goldens. Zero-ceiling and missing-FTP fixtures become invalid cases instead of happy paths.

The full `make check` gate must pass before completion.

The live smoke is serial and is not part of `make check`: load the full-delete-mode disposable test athlete, capture the complete original percentage and name arrays in memory, mutate one safely ordered ceiling, verify exact read-back, and restore the complete originals in a guaranteed cleanup path even after validation failure. Verify the restored read-back before reporting success.

## Documentation and contract updates

Implementation updates:

- `docs/prd/PRD-icuvisor.md` for profile, analyzer, and write behavior;
- `ROADMAP.md` where zone-energy behavior is described;
- analyzer formula resources and their golden text;
- sport-settings upstream-gap and dogfood documentation;
- generated tool/schema documentation and stable input snapshots; and
- `CHANGELOG.md` under `[Unreleased]`.

Changing the canonical zone-energy formula is intentional definition drift. Preserve the existing `icuvisor://analysis-formulas#power_zone_mechanical_work` entry as the legacy already-normalized-watt definition. Add `icuvisor://analysis-formulas#power_zone_mechanical_work_v2` for upstream percentage-ceiling normalization plus watt integration, point `compute_zone_energy` metadata to v2, and update all matching goldens together.

## Rollout and compatibility

This is a correctness fix with an intentional behavioral break for callers that followed the old power-zone write description. Cached schemas may continue to describe watts until a new conversation is started, so release notes and schema-cache guidance must call out the change explicitly.

`get_athlete_profile` reads are fresh, while `icuvisor://athlete-profile` may retain its existing 15-minute cache after a write. Tool/resource parity means identical field semantics and shape, not immediate read-after-write equality; smoke guidance accounts for that cache.

No migration heuristic is provided because watt values and percentage values overlap and cannot be distinguished safely. Reads become more explicit by providing both representations, while writes accept only the canonical upstream percentage form.
