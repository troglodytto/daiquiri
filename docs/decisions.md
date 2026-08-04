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
_reversed by looking at the actual data_. Each would have been a rewrite if it
had been discovered after implementation instead of before.

---

## The product

### D-03 — The deliverable is a causal trail, not a list of findings

**Status:** Accepted

North star: an engineer paged at 3am pulls on the thread of the alert and walks
back to where the failure started.

**Why:** the brief allocates 35% to the Analysis Report and explicitly warns
that _"a summary that lists counts without an interpretation is half the work"_,
and that _"seemingly unconnected findings are sometimes side effects of the same
underlying issue."_ A flat finding list scores badly on both.

**Evidence:** all four non-trivial fixtures resolve to a 2–3 hop trail.

| Fixture | Symptom                                         | Origin                                                                                      |
| ------- | ----------------------------------------------- | ------------------------------------------------------------------------------------------- |
| 02      | `BackOff` ×91 on recommendation-service         | `OOMKilling` on the same pods, seconds earlier — trail runs off the front of the capture    |
| 03      | `Failed` ×24 / `BackOff` ×18 on payment-service | `ScalingReplicaSet payment-service` @ 10:17:59.977, 4s before                               |
| 04      | `Unhealthy` ×222 on checkout-service            | `ScalingReplicaSet checkout-service → 5` @ 10:21:59.977, 7s before, **zero** symptoms prior |
| 05      | `Evicted` ×10 across 6 workloads / 3 namespaces | `NodeHasDiskPressure node-4` @ 10:15:00.000, 15s before                                     |
| 06      | `FailedScheduling` ×177 on data-pipeline        | `ScalingReplicaSet data-pipeline → 12` @ 10:19:59.977, 5s before                            |

---

### D-04 — Prioritised list at the top, trail underneath; each headline is its origin

**Status:** Accepted

The brief requires a severity-prioritised summary. The north star wants
chronology. Both are satisfied by ordering _incidents_ by severity while making
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
the memory leak's fill rate, and the fact that it is _contracting_. Both
endpoints are noise records. Discard them and the strongest sentence in the
analysis report becomes unavailable.

**Cost:** ~20,000 events held in memory ≈ 1.6 MB, against a 16 MB input file.

**Note:** `internal/classify` §Category already documents this intent —
_"the filter categorizes rather than deletes, so that a record can be excluded
from the report while still being available to correlation"_ — and the pipeline
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

| Kind       | Records | Shape                          | Match |
| ---------- | ------- | ------------------------------ | ----- |
| Pod        | 94,686  | `<workload>-<9 hex>-<5 alnum>` | 100%  |
| ReplicaSet | 25,311  | `<workload>-<9 hex>`           | 100%  |
| Deployment | 3       | bare                           | n/a   |
| Node       | 1       | bare                           | n/a   |

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
`LALALALA` whose body reads _"In case there are some new events that we haven't
really recognized and handled, we'd much rather surface it, instead of burying
it"_. The current fallback buries it.

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
_"repeated logs as the API server's `count` increments"_. That path cannot be
tested against this corpus and must be covered by a synthetic test. Naïve
`+= Count` double-counts under a count-incrementing pipeline.

---

## Grouping (Step 2)

### D-11 — Group key is `(Kind, Workload, Namespace, Reason)`

**Status:** Accepted, extended by D-28 which adds `Rule`

**Status:** Accepted

**Why `Workload` rather than `Object.Name`:** `k8s.object.name` for a Pod is the
disposable instance. 04's 225 `Unhealthy` records span 5 pod instances; keyed on
the literal name that is 5 findings, keyed on workload it is 1. The brief's
"same object" means the thing that broke, not the instance it broke on.

**Resulting finding counts** — the acceptance data for Step 2:

| Fixture        | Findings |
| -------------- | -------- |
| 01-healthy     | 3        |
| 02-memory-leak | 5        |
| 03-image-pull  | 6        |
| 04-test-a      | 5        |
| 05-test-b      | 10       |
| 06-test-c      | 6        |

---

### D-12 — `Node` is **not** in the group key

**Status:** Accepted — **reverses an earlier decision in this same design pass**

Originally accepted on the strength of 05-test-b, where `data-pipeline` is
evicted on node-2 at 10:02:32 (background) and on node-4 between 10:15:25 and
10:19:56 (the incident). Without `Node` in the key those merge into one finding
whose `FirstSeen` is 10:02:32 — _before_ the `NodeHasDiskPressure` at 10:15:00
that caused three of them, so the causal link would be correctly rejected and
the diagnosis destroyed.

**Reversed** once the same key was applied to the other five fixtures:

| Fixture                   | Findings without `Node`               | With `Node`                         |
| ------------------------- | ------------------------------------- | ----------------------------------- |
| 02 recommendation-service | 2 (`OOMKilling` n=26, `BackOff` n=91) | **8** — 4 pods on 4 different nodes |
| 03 payment-service        | 2 (`Failed` n=24, `BackOff` n=18)     | **6** — 3 nodes                     |
| 04 checkout-service       | 1 (`Unhealthy` n=222)                 | **3** — 3 nodes                     |
| 05 data-pipeline          | 1 impure (n=4, 2 nodes)               | 2 clean                             |

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
tests fail _intermittently_ — the worst possible way to find this.

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

| Fixture    | Records | Issue records |
| ---------- | ------- | ------------- |
| 01-healthy | 20,000  | 5             |
| 02         | 20,000  | 122           |
| 03         | 20,000  | 47            |
| 04         | 20,000  | 227           |
| 05         | 20,000  | 16            |
| 06         | 20,001  | 182           |

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
— and time is applied afterwards only to _reject_ matches that are too far
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

**Why:** transposed, "first match" silently becomes _nearest candidate matching
any rule_ instead of _strongest rule matching any candidate_. In 05 that would
let a weak same-workload link to the background node-2 eviction win over the
node-condition link that is actually correct.

---

### D-23 — 05's impure eviction finding is disclosed, not split

**Status:** Reversed by D-28

`data-pipeline / data / Evicted` in 05 has n=4 across two nodes: one background
eviction on node-2 at 10:02:32 and three incident evictions on node-4 between
10:15:25 and 10:19:56. This decision kept the finding merged and disclosed the
split in the evidence line — _"3 of 4 evictions on node-4, 25s–4m56s after
NodeHasDiskPressure"_ — on the reasoning that disclosing impurity beats
inventing a threshold to hide it.

**Why it fell:** it assumed the two evictions were the same failure mode
observed in two places. Reading the bodies showed they are not.

| Time     | Node   | Body                                                     |
| -------- | ------ | -------------------------------------------------------- |
| 10:02:32 | node-2 | `The node was low on resource: memory. Container pipe...` |
| 10:15:25 | node-4 | `The node had condition: [DiskPressure].`                 |

The taxonomy already separates these — `evicted/memory-pressure` and
`evicted/disk-pressure` are distinct rules — so no new threshold or heuristic is
needed to tell them apart. The information was there and the key was throwing it
away.

**Alternative still rejected — episode splitting on a temporal gap.** Splitting
wherever the inter-occurrence gap exceeds some multiple of the median would also
separate these two, but it requires a constant nothing in the data justifies and
would fire unpredictably on sparse findings elsewhere.

