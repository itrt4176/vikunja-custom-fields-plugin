# S2 De-Risking Spike Results (Task 1, the gate)

Run date: 2026-08-29/30 against `itrt4176/vikunja:2.5-plugin-fix-backport` (PR #3549
backport; xorm + xormigrate in the yaegi symbol table). DB wiped and reseeded on
every restart. All spikes were throwaway `main.go` replacements, restored afterward
(`git diff --exit-code main.go` clean).

## Recorded decisions

- `field_config: json` — rung 1 (`xorm:"json null"`) round-trips under yaegi.
- `model-layer: methods` — methods with `*xorm.Session`/`*user.User` args on a
  yaegi struct work; Task 7 uses methods on `CustomFieldDefinition`.
- `model-events: BLOCKED` — plugin-defined event types bridge to the host
  `events.Event` interface but are then UNMARSHALABLE by the host's
  `json.Marshal(event)` in `DispatchWithContext` (pkg/events/events.go:220), so
  the event is never published and listeners receive nothing. Exact error:
  `Failed to dispatch event spike.event: json: unsupported type: func() string`
  (the yaegi wrapper exposes the interpreted `Name() string` method as a
  `func() string` bridge field, which `encoding/json` refuses to serialize).

## Spike 1 — xorm `json null` reflection on a yaegi struct (PASS, rung 1)

`SpikeThing{ID int64; Cfg SpikeCfg}` with `Cfg SpikeCfg \`xorm:"json null"\"` and
`SpikeCfg{Required bool; Min *float64}`, table `spike_things`, route
`/api/v1/plugins/spike/json` (insert + `ID(in.ID).Get(&out)`).

Observed:
- Migration `20260829170000-spike` applied; no reflect error on `Sync2`.
- DB: `CREATE TABLE \`spike_things\` (\`id\` INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, \`cfg\` TEXT NULL)`
  and row `1|{"required":true,"min":5}` — the write path serialized the struct
  correctly, including the `*float64` pointer field.
- Field-level decode check (inside interpreted code): `required: true`,
  `min_nil: false`, `min: 5` — the read path unmarshals back into the struct.
- Caveat for Task 7 handlers: echoing an interpreted struct via `c.JSON` serializes
  as `{}` (same root cause class as spike 3's marshal failure — yaegi wrapper fields
  are not json-visible the same way). Handlers must return maps/primitives, not
  interpreted structs.

## Spike 2 — session-arg method on a yaegi struct (PASS)

`func (r *SpikeRec) Create(s *xorm.Session, u *user.User) error` called from a
route handler via `r.Create(s, u)` after `user.GetCurrentUser(c)`; migration
`20260829170100-spike2`; route `/api/v1/plugins/spike2/method`.

Observed: HTTP 200 `{"id": 1}`; row `id=1` present in `spike_recs`; logs clean
(`Loaded plugin spike2 v0.0.1`, status 200, no reflect/panic lines).

## Spike 3 — plugin event type → host `events.Event` (BLOCKED: seam dead for plugin-defined types)

`SpikeEvent struct{ Msg string }` with `func (SpikeEvent) Name() string
{ return "spike.event" }`, passed to `events.DispatchOnCommit(s, evt)` then
`events.DispatchPending(...)`; route `/api/v1/plugins/spike3/event`.

Observed:
- HTTP 200 `{"dispatched":"spike.event"}` — the HTTP response lies; do not trust it.
- Docker log (03:18:53.784Z):
  `level=ERROR msg="Failed to dispatch event spike.event: json: unsupported type: func() string"`
- What works: the interpreted struct satisfies the host `events.Event` interface
  (`DispatchOnCommit` accepted and queued it; host code called `Name()` — the log
  line contains the interpreted method's return value).
- What fails: `DispatchPending` → `DispatchWithContext` → `json.Marshal(event)`
  (host pkg/events/events.go:220) errors on the yaegi wrapper's method-bridge
  field (`func() string`), the event is never published to the bus, and listeners
  receive nothing. The queueing works but delivery is dead.

Extra probe (spike3b, same run series) — HOST-defined event type constructed inside
plugin code (`&models.TaskUpdatedEvent{Task: &models.Task{ID: 1}, Doer: u}`, route
`/api/v1/plugins/spike3b/event`):
- HTTP 200, no `Failed to dispatch` error.
- The event WAS published and reached listeners: webhook listener logged
  `event task.updated does not contain a project id, not handling webhook`, and the
  `task.updated.mentions` listener ran and failed on my synthetic task's data
  (`Project does not exist [ID: 0]`) — its poisoned-message log dumps the fully
  marshaled payload (`{"task":{...},"doer":{"id":1,"username":"testuser",...}}`).
- Conclusion: host-defined event types populated by plugin code DO work end-to-end
  through `DispatchOnCommit`/`DispatchPending`. A host-defined custom-field event
  type would rescue the seam, but none exists today in `pkg/models` (requires a
  host-side change to the Vikunja fork + image rebuild). Decision deferred to the
  controller.

## Remaining unspiked reflect risks (watch during implementation)

- `s.Find(&slice)` on a yaegi-interpreted slice (ReadOne/ReadAll).
- `s.Table(...).Insert(&[]CustomFieldProject{})` — inserting an interpreted slice.
- `c.Bind(&req)` into a struct with nested `FieldConfig` (`*float64` pointers).
- `s.Table("projects").Where(...).Exist(&models.Project{})` — host-type bean via yaegi.
- `.OrderBy(...)`/`.AllCols()`/`.UseBool()` chain methods.
