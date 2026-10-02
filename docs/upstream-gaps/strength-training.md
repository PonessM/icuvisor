# Strength training and gym support upstream gap

## Current best-effort support

icuvisor does not currently expose first-class strength-training tools or structured strength-set writes. The supported representation is a free-text session description:

- Use `add_or_update_event` with category `NOTE` to schedule a gym time block, mobility reminder, or free-text strength plan note.
- If the athlete account has a documented upstream workout/activity `type` for the intended session, such as `WeightTraining`, use a simple `WORKOUT` event only for date/time, optional duration/load targets, name, tags, and free-text description.
- For a completed non-Strava `WeightTraining` activity, `update_activity` can replace its free-text description. Read the existing description before editing so athlete notes and performed work are retained unless the user wants them changed.
- Treat `WeightTraining` as an upstream activity/event type label only. Do not infer an exercise catalog, structured set list, or strength-session schema from the type name.
- Do not encode sets, reps, external load, bodyweight, rest periods, exercise libraries, or progression rules into `workout_doc`; that DSL is for intervals.icu structured endurance workouts and target steps.

This lets an athlete or coach read a useful session on the calendar or activity page without implying that icuvisor can round-trip structured strength data.

## Free-text strength-session contract

Write new strength descriptions as Markdown. Put a blank line before a list and start each exercise on its own line with `- `, not `•`. Keep the list flat unless a real circuit or superset needs a heading. A heading, goal, warm-up, exercise order, cool-down, and technique notes are optional. Do not add empty headings or placeholder values. This is presentation guidance, not a new Intervals.icu strength DSL: a hyphen also starts an endurance workout step, so do not treat a readable strength list as evidence that Intervals.icu parsed exercises or can send them to a device. Read back a planned `WORKOUT` event to confirm its description survived the write; any returned `workout_doc` or warning must be interpreted as endurance-parser output, not a native strength-set confirmation.

Use enough detail for the athlete to execute the intended session while allowing bodyweight, unilateral, timed, distance, assisted, and equipment-specific movements:

| Detail | Write it when known | Avoid |
| --- | --- | --- |
| Exercise and order | Free-text movement name, one line per movement; preserve the athlete's wording. | Inventing a canonical exercise ID or silently substituting equipment. |
| Dose | Planned or performed sets × reps, a rep range, seconds, or distance; say `/side` for unilateral work. | Treating `3 × 8–10` as eight completed reps or a distance as a repetition count. |
| Load | Number plus `kg` or `lb`, with `total`, `per hand`, or `per side`; say `bodyweight` or `assisted` when that is the actual prescription. | Ambiguous `@ 20`, inferred barbell/dumbbell basis, or converting an unspecified load. |
| Effort | Label target or actual `RPE` (1–10) or `RIR` (reps in reserve) when supplied. | Conflating strength RPE/RIR with activity `feel` (1–5) or inventing an effort target. |
| Rest and grouping | State whether rest is between sets or rounds; label a superset/circuit and its rounds when prescribed. | Flattening grouped work into a misleading sequence or inventing a rest period. |
| Cues and substitutions | Preserve concise technique cues, equipment constraints, and explicitly offered alternatives. | Turning a cue into an unverified medical or progression rule. |

For a planned calendar event, one valid free-text example is:

```text
## Strength A

- Back squat: 3 × 5 @ 80 kg total; rest 2 min between sets; target RPE 7.
- Split squat: 3 × 8/side @ 16 kg per hand; rest 90 s between sets.
- Farmer carry: 3 × 40 m @ 24 kg per hand; rest 2 min between carries.

Keep the final rep controlled; use the planned load only if it matches today's instructions.
```

For a completed activity, label actual work instead of copying the plan as if it happened. For example, `- Back squat: completed 3 × 5 @ 80 kg total; last set RPE 8.` A session may mix dose types and omit load, rest, or effort when unknown. Supersets and circuits can use a heading such as `### A1/A2 superset — 3 rounds` followed by one `- ` line per exercise. Do not auto-rewrite existing athlete/coach descriptions or replace planned content with inferred actuals. Both `add_or_update_event.description` and `update_activity.description` are replacement fields, so preview the full replacement before an edit.

## Upstream gap