---

### D-28 — The fired taxonomy rule is part of the group key

**Status:** Accepted — supersedes D-23

Final key: **`(Kind, Workload, Namespace, Reason, Rule)`**, where `Rule` is a
short stable identifier for the taxonomy row that classified the record.

**Why:** one reason can carry two failure modes. Measured across the corpus,
`Evicted` splits into disk-pressure and memory-pressure on the same workload
(`data-pipeline`: 3 and 5; `batch-reporter`: 1 and 1). Two different failure
modes are not one fact about a workload, and merging them drags the incident
finding's `FirstSeen` thirteen minutes earlier than the event that explains it —
at which point the cause postdates its effect and the causal link is correctly
rejected.

**Why `Rule` and not `Cause`:** `Cause` is display text written for a human
reader. Keying on it would mean rewording a sentence silently changes how
records coalesce. `Rule` is an identifier whose only job is identity.

**Cost — verified, none.** Every other group in the corpus matches a single
rule, so nothing else fragments: 04's 222 `Unhealthy` are all readiness probes,
03's 24 `Failed` are all image-pull, 02's 91 `BackOff` are all crash-loop.
The only fixture whose count changes is 05, from 9 findings to 10.

**This is what D-12 promised.** The node was removed from the key on the grounds
that a real fix existed for 05; this is it, and unlike the node it costs nothing
elsewhere.

---

### D-24 — Union-find: skeleton adopted, algorithm rejected

**Status:** Rejected

