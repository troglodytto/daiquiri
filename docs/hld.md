# High-Level Design

daiquiri reads a JSONL capture of Kubernetes events shipped as OpenTelemetry log
records, and tells an on-call engineer what broke and where it started.

Governed by `docs/engineering-standards.md`. Every decision below is recorded
with its alternatives in `docs/decisions.md`; this document states *what* the
system is, the ledger states *why* and *what lost*.

Status: **Steps 1, 2 and 5 implemented and tested. Step 3 designed, thresholds
open. Step 4 outlined.**

---

## 1. The problem

~20,000 records per capture, of which **5 to 227** are not routine lifecycle
noise. Each occurrence is an independent log record, the tool must coalesce
them itself.

The brief's own weighting sets the priorities:

| Criterion | Weight |
|---|---|
| Analysis Report — SRE judgment on the three test scenarios | 35% |
| Tool correctness on the three provided scenarios | 25% |
| Event interpretation and coalescing logic | 15% |
| Output clarity and noise discrimination | 10% |
| Code quality and tests | 10% |
| Documentation | 5% |

60% of the grade is reasoning. The tool exists to produce reasoning a human can
check, not to be an elegant codebase.

**North star** (D-03): an engineer paged at 3am pulls the thread of the alert and
walks back to where the failure started.

---

## 2. Pipeline

```
       JSONL bytes
            │
  ┌─────────▼─────────┐
  │  internal/otel    │  streaming wire decode, per-record resilient
  └─────────┬─────────┘  malformed line → counted + skipped, never fatal
            │  []event.Event                              ~20,000
  ┌─────────▼─────────┐
  │ internal/classify │  STEP 1 — table-driven; noise / unclassified / marker / issue
  └─────────┬─────────┘
            │  partitioned, nothing discarded
  ┌─────────▼─────────┐
  │  internal/group   │  STEP 2 — coalesce on (Kind, Workload, Namespace, Reason, Rule)
  └─────────┬─────────┘
            │  []group.Finding                               3 … 10
  ┌─────────▼─────────┐
  │     diagnose      │  STEP 4 — shape over time, severity, likely cause
  └─────────┬─────────┘
            │  []Finding, diagnosed
  ┌─────────▼─────────┐
  │      trail        │  STEP 3 — filter + sort raw records, on demand
  └─────────┬─────────┘
            │  []Incident (findings bucketed by root record)
  ┌─────────▼─────────┐
  │ internal/report   │  STEP 5 — lipgloss table; styled on a tty, plain when piped
  └───────────────────┘
       stdout
```

Data flows one way. `internal/event` is the leaf and imports nothing internal.
`internal/triage` wires the stages; `cmd/triage` handles flags and exit codes
only. Package boundaries beyond Step 2 are **open** (`docs/decisions.md` O-01).

---

## 3. Data model

Three slices. No graphs, no arenas, no index arithmetic (D-20).

```go
records  []event.Event  // 20,000 — every decoded record, retained
findings []group.Finding // 3–10  — coalesced and diagnosed
trail    []event.Event  // filtered + time-sorted, built on demand, stored nowhere
```

### 3.1 `event.Event`

Already implemented. Two additions:

```go
// Workload returns the owning workload, or the object's own name when it has
// none encoded. Kind-dispatched and total: an unmatched name returns unchanged,
// so the failure mode is "no rollup", never "wrong rollup".   (D-07)
func (o Object) Workload() string

// At decode: the one record that names a Node carries it in object.name, not
// node.name. Without this every "same node" rule fails to match it.  (D-08)
if e.Object.Kind == "Node" && e.Node == "" { e.Node = e.Object.Name }
```

### 3.2 `Finding`

```go
type Finding struct {
    // key — D-11, D-28. Node is deliberately absent; see D-12.
    Kind, Workload, Namespace, Reason, Rule string

    Count     int             // max(Count) per distinct EventUID, summed — D-10
    FirstSeen time.Time
    LastSeen  time.Time
    Pods      []string        // distinct instances — blast radius
    Nodes     []string        // observed, not keyed on
    Events    []event.Event   // every member, time-sorted. All timestamps
                              // retained so shape is derivable — D-16

    Severity  event.Severity
    Cause     string
    Pattern   Pattern
}
```

---

## 4. Step 1: Classification

One pass. Partition, never delete (D-05, D-06).

| Category | Destination | Volume |
|---|---|---|
| `CategoryIssue` | grouped, reported | 5 … 227 |
| `CategoryDeployMarker` | grouped, causal candidate | 0 … 1 |
| `CategoryUnclassified` | grouped, demoted to footer — D-09 | 0 … 1 |
| `CategoryNoise` | **retained**, never narrated | ~19,800 |

