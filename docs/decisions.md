# Decision Ledger

Every design decision taken on daiquiri, including the ones that were reversed
and the alternatives that lost. A decision with no recorded alternative is a
decision that was never actually made.

Ledger convention:

- **Accepted** — in force.
- **Reversed** — was accepted, then overturned by evidence. The original stays
  in the ledger with the reason it fell. Deleting it hides the reasoning.
- **Parked** — argued through, not needed yet, may return.
- **Rejected** — considered and declined.
- **Open** — identified, not yet settled.

Evidence is quoted from the six fixtures in `testdata/`. A decision justified
only by taste says so.

---

## Scope and process

### D-01 — Rebuild by hand; adopt only the standards document from the prior attempt
**Status:** Accepted

`~/Projects/daiquiri-ai` is an earlier attempt at this challenge, built largely
by an AI agent. It is abandoned. The current repository is written by hand.

`docs/engineering-standards.md` is copied across verbatim because it encodes the
author's own engineering standard, not the old implementation. Nothing else —
no code, no architecture, no package layout — is inherited.

**Why:** the brief grades reasoning quality, and reasoning you did not do is not
reasoning you can defend in a review.

**Consequence:** `docs/engineering-standards.md` §1.1 describes a package layout
(`group`, `diagnose`, `correlate`, `report`) that this repository has not
adopted and may not adopt. That section is aspirational until an ADR settles the
layout. It also refers to an `AGENTS.md` that does not exist here.

---

### D-02 — Design is argued in the open before code is written
**Status:** Accepted

Structure, data model and thresholds are settled in writing, against fixture
evidence, before implementation. This ledger and `docs/hld.md` are that record.

**Why:** three separate decisions in this ledger (D-11, D-14, D-21) were
*reversed by looking at the actual data*. Each would have been a rewrite if it
had been discovered after implementation instead of before.

---

## The product

### D-03 — The deliverable is a causal trail, not a list of findings
**Status:** Accepted

North star: an engineer paged at 3am pulls on the thread of the alert and walks
back to where the failure started.

**Why:** the brief allocates 35% to the Analysis Report and explicitly warns
that *"a summary that lists counts without an interpretation is half the work"*,
and that *"seemingly unconnected findings are sometimes side effects of the same
underlying issue."* A flat finding list scores badly on both.

**Evidence:** all four non-trivial fixtures resolve to a 2–3 hop trail.

| Fixture | Symptom | Origin |
|---|---|---|
| 02 | `BackOff` ×91 on recommendation-service | `OOMKilling` on the same pods, seconds earlier — trail runs off the front of the capture |
| 03 | `Failed` ×24 / `BackOff` ×18 on payment-service | `ScalingReplicaSet payment-service` @ 10:17:59.977, 4s before |
| 04 | `Unhealthy` ×222 on checkout-service | `ScalingReplicaSet checkout-service → 5` @ 10:21:59.977, 7s before, **zero** symptoms prior |
| 05 | `Evicted` ×10 across 6 workloads / 3 namespaces | `NodeHasDiskPressure node-4` @ 10:15:00.000, 15s before |
| 06 | `FailedScheduling` ×177 on data-pipeline | `ScalingReplicaSet data-pipeline → 12` @ 10:19:59.977, 5s before |

---

### D-04 — Prioritised list at the top, trail underneath; each headline is its origin
**Status:** Accepted

The brief requires a severity-prioritised summary. The north star wants
chronology. Both are satisfied by ordering *incidents* by severity while making
each incident's headline the **origin**, not the symptom.

**Why:** the first line a paged engineer reads is then already the root cause.

---

## Classification (Step 1)

### D-05 — Noise is retained, not counted and dropped
**Status:** Accepted — **reverses the current implementation**

`internal/triage` presently does `res.Noise++` and discards the record. Noise
becomes a retained slice instead.

**Why:** lifecycle events are the evidence for the most quantitative claim the
tool can make.

**Evidence** — one crash-looping pod in 02:

```
10:03:42  Killing      ← classified noise
10:03:43  OOMKilling
10:03:52  BackOff
10:04:27  Started      ← classified noise
10:09:08  Killing      ← classified noise
```