Union-find's physical form — `parent []int` with `find()` walking to a root — is
exactly the shape a causal forest takes, and its native question ("are these the
same set?") is exactly 05's question. Both of its optimisations are nonetheless
disqualifying:

- **Path compression** repoints each node directly at its root. The intermediate
  hops _are the product_: `BackOff → OOMKilling → origin` compressed to
  `BackOff → origin` keeps the verdict and throws away the diagnosis.
- **Union by rank** picks whichever parent balances the tree. The requirement is
  the parent that is _true_. Unrelated criteria.

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

| Fixture    | Background warnings                                       |
| ---------- | --------------------------------------------------------- |
| 01-healthy | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy`               |
| 02         | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy`               |
| 03         | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy`               |
| 04         | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy` _(+222 real)_ |
| 05         | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy` _(+10 real)_  |
| 06         | 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy` _(+177 real)_ |

The affected workload is randomised per file. These are genuine `Warning`
records that the taxonomy correctly classifies as issues, so the acceptance
criterion _"no false positives on 01-healthy.jsonl"_ **cannot** be met by
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
`OOMKilling` is 10:02:37. The honest origin is _"earliest evidence at 10:02:37 —
the cause predates this capture window"_, materially different from 04 where the
origin is a real causal event.

**Why it matters:** the brief requires a confidence level per scenario and asks
what would raise it. A tool that emits that has done on-call reasoning; a tool
whose author writes it by hand afterwards has not.

---

## Output (Step 5)

### D-29 — lipgloss table, no bubbletea and no spinner

**Status:** Accepted

`charmbracelet/lipgloss` renders a styled findings table. `bubbletea` and
`bubbles` are **not** taken as dependencies, and there is no spinner.

**Why:** the pipeline completes a 20,000-record capture in 131–138 ms. A spinner
over that renders roughly two frames and reads as a flicker, not as progress.
bubbletea is not a spinner library either — it is an event loop that takes over
the terminal, which sits badly beside the "no CLI framework" non-goal.

Output clarity is 10% of the grade and the table is where that is won. The
spinner would have bought a dependency, an event loop and a lifecycle, for
something no one would see.

**Alternative considered — spinner above a duration threshold.** Start a spinner
only if the pipeline is still running after ~300 ms, so it never appears on
these captures but would on a genuinely large one. Honest, but it is machinery
for a case that does not exist in the submission. Revisit if input sizes grow.

---

### D-30 — Styling is TTY-conditional and emphasis only

**Status:** Accepted

Two constructors. `report.New` styles only when its writer is a character
device and `NO_COLOR` is unset; a pipe, a file and a test buffer all render
plain. `report.NewStyled` always styles, and exists so the property below is
testable at all.

**Why:** the brief requires captured stdout files for scenarios 04, 05 and 06 —
*"This is required, not optional"* — and a grader opening a file full of escape
sequences is reading noise. `docs/engineering-standards.md` §5 states the same
rule independently.

**The property, and how it is enforced:** strip the CSI sequences from the
styled rendering and it is byte-identical to the plain one. No fact is ever
carried by colour alone. `TestStylingIsEmphasisOnly` asserts exactly that, and
`TestRenderPlainHasNoEscapeSequences` asserts the plain path emits no `ESC` at
all. Layout is computed identically in both modes; the only branch is whether a
style is applied to an already-padded string.

Verified end to end: piped output contains **0** escape sequences, the same
command under a pty contains **14** styled lines.

**Implementation note worth keeping.** The colour profile must be forced with
`renderer.SetColorProfile(termenv.ANSI)` *after* construction.
`lipgloss.NewRenderer(w, termenv.WithProfile(...))` does not work — lipgloss
re-detects from the writer and overrides it back to Ascii, so `NewStyled` would
silently become identical to `New` and the emphasis-only test would pass
vacuously. Measured: the option path yields termenv profile 3, which is Ascii —
termenv numbers `TrueColor` 0 down to `Ascii` 3, so a larger number is *less*
colour, which is easy to misread.

---

## Causality (Step 3)

### D-31 — The causal structure is a parent-array forest over findings

**Status:** Accepted — un-parks D-18, narrows D-17

```go
type Forest struct {
    Findings []group.Finding // ascending by FirstSeen
    Edges    []Edge          // parallel; Edges[i] describes Findings[i]
}
type Edge struct { Parent int; Kind Kind; Evidence string }
```

One `int` per finding. `Parent == -1` is a root.

**Invariant:** `Edges[i].Parent < i`, because `Build` only scans backwards from
`i`. Everything safety-related falls out of it — cycles are structurally
impossible (no visited set, no cycle check, no error path), `Findings` is
already a topological order because sorting by time *is* the topological sort,
and `Children(i)` need only scan forward from `i+1`.

**Grain: finding→finding, not finding→record.** D-18 was parked as superseded by
D-17's trail-on-demand; the simulation showed it was the right shape at the
wrong grain. Both now survive at different jobs — the forest is the causal
structure, and raw-record queries remain the *evidence* mechanism for quoting
things like 02's 4m41s leak interval.

**Why `Forest` wraps both slices** rather than `Build` returning a bare
`[]Edge`: the two are index-coupled, and handing them back separately makes
"same length, same order" an unenforced convention that one misplaced
`sort.Slice` breaks silently. The failure mode would be a *wrong diagnosis*, not
a crash. Only `Build` constructs the pair.

---

### D-32 — Rule priority is dimension specificity: pod > node > workload

**Status:** Accepted

Rule priority is the outer loop, time proximity the inner (D-22).

**The fight:** the alternative was ordering by *parent kind* — cause-shaped
parents (deploy markers, node conditions) before symptom parents. It loses 03's
depth. `BackOff`[image-pull-retry] matches both the same-pod rule (against
`Failed`) and the deploy rule (against `ScalingReplicaSet`). Under specificity
ordering the chain is three deep:

```
ScalingReplicaSet → Failed[image-pull] → BackOff[retry]
```

Under kind ordering it flattens to two siblings under the deploy, losing the
claim that the retry is caused by the pull failure rather than by the rollout.

**Known limitation, recorded not hidden:** the same-pod rule links to *any*
earlier finding sharing a pod, so an unrelated earlier symptom on that pod could
become its parent. Checked against the corpus and it does not occur — 05's
`batch-reporter` Unhealthy (pod `19cb12bab-7143a`) and its eviction (pod
`820090759-b420b`) are different instances, so the node-condition rule correctly
wins. Guarding it needs a reason-precedence table, which is inflation for a case
with no evidence behind it.

---

### D-33 — A single 300-second window, derived from the data

**Status:** Accepted — closes O-03

Measured across all six captures:

| | value |
|---|---|
| every true edge | +4.4, +4.7, +7.3, +7.4, +10.4, +15.4, +25.8, +48.7, +85.4, +88.6, **+97.9s** |
| nearest false candidate | 06's `LALALALA`, **+600s** after the data-pipeline rollout |

Any window in **(98s, 600s)** produces identical results. 300s sits mid-gap —
3× the largest true edge, half the smallest false one.

**One constant, not three.** Per-rule windows would be tunable to the corpus and
therefore overfitted to it; a single number with a stated margin is honest about
how much evidence actually backs it.

---

### D-34 — Roots are classified by what they are, not by when they are

**Status:** Accepted — supersedes D-27's time-based truncation test

| Root | Meaning | Fires on |
|---|---|---|
| deploy marker or node condition | **explained** — a cause was reached | 03, 04, 05, 06 |
| a failure *with* children | **partially explained** — proximate cause found, nothing upstream | 02 (`OOMKilling`) |
| a failure with *no* children | **unexplained** | 01's three, and every background finding |

**Why it beats D-27:** that version tested whether the root sat within seconds of
the capture start. 02's first `OOMKilling` is 156s in — not "seconds" — so the
heuristic would have mis-called it, and it needed a threshold nothing justified.
This version needs **no threshold at all** and produces the honest sentence
directly: *"the crash-loop is explained by OOM kills; what drove the memory
growth is not in this capture."*

"Unexplained + transient" is also exactly the suppression predicate Step 4 needs
to silence `01-healthy` without deleting 05.

---

### D-35 — An edge must be provable from record text or object identity

**Status:** Accepted — strengthens D-21

Co-occurrence is never sufficient. Every rule must point at something a human
can read in the data:

| Rule | Proof | Strength |
|---|---|---|
| same pod | **identity** — same `k8s.object.name` | inferred (adjacency) |
| node condition | **named** — `NodeHas<X>` ↔ child body contains `[<X>]` | stated |
| deploy marker | **named** — deploy body gives `<workload>-<hash>`; child pods are `<that>-<suffix>` | stated |

**Evidence, 05-test-b:**

```
10:15:00.000  node-4  NodeHasDiskPressure  "Node node-4 status is now: NodeHasDiskPressure"
10:15:15.359  node-4  Evicted              "The node had condition: [DiskPressure]."   ×10
10:02:32.345  node-2  Evicted              "The node was low on resource: memory. ..."  ← control
```

The evicted pod's own record names the condition that evicted it. The node-2
eviction is the control that proves the rule discriminates: same `Evicted`
reason, different named cause, correctly unlinked — and it would still be
rejected even if it were on node-4.

**Why this matters beyond correctness:** the brief asks for *"Evidence: which
events from your tool's output led you there? Quote specific findings."* A
stated-cause edge yields a verbatim quote. A co-occurrence edge yields a
hand-wave.

**Honest asymmetry:** the same-pod rule is the *weakest* of the three, not the
strongest. `OOMKilling → BackOff` is identity plus adjacency — the `BackOff`
body says "Back-off restarting failed container" and never names the OOM. It
still outranks the others for the depth reason in D-32, but the difference is
real and must flow into the confidence Step 4 reports.

---

### D-36 — The deploy rule keys on the named ReplicaSet, not the workload

**Status:** Accepted — supersedes the same-workload rule specified earlier in this pass

The deploy body names the exact ReplicaSet it scaled. Failing pods must belong
to **that** ReplicaSet, not merely to the same service.

```
03: "Scaled up replica set payment-service-9e3f1a2b8 to 3"
    failing:    payment-service-9e3f1a2b8-{005e2,45cbb,b8c2e}      3/3 from that RS
    background: auth-service-df386e8ed-…, data-pipeline-c7a123947-…  different RS
04: 5/5 from checkout-service-7d4f8b9c5        06: 6/6 from data-pipeline-3c7d2e1a9
```

**Why:** "same workload" links a service that merely *happened* to be deployed
recently. "Pods from the ReplicaSet this rollout created" is the actual causal
claim, and it is the difference between *"the deploy caused this"* and *"this
service was deployed at some point"*.

---

### D-37 — Two edge tiers, not a confidence score

**Status:** Accepted

`Caused` — the record text names the link, or it is the same object.
`MayRelate` — a shared dimension inside the window, with nothing in the text
connecting them.

**Why not a numeric score:** a score is a model, and a model needs calibration
data that does not exist here. Two tiers are each defensible from the rule that
fired.

**The tier is empty on this corpus, and stays empty.** Nothing sits on node-4 in
05 but the condition and its ten evictions, and every deploy edge is textually
provable. Loosening a rule to populate the tier would manufacture exactly the
weak edges D-21 exists to prevent.

---

### D-38 — Complexity, scale, and why no graph library

**Status:** Accepted

**Time and space — measured, not estimated.** `go test -bench . -benchmem`,
Linux, 16 logical cores.

`link.Build`, findings synthesised in the shape of a real capture (a rollout
followed by failures on its pods, so about half acquire a parent):

| n (findings) | ns/op | B/op | allocs/op |
|---|---|---|---|
| **10** (the real working point) | 29,868 | 3,634 | 51 |
| 100 | 1,411,987 | 130,530 | 1,561 |
| 1,000 | 32,289,829 | 1,497,458 | 17,766 |

Read side, at n=1000: `Roots` 2,801 ns / 10 allocs; `RootOf` **2.3 ns**, zero
allocations — an integer loop over a slice already in cache.

Whole pipeline, capture read into memory and replayed from a `bytes.Reader` so
disk is excluded:

| fixture | ns/op | throughput | B/op | allocs/op |
|---|---|---|---|---|
| 01-healthy | 140,288,461 | 118 MB/s | 15,154,132 | 338,688 |
| 04-test-a (heaviest, 227 reportable) | 138,623,705 | 120 MB/s | 15,368,683 | 339,221 |
| 05-test-b | 140,015,896 | 118 MB/s | 15,163,105 | 338,797 |

**~140 ms against the brief's 5-second budget: 35x headroom.** Allocation is
~17 per record, dominated by JSON unmarshalling into the wire struct; at this
margin there is nothing worth reclaiming.

**Two corrections to what this entry originally claimed**, both from estimates
that were never measured:

- It said n=1,000 would be "a few ms". It is **32 ms** — roughly ten times
  slower. Each comparison is a function call doing slice intersection and string
  work, not a bare integer compare, and the estimate assumed the latter.
- It extrapolated n=5,000 at "~100ms". Measured scaling puts it nearer 300 ms.
  The figure is dropped rather than re-estimated; if that size ever matters it
  should be measured.

**The scaling is sub-quadratic in practice.** Ten times the findings costs 47x
then 23x, not the 100x a true `O(n²)` would. The backwards scan stops at the
first match and most findings find a parent within a few steps, so the inner
loop rarely runs to completion. The worst case remains quadratic; the observed
case is not.

**Why n stays small.** n counts *findings*, not records. Findings grow with the
number of distinct `(workload, namespace, reason, rule)` tuples — that is,
distinct failure modes — not with event volume. A cluster emitting ten times the
events has roughly the same number of distinct failure modes. Observed: 20,000
records → 3–10 findings. The quadratic term is over a quantity that does not
track input size.

**Why overlap-based structures are wrong, not merely unnecessary.** Interval
trees and sweep lines answer "which intervals overlap". Measured against the
corpus, that is the wrong question in both directions:

| Quadrant | Corpus example | Overlap-based verdict |
|---|---|---|
| overlapping, **unrelated** | 02: `order-service/Unhealthy` 10:17:02–14 on node-6 sits **inside** `OOMKilling` 10:02:37–10:28:20 which spans node-6 | false positive |
| overlapping, related | 02 `OOMKilling`/`BackOff`; 03 `Failed`/`BackOff` | correct |
| **non-overlapping, related** | every deploy edge and the whole 05 incident — causes are *instants*, effects begin after, **zero overlap** | **all missed** |
| non-overlapping, unrelated | the background floor | correct |

Overlap would miss the four best diagnoses in the corpus and invent a link
between a memory leak and an unrelated readiness probe. The real criterion is
**ordering plus a named shared dimension**, and ordering is one scalar
comparison — which is precisely why a sorted slice suffices.

**Time enters in exactly two places, never as a proposer:** as ordering (a
parent must be strictly earlier, which is also what makes cycles impossible),
and as a veto window that can only reject a candidate a dimension already
proposed.

**Why not union-find.** Its physical form is the same `parent []int`. Both of
its optimisations disqualify it: path compression repoints each node at its root
and deletes the intermediate hops, which *are the product*; union by rank picks
whichever parent balances the tree, when the requirement is the parent that is
true. Stripped of both it is just this forest, and at n ≤ 10 there is nothing
left for the algorithm to contribute.

**The one case union-find shape would address, named as the boundary:** an
undirected merge — *"these are one incident but the cause is not in the data."*
If 05's capture had begun at 10:15:10 the `NodeHasDiskPressure` record would be
absent, leaving six orphan evictions all naming `[DiskPressure]` on node-4:
obviously one incident, six roots. Cost to handle: ~20 lines grouping orphans by
(stated cause, shared dimension) under a synthetic root — **a map, not a
library**. Does not occur in this corpus; Step 2's workload rollup already
merges the within-workload version. Deferred, not forgotten.

**What would force a real graph:** multiple parents (joint causation — D-19, no
fixture has it), cycles (impossible when parents are strictly earlier),
reachability or shortest-path at scale (n ≤ 10), or incremental update as
records stream (this is a batch tool that reads a file and exits). None apply.

---

## Process amendments

### D-39 — No per-package LLDs; the ledger carries their content

**Status:** Accepted — amends `docs/engineering-standards.md` §3.1

§3.1 required `docs/lld/<package>.md` written *before* each package. None were
written for `group`, `link` or `report`. Rather than leave the standard saying
one thing while the repository does another, the standard is amended.

**Why:** the ledger already carries what an LLD is for — types, algorithm, edge
cases, failure modes, and the evidence behind each threshold. D-31 through D-38
are the `link` LLD in everything but filename. A parallel per-package document
would be a second copy of the same reasoning, and the second copy is the one
that goes stale.

**The alternatives, and why they lost:**

- _Write the three missing LLDs retroactively._ Compliant on paper, but a
  retroactive LLD documents what was built rather than designing what will be —
  which is the whole point of §3.1's "before". Half a day spent producing a
  document that cannot do its job.
- _Accept the deviation silently._ Contradicts the standing rule that a
  deviation is a blocking defect, and leaves a binding document that is untrue.

**What is unchanged:** design is still argued in the open before code (D-02),
every decision still lands in the ledger at the time it is made, every package
still states its prohibitions in `doc.go`, and every exported identifier still
carries godoc.

---

### D-40 — The pattern is a diagnose-stage verdict, not a field on `Finding`

**Status:** Accepted — corrects the plan recorded in `handover.md` §8.1

The plan said "add a `Pattern` to `group.Finding`". That is wrong on two counts,
both of which only became visible once the forest existed:

1. `group`'s own prohibition is that it *"does not interpret shape over time,
   rank findings, or decide what caused what"*. A pattern is exactly the first
   of those.
2. Two of the five patterns — deploy-correlated and node issue — are read off
   `RootOf(i)`. `group` runs before `link` and has no forest to consult. The
   field would have to be filled in later by someone else, which is a mutable
   hole in a value the rest of the pipeline treats as final.

So a fifth package, `internal/diagnose`, consuming a forest and producing a
verdict per finding:

```go
type Chart struct {
	link.Forest              // embedded: Findings, Edges, Roots, Children, RootOf
	Diagnoses []Diagnosis    // parallel to Forest.Findings
}
```

Embedded rather than a field, so `Chart` is usable everywhere a `Forest` was and
the three parallel slices travel as one value. That is the same discipline D-31
applied to `Findings`/`Edges`, for the same reason: index-coupled slices that
can be separated will eventually be sorted apart, and the failure mode is a
wrong diagnosis rather than a crash.

**Rejected:** returning `[]Diagnosis` on its own and letting `triage` hold three
slices. It compiles, it is one type fewer, and it makes the misalignment
possible.

---

### D-41 — The pattern vocabulary is the brief's, verbatim

**Status:** Accepted

The brief, item 4: *"surface the pattern (sustained crash-loop, transient blip,
deploy-correlated failure, capacity issue, node issue, etc.)"* — and its example
output has a `Pattern:` line. Those five, with those words.