The taxonomy is data, a map of reason to ordered rules, first match wins.
Adding a Kubernetes reason is adding a table row, never editing a `switch`
(`engineering-standards.md` §1.3).

Two domain traps, both already handled:

- `ImagePullBackOff` and `ErrImagePull` are **not** event reasons. They appear
  in the *body* of `Failed` events, so classification must read the body.
- `BackOff` means crash-loop at `Warning` severity and image-pull retry at
  `Normal`. Both are critical; neither is noise.

---

## 5. Step 2: Grouping

Key: `(Kind, Workload, Namespace, Reason, Rule)`, D-11, D-28. `Rule` is the
taxonomy row that fired, so one reason carrying two failure modes (05's
disk-pressure and memory-pressure evictions) stays two findings. Non-windowed, every
occurrence timestamp retained, D-16. Sorted on a **total** order (`FirstSeen`,
then the full key) because Go randomises map iteration and 05 has same-second
ties, D-13.

### 5.1 Acceptance data

This is the expected output of Step 2 and the basis of the end-to-end tests.

| Fixture | Findings | Principal |
|---|---|---|
| 01-healthy | **3** | all n≤3, 1 pod, ≤13s span — background only |
| 02-memory-leak | **5** | `recommendation-service OOMKilling n=26 pods=4`, `BackOff n=91 pods=4` |
| 03-image-pull | **6** | `payment-service Failed n=24 pods=3`, `BackOff n=18 pods=3` |
| 04-test-a | **5** | `checkout-service Unhealthy n=222 pods=5 nodes=3` |
| 05-test-b | **10** | `NodeHasDiskPressure node-4` + 7 eviction findings, disk- and memory-pressure kept apart |
| 06-test-c | **5** | `data-pipeline FailedScheduling n=177 pods=6`; line 20,001 skipped |

Full expected finding tables live in `testdata/golden/`.

---

## 6. Step 3: Causality

A trail is a **filtered, time-sorted slice of the raw records** (D-17). Not a
structure, a query.

```go
func Trail(all []event.Event, f Finding, window time.Duration) []event.Event
```

Three OR'd predicates, one linear scan, then sort by timestamp:

| # | Predicate | Surfaces |
|---|---|---|
| 1 | `e.Object.Name ∈ f.Pods` | the pods' own `Killing` / `OOMKilling` / `BackOff` / `Pulled` / `Started` — including noise |
| 2 | `isNodeCondition(e) ∧ e.Node ∈ f.Nodes` | `NodeHasDiskPressure`, `NodeNotReady` on the nodes these pods live on |
| 3 | `e.Reason == "ScalingReplicaSet" ∧ e.Object.Name == f.Workload ∧ within(window)` | the rollout that preceded the trouble |

**Time is a veto, never a proposal** (D-21). Every predicate requires a concrete
shared dimension, pod, node, or workload, before time is consulted, and time
is then only allowed to *reject*.

**Root cause** = the earliest *cause-shaped* record in the trail (deploy marker,
node condition, `OOMKilling`), not merely the earliest record, which is usually
a `Pulled`.

**Incidents** = findings bucketed by their root record's event UID. In 05 the
seven disk-pressure eviction findings root to the same `NodeHasDiskPressure` →
one incident, seven symptoms. The eighth eviction finding, memory pressure on
node-2, thirteen minutes earlier, finds no parent and stays a separate,
demoted, transient.

**Termination is reported honestly** (D-27): *origin found* versus *trail
truncated at the capture boundary*, with the second stating what would raise
confidence.

---

## 7. Step 4: Diagnosis

Derived from `f.Events` (time-sorted) plus the trail. Thresholds are **open**
(O-02) and each will be a named constant with its derivation recorded.

| Pattern | Test | Fires on |
|---|---|---|
| **Transient blip** | `Count ≤ 3` ∧ span < 60s ∧ ended well before capture end ∧ 1 pod | the background floor in **all six** fixtures |
| **Sustained** | occupies ≥60% of one-minute buckets ∧ `LastSeen` near capture end | 04 (222/8min, still firing), 02 (91/26min) |
| **Recurring** | `Started`→`Killing` gaps in the trail are regular (±25%) | 02 — the ~4.5 min OOM cycle |
| **Deploy-correlated** | trail contains a `ScalingReplicaSet` for `Workload` ∧ **zero** occurrences before it | 03, 04, 06 |
| **Capacity** | `Reason == FailedScheduling` ∧ body contains `Insufficient` | 06 |
| **Node issue** | trail contains a node condition ∧ ≥3 distinct workloads share that node | 05 (node-4: 6 workloads, 3 namespaces) |