The product scope already treats strength-training data as conditional on upstream API support. The PRD lists strength training only as included if the intervals.icu API exposes it, and the roadmap keeps strength training endpoints in the v1.x bucket behind the same assumption. The [live public OpenAPI document](https://intervals.icu/api/v1/docs), checked on 2026-10-02, lists `WeightTraining` as an activity/event type and a scalar `Activity.kg_lifted`. It has no strength/exercise/set endpoint or schema; that scalar does not identify exercises or per-set loads. The repository-held baseline has the same gap. Intervals.icu athletes [ask for a first-class strength builder](https://forum.intervals.icu/t/advanced-strength-workout-builder/115495) with sets, reps, load, RIR, rest, time, distance, and grouping, while a [completed-activity discussion](https://forum.intervals.icu/t/strength-training-feature-set/114622) describes current use of the description field for strength plans and actuals. A [FIT import report](https://forum.intervals.icu/t/fit-workout-import-silently-drops-reps-steps-and-mis-scales-distance-steps/131081) describes rep-based strength steps being dropped; do not promise device delivery or native round-trip from free-text descriptions.

### Candidate first-class schema, pending upstream support

This is a requirements checklist for a future upstream-backed tool, not accepted icuvisor input today. Keep planned prescriptions and performed logs separate so editing a plan cannot rewrite history. The smallest useful shape has an ordered session with optional blocks, ordered exercises with free-text names, and optional set-level details:

| Field group | Required capability before exposure |
| --- | --- |
| Session | Stable ID, sport/type, title, athlete-local date, planned/completed state, optional duration and notes, and calendar/activity linkage. |
| Exercise | Stable order and free-text name; optional side/laterality, equipment, cues, and substitution. No fixed catalog unless upstream supplies one. |
| Group | Optional superset/circuit identity, order, and round count without losing per-exercise order. Allow ordinary straight sets without a group. |
| Set | Planned versus performed values kept distinct; exact or ranged reps, time, or distance; optional set kind (warm-up, working, drop, etc.), rest, and notes. Preserve missing values as missing. |
| Load and effort | Explicit load unit and basis (`total`, `per hand`, `per side`, bodyweight, assisted); separate RPE and RIR with named scales. No inferred 1RM, volume load, or training load. |
| Evolution | Documented unknown-field handling and loss reporting, so unfamiliar upstream exercise or set variants are not silently discarded. |

Any field or grouping exposed as structured input must have upstream read/write evidence, unit semantics, and a round-trip fixture. Until then, keep these details in the readable description above.

Open questions before implementation:

- Which upstream endpoint reads strength sessions, templates, or exercises?
- Which endpoint writes them, and is the write idempotent or safe to retry?
- What schema represents exercises, sets, reps, external load, bodyweight, rest, RPE/RIR, sides, supersets/circuits, and notes?
- Does the calendar expose strength as a `WORKOUT` type, a separate event category, custom items, or another resource?
- What response shape identifies completed strength work versus planned gym notes?
- Which fields are device-owned or computed upstream and must be read-only?

## Evidence required for first-class tools

Before adding strength-training MCP tools, collect black-box or public API evidence for:

1. Read endpoint paths, required query parameters, pagination, and example responses.
2. Write endpoint paths, required fields, partial-update behavior, idempotency semantics, and error shapes.
3. Calendar integration: how planned strength appears in `get_events` and how completed sessions appear in `get_activities` or any dedicated strength endpoint.
4. Supported schema fields and units, including whether exercise names are free text or selected from an upstream catalog.
5. Safe-delete/update behavior so destructive operations can be registered behind icuvisor's existing capability gates.
6. Terse response shape that summarizes the session without dumping large exercise/set payloads unless `include_full: true` is requested.

Safe follow-up probe, with operator approval and a synthetic test athlete only:

1. Create one free-text `WORKOUT` event with `type: "WeightTraining"`, no `workout_doc`, and no real athlete notes.
2. Read it back through event detail and event list endpoints and verify which generic fields round-trip.
3. Complete or import a synthetic strength activity if the upstream UI/device flow permits it, then inspect activities and any documented strength endpoints for exercise/set payload fields.
4. Delete the synthetic calendar item using the existing gated delete flow after capturing redacted schema evidence.

### Synthetic test-athlete probe on 2026-10-02

Using the dedicated `.env-dev` athlete after checking that its athlete ID and API key differ from `.env`, a synthetic `WORKOUT` event with `type: "WeightTraining"` was created with a heading and two `- ` exercise lines, without a `workout_doc` input. The create and detail-read endpoints both returned HTTP 200. The detail response preserved the description exactly, including the blank line and both Markdown list lines. It returned a `workout_doc` object with zero parsed steps. The exact synthetic event was deleted afterward; the delete endpoint returned HTTP 200. No athlete identifiers or credentials were retained in this record.

This verifies API storage and readback of the free-text list, not website/mobile Markdown rendering, execution on a device, or first-class exercise/set round-trip. A signed-in test-athlete UI check remains necessary for rendering claims.

## Implementation criteria

First-class strength/gym tools should not be added until the evidence above can answer each contract question below:

- **Endpoint contract:** documented or black-box verified read/write endpoints, authentication scope, pagination model, stable identifiers, and representative success/error payloads.
- **Schema contract:** typed fields for exercise, set, rep, load, bodyweight, rest, effort, side/limb, superset/circuit grouping, notes, planned-versus-completed state, and units/scale labels.
- **Response contract:** a terse default shape that can summarize one session and a list of sessions without flooding the model context, plus an explicit `include_full: true` expansion for full set-level payloads.
- **Write-safety contract:** idempotency or retry semantics for create/update, partial-update behavior, conflict handling, and destructive-operation behavior that can be gated by icuvisor's existing safety policy.
- **Round-trip contract:** proof that data written by icuvisor is returned by the upstream API without silently losing structured sets, loads, notes, or calendar linkage.

Until that evidence exists, docs and prompts should steer assistants to schedule gym blocks as notes or simple supported calendar events and explicitly avoid inventing structured strength-set support.