**The fight.** There is a real argument for a different vocabulary. Two of the
five — deploy-correlated and node issue — are pure restatements of *"my root is
a deploy marker"* / *"my root is a node condition"*, which the forest already
says, with evidence, in far more detail:

> `rollout created replica set payment-service-9e3f1a2b8 4.4s earlier; all 3
> affected pods belong to it`

Next to that, `Pattern: deploy-correlated failure` adds nothing. A vocabulary
that described **shape only** — transient / sustained / point — would carve the
space along an axis the forest does not already cover, and would be defensible
as a design.

**Why it lost.** The brief names its five and asks for them by name. A grader
reading for their own vocabulary should find their own vocabulary. Inventing a
cleaner taxonomy and making them translate is scoring points against yourself.
The redundancy is real but harmless: the pattern is the one-word category, the
edge is the evidence, and they sit on different lines.

---

### D-42 — Patterns are a priority table; the mechanism outranks the trigger

**Status:** Accepted — row 6's guard strengthened by D-46

06's `FailedScheduling` is genuinely both. Its body reads
`0/6 nodes are available: 6 Insufficient cpu` — a capacity issue — and its root
is `data-pipeline`'s rollout 4.7s earlier — deploy-correlated. One finding, two
true labels, and `Pattern` is one value.

The table, first match wins:

