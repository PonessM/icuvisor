# Press-lap workout steps: public-evidence contract

## Decision

**Decision: `supported` for the intervals.icu description DSL.**

On 2026-08-25, the public Intervals.icu Workout Builder guide explicitly
documents an “End when lap button pressed” option that inserts `Press lap` into
a timed workout step, including `- Press lap when ready 20m 50%`. It says the
time is used to calculate planned duration and load, while a compatible device
continues the step until the Lap button is pressed. The guide documents Garmin
via Garmin Connect integration; it does not establish universal device support.

icuvisor exposes this documented text-grammar control as the structured
`press_lap: true` step field. It serializes the canonical `Press lap` marker
into the only verified upstream write channel (`description`) and parses that
marker back from DSL. The field is an icuvisor representation, not an invented
upstream JSON write field: the public OpenAPI schema still exposes
`workout_doc` generically and does not declare a dedicated `press_lap`
property.

## Reproducible evidence record

### Public source references

| Source | Exact area to inspect | Evidence available in this task | Retrieval/provenance |
| --- | --- | --- | --- |
| <https://forum.intervals.icu/t/workout-builder/1163/149> | Workout Builder “End when lap button pressed” guidance | Documents the `Press lap` text marker, a timed example, Garmin Connect scope, and that the time is used for planned duration/load while the device waits for the lap button. | Retrieved 2026-08-25. |
| <https://intervals.icu/api/v1/docs> | `components.schemas.Workout.properties.description`, `components.schemas.Workout.properties.workout_doc`, and workout/event create/read operations | Live schema version `v1.0.0` names `description` and generic `workout_doc`; it does not name a dedicated press-lap JSON property. | Retrieved 2026-08-25. |

The local snapshot is implementation evidence, not current public-upstream
proof. In particular, its missing field cannot establish that the live API
supports or rejects press-lap control. The source URLs alone are not evidence
of a device capability.

### Local implementation baseline (not upstream proof)

The following local files describe what icuvisor can safely represent and test;
they do not establish Garmin, Wahoo, or intervals.icu execution behavior:

- `internal/workoutdoc/types.go`: `Step` has description, duration/distance,
  power/HR/pace/RPE/cadence targets, ramp/freeride, repeat fields, and the
  icuvisor-owned `press_lap` representation.
- `internal/workoutdoc/parse.go` and `serialize.go`: the canonical grammar is
  line-oriented: `- [label] [duration|distance] [target]`, with `Nx` repeat
  headers and indented child steps. `press_lap` serializes as `Press lap` and
  parses from that marker at any position in a simple step.
- `internal/workoutdoc/syntax.go`: the published local syntax reference lists
  duration/distance, Press Lap, repeats, ramps, freeride, targets, and cadence.
- `internal/workoutdoc/testdata/`: checked-in DSL/structured pairs prove local
  parser/serializer behavior. The `06-full-surface-upstream-response-workout-doc.json`
  file is a sanitized historical capture with documented partial fidelity loss,
  not public proof of press-lap support and not a new fixture for this task.
- `internal/tools/validate_workout.go` and `internal/tools/decode.go`:
  `validate_workout` is read-only and strict-decodes its request; unknown
  top-level arguments are rejected before validation. Structured step fields
  are validated through the same serializer path used for writes.
- `internal/tools/workout_doc_fidelity.go`: write responses expose a returned
  structured-summary/fidelity warning boundary. An upload marker or canonical
  DSL is not proof that upstream rendered the intended structure.
- `docs/prd/PRD-icuvisor.md` §7.2.C (Events & workouts) and §7.4 assumptions:
  the documented write channel is the description-string DSL, with structured
  round-trip and lossy-field requirements. Those product requirements do not
  add a press-lap grammar.

## What the evidence does and does not show

Positive evidence establishes the existing endurance-workout grammar plus the
`Press lap` control marker on a timed/distance step. ICUVisor models that marker
with `press_lap: true`; it must not be duplicated in `description`, because the
serializer owns its canonical placement. A `Press lap` line without a duration
or distance remains invalid: the public guide requires time to calculate the
planned duration and load.

The public API schema still does not establish an upstream structured JSON
field. ICUVisor therefore never sends `workout_doc` upstream and never claims
that a returned generic `workout_doc` alone proves device compatibility. Its
write-fidelity check includes the control when an upstream returned document
exposes `press_lap`; absence of that field produces the usual partial-fidelity
warning rather than a compatibility claim.

Garmin and Wahoo execution behavior is not established by the intervals.icu
DSL. Device workout-upload capabilities, whether a device exposes a manual lap
button during a workout, and whether an uploaded step waits for a lap are
separate vendor/device contracts. No TSS, training-load, duration, or other
load semantics may be inferred from a hypothetical press-lap action. In
particular, a manual lap is not a substitute for a timed or distance step in
load calculations.

## Remaining verification boundary

The current evidence supports authoring the documented DSL marker and its
Garmin-focused caveat. Before claiming support for another device, a distinct
upstream JSON field, or completed-workout fidelity, obtain device-specific
public evidence and a sanitized write/read fixture. Until then, an upstream
response that omits `press_lap` remains partial-fidelity evidence rather than a
claim that the control was delivered to a device.

The supported authoring shape is a timed/distance structured step with
`press_lap: true`. It remains a Garmin-focused feature according to the public
Intervals.icu guide; other device behavior must be treated as device-specific
unless separately documented.