`Started → Killing` is **4m41s**, then **4m12s** on the following cycle. That is
the memory leak's fill rate, and the fact that it is *contracting*. Both
endpoints are noise records. Discard them and the strongest sentence in the
analysis report becomes unavailable.

**Cost:** ~20,000 events held in memory ≈ 1.6 MB, against a 16 MB input file.

**Note:** `internal/classify` §Category already documents this intent —
*"the filter categorizes rather than deletes, so that a record can be excluded
from the report while still being available to correlation"* — and the pipeline
does not honour it. The doc comment was right; the code was not.

---

### D-06 — Noise is retained but never narrated
**Status:** Accepted

Every record is kept. Only issues, markers and unknowns create a finding.
Lifecycle events are evidence attached to pods, reachable when a trail is built.

**Why:** the alternative readings both fail. Dropping noise loses D-05's
evidence; letting noise create findings adds ~2,900 `Pulled` findings per
capture and drowns the report.

---

### D-07 — `Workload()` derives the owner; kind-dispatched, conservative fallback
**Status:** Accepted

A method on `event.Object`. Strips the ReplicaSet hash and pod suffix for
`Pod`, the hash for `ReplicaSet`, returns the name unchanged for everything
else — including anything unmatched.

**Evidence** — measured across all six fixtures:

| Kind | Records | Shape | Match |
|---|---|---|---|
| Pod | 94,686 | `<workload>-<9 hex>-<5 alnum>` | 100% |
| ReplicaSet | 25,311 | `<workload>-<9 hex>` | 100% |
| Deployment | 3 | bare | n/a |
| Node | 1 | bare | n/a |

Twelve distinct workloads, all `[a-z-]+`. **No workload name itself ends in a
hash-shaped segment**, so a single strip is unambiguous. No StatefulSet
ordinals and no DaemonSet-shaped names appear in the corpus.

**Why in `event` and not in the grouper:** owner is a property of identity.
Putting the parse in the grouper would give a package whose job is coalescing a
working knowledge of Kubernetes naming conventions.

**Why conservative:** an unmatched name returns unchanged, so the failure mode
is "no rollup" rather than "wrong rollup". Real clusters use a different hash
alphabet and variable hash length; this keeps that from silently mis-grouping.

**On `regexp`:** called only during grouping, over ≤227 issue records — not
20,000. Roughly 0.1 ms of a 5,000 ms budget. Readability wins; there is nothing
here to optimise.

---

### D-08 — Node identity is normalised at decode
**Status:** Accepted

```go
if e.Object.Kind == "Node" && e.Node == "" {
    e.Node = e.Object.Name
}
```

**Evidence:** the single `NodeHasDiskPressure` record in the corpus carries
`k8s.object.name = "node-4"` and **no** `k8s.node.name`. The one record that
names a node has an empty `Node` field. Without this, every "same node" rule in
Step 3 silently fails to match the very event it exists to find.

---

### D-09 — Unrecognised `Normal` events get their own category
**Status:** Accepted

The classifier's fallback currently routes unrecognised `Normal` events to
`CategoryNoise`. Add `CategoryUnknown`; they group like anything else and are
reported, demoted, at the foot of the output. Unrecognised **Warning** keeps its
present behaviour — surfaced as an issue.

**Why:** `06-test-c.jsonl` contains a deliberately planted record with reason
`LALALALA` whose body reads *"In case there are some new events that we haven't
really recognized and handled, we'd much rather surface it, instead of burying
it"*. The current fallback buries it.

**Why a category and not a special case:** routing unknowns through normal
grouping means 2,000 unknown records collapse to one finding per reason rather
than 2,000 lines.

---

### D-10 — Occurrence count is `max(Count)` per distinct `EventUID`, summed
**Status:** Accepted

**Evidence:** `k8s.event.count` is `1` for **all 120,001 records** across the six
fixtures, and event UIDs are unique. So `Count == len(members)` in every test
available.

**Why implement the harder rule anyway:** the brief states the tool must handle
*"repeated logs as the API server's `count` increments"*. That path cannot be
tested against this corpus and must be covered by a synthetic test. Naïve
`+= Count` double-counts under a count-incrementing pipeline.

---

## Grouping (Step 2)