| # | Pattern | Fires when | Fires on |
|---|---|---|---|
| 0 | *(none)* | the finding is a deploy marker | 03, 04, 06 rollouts |
| 1 | capacity issue | rule is `failed-scheduling` **and** a body names `Insufficient` | 06 |
| 2 | sustained crash-loop | rule is `backoff/crash-loop` or `oom-killed`, and not transient | 02 ×2 |
| 3 | node issue | the root is a Node-kind finding | 05 ×7 |
| 4 | deploy-correlated failure | the root is a deploy marker | 03 ×2, 04 |
| 5 | transient blip | transient by shape (D-43) | every background finding |
| 6 | *(none)* | anything else, including unclassified reasons | 06's `LALALALA` |

**Ordering principle: a pattern that names the failure *mechanism* outranks one
that names its *trigger*,** because the trigger is already stated by the causal
edge and the mechanism is not stated anywhere else. So 06 reads *"capacity
issue"*, and the rollout that provoked it appears one line below as the edge.

**Two deliberate abstentions.**

*A deploy marker gets no pattern.* Rule 4 as written would fire on the rollout
itself — `RootOf(i) == i`, and it is a deploy marker — labelling a successful
deployment a "deploy-correlated failure". It is an anchor, not a failure.
Row 0 catches it before anything else can.

*An unclassified reason gets no pattern.* 06's `LALALALA` is transient by every
shape measure and row 5 would call it a transient blip. Handing a diagnosis
label to a reason the taxonomy could not interpret is precisely the confident
wrongness the `CategoryUnclassified` fallback exists to avoid. It is reported,
uninterpreted, exactly as classification left it.

**Rejected:** a set of patterns per finding rather than one. It is more truthful
— 06 really is both — and it makes the output a bag of labels to read rather
than a category to scan, in a report whose entire premise is that the reader is
under time pressure. The full truth is still recoverable: it is the pattern line
plus the edge line.

---

### D-43 — "Transient" is a conjunction of three named bounds

**Status:** Accepted — closes half of O-02

Measured across all six captures, per finding:

| | count | pods | span |
|---|---|---|---|
| background findings (3 in every file) | 1–3 | 1 | 0–16s |
| real problems (02, 03, 04, 06) | 18–222 | 3–6 | 7m49s–26m6s |

```go
transientCount = 10   // gap is (3, 18); mid-gap
transientPods  = 1    // not a threshold: one instance, or the workload
transientSpan  = 60s  // gap is (16s, 4m31s)
```

**Why a conjunction rather than the count alone.** Count alone separates the
corpus with a 6× margin and would be simpler. But 05's
`data-pipeline/Evicted[disk-pressure]` is `n=3` across **3 pods** over
**4m31s** — count alone calls that a blip, and it is part of a node-wide
incident. Blast radius and duration are independent axes from volume, and a
definition of "transient" that reads only volume is wrong on its face.

**`transientPods = 1` is not a tuned number.** It is the boundary between one
instance misbehaving and the workload misbehaving. The comparison is `<= 1`
rather than `== 1` because a Node or Deployment finding carries no pods at all
(D-30), and node-4's condition must be able to be transient by shape — it is
kept by D-44's second clause, not by pretending it is large.

**`transientSpan` is inert on this corpus and is included anyway.** No finding
in any capture is decided by it: nothing with `count <= 10` and `pods <= 1` has
a span over 16s. It is here because the alternative is a tool that prints
"transient blip" beside a pod that has failed once a minute for twenty minutes.
Its bounds are still observed, not invented — 3.75× above the background ceiling
and 4.5× below the shortest real problem.

**Measured and dropped: occupancy.** The plan called for "minutes occupied" —
distinct wall-clock minutes containing at least one event — to separate a dense
crash-loop from a sparse trickle. Computed over the corpus it is 1–2 for every
background finding and 5–22 for every real one, which is a clean separation that
`span` already makes identically. A fourth metric that never changes an answer
is a fourth metric to explain. Dropped.

---

### D-44 — Suppression is `issue ∧ transient ∧ unexplained ∧ non-explanatory`

**Status:** Accepted — closes O-02, un-parks the predicate D-34 anticipated;
first clause strengthened by D-46

The controlling evidence, three findings from the corpus:

| | n | pods | span | parent | children |
|---|---|---|---|---|---|
| 01 `data-pipeline/Evicted[memory-pressure]` | 1 | 1 | 0s | −1 | 0 |
| 05 `auth-service/Evicted[disk-pressure]` | 1 | 1 | 0s | **3** | 0 |
| 05 `node-4/NodeHasDiskPressure` | 1 | 0 | 0s | −1 | **6** |

**Identical on every shape metric, and they need three different verdicts.** The
first is background noise that must not be reported. The second is a pod killed
by a dying node. The third is the single most important line in that capture. No
threshold on count, pods, span or occupancy can separate them, because there is
nothing there to separate. Only the forest can.

```
suppress(i) := Category == Issue
             ∧ transient(Findings[i])          // D-43 — it is small
             ∧ Edges[i].Parent == noParent      // nothing explains it
             ∧ len(Children(i)) == 0            // and it explains nothing
```

**The fourth clause is not decoration.** Without it node-4's condition — `n=1`,
`pods=0`, `span=0`, a root — is suppressed, and with it goes the entire
`05-test-b` incident, since every one of its six evictions hangs off it.

**Why `Category == Issue` guards the whole thing.** 06's `LALALALA` is
transient, unexplained and childless, and would be suppressed. But
`CategoryUnclassified` exists precisely so that a reason we cannot interpret is
*surfaced demoted rather than buried* — suppressing it re-buries it and defeats
the category. Deploy markers are excluded by the same clause, which is correct
for a different reason: a marker with no children is a rollout that broke
nothing, and there is no shape argument for hiding it.

**Result across the corpus:**

| Fixture | findings | suppressed | reported |
|---|---|---|---|
| 01-healthy | 3 | **3** | **0** |
| 02-memory-leak | 5 | 3 | 2 |
| 03-image-pull-failure | 6 | 3 | 3 |
| 04-test-a | 5 | 3 | 2 |
| 05-test-b | 10 | 3 | 7 |
| 06-test-c | 6 | 3 | 3 |

**Exactly three per capture, in every capture** — the same three shapes each
time (an `Evicted[memory-pressure]`, a `FailedMount`, an `Unhealthy` ×3). The
brief plants an identical background floor in all six files. A predicate tuned to
one file would not land on the same three in the other five, so this is
corroboration rather than a fit.

`01-healthy` reports nothing. That was the point.

---

### D-45 — Suppressed findings are counted and disclosed, never deleted

**Status:** Accepted

`Suppressed` is a field on the diagnosis, not a filter applied inside
`diagnose`. The header states the count, the same way it already states skipped
and unrecognised records:

```
20,000 records, 19,772 filtered as noise, 3 suppressed as background, 7 findings
```