**The first row is what earns "no false positives on 01-healthy"** (D-26). Those
records are genuine `Warning` events the taxonomy correctly calls issues, only
shape separates them from real signal.

---

## 8. Test strategy

Per `engineering-standards.md` §2: RED before GREEN with **observed** failure
output pasted into the commit body; table-driven subtests named for behaviour;
`testify` (`require` for preconditions, `assert` for independent checks);
`-race` always; a passing run prints nothing but pass lines.

### 8.1 Unit

| Under test | Cases |
|---|---|
| `Object.Workload()` | Pod → stripped; ReplicaSet → stripped; Deployment → unchanged; **Node `node-4` → unchanged** (regression: a naive strip mangles it); malformed suffix → unchanged; empty name; a workload whose own name ends in 9 hex chars |
| Node normalisation | `Kind=Node` with absent `node.name` → `Node == Object.Name`; already-populated `Node` untouched |
| Taxonomy | every reason in the brief's table; `BackOff`+`Warning` → crash-loop vs `BackOff`+`Normal` → image-pull retry; `Failed` with each of the three image-pull body markers vs without; `Evicted` with `[DiskPressure]` vs without |
| Classifier fallback | unknown + `Warning` → issue, `Recognised=false`; unknown + `Normal` → `CategoryUnclassified` (the `LALALALA` case, D-09); unnamed + `Normal` → noise |
| Taxonomy totality | property test: every reason's final rule is unconditional, so classification never falls through |

### 8.2 Grouping

| Under test | Cases |
|---|---|
| Must merge | same workload, different pod instances, same reason → one finding (04's five checkout pods) |
| Must **not** merge | different reason; different namespace; different workload |
| Node not keyed | 02's four recommendation-service pods on four different nodes → **one** finding per reason (D-12 regression) |
| Count semantics | **synthetic** — repeated records sharing an `EventUID` with rising `Count` sum to `max`, not to the total. Untestable against the fixtures, where `count` is `1` for all 120,001 records (D-10) |
| Determinism | group the same input 100× and assert byte-identical output — catches the map-ordering trap in D-13, which otherwise fails intermittently |
| Time range | `FirstSeen`/`LastSeen` are the true min/max of members, not first/last encountered |

### 8.3 Shape

Synthetic occurrence distributions, not fixtures, a burst, a sustained run, a
regular cycle, and an escalating ramp, each asserted to classify correctly.
Fixture-driven shape tests would pin thresholds to one dataset.

### 8.4 End-to-end, per fixture

Assert the brief's acceptance criteria directly:

- **01-healthy → zero reported issues, exit 0.** The three background findings
  must be classified `Transient blip` and suppressed. This is the single most
  important test in the suite.
- 02 → primary finding is `recommendation-service` `OOMKilling`+`BackOff`,
  pattern `Sustained`+`Recurring`.
- 03 → primary is `payment-service`, image-pull distinguished from other
  `Failed` by body, pattern `Deploy-correlated`.
- 04, 05, 06 → finding counts match §5.1; root cause matches D-03's table.
- Every fixture → finding count exactly as tabulated.

### 8.4a Report

`TestStylingIsEmphasisOnly` renders the same result twice and asserts the styled
output, with CSI sequences stripped, is byte-identical to the plain one, so no
fact can be carried by colour. `TestRenderPlainHasNoEscapeSequences` asserts the
plain path emits no `ESC` at all, which is what makes the submitted capture
files readable. `TestColumnsFitTheirContent` widens a workload name past its
column and asserts every following column still aligns.

### 8.5 Golden files

`testdata/golden/` for rendered output, regenerated **only** under an explicit
`-update` flag, and every diff read before acceptance. A blindly regenerated
golden is worse than no test. Output must degrade to clean plain text when
stdout is not a terminal, the submission requires captured files, and the
goldens pin the plain path.

### 8.6 Benchmarks

`-benchmem` on ingest and grouping. The 5-second budget for 20,000 records is an
acceptance criterion, so it gets a benchmark rather than a hope. Current
measurement: full decode of a 16 MB capture ≈ 115 ms.

---

## 9. Non-goals

Decisions already taken; reintroducing one as an optimisation is a regression.

- No concurrency or fan-out, sequential decode is ~115 ms against a 5,000 ms
  budget.
- No SIMD, no custom JSON parser.
- No second implementation of anything.
- No persistence, server or daemon. It reads a file and exits.
- No configuration file. Flags only, stdlib `flag`, no CLI framework.
- No graph library (D-20, D-24).
