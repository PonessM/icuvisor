# Sport-settings write contract

The live public OpenAPI document at `https://intervals.icu/api/v1/docs` was reconfirmed without credentials on 2026-07-10.

## Update

`PUT /api/v1/athlete/{athleteId}/sport-settings/{id}` requires the boolean query parameter `recalcHrZones`. The request body is the existing sparse `SportSettings` JSON object; it includes only the writable fields supplied by the caller.

The MCP `update_sport_settings` input exposes this as optional `recalc_hr_zones`. Omission resolves to `true`; an explicit `false` is preserved. The decoder uses presence-aware input so `false` is distinguishable from omission, then forwards the resolved value to `WriteSportSettingsParams` for query encoding.

`effective_date` is not part of the MCP request, examples, metadata, or generated schema. Strict decoding rejects it, like any other unknown argument, before a profile lookup or upstream request.

## Corrected power-zone contract

The upstream `power_zones` array is not watts. It is the exact ordered `[]integer` of positive, strictly increasing **percentage upper ceilings of FTP**. `get_athlete_profile` exposes it as `power_zones_percent_of_ftp`, and exposes the matching derived `[]number` watt ceilings as `power_zones_watts`, where each value is `ftp_watts * percentage / 100`. `power_zone_names`, when present, aligns one-for-one with those ceilings. `ftp_watts` and optional `indoor_ftp_watts` remain threshold fields; neither is inferred from a zone boundary.

For `update_sport_settings`, a `zones` item with `kind: "power"` must send those same positive, strictly increasing integer percentage ceilings in `boundaries`; it must never send watt values. Supplying zones replaces the existing zone definitions and requires `ICUVISOR_DELETE_MODE=full`. If names are supplied they must be nonempty and match the ceiling count; omitting names preserves existing names only when the old count is compatible.

This intentionally breaks the former documented watt-boundary write shape. Clients with a cached MCP schema must start a new conversation after upgrading, then resend power-zone writes as percentages. The stable normalization/failure codes are `missing_power_zones`, `missing_power_ftp`, `invalid_power_zone_ceilings`, and `mismatched_power_zone_names`; profile readiness additionally reports `missing_power_threshold` when the sport FTP threshold is absent.

Power analyzers normalize the percentage ceilings with FTP before use. Configured named zones receive power in `[lower, upper)`; an explicit above-final bucket receives power at or above the last derived watt ceiling. This overflow bucket is an analyzer result, not an additional persisted upstream zone. The integration remains left-endpoint watts over timestamp intervals.

## Apply

`PUT /api/v1/athlete/{athleteId}/sport-settings/{id}/apply` takes no query parameters and no request body. It is a distinct explicit client operation and is not invoked by `UpdateSportSettings` or the MCP update tool.

The upstream operation is asynchronous and its public contract does not provide a date boundary. Consequently, icuvisor does not claim a date-scoped historical recomputation.

## Client implementation boundary

`WriteSportSettingsParams` carries a resolved `RecalcHRZones bool`. The update client encodes it as the required `recalcHrZones` query key with `strconv.FormatBool`; the sparse JSON body never contains that option. The MCP decoder, rather than a client zero-value heuristic, resolves omission to true before constructing these parameters.

`ApplySportSettings` takes only `(ctx, sportSettingID)` so callers cannot pass a date. Its transport uses a bodyless PUT helper that creates a fresh `http.Request` with a nil body for each retry, retains the existing retry/error behavior, and closes each response body. The update path uses a body-plus-query helper with the same retry and error semantics. Neither helper changes existing callers. `UpdateSportSettings` does not call apply; an internal `EffectiveDate`, if temporarily retained during migration, has no transport effect and is removed with the MCP producer.

## Response metadata

The update response reports `hr_zone_recalculation_requested`, the boolean sent as `recalcHrZones`. This describes the requested update option only; it does not claim that activity recomputation is pending or complete. The former `effective_date` and `recompute_pending` metadata claims are removed.

## Schema migration and stability approval

The request uses `*bool` while decoding to preserve whether `recalc_hr_zones` was supplied. Decoding resolves nil to true and copies that resolved value to `WriteSportSettingsParams.RecalcHRZones`; `EffectiveDate` is removed from both types. The input schema requires only `sport`, exposes optional boolean `recalc_hr_zones` with `default: true`, and uses examples without dates.

The response `_meta` always emits `hr_zone_recalculation_requested`, exactly the resolved option. It retains delete-mode and unit metadata but removes `effective_date` and `recompute_pending`.

Schema stability retains its generic property-removal protection. Production `internal/toolchecks/schema_stability.go` holds an `approvedSchemaPropertyRemovals` policy keyed by tool and property. Its sole entry is `update_sport_settings.effective_date`, with the TP-228 safety-correction rationale that the field falsely implied date-scoped upstream recomputation. `compareStableSchema` consults that policy only before emitting a `property-removed` failure. Tests exercise `CheckSchemaStability` itself: the one approved removal passes, while another `update_sport_settings` property and `effective_date` on a different tool fail. The schema snapshot and generated website data are refreshed with `make docs-tools`.

## Regression boundary

Wire tests assert update method, path, sparse JSON body, and both resolved query values. Apply tests assert a bodyless, queryless PUT. Tool tests assert omitted/default-true and explicit-false forwarding, rejection of legacy `effective_date` before an upstream call, and no implicit apply path.