This is the same rule the pipeline already follows twice — D-09 keeps noise
records in `Result.Records`, and the decoder discloses its skip count so a
truncated capture cannot look like a clean one. It is what makes an aggressive
threshold safe: the worst case for a mis-tuned `transientCount` is a line the
reader has to ask about, not a fact that silently left the building.

It also keeps `report` honest under its own prohibition — it renders what it is
given and decides nothing — while leaving room for a `--all` flag to render the
suppressed rows without any stage having to re-derive them.

---

### D-46 — An unrecognised reason is never labelled and never suppressed

**Status:** Accepted — strengthens the `Category == Issue` guard in D-42 and D-44

**How it surfaced.** `06-test-c.jsonl`'s planted probe record was internally
inconsistent: `severity_text: "Normal"` alongside `severity_number: 13`, which is
Warning. Corrected to Warning, it stops taking the fallback's Normal branch —
`CategoryUnclassified` — and takes the Warning branch instead, which promotes it
to `CategoryIssue` with `Recognised: false`.

That walked it straight past both guards as written. It is one occurrence, one
pod, zero span, a root with no children: **transient, unexplained, unexplanatory,
and now an issue.** D-44's predicate would have suppressed it, and D-42's table
would have labelled it a transient blip.

The record argues against both, in its own body:

> *"In case there are some new events that we haven't really recognized and
> handled, we'd much rather surface it, instead of burying it"*

**The principle, which is what actually changed.** Suppressing a finding is a
claim to understand it well enough to know it does not matter. Naming its pattern
is a claim to know what kind of thing it is. Neither claim can be made about a
reason that is not in the taxonomy — that is the entire premise of the
conservative fallback, and letting shape override it re-buries exactly what the
fallback exists to surface.

So `Recognised` is carried from `classify.Classification` through
`group.Finding` to the diagnose stage, and one predicate governs both questions:

```go
func diagnosable(f group.Finding) bool {
	return f.Category == classify.CategoryIssue && f.Recognised
}
```

One function rather than two checks, because the two must not drift: **a finding
we decline to categorise is exactly a finding we may not dismiss.**

**Why this is better than the guard it replaces.** `Category == Issue` excluded
unclassified reasons only by side effect — they happen to sit in a different
category. The moment severity moved, the side effect stopped holding. Keying on
`Recognised` states the actual reason and covers both fallback branches at once.

**Consequence for coverage.** No capture now exercises the
`CategoryUnclassified` branch; `classify`'s own tests do, and the triage test
says so explicitly rather than leaving the gap silent.

---

### D-47 — The healthy result is a rendered answer, not an empty table

**Status:** Accepted

`no issues detected` becomes:

```
✓  ALL CLEAR

   No findings. Nothing here needs an on-call response.
   3 transient blips held back as background -- each explained by nothing,
   and explaining nothing.
```

**Why it earns the space.** A clean capture is a real answer, not the absence of
one, and it is the answer a reader at 3am most needs to trust at a glance. It is
also the single output most likely to be misread: a tool that prints nothing is
indistinguishable from a tool that failed.

**Why the second line is not optional.** "No findings" from a tool that quietly
suppressed three things is a claim the reader cannot check. D-45 rests on the
suppressed count staying visible, and the all-clear is the one output where there
is nothing else on screen to carry it. Where nothing was held back it says so
instead — silence because there was nothing, not silence because we filtered.

**On the colour.** Green is the palette's fourth entry and the only one that is
not a severity, which nominally weakens the "three conventional colours and
nothing else" rule the table follows. It is admitted because it appears on
exactly one line and that line never coexists with a table, so no scan is made
harder by it. The emphasis-only contract still holds and is tested on this path
specifically: strip the escapes and the styled rendering is byte-identical to the
plain one, so the captured output files the brief requires still read as
sentences.

---

### D-48 — The "why" is a signature over record bodies, not a new derivation

**Status:** Accepted

The patterns answer *what kind of failure this is*. They do not answer *why*, and
the brief asks for both — its output spec calls for *"evidence: event counts,
**signatures**, patterns observed"*.

The answer was already in the capture and was being discarded at render time:

| Finding | What the records actually say |
|---|---|
| 04 `checkout-service/Unhealthy` ×222 | `Readiness probe failed: HTTP probe failed with statuscode: 404` |
| 03 `payment-service/Failed` ×24 | `Failed to pull image "registry.internal/payment-service:v2.14.0-rc3": manifest not found` |
| 02 `recommendation-service/OOMKilling` ×26 | `Container recommendation in pod <pod> exceeded memory limit (512Mi)` |
| 06 `data-pipeline/FailedScheduling` ×177 | `0/6 nodes are available: 6 Insufficient cpu` |

04 is the case that settles it. `Unhealthy` plus `deploy-correlated failure`
says a probe is failing after a rollout. **The 404 says the rollout shipped a
build whose `/healthcheck` path no longer exists** — which is the actual
diagnosis, the actual remediation, and one substring away from being free.

**Why a signature rather than quoting a body.** A finding holds up to 222
records and their bodies differ in pod name, IP and UID. Quoting the first is
arbitrary and hides that 04 has *three distinct symptoms*, not one. Normalising
the volatile tokens and counting collapses them to a handful:

| Finding | records | signatures |
|---|---|---|
| 04 `Unhealthy` | 222 | 3 |
| 06 `FailedScheduling` | 177 | 3 |
| 02 `BackOff` | 91 | 1 |
| 03 `Failed` | 24 | 3 |

**Normalised:** pod names (`<pod>`), IPv4 addresses (`<ip>`), and
parenthesised UIDs (dropped). Nothing else. In particular **numbers with units
are never touched** — `512Mi`, `404`, `8080` and `0/6` are the answer, not
noise, and a normaliser that ate them would delete exactly what this feature
exists to surface.

**Why this belongs in `diagnose` and not in `report`.** Reducing 222 bodies to 3
ranked signatures is an interpretation of shape across a finding's records,
which is this stage's remit and which `report` is explicitly forbidden to do.
`report` renders the list it is handed.

---

### D-49 — Signatures rank by specificity, then by frequency

**Status:** Accepted

Frequency alone is the wrong sort key, and 03 is the proof:

```
Error: ImagePullBackOff                                          18 of 24
Failed to pull image "registry.internal/payment-service:v2.14.0-rc3"
  ... manifest ... not found                                      3 of 24
Error: ErrImagePull                                               3 of 24
```

The **rarest** signature is the **only** one that says anything. `Error:
ImagePullBackOff` is a restatement of the REASON column; the 3-occurrence line
names the tag that does not exist. Leading with the common one buries the
answer under a paraphrase of the question.

**The rule:** a signature is *specific* if — after normalisation — it contains a
**quoted string** or a **digit**. Specific signatures sort above generic ones;
within each group, frequency decides.

**Why that predicate.** Normalisation has already removed the volatile digits
(IPs, UIDs, pod hashes), so a digit that survives is a fact about the failure:
a status code, a resource amount, a port, a node tally. A quoted string is a
name the cluster chose to quote — an image ref, a volume, a configmap.

**Verified against every multi-signature finding in the corpus:**