### D-11 — Group key is `(Kind, Workload, Namespace, Reason)`
**Status:** Accepted

**Why `Workload` rather than `Object.Name`:** `k8s.object.name` for a Pod is the
disposable instance. 04's 225 `Unhealthy` records span 5 pod instances; keyed on
the literal name that is 5 findings, keyed on workload it is 1. The brief's
"same object" means the thing that broke, not the instance it broke on.

**Resulting finding counts** — the acceptance data for Step 2:

| Fixture | Findings |
|---|---|
| 01-healthy | 3 |
| 02-memory-leak | 5 |
| 03-image-pull | 6 |
| 04-test-a | 5 |
| 05-test-b | 9 |
| 06-test-c | 6 |

---

### D-12 — `Node` is **not** in the group key
**Status:** Accepted — **reverses an earlier decision in this same design pass**

Originally accepted on the strength of 05-test-b, where `data-pipeline` is
evicted on node-2 at 10:02:32 (background) and on node-4 between 10:15:25 and
10:19:56 (the incident). Without `Node` in the key those merge into one finding
whose `FirstSeen` is 10:02:32 — *before* the `NodeHasDiskPressure` at 10:15:00
that caused three of them, so the causal link would be correctly rejected and
the diagnosis destroyed.

**Reversed** once the same key was applied to the other five fixtures:

| Fixture | Findings without `Node` | With `Node` |
|---|---|---|
| 02 recommendation-service | 2 (`OOMKilling` n=26, `BackOff` n=91) | **8** — 4 pods on 4 different nodes |
| 03 payment-service | 2 (`Failed` n=24, `BackOff` n=18) | **6** — 3 nodes |
| 04 checkout-service | 1 (`Unhealthy` n=222) | **3** — 3 nodes |
| 05 data-pipeline | 1 impure (n=4, 2 nodes) | 2 clean |

It fixes one fixture and fragments four. A memory leak is a property of the
workload; the node it lands on is incidental.

**How 05 is handled instead:** see D-21.

---

### D-13 — Findings sort on a **total** order
**Status:** Accepted

Sort key: `FirstSeen`, then `Kind`, `Workload`, `Namespace`, `Reason`.

**Why:** Go randomises map iteration order, and 05 contains several evictions
within the same second. Sorting on `FirstSeen` alone leaves ties resolved by
map order, so two runs on the same input produce different output and golden
tests fail *intermittently* — the worst possible way to find this.

---

### D-14 — Markers and unknowns group through the identical path
**Status:** Accepted

`ScalingReplicaSet` → `Kind=Deployment`, `Workload=checkout-service` → one
finding, count 1, `FirstSeen == LastSeen`.

**Why:** no special case, and it makes deploy markers available as causal
candidates in Step 3 for free. A point event is a span of zero duration.

---

### D-15 — Grouping is conservative; linking does the joining
**Status:** Accepted

Never merge across a dimension that cannot be shown irrelevant. Merging
destroys information irreversibly; linking preserves it. **An incident is a set
of related findings, not a single bigger finding.**

**Evidence:** 04's `checkout-service Unhealthy` spans three nodes. Merging is
right there. 05's `data-pipeline Evicted` spans two nodes and merging is wrong
there. No grouping key can tell those apart — only the causal rules can.

---

### D-16 — Grouping is non-windowed; analysis is windowed
**Status:** Accepted

One finding per key spanning the whole capture, with **every** occurrence
timestamp retained so shape can be derived afterwards.

**Why:** keeping only `count + first + last` makes a 15-second burst
indistinguishable from a 27-minute crash loop. 01-healthy's `Unhealthy` finding
(n=3 over 13s) and 02's `BackOff` finding (n=91 over 26m) would both read as
"a finding with a first and last timestamp".

---

## Causality (Step 3)

### D-17 — A trail is a filtered, time-sorted slice of the raw records
**Status:** Accepted — **reverses D-18 through D-20 below**

```go
func Trail(all []Event, f Finding, window time.Duration) []Event
```

Three OR'd conditions — same pod as any of `f.Pods`; a node condition on
`f`'s node; a `ScalingReplicaSet` for `f.Workload` within `window` before
`f.FirstSeen` — then sort by timestamp. One linear scan of 20,000 records,
computed on demand, stored nowhere.

