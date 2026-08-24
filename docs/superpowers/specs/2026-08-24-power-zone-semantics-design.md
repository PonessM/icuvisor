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

A controlled write changed the first ceiling from 55 to 54. intervals.icu returned 54 unchanged on the next read. The original 55 value was then restored and verified. This confirms that the upstream read and write representation is integer percent-of-FTP upper ceilings.

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
- Reworking the generic histogram or zone-energy integration algorithms.

## Public contract

### Athlete profile

Each power-zone definition exposes two same-index upper-ceiling arrays paired with `power_zone_names`:

- `power_zones_percent_of_ftp`: exact upstream integer percentages.
- `power_zones_watts`: `FTP * percent / 100`, represented as decimals.

For FTP 228, these are `[55,75,90,105,120,150,999]` and `[125.4,171,205.2,239.4,273.6,342,2277.72]`.

If FTP is absent or nonpositive, the percentage array remains available and the derived watt array is omitted. Profile metadata states that both arrays contain upper ceilings and that names align by index.

### Power-zone writes

For `update_sport_settings`, `zones[].boundaries` with `kind: "power"` means ordered integer percent-of-FTP upper ceilings. The server sends those integers unchanged as upstream `power_zones`.

The values must be positive and strictly increasing. Optional names must match the number of ceilings. The server does not require or synthesize a terminal `999` value because that upstream convention is not required by current evidence.

This deliberately replaces the incorrect documented watt-input behavior. There is no compatibility unit or heuristic.

### Analyzer boundaries

Watt analyzers consume lower boundaries derived from the upper ceilings:

```text
upper percentages: [55, 75, 90, 105, 120, 150, 999]
lower watts:       [0, FTP*0.55, FTP*0.75, FTP*0.90, FTP*1.05, FTP*1.20, FTP*1.50]
```

The final upper ceiling is not emitted as a new lower boundary. The last named zone is open-ended.

## Internal design

Rename the raw `intervals.SportSettings` field to identify it as percentage upper ceilings while retaining JSON tag `power_zones`. Add a focused normalization module under `internal/intervals` that:

1. validates a non-empty, positive, strictly increasing integer ceiling array;
2. derives same-index upper watt ceilings when FTP is positive; and
3. derives analyzer lower watt boundaries by prepending zero and converting every ceiling except the final one.

`internal/athleteprofile`, `get_activity_histogram`, and `compute_zone_energy` consume this shared normalization instead of copying or independently converting the raw field. Readiness and data-quality checks continue to test zone availability but use corrected field names in warnings.

The pure histogram and zone-energy algorithms remain watt-based and independent of intervals.icu transport types.

## Error handling

- Empty, zero, negative, duplicate, or descending percentage ceilings are invalid.
- Missing or nonpositive FTP prevents watt derivation.
- Athlete-profile reads preserve valid raw percentages and omit derived watts when FTP is unavailable.
- `compute_zone_energy` reports an invalid or missing power-zone configuration without fetching streams.
- `get_activity_histogram` uses its existing fixed-width fallback when configured watt zones cannot be derived.
- Power-zone writes reject fractional, malformed, or unordered ceilings before any HTTP request.
- Upstream write failures continue through the existing short, sanitized error path.

## Testing

Implementation follows red-green-refactor and adds behavior-first coverage for:

- the live FTP 228 vector and exact upper/lower watt derivations;
- correct profile tool/resource parity for percentage and watt arrays;
- omitted derived watts when FTP is unavailable;
- 200 W classified as Tempo in both histogram and zone-energy paths;
- unchanged mechanical-work totals with corrected zone attribution;
- malformed ceiling arrays and mismatched names failing closed;
- exact integer percentage JSON emitted by `update_sport_settings`;
- unchanged HR and pace zone behavior;
- schema snapshots and generated tool documentation; and
- a final `.env-dev` test-account percentage write/read/restore smoke test.

The full `make check` gate must pass before completion.

## Documentation and contract updates

Implementation updates:

- `docs/prd/PRD-icuvisor.md` for profile, analyzer, and write behavior;
- `ROADMAP.md` where zone-energy behavior is described;
- analyzer formula resources and their golden text;
- sport-settings upstream-gap and dogfood documentation;
- generated tool/schema documentation and stable input snapshots; and
- `CHANGELOG.md` under `[Unreleased]`.

Changing the canonical zone-energy formula text is intentional definition drift. The existing `icuvisor://analysis-formulas#power_zone_mechanical_work` reference remains stable, while the formula text and all matching goldens are updated together to document percentage-ceiling normalization before watt integration.

## Rollout and compatibility

This is a correctness fix with an intentional behavioral break for callers that followed the old power-zone write description. Cached schemas may continue to describe watts until a new conversation is started, so release notes and schema-cache guidance must call out the change explicitly.

No migration heuristic is provided because watt values and percentage values overlap and cannot be distinguished safely. Reads become more explicit by providing both representations, while writes accept only the canonical upstream percentage form.