| Finding | Leads with | Correct? |
|---|---|---|
| 03 `Failed` | `Failed to pull image "…v2.14.0-rc3"` (quoted, 3×) | yes — the bad tag |
| 04 `Unhealthy` | `HTTP probe failed with statuscode: 404` (digit, 178×) | yes — the missing endpoint |
| 06 `FailedScheduling` | `0/6 nodes are available: 6 Insufficient cpu` (digit, 118×) | yes |
| 05 `Evicted` | `The node had condition: [DiskPressure].` (sole signature) | n/a |

**Rejected: longest-first.** It gets 03 and 04 right, and it gets them right by
accident — length is a proxy for nothing. A rule that happens to work is a rule
that will stop working without telling you.

**Rejected: a taxonomy of "interesting" tokens per reason.** More accurate and
more fitted to this corpus. Specificity is a property of a sentence, not of a
Kubernetes reason, and the general rule is the one that survives an event type
we have never seen.

**Binary, not a score.** A ranking function with weights would need every weight
justified, and the corpus supports exactly one distinction: *does this line
carry a concrete noun or not*. Frequency is a real tiebreak; anything finer
would be invented.

---

### D-50 — Edges carry the rule that produced them

**Status:** Accepted

`link.Edge` gains `Rule string` — the name of the causal rule that fired.

It was already present as a field on the rule table and thrown away. Two callers
need it, and both were about to re-derive it from the evidence string, which is
display text:

1. The tree collapses sibling children that share an explanation (D-51), and
   "same explanation" means *same causal rule*, not *same evidence text* — the
   six evictions in 05 all fired `node-condition-named` but their evidence
   strings differ in elapsed time.
2. `ANALYSIS.md` needs to say which relationships the tool asserted, and
   counting rule names is not the same as grepping prose.

Parsing a sentence to recover a decision that was made in code is the failure
this prevents.

---

### D-51 — Siblings that share an explanation collapse to one line each

**Status:** Accepted

05 renders six children of one node condition, each repeating an identical
`why` and a near-identical `caused`:

```
├── 10:15:15.359  Evicted on batch-reporter  ×1 · 1 pods
│        why:  The node had condition: [DiskPressure].
│        caused: node-4 reported DiskPressure 15.4s earlier, and this record names it…
├── 10:15:25.779  Evicted on data-pipeline  ×3 · 3 pods · 4m31s
│        why:  The node had condition: [DiskPressure].
│        caused: node-4 reported DiskPressure 25.8s earlier, and this record names it…
   … four more, identical
```

Thirty lines to say one thing six times. The repetition actively hides the
information that *is* per-child — which workloads, which namespaces, how far
apart.

**Collapsed:** the shared explanation is stated once on the parent, and each
child becomes one line carrying only what differs.

```
10:15:00.000  NodeHasDiskPressure on node-4
     pattern: node issue
     why:  Node node-4 status is now: NodeHasDiskPressure
     → evicted 6 workloads across 3 namespaces in 1m38s, each naming
       "The node had condition: [DiskPressure]"
│
├── 10:15:15.359  Evicted  batch-reporter (data)       ×1 · 1 pod   +15.4s
├── 10:15:25.779  Evicted  data-pipeline (data)        ×3 · 3 pods  +25.8s
└── … four more
```

**The collapse condition is a fact, not a judgement:** every child shares the
same edge rule (D-50), the same reason, and the same leading signature. Any
child that differs on any of the three renders in full, beside its collapsed
siblings — so the collapse can never hide a child that is telling a different
story.

**Rejected: a `--verbose` escape hatch.** A second rendering path and a second
set of golden files, to restore text that is by construction identical. The one
case where the detail matters — a child whose explanation differs — is not
collapsed in the first place.

**Rejected: keeping every child in full** so each node is independently
quotable into `ANALYSIS.md`. The report is read under time pressure before it is
quoted, and a reader who cannot see six workloads in one glance is worse off
than one who has to look up a line.

---

### D-52 — One `PAGED HERE` marker per incident, on the worst leaf

**Status:** Accepted

The north star is pulling on the thread of a page and walking back to the
origin, so the tree has to show where the thread starts. The marker goes on the
**deepest node of highest severity** — the symptom that would actually have
raised the alert — with the root labelled as the cause it traces back to.

```
10:17:59.977  ▸ ScalingReplicaSet on payment-service     ◀── ROOT CAUSE
│
└── 10:18:04.412  Failed on payment-service  ×24
    │
    └── 10:18:14.858  BackOff on payment-service  ×18   ◀── PAGED HERE
```

**Rejected: marking every leaf.** In 05 that is all six evictions, and a marker
on most of the tree marks nothing.

**Rejected (deferred, not dismissed): `--trace <workload>`.** Naming the service
you were paged for and dimming the rest is closer to the real 3am workflow than
any default can be. It is genuinely better *and* it does not remove the need for
a default, since the captured output files the brief requires are produced
without arguments. Worth building if the mandatory deliverables land early.

**Severity of an incident is the maximum over its tree, not its root's.** 03's
root is a rollout at INFO, and the outage beneath it is CRITICAL; taking the
root's severity labelled the whole incident INFO and would have sorted it below
a readiness blip.

---
### D-53 — Remediation is a column in the taxonomy, not a rules engine

**Status:** Accepted

The brief asks the analysis report for *"remediation for the next five minutes"*,
and a triage tool that names a cause without naming a next move stops one step
short of useful. Each taxonomy row gains a `fix`, rendered as `RECOMMENDED` at
the foot of the incident it belongs to.

**Keyed on the mechanism's rule, not the root's.** 03's root is a rollout; the
fix for a rollout is nothing. The fix belongs to what actually broke — the same
node the verdict quotes.

**Templated, so it is copy-pasteable.** `{workload}`, `{namespace}`, `{node}` and
`{pod}` are substituted from the finding, because a command an on-call engineer
has to hand-edit at 3am is a command they will get wrong.

**Diagnostic before destructive.** Where both exist, the safe command comes
first. `kubectl logs --previous` before `kubectl set resources`; `kubectl
describe node` before `kubectl cordon`. The tool is confident about what it
observed and has no business being confident about what to change.

**Explicitly not a rules engine.** No conditionals, no severity-dependent
advice, no synthesis across findings. One string per taxonomy row, the same
shape as `cause`. Adding remediation for a new failure mode is filling in a
column — and a row with no `fix` renders nothing rather than something vague.

**A limitation stated rather than hidden.** The fix table covers only the
fourteen reasons the taxonomy covers, which are the reasons that occur in these
six captures. A real cluster produces many more --
`CreateContainerConfigError`, `FailedAttachVolume`, `NetworkNotReady`,
`Preempted` and so on -- and for every one of them this tool falls back to
"unrecognised reason, surfaced uninterpreted, no remediation known". That is the
correct behaviour and it is still a gap; see O-04.

---

### D-54 — `Incident` is the unit the report renders, and it is built in `diagnose`

**Status:** Accepted — supersedes the flat ordering sketched in `handover.md` §8.2

The renderer needs, per incident: its severity, its blast radius, which node
explains it, which node paged, and where it sits in priority order. Every one of
those is a derived fact, and `report` is forbidden to derive. So they are
computed once, in `diagnose`, and handed over:

```go
type Incident struct {
	Root, Mechanism, Paged int   // Paged is -1 when several symptoms tie
	Severity   event.Severity     // MAX over the tree, never the root's
	Members    []int              // root and descendants, in time order
	Events, Pods, Workloads, Namespaces int
	First, Last  time.Time
	StillFailing bool
}
```