**Why:** after classification the working set is ≤227 records, not 20,000.

| Fixture | Records | Issue records |
|---|---|---|
| 01-healthy | 20,000 | 5 |
| 02 | 20,000 | 122 |
| 03 | 20,000 | 47 |
| 04 | 20,000 | 227 |
| 05 | 20,000 | 16 |
| 06 | 20,001 | 182 |

At n=227 a linear scan is free. Everything in D-18…D-20 was solving a
performance problem that does not exist.

---

### D-18 — Precomputed parent forest over a time-sorted entry array
**Status:** Parked (superseded by D-17)

Every entry carries `Parent int` into a slice sorted by `FirstSeen`, `-1`
meaning origin. The invariant `Parent[i] < i` — a cause is always earlier than
its effect — makes cycles structurally impossible, makes the time sort double as
a topological sort, and makes the ancestor walk an integer loop.

**Why parked and not rejected:** the reasoning survives even though the
implementation is not needed. If the trail-on-demand approach turns out to need
cross-finding root grouping in the report, this is the shape it takes.

---

### D-19 — Forest, not a general DAG: at most one parent
**Status:** Accepted (as a rule; the mechanism is D-17)

Real-world causality is a DAG — several causes contribute to one effect. The
artifact is deliberately a forest.

**Why:** the moment a node has two parents, "pull the thread" stops being a
well-defined gesture. The engineer gets a branching interrogation instead of an
answer:

```
Why was auth-service evicted?
├── node-4 had disk pressure
├── the 10:16 deploy increased replicas
└── the pod exceeded its memory request
```

At 3am that is a research project, not a diagnosis.

**Known limitation, to be stated in the report:** genuine joint causation cannot
be expressed. A pod evicted from node-4 for disk pressure that then cannot
reschedule because the cluster is CPU-starved has two real contributing causes,
and this model must pick one. **No fixture in this corpus exhibits it** — 02, 03,
04, 05 and 06 are all single chains — but the boundary is real and knowing it is
the difference between a decision and a guess.

---

### D-20 — Two-arena physical layout (index ranges into flat occurrence arrays)
**Status:** Rejected (superseded by D-17)

Entries holding `lo, hi int` windows into a flat `[]Occurrence` sorted by
`(entry, time)`, plus a second arena for lifecycle events sorted by `(pod, time)`
and binary-searched.

**Why rejected:** it is a cache-locality optimisation for a working set of ≤227
records that fits in ~30 KB regardless. It bought nothing and cost a great deal
of conceptual weight. Plain `[]Event` and `[]Finding` with linear scans.

---

### D-21 — Time is a veto, never a proposal
**Status:** Accepted

No causal rule may say "these are close in time, therefore related." Every rule
requires a concrete shared dimension first — same pod, same node, same workload
— and time is applied afterwards only to *reject* matches that are too far
apart. Time can remove a candidate edge; it can never create one.

**Evidence:** 04-test-a contains 3 baseline `Unhealthy` events on `data-pipeline`
in the same window as 222 `Unhealthy` events on `checkout-service`. Same
minutes, zero relationship. A rule keyed on temporal proximity reports one
incident where there are two.

---

### D-22 — Rule priority is the outer loop; time proximity is the inner loop
**Status:** Accepted

```go
for _, r := range rules {          // strongest relationship first
    for j := i - 1; j >= 0; j-- {  // then nearest in time
        if r.links(...) { return j }
    }
}
```

**Why:** transposed, "first match" silently becomes *nearest candidate matching
any rule* instead of *strongest rule matching any candidate*. In 05 that would
let a weak same-workload link to the background node-2 eviction win over the
node-condition link that is actually correct.

---

### D-23 — 05's impure eviction finding is disclosed, not split
**Status:** Accepted

`data-pipeline / data / Evicted` in 05 has n=4 across two nodes: one background
eviction on node-2 at 10:02:32 and three incident evictions on node-4 between
10:15:25 and 10:19:56.

The finding stays merged. The causal rule evaluates against **member
occurrences**, not the finding's aggregate `FirstSeen`, and the evidence line
states the split: *"3 of 4 evictions on node-4, 25s–4m56s after
NodeHasDiskPressure."*

