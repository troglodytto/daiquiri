# Handover

Written 2026-08-05. Everything a fresh session needs to pick this up without
re-deriving it. `docs/decisions.md` is the authority on *why*; this is *where we
are* and *what happens next*.

---

## 1. What this is

A Go CLI for an SRE/DevOps hiring challenge. It reads a JSONL capture of ~20,000
Kubernetes events shipped as OpenTelemetry log records, and tells an on-call
engineer what broke and where it started. Brief:
`extras/sre-devops-product-engineer-challenge.pdf`. Deadline: 4 days from
2026-08-03, so **~2 days remain**.

**North star (the author's, D-03):** PagerDuty pings, a dev pulls the thread of
that ping, and walks back to the point where things started going wrong. Stated
as *"far smaller than the PDF"* — but see §7, it is in fact wider, and that
matters for time budgeting.

**Grading weights:** Analysis Report 35% · tool correctness 25% · coalescing
logic 15% · output clarity 10% · code quality + tests 10% · docs 5%.
**60% is reasoning, not code.**

---

## 2. Where we are

| Stage | Package | Status |
|---|---|---|
| 1. decode | `internal/otel` | done, tested |
| 1. classify | `internal/classify` | done, tested |
| 2. coalesce | `internal/group` | done, tested |
| 3. causality | `internal/link` | done, tested, benched, independently audited |
| 4. **diagnose** | — | **not started — next** |
| 5. render | `internal/report` | table only; needs pattern/evidence lines |

`internal/triage` orchestrates. `cmd/triage` does flags and exit codes only.

**Verification as of the last commit:** `gofmt` clean, `go vet` clean,
`go test ./... -race` green across 7 packages.

**Benchmarks** (`go test -bench . -benchmem`): whole pipeline ~140 ms for a
16.5 MB / 20,000-record capture at 119 MB/s, 15 MB and 339k allocations. Against
the brief's 5-second budget that is **35x headroom**. `link.Build` at the real
working point (n=10 findings) is 29.9 µs / 51 allocs; `RootOf` is 2.3 ns and
allocation-free.

---

## 3. Architecture

```
JSONL bytes
   │  internal/otel        streaming decode, per-record resilient
   ▼                       malformed line → counted + skipped, never fatal
[]event.Event                                          ~20,000, ALL retained
   │  internal/classify    table-driven; noise / unclassified / marker / issue
   ▼
   │  internal/group       coalesce on (Kind, Workload, Namespace, Reason, Rule)
   ▼
[]group.Finding                                        3 … 10
   │  internal/link        parent array over findings, one int each
   ▼
link.Forest                                            findings + edges
   │  diagnose             ← STEP 4, TO BUILD
   ▼
   │  internal/report      lipgloss table; styled on a tty, plain when piped
   ▼
stdout
```

Data flows one way. `internal/event` is the leaf and imports nothing internal.

### The data model

```go
triage.Result{
    Ingested, Skipped, Noise, Unrecognised int
    Cluster string
    Elapsed time.Duration
    Records []event.Event   // all 20,000 — noise retained as evidence
    Forest  link.Forest
}

link.Forest{
    Findings []group.Finding // ascending by FirstSeen
    Edges    []link.Edge     // parallel: Edges[i] describes Findings[i]
}

link.Edge{ Parent int; Kind Kind; Evidence string }   // Parent == -1 → root

group.Finding{
    Kind, Workload, Namespace, Reason, Rule string  // the coalescing key
    Category classify.Category
    Severity event.Severity
    Cause    string
    Count     int          // max(Count) per distinct EventUID, summed
    FirstSeen, LastSeen time.Time
    Pods  []string         // ONLY for Pod-kind findings; see §6
    Nodes []string
    Events []event.Event   // every occurrence, so shape stays derivable
}
```

**The causal structure is one `int` per finding.** No graph library, no
pointers, no union-find. The invariant `Edges[i].Parent < i` makes cycles
structurally impossible, and sorting by time *is* the topological sort. Queries
are `Roots()`, `Children(i)`, `RootOf(i)` — three linear scans.

---

## 4. Design philosophy — how decisions get made here

These are load-bearing. Violating one is a defect, not a style preference.

1. **Timeline first.** The timeline is the primary artifact. The diagnosis comes
   from ordering and adjacency, not from findings judged in isolation.
2. **One trail to walk up.** At most one parent per finding — a forest, not a
   general DAG. Two parents turns "pull the thread" into a branching
   interrogation, which is not an answer at 3am.
3. **Do not inflate.** Said repeatedly and emphatically. Sorted slices and
   integer indices over graph libraries; fixed-width buckets over statistics;
   small ordered rule tables over scoring models. If it does not make the
   timeline clearer or the diagnosis better, it does not belong however elegant.
   *Two designs were reverted for breaching this — a two-arena cache layout
   (D-20) and an interval-overlap structure.*
4. **Every edge must be provable** from record text or object identity.
   Co-occurrence is never sufficient (D-35).
5. **Time is a veto, never a proposal** (D-21). Every rule requires a concrete
   shared dimension *first*; time may only reject a candidate, never create one.
6. **Group conservatively; link to join** (D-15). Never merge across a dimension
   you cannot show irrelevant — merging destroys information irreversibly,
   linking preserves it. An incident is a *tree of findings*, not one big
   finding.
7. **Taxonomies are data.** Adding a Kubernetes reason or a causal relationship
   means adding a table row, never editing a `switch`.
8. **Consumers declare interfaces; producers return concrete structs.** There is
   no `interfaces.go`; its appearance is a defect.
9. **Design argued in the open before code** (D-02). Three decisions were
   reversed by looking at the data before implementing — each would have been a
   rewrite otherwise.
10. **TDD with observed RED.** Watch the test fail for the right reason before
    writing the implementation. "I know it would fail" is not evidence.
11. **Every decision recorded at the time it is made**, with the alternatives
    that lost. Reversed decisions stay in the ledger with the reason they fell;
    deleting them hides the reasoning.
12. **Styling is emphasis only.** Strip the escapes from styled output and it is
    byte-identical to plain. The submission requires captured stdout files.
13. **Numbers are never magic.** Every threshold is a named constant with its
    derivation recorded. The 300s causal window was derived from a measured gap,
    not chosen.
14. **No AI attribution trailers on commits.**

### Explicit non-goals

No concurrency or fan-out · no SIMD or custom JSON parser · no second
implementation of anything · no persistence, server or daemon · no config file
(stdlib `flag`, no CLI framework) · no graph library · no bubbletea/spinner
(D-29) · **no per-package LLDs** (D-39, dropped 2026-08-05).

---

## 5. Acceptance data — do not lose these

Independently derived from the raw JSONL *before* the code existed. A change to
any of these is a behaviour change, not a test that needs updating.

**Findings per fixture:** `01→3  02→5  03→6  04→5  05→10  06→6`

**Causal edges (everything else is a root):**

| Fixture | Edge |
|---|---|
| 02 | `BackOff` ← `OOMKilling`, same pods, +7.4s |
| 03 | `Failed[image-pull]` ← `ScalingReplicaSet`, +4.4s; `BackOff[retry]` ← `Failed`, +10.4s **(depth 3)** |
| 04 | `Unhealthy` ×222 ← `ScalingReplicaSet`, +7.3s |
| 05 | six eviction findings ← `NodeHasDiskPressure` on node-4, +15.4s … +97.9s |
| 06 | `FailedScheduling` ×177 ← `ScalingReplicaSet`, +4.7s; `LALALALA` vetoed at +600s |

**Shape data for Step 4** — the whole corpus, measured:

| | count | pods | span | minutes occupied | seconds to capture end |
|---|---|---|---|---|---|
| background floor (all six files) | 1–3 | 1 | 0–16s | 1–2 | 600–1730 |
| real problems (02, 03, 04, 06) | 18–222 | 3–6 | 469–1566s | 5–22 | 0–100 |

Huge margins — any threshold in those gaps works.

**Causal window derivation:** every true edge lands +4.4s…+97.9s; nearest false
candidate is +600s. Any window in (98s, 600s) behaves identically; 300s chosen
mid-gap.

---

## 6. Traps discovered the hard way

- **`ImagePullBackOff` and `ErrImagePull` are not event reasons.** They appear
  only in the *body* of `Failed` events. Classification must read the body.
- **`BackOff` is severity-sensitive.** Warning → crash-loop. Normal →
  image-pull retry. Both critical, neither noise.
- **A Node-kind event has no `k8s.node.name`.** The one record that names a node
  carries it in `k8s.object.name`. Normalised at decode (D-08); without it every
  node-correlation rule silently fails to match the event it exists to find.
- **Every capture, including `01-healthy`, carries the same background floor**
  of 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy` on randomly chosen workloads.
  These are genuine Warnings the taxonomy correctly calls issues, so **"no false
  positives on 01-healthy" cannot be met by reason-based filtering.**
- **05's individual evictions are shape-identical to that floor** (n=1, 1 pod,
  span 0). Shape alone cannot separate them — only the causal link can. This is
  why the suppression predicate is `transient ∧ unexplained`, not `transient`.
- **Pod names are cattle.** `k8s.object.name` is the instance; grouping must roll
  up to the workload or 04's 222 records become dozens of findings.
- **One reason can carry two failure modes.** `Evicted` is disk-pressure *and*
  memory-pressure in 05, on the same workload. Hence `Rule` in the group key.
- **`k8s.event.count` is `1` for all 120,001 records.** The count-incrementing
  path the brief requires is only testable synthetically.
- **Go randomises map iteration.** The finding sort must be **total** (through
  the whole key), or golden tests fail intermittently.
- **`Finding.Pods` is populated only for Pod-kind findings.** Filling it blindly
  put a node in a field called `Pods`, and would have made the same-pod rule
  print evidence reading *"same pod node-4"*.
- **lipgloss:** `lipgloss.NewRenderer(w, termenv.WithProfile(...))` does **not**
  work — lipgloss re-detects from the writer and reverts to Ascii. Use
  `renderer.SetColorProfile(termenv.ANSI)` after construction. Also termenv
  numbers `TrueColor` 0 *down to* `Ascii` 3, so a higher number is less colour.

---

## 7. Scope reality check

The author framed the causal work as *"far smaller than the PDF"*. It is not —
it is **wider**, and this was verified:

- The "look for shared dimensions across findings" sentence sits in **Part 2**,
  addressed to the analyst writing ANALYSIS.md — not to the tool.
- **No acceptance criterion mentions causality or correlation.**
- The brief's own example output is flat: one block per finding, no nesting.

What *is* required: `deploy-correlated failure` and `node issue` are named in
the brief's pattern list, and neither label can be produced without looking
outside the finding. So the correlation is load-bearing; the **rendered tree**
is the extra.

**Consequence for time budgeting:** the tree rendering is the thing that
impresses and should be built *last*, after the graded checkboxes are green.

---

## 8. Next steps, in order

### 8.1 Step 4 — diagnose *(next; ~half a day)*

Add a `Pattern` to `group.Finding` and a suppression decision. Everything needed
is already on `Finding` and `Forest`.

| Pattern | Decided from | Needs the forest? |
|---|---|---|
| transient blip | count, span, pod count | no |
| sustained crash-loop | occupancy over time + reason | no |
| capacity issue | reason + body contains `Insufficient` | no |
| deploy-correlated | root is a deploy marker | **yes** |
| node issue | root is a node condition | **yes** |

**The suppression predicate is `transient ∧ unexplained`** — where unexplained
means `Parent == -1` *and* no children. Shape alone deletes 05; the conjunction
is what silences `01-healthy` without it.

**Root classification** (D-34, needs no threshold):
deploy marker or node condition root → *explained*, high confidence · a failure
root **with** children → *partially explained* (02: "the crash-loop is explained
by OOM kills; what drove the memory growth is not in this capture") · a failure
root with **no** children → *unexplained*.

Open: **O-02**, the numeric thresholds. Margins are large (§5); each still needs
a named constant with its derivation.

### 8.2 Report — prioritised output *(few hours)*

Order by severity, then blast radius. Add the pattern line, the evidence line
and the likely-cause line, in the brief's shape:

```
[CRITICAL] Failed on payment-service (production)
  24 events across 3 pods on 3 nodes, 10:18:04 → 10:28:19, still failing
  Pattern: deploy-correlated · image pull failure
  Likely cause: the 10:17:59.977 rollout of payment-service; first failure
                4.4s after it, zero occurrences before it
```

### 8.3 Deliverables — **all three are hard requirements and all are missing**

- **README** with build/run instructions and example output.
- **Captured raw output** for 04, 05, 06. The brief says *"This is required, not
  optional."* Must be plain text — the renderer already degrades correctly when
  piped.
- **ANALYSIS.md** — 04, 05, 06, ~150 words each: Diagnosis, Evidence quoted from
  the tool's output, remediation for the next five minutes, Confidence plus what
  would raise it. **This is 35% of the grade — protect its time ruthlessly.**

### 8.4 Optional, only if 8.1–8.3 are done

- `--json` (brief calls it "optional but recommended")
- the rendered causal tree with `caused` / `may relate` tiers and per-incident
  sections — the shape the author asked for, deferred by §7
- the undirected-merge case in D-38: orphan findings sharing a stated cause with
  no visible parent. ~20 lines and a map. Does not occur in this corpus.

### 8.5 The working loop

**decisions → implement → test → bench → test → refine → docs.** Decisions land
in `docs/decisions.md` when made. RED observed before GREEN. `gofmt`, `go vet`
and `go test ./... -race` clean before anything is called done, with output
pasted.