**Three different "important nodes", and conflating them was a real bug.**

| | Which node | Answers |
|---|---|---|
| `Root` | nothing explains it | *what set this off* |
| `Mechanism` | **shallowest** failure of max severity | *what actually broke* |
| `Paged` | **deepest** leaf of max severity | *what raised the alert* |

The verdict first quoted the deepest node. In 03 that is `BackOff` — *"repeated
image-pull retry"*, a consequence — where the shallowest failure is `Failed`,
which names the tag that does not exist. Depth is the right axis for *what paged
you* and the wrong one for *what went wrong*.

**`Paged` is -1 on a tie.** 05 has six equally-bad leaves and no way to know
which one raised the page; marking the first is a fabrication. The root carries
the incident instead.

**Blast radius counts Pod-kind findings only.** Counting all members made 05
read *"7 workloads across 4 namespaces"* — node-4 is not a workload, and
`default` is a namespace where nothing happened. Six workloads, three
namespaces.

**Ordering: severity, then blast radius, then time.** This is the brief's
"prioritised summary", applied to incidents rather than findings, so an incident
is never split across the ranking.

**`StillFailing`** is `capture end - Last <= 2m`. Derived: across the corpus
every real problem ends 0–100s before the capture does, and every background
finding ends 600–1730s before. Any value in that gap behaves identically; two
minutes sits inside it and reads as a round number rather than a tuned one. It
is the difference between *"this is happening now"* and *"this happened", which
is the first thing an on-call reader needs and the last thing a flat report
tells them. `diagnose.Build` therefore takes the capture end as a parameter —
the stage cannot say whether something is ongoing without knowing when the
observation stopped.

---

### D-55 — The JSON is the audit trail, not a second summary

**Status:** Accepted

`--json` emits a document whose organising principle is that **every verdict
sits next to the inputs that produced it**. A conclusion on its own is something
you have to trust; a conclusion beside its evidence and the thresholds that were
applied is something you can check — and disagree with — using nothing but the
file.

Concretely, each finding carries:

| | Why it is there |
|---|---|
| the coalescing identity | why these records are one fact rather than several |
| **every member record, verbatim** | so every count and time range is *recomputable*, not merely asserted |
| the causal edge, with its rule and evidence | so an asserted link can be checked against the capture |
| the diagnosis, **with each suppression clause separately** | so "suppressed: true" can be traced to which clause decided it |

And the document header carries the causal window and every threshold, because
**a threshold nobody can see is a threshold nobody can challenge.**

**Suppressed findings are included, flagged.** Excluding them would make the
document agree with the tool by construction, which is the opposite of the
point: the first thing a sceptical reader wants is the list of things that were
held back.

**Lifecycle noise is counted but not reproduced.** It is ~19,800 of 20,000
records, it is already in the input file byte for byte, and duplicating it would
make the document larger than its own source while adding nothing the source
does not hold. The rule drawn: *everything the tool concluded is here;
everything it read is in the file it read.* Sizes with that rule: 7.5 KB
(01-healthy) to 102 KB (04-test-a), against a 16 MB input.

**Rejected: nesting findings inside incidents.** It reads better and duplicates
every finding that belongs to a tree. Incidents index into `findings` instead,
so there is exactly one copy of each and a consumer can walk either structure.

**Rejected: emitting durations as nanosecond integers.** `60000000000` is a
number a reader has to decode; `"1m0s"` is one they can act on. This document is
read by a person writing an analysis report at least as often as by a program.

**On ownership:** `diagnose.Config` and `diagnose.Suppression` carry no struct
tags. How a verdict is spelled on a wire is the renderer's business, and a tag
in a diagnosis stage would make that stage own a file format. `report` maps them
into its own wire types.

---

### D-56 — A verdict is derived from its reasons, never stored beside them

**Status:** Accepted — found by the test written for D-55

`Diagnosis` held both `Suppressed bool` and the four clauses behind it. Writing
the JSON test that asserts *"the published outcome must follow from the
published clauses"* immediately failed — on the test fixtures, which set the
outcome and left the clauses zeroed.

That is a fixture bug and a design bug. Two fields that must agree, and which
nothing forces to agree, will eventually disagree — and here the disagreement
would be published as an audit trail, which is worse than not publishing one.

`Suppressed` is now a method:

```go
func (d Diagnosis) Suppressed() bool { return d.Because.holds() }
```

There is exactly one place that says what suppression means, it reads the four
values the document publishes, and a hand-built chart can no longer claim an
outcome its own reasons contradict.

**The general rule this is an instance of:** where a value and its justification
are both exported, derive the value. A stored conclusion is a second source of
truth, and the audit trail is only worth anything if it cannot be inconsistent
with the thing it audits.

---


## Open

### O-01 — Package layout

`docs/engineering-standards.md` §1.1 names `group` / `diagnose` / `correlate` /
`report`. The repository currently has `otel` / `event` / `classify` / `triage`.
D-17 collapses correlation into an on-demand function, which may not warrant its
own package. Needs an ADR.

### O-02 — Pattern thresholds  *(CLOSED by D-43 and D-44)*

D-26 requires numeric thresholds for burst / sustained / recurring /
deploy-correlated / capacity / node-issue. Each must be a named constant with
its derivation recorded (`engineering-standards.md` §3.2: _"numbers are never
magic"_).

Settled at three constants, not six: only "transient" needs numbers at all.
Every other pattern is decided by a rule ID, a body substring or the root of the
incident, and the suppression that the thresholds feed is a conjunction with the
forest rather than a threshold on its own.

### O-03 — Deploy-correlation window  *(CLOSED by D-33)*

D-17's `window` parameter. Observed deploy→first-symptom deltas: 03 = 4.4s,
04 = 7.3s, 06 = 4.7s. All under 10 seconds, which suggests a generous window is
safe — but the value needs justifying, not guessing.

### O-04 — The taxonomy covers only the reasons these captures contain

Fourteen reasons are classified, and they are exactly the reasons that occur in
the six provided files. A real cluster emits many more --
`CreateContainerConfigError`, `FailedAttachVolume`, `NetworkNotReady`,
`Preempted`, `FailedKillPod`, `ImageInspectError`, `NodeHasMemoryPressure`,
`NodeHasPIDPressure`, `TaintManagerEviction` among them.

Every one of them currently lands on the conservative fallback: surfaced as an
issue if it is a Warning, never labelled with a pattern, never suppressed, no
remediation. That is the *correct* failure mode -- nothing is silently dropped
and nothing is confidently mislabelled -- but the tool is measurably less useful
on a cluster that is not one of these six files, and saying so is more honest
than a taxonomy that looks complete.

Two questions to settle, neither of them settled by this corpus:

1. **How far to extend the taxonomy** without evidence. Every row added from
   documentation rather than from observed records is a row whose body matchers
   are guesses -- and D-?? already records one case where the brief's own
   example body text would never have matched the records
   (`OOMKilling`).
2. **Whether the causal rules generalise.** All four were derived from edges
   visible in these captures. `NodeHasMemoryPressure` would flow through
   `node-condition-named` unchanged; a `Preempted` pod naming its preemptor
   would need a rule that does not exist.
