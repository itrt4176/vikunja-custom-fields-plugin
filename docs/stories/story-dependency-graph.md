# Story Dependency Graph

The full dependency graph of all non-rejected stories, plus the critical path
expressed as a checklist.

Rejected stories (S4 Config File Field Definitions, S6 Admin Field Management UI)
are excluded entirely — their responsibilities were folded into S8 (config
whitelist) and S9 (plugin-served management UI) respectively.

## Full Graph

Edges are hard "must come after" dependencies. The minimal DAG below has
transitive edges collapsed; the complete per-story table follows.

```
                    S1 (Plugin Foundation)
                    │
                    ▼
                    S8 (Config Whitelist)
                    │
                    ▼
                    S2 (Field Definition API)
                   ╱ ╲
                  ╱   ╲
                 ▼     ▼
   S3 (Task Field   S9 (Management UI)
    Values API)        │
                 │     │
                 ▼     │
   S5 (Custom Fields   │
     on Task Detail)   │
                 │     │
                 └──┬──┘
                    ▼
                S7 (Build, Deploy & Document)
```

### Minimal DAG

```
S1 ──▶ S8 ──▶ S2 ─┬─▶ S3 ──▶ S5 ──┐
                  └──▶ S9 ────────┴──▶ S7
```

### Valid parallel pairs

Only two story pairs are genuinely parallel-safe — neither is an ancestor or
descendant of the other in the hard-dependency graph:

- **S3** ↔ **S9** (Task Field Values API ↔ Management UI)
- **S5** ↔ **S9** (Custom Fields on Task Detail ↔ Management UI)

> Note: earlier drafts of several stories listed parallel pairs that the
> hard dependencies already forbade (e.g. "S3 parallel with S8", when S3
> depends on S2 which depends on S8). Those entries have been corrected in the
> source stories. A "can run in parallel with" claim is only valid if the named
> story is neither an ancestor nor a descendant.

## Dependency Table

| Story | Must come after | Must come before | Can run in parallel with |
|-------|-----------------|------------------|--------------------------|
| **S1** Plugin Foundation | — | S2, S3, S5, S7, S8, S9 (all) | none |
| **S2** Field Definition API | S1, S8 | S3, S9 | none |
| **S3** Task Field Values API | S2 | S5 | S9 |
| **S5** Custom Fields on Task Detail | S3 | S7 | S9 |
| **S7** Build, Deploy & Document | S1, S2, S3, S5, S8, S9 (all) | — | none |
| **S8** Config Whitelist | S1 | S2, S9 | none |
| **S9** Management UI | S2, S8 | S7 | S3, S5 |

## Critical Path

The longest dependency chain — the spine that gates the entire epic. Each story
on the path blocks the next; nothing on this chain can be reordered or skipped
without slipping the final S7 ship date.

**S1 → S8 → S2 → S3 → S5 → S7**

- [x] **S1** — Plugin Foundation
- [x] **S8** — Config Whitelist
- [x] **S2** — Field Definition API
- [x] **S3** — Task Field Values API
- [ ] **S5** — Custom Fields on Task Detail
- [ ] **S7** — Build, Deploy & Document

## Notes

- **S8 is a stealth blocker.** Priority 70, but it sits on the critical path
  between S1 and S2 — S2 cannot begin until both S1 and S8 are complete. It
  gates the entire backend CRUD layer.
- **Two parallel UI tracks fork off S2.** S5 (frontend task-detail view, fed by
  S3) and S9 (plugin-served management UI, fed by S8) are the only pairable
  stories once their backends exist. S9 is the common parallel partner for both
  S3 and S5.
- **S7 is the universal sink.** It depends on every active story and is the
  final integration/deploy gate.