**Alternative rejected — episode splitting on a temporal gap.** Splitting a
finding wherever the inter-occurrence gap exceeds some multiple of the median
would separate the two cleanly. It requires a threshold nothing in the data
justifies, and it would fire unpredictably on sparse findings elsewhere.
Disclosing impurity beats inventing a constant to hide it.

---

### D-24 — Union-find: skeleton adopted, algorithm rejected
**Status:** Rejected

Union-find's physical form — `parent []int` with `find()` walking to a root — is
exactly the shape a causal forest takes, and its native question ("are these the
same set?") is exactly 05's question. Both of its optimisations are nonetheless
disqualifying:

- **Path compression** repoints each node directly at its root. The intermediate
  hops *are the product*: `BackOff → OOMKilling → origin` compressed to
  `BackOff → origin` keeps the verdict and throws away the diagnosis.
- **Union by rank** picks whichever parent balances the tree. The requirement is
  the parent that is *true*. Unrelated criteria.

Union-find minus rank minus path compression is just a forest. At n≈20 there is
nothing left for the algorithm to contribute.

---

### D-25 — Fixed-width time buckets derive shape; they never link findings
**Status:** Accepted

Buckets summarise **one** finding's behaviour over time — occupancy gives
burst / sustained / recurring / escalating. Buckets must never be used to
associate two findings; that is D-21 restated at the implementation level.

Buckets are computed on demand from retained occurrence timestamps and stored
nowhere — a second copy of a derivable fact is a second source of truth.

---

## Diagnosis (Step 4)

### D-26 — Shape, not reason, discriminates real problems from background
**Status:** Accepted

**Evidence — every capture, including 01-healthy, carries the same background
floor:**

| Fixture | Background warnings |
|---|---|
| 01-healthy | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy` |
| 02 | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy` |
| 03 | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy` |
| 04 | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy` *(+222 real)* |
| 05 | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy` *(+10 real)* |
| 06 | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy` *(+177 real)* |

The affected workload is randomised per file. These are genuine `Warning`
records that the taxonomy correctly classifies as issues, so the acceptance
criterion *"no false positives on 01-healthy.jsonl"* **cannot** be met by
reason-based filtering.

The only separator is shape: 3 events on 1 pod over 13 seconds, ended long
before capture end, versus 222 events on 5 pods over 8 minutes still firing at
capture end. Shape only exists once records are grouped (D-16).

---

### D-27 — The trail must be able to report that it ran out of data
**Status:** Accepted

Two distinct terminations, reported differently:

- **Origin found** — a cause-shaped record with nothing before it
  (`ScalingReplicaSet`, `NodeHasDiskPressure`). High confidence.
- **Trail truncated** — the head of the chain sits within seconds of the capture
  start. Medium confidence, and the tool names what would raise it.

**Evidence:** 02 has no deploy marker. Capture opens 10:00:01; first
`OOMKilling` is 10:02:37. The honest origin is *"earliest evidence at 10:02:37 —
the cause predates this capture window"*, materially different from 04 where the
origin is a real causal event.

**Why it matters:** the brief requires a confidence level per scenario and asks
what would raise it. A tool that emits that has done on-call reasoning; a tool
whose author writes it by hand afterwards has not.

---

## Open

### O-01 — Package layout
`docs/engineering-standards.md` §1.1 names `group` / `diagnose` / `correlate` /
`report`. The repository currently has `otel` / `event` / `classify` / `triage`.
D-17 collapses correlation into an on-demand function, which may not warrant its
own package. Needs an ADR.

### O-02 — Pattern thresholds
D-26 requires numeric thresholds for burst / sustained / recurring /
deploy-correlated / capacity / node-issue. Each must be a named constant with
its derivation recorded (`engineering-standards.md` §3.2: *"numbers are never
magic"*).

### O-03 — Deploy-correlation window
D-17's `window` parameter. Observed deploy→first-symptom deltas: 03 = 4.4s,
04 = 7.3s, 06 = 4.7s. All under 10 seconds, which suggests a generous window is
safe — but the value needs justifying, not guessing.

### O-04 — `.gitignore`
The repository has none. `output/` (captured tool output) and `.vscode/` are
currently untracked and undecided.
