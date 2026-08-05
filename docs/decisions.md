# Decision ledger

Every design decision taken on daiquiri, the alternatives that lost, and the
ones I reversed. A decision with no recorded alternative was never really made.

## How to read this

Entries are grouped by **layer**, which is the stage of the pipeline they
govern. Within a layer they're in the order they were taken, so the D-numbers
jump around. That's deliberate. The number says *when* I decided it, the layer
says *where it lives*, and both are useful.

| Status | Means |
|---|---|
| **Accepted** | in force |
| **Reversed** | was accepted, then overturned by evidence. The original stays here with the reason it fell, because deleting it hides the reasoning |
| **Parked** | argued through, not needed yet, may return |
| **Rejected** | considered and declined |
| **Open** | identified, not settled |

Evidence is quoted from the six captures in `testdata/`. Where a decision rests
on taste, it says so.

## Index

| Layer | Package | Decisions |
|---|---|---|
| [0. Scope and process](#layer-0-scope-and-process) | — | D-01, D-02, D-39 |
| [1. The product](#layer-1-the-product) | — | D-03, D-04 |
| [2. Ingest](#layer-2-ingest) | `otel`, `event` | D-08, D-10 |
| [3. Classification](#layer-3-classification) | `classify` | D-05, D-06, D-07, D-09 |
| [4. Coalescing](#layer-4-coalescing) | `group` | D-11, D-12, D-13, D-14, D-15, D-16, D-23, D-28 |
| [5. Causality](#layer-5-causality) | `link` | D-17 – D-22, D-24, D-25, D-31 – D-38, D-50 |
| [6. Diagnosis](#layer-6-diagnosis) | `diagnose` | D-26, D-27, D-40 – D-46, D-48, D-49, D-53, D-54, D-57, D-58 |
| [7. Output](#layer-7-output) | `report` | D-29, D-30, D-47, D-51, D-52, D-55, D-56, D-59 |
| [Open](#open-questions) | — | O-01 – O-04 |

Six entries changed their mind about something. They're collected in
[Reversals and corrections](#reversals-and-corrections) at the bottom.

---

# Layer 0: scope and process

## D-01: Rebuild by hand; take only the standards document

**Accepted.**

`~/Projects/daiquiri-ai` is an earlier run at this challenge, built largely by
an AI agent, and abandoned. This repository is written by hand.

`docs/engineering-standards.md` is copied across verbatim, because it encodes my
own engineering standard rather than the old implementation. Nothing else comes
with it: no code, no architecture, no package layout.

**Why.** The brief grades reasoning quality, and reasoning you didn't do is
reasoning you can't defend in a review.

**Consequence.** `engineering-standards.md` §1.1 describes a package layout
(`group`, `diagnose`, `correlate`, `report`) this repository hasn't adopted and
may not. That section is aspirational until O-01 settles it. It also refers to
an `AGENTS.md` that doesn't exist here.

## D-02: Design is argued in the open before code is written

**Accepted.**

Structure, data model and thresholds get settled in writing, against fixture
evidence, before implementation. This ledger and `docs/hld.md` are that record.

**Why.** Three decisions here (D-11, D-14, D-21) were reversed by looking at the
actual data. Each would have been a rewrite if I'd found it after implementing
rather than before.

## D-39: No per-package LLDs; the ledger carries their content

**Accepted.** Amends `engineering-standards.md` §3.1.

§3.1 required `docs/lld/<package>.md` written *before* each package. None were
written for `group`, `link` or `report`. The standard is amended rather than
left saying one thing while the repository does another.

**Why.** The ledger already carries what an LLD is for: types, algorithm, edge
cases, failure modes, and the evidence behind each threshold. D-31 through D-38
are the `link` LLD in everything but filename. A parallel per-package document
would be a second copy of the same reasoning, and the second copy is the one
that goes stale.

**Alternatives that lost.**

- *Write the three missing LLDs retroactively.* Compliant on paper. A
  retroactive LLD documents what got built where §3.1's "before" exists to
  design what will be. Half a day producing a document that can't do its job.
- *Accept the deviation silently.* Contradicts the standing rule that a
  deviation is a blocking defect, and leaves a binding document that's untrue.

**Unchanged.** Design is still argued before code (D-02), every decision still
lands here when it's made, every package still states its prohibitions in
`doc.go`, and every exported identifier still carries godoc.

---

# Layer 1: the product

## D-03: The deliverable is a causal trail

**Accepted.**

North star: an engineer paged at 3am pulls on the thread of the alert and walks
back to where the failure started.

**Why.** The brief puts 35% on the Analysis Report and warns that *"a summary
that lists counts without an interpretation is half the work"*, and that
*"seemingly unconnected findings are sometimes side effects of the same
underlying issue."* A flat finding list scores badly on both.

**Evidence.** All five failing captures resolve to a 2 or 3 hop trail.

| Capture | Symptom | Origin |
|---|---|---|
| 02 | `BackOff` ×91 on recommendation-service | `OOMKilling` on the same pods seconds earlier; the trail runs off the front of the capture |
| 03 | `Failed` ×24 / `BackOff` ×18 on payment-service | `ScalingReplicaSet payment-service` @ 10:17:59.977, 4s before |
| 04 | `Unhealthy` ×222 on checkout-service | `ScalingReplicaSet checkout-service → 5` @ 10:21:59.977, 7s before, **zero** symptoms prior |
| 05 | `Evicted` ×10 across 6 workloads / 3 namespaces | `NodeHasDiskPressure node-4` @ 10:15:00.000, 15s before |
| 06 | `FailedScheduling` ×177 on data-pipeline | `ScalingReplicaSet data-pipeline → 12` @ 10:19:59.977, 5s before |

## D-04: Prioritised list on top, trail underneath, each headline its own origin

**Accepted.**

The brief wants a severity-prioritised summary. The north star wants chronology.
Both hold if you order *incidents* by severity and make each incident's headline
the **origin** rather than the symptom.

**Why.** The first line a paged engineer reads is then already the root cause.

---

# Layer 2: ingest

## D-08: Node identity is normalised at decode

**Accepted.**

```go
if e.Object.Kind == "Node" && e.Node == "" {
    e.Node = e.Object.Name
}
```

**Evidence.** The single `NodeHasDiskPressure` record in the corpus carries
`k8s.object.name = "node-4"` and **no** `k8s.node.name`. The one record that
names a node has an empty `Node` field. Without this, every "same node" rule in
the causal layer silently fails to match the exact event it exists to find.

## D-10: Occurrence count is `max(count)` per event UID, summed

**Accepted.**

**Evidence.** `k8s.event.count` is `1` for **every record across the six
captures** (120,001 lines; the decoder parses 120,000 and skips one deliberately
malformed line, see O-04), and event UIDs are unique. So `Count == len(members)` in every test
available to me.

**Why implement the harder rule anyway.** The brief says the tool has to handle
*"repeated logs as the API server's `count` increments"*. A naive `+= Count`
double-counts under that kind of pipeline. The path can't be tested against this
corpus, so it's covered by a synthetic test and this entry says plainly that the
corpus can't exercise it.

---

# Layer 3: classification

## D-05: Noise is retained rather than counted and dropped

**Accepted.** Reversed the implementation at the time.

`internal/triage` was doing `res.Noise++` and discarding the record. Noise
becomes a retained slice.

**Why.** Lifecycle events are the evidence behind the most quantitative claim
the tool can make. One crash-looping pod in 02:

```
10:03:42  Killing      ← classified noise
10:03:43  OOMKilling
10:03:52  BackOff
10:04:27  Started      ← classified noise
10:09:08  Killing      ← classified noise
```

`Started → Killing` is the memory leak's fill rate, and both endpoints are noise
records. Discard them and the strongest sentence in the analysis report becomes
unavailable.

> **Corrected by D-58.** This entry originally read those two intervals as
> 4m41s *contracting* to 4m12s and called the leak accelerating. That's two
> intervals on one pod. Across all 22 the ratio is 0.79. The tool reports
> steady.

**Cost.** ~20,000 events held in memory, about 1.6 MB, against a 16 MB input.

## D-06: Noise is retained but never narrated

**Accepted.**

Every record is kept. Only issues, markers and unknowns create a finding.
Lifecycle events are evidence attached to pods, reachable when a trail is built.

**Why.** Both alternatives fail. Dropping noise loses D-05's evidence. Letting
noise create findings adds ~2,900 `Pulled` findings per capture and drowns the
report.

## D-07: `Workload()` derives the owner, kind-dispatched, conservative fallback

**Accepted.**

A method on `event.Object`. Strips the ReplicaSet hash and pod suffix for `Pod`,
the hash for `ReplicaSet`, and returns the name unchanged for everything else
including anything unmatched.

**Evidence**, measured across all six captures:

| Kind | Records | Shape | Match |
|---|---|---|---|
| Pod | 94,686 | `<workload>-<9 hex>-<5 alnum>` | 100% |
| ReplicaSet | 25,311 | `<workload>-<9 hex>` | 100% |
| Deployment | 3 | bare | n/a |
| Node | 1 | bare | n/a |

Twelve distinct workloads, all `[a-z-]+`. **No workload name itself ends in a
hash-shaped segment**, so a single strip is unambiguous. No StatefulSet ordinals
and no DaemonSet-shaped names appear in the corpus.

**Why it lives in `event` and not in the grouper.** Owner is a property of
identity. Putting the parse in the grouper would give a package whose job is
coalescing a working knowledge of Kubernetes naming conventions.

**Why conservative.** An unmatched name comes back unchanged, so the failure
mode is "no rollup" and never "wrong rollup". Real clusters use a different hash
alphabet and variable hash length, and this keeps that from silently
mis-grouping. A `Node` called `node-4` also survives it, which took a regression
test to keep true.

**On `regexp`.** Called only during grouping, over ≤227 issue records rather
than 20,000. Roughly 0.1 ms of a 5,000 ms budget. Readability wins and there's
nothing here to optimise.

## D-09: Unrecognised `Normal` events get their own category

**Accepted.**

The classifier's fallback routed unrecognised `Normal` events to
`CategoryNoise`. Add `CategoryUnclassified`. They group like anything else and
are reported, demoted, at the foot of the output. Unrecognised **Warning** keeps
its existing behaviour and is surfaced as an issue.

**Why.** `06-test-c.jsonl` contains a planted record with reason `LALALALA`
whose body reads *"In case there are some new events that we haven't really
recognized and handled, we'd much rather surface it, instead of burying it"*.
The old fallback buried it.

**Why a category and not a special case.** Routing unknowns through normal
grouping means 2,000 unknown records collapse to one finding per reason rather
than 2,000 lines.

---

# Layer 4: coalescing

## D-11: Group key is `(Kind, Workload, Namespace, Reason)`

**Accepted**, later extended by D-28 which adds `Rule`.

**Why `Workload` and not `Object.Name`.** `k8s.object.name` for a Pod is the
disposable instance. 04's 225 `Unhealthy` records span 5 pod instances. Keyed on
the literal name that's 5 findings; keyed on workload it's 1. The brief's "same
object" means the thing that broke.

**Resulting finding counts**, which are the acceptance data for this layer:

| Capture | Findings |
|---|---|
| 01-healthy | 3 |
| 02-memory-leak | 5 |
| 03-image-pull | 6 |
| 04-test-a | 5 |
| 05-test-b | 10 |
| 06-test-c | 5 † |

† 06-test-c is **5 findings, 2 reported** as the file ships. Line 20,001 is a
hand-added record with reason `LALALALA`, commented out with a leading `// `, so
the decoder counts it as `skipped 1`. Uncomment it and 06 reads 6 findings and 3
reported, with the extra one surfaced unlabelled and never suppressed (D-46).
Demonstrated in [`analysis/06-test-c.md`](../analysis/06-test-c.md#the-extra-line-at-the-end-of-this-file).

## D-12: `Node` is **not** in the group key

**Accepted.** Reverses an earlier decision in the same design pass.

Originally accepted on the strength of 05-test-b, where `data-pipeline` is
evicted on node-2 at 10:02:32 (background) and on node-4 between 10:15:25 and
10:19:56 (the incident). Without `Node` in the key those merge into one finding
whose `FirstSeen` is 10:02:32, which is *before* the `NodeHasDiskPressure` at
10:15:00 that caused three of them. The cause would postdate its effect, the
causal link would be correctly rejected, and the diagnosis would die.

**Reversed** once the same key met the other five captures:

| Capture | Findings without `Node` | With `Node` |
|---|---|---|
| 02 recommendation-service | 2 (`OOMKilling` n=26, `BackOff` n=91) | **8**, 4 pods on 4 different nodes |
| 03 payment-service | 2 (`Failed` n=24, `BackOff` n=18) | **6**, 3 nodes |
| 04 checkout-service | 1 (`Unhealthy` n=222) | **3**, 3 nodes |
| 05 data-pipeline | 1 impure (n=4, 2 nodes) | 2 clean |

It fixes one capture and fragments four. A memory leak is a property of the
workload. The node it lands on is incidental.

**How 05 is handled instead:** D-28.

## D-13: Findings sort on a **total** order

**Accepted.**

Sort key: `FirstSeen`, then `Kind`, `Workload`, `Namespace`, `Reason`.

**Why.** Go randomises map iteration order, and 05 contains several evictions
inside the same second. Sorting on `FirstSeen` alone leaves ties resolved by map
order, so two runs on the same input produce different output and golden tests
fail *intermittently*, which is the worst possible way to find this.

## D-14: Markers and unknowns group through the identical path

**Accepted.**

`ScalingReplicaSet` → `Kind=Deployment`, `Workload=checkout-service` → one
finding, count 1, `FirstSeen == LastSeen`.

**Why.** No special case, and it makes deploy markers available as causal
candidates for free. A point event is a span of zero duration.

## D-15: Grouping is conservative; linking does the joining

**Accepted.**

Never merge across a dimension that can't be shown irrelevant. Merging destroys
information irreversibly; linking preserves it. **An incident is a set of
related findings.**

**Evidence.** 04's `checkout-service Unhealthy` spans three nodes and merging is
right there. 05's `data-pipeline Evicted` spans two nodes and merging is wrong
there. No grouping key can tell those apart. Only the causal rules can.

## D-16: Grouping is non-windowed; analysis is windowed

**Accepted.**

One finding per key spanning the whole capture, with **every** occurrence
timestamp retained so shape can be derived afterwards.

**Why.** Keeping only `count + first + last` makes a 15-second burst
indistinguishable from a 27-minute crash loop. 01-healthy's `Unhealthy` finding
(n=3 over 13s) and 02's `BackOff` finding (n=91 over 26m) would both read as "a
finding with a first and last timestamp".

## D-23: 05's impure eviction finding is disclosed rather than split

**Reversed by D-28.**

`data-pipeline / data / Evicted` in 05 has n=4 across two nodes: one background
eviction on node-2 at 10:02:32, and three incident evictions on node-4 between
10:15:25 and 10:19:56. This decision kept the finding merged and disclosed the
split in the evidence line, on the reasoning that disclosing impurity beats
inventing a threshold to hide it.

**Why it fell.** It assumed the two evictions were the same failure mode
observed in two places. Reading the bodies showed otherwise:

| Time | Node | Body |
|---|---|---|
| 10:02:32 | node-2 | `The node was low on resource: memory. Container pipe...` |
| 10:15:25 | node-4 | `The node had condition: [DiskPressure].` |

The taxonomy already separates these. `evicted/memory-pressure` and
`evicted/disk-pressure` are distinct rules, so no new threshold is needed to
tell them apart. The information was there and the key was throwing it away.

**Alternative still rejected: episode splitting on a temporal gap.** Splitting
wherever the inter-occurrence gap exceeds some multiple of the median would also
separate these two. It needs a constant nothing in the data justifies, and it
would fire unpredictably on sparse findings elsewhere.

## D-28: The fired taxonomy rule is part of the group key

**Accepted.** Supersedes D-23.

Final key: **`(Kind, Workload, Namespace, Reason, Rule)`**, where `Rule` is a
short stable identifier for the taxonomy row that classified the record.

**Why.** One reason can carry two failure modes. Measured across the corpus,
`Evicted` splits into disk-pressure and memory-pressure on the same workload
(`data-pipeline`: 3 and 5; `batch-reporter`: 1 and 1). Two different failure
modes are two facts, and merging them drags the incident finding's `FirstSeen`
thirteen minutes earlier than the event that explains it.

**Why `Rule` and not `Cause`.** `Cause` is display text written for a human
reader. Keying on it would mean rewording a sentence silently changes how
records coalesce. `Rule` is an identifier whose only job is identity.

**Cost, verified: none.** Every other group in the corpus matches a single rule,
so nothing else fragments. 04's 222 `Unhealthy` are all readiness probes, 03's
24 `Failed` are all image-pull, 02's 91 `BackOff` are all crash-loop. The only
capture whose count changes is 05, from 9 findings to 10.

**This is what D-12 promised.** The node came out of the key on the grounds that
a real fix existed for 05. This is it, and unlike the node it costs nothing
elsewhere.

---

# Layer 5: causality

## D-17: A trail is a filtered, time-sorted slice of the raw records

**Accepted.** Reverses D-18 through D-20; later narrowed by D-31.

```go
func Trail(all []Event, f Finding, window time.Duration) []Event
```

Three OR'd conditions (same pod as any of `f.Pods`; a node condition on `f`'s
node; a `ScalingReplicaSet` for `f.Workload` within `window` before
`f.FirstSeen`), then sort by timestamp. One linear scan of 20,000 records,
computed on demand, stored nowhere.

**Why.** After classification the working set is ≤227 records:

| Capture | Records | Issue records |
|---|---|---|
| 01-healthy | 20,000 | 5 |
| 02 | 20,000 | 122 |
| 03 | 20,000 | 47 |
| 04 | 20,000 | 227 |
| 05 | 20,000 | 16 |
| 06 | 20,000 (+1 skipped) | 182 |

At n=227 a linear scan is free. Everything in D-18 through D-20 was solving a
performance problem that doesn't exist.

## D-18: A precomputed Forest Data Structure over a time-sorted entry array

**Parked** by D-17, **un-parked** by D-31.

Every entry carries `Parent int` into a slice sorted by `FirstSeen`, with `-1`
meaning origin. The invariant `Parent[i] < i` (a cause is always earlier than
its effect) makes cycles structurally impossible, makes the time sort double as
a topological sort, and makes the ancestor walk an integer loop.

**Why parked rather than rejected.** The reasoning survives even where the
implementation isn't needed. D-31 brought it back at a different grain.

## D-19: Forest, not a general DAG: at most one parent

**Accepted** as a rule; the mechanism is D-31.

Real causality is a DAG, since several causes contribute to one effect. The
artifact is deliberately a Forest Data Structure.

**Why.** The moment a node has two parents, "pull the thread" stops being a
well-defined gesture:

```
Why was auth-service evicted?
├── node-4 had disk pressure
├── the 10:16 deploy increased replicas
└── the pod exceeded its memory request
```

Three leads and no answer. At 3am that's a research project.

**Known limitation, stated in the report.** Genuine joint causation can't be
expressed. A pod evicted from node-4 for disk pressure that then can't
reschedule because the cluster is CPU-starved has two real contributing causes
and this model must pick one. **No capture in this corpus exhibits it.** 02, 03,
04, 05 and 06 are all single chains. The boundary is real and it belongs in the
write-up.

## D-20: Two-arena physical layout

**Rejected**, superseded by D-17.

Entries holding `lo, hi int` windows into a flat `[]Occurrence` sorted by
`(entry, time)`, plus a second arena for lifecycle events sorted by `(pod,
time)` and binary-searched.

**Why rejected.** It's a cache-locality optimisation for a working set of ≤227
records that fits in ~30 KB regardless. It bought nothing and cost a great deal
of conceptual weight. Plain `[]Event` and `[]Finding` with linear scans.

## D-21: Time is a veto, never a proposal

**Accepted.**

No causal rule may say "these are close in time, therefore related." Every rule
requires a concrete shared dimension first (same pod, same node, same workload)
and time is applied afterwards only to *reject* matches that are too far apart.
Time can remove a candidate edge. It can never create one.

**Evidence.** 04-test-a contains 3 baseline `Unhealthy` events on
`data-pipeline` in the same window as 222 `Unhealthy` events on
`checkout-service`. Same minutes, no relationship. A rule keyed on temporal
proximity reports one incident where there are two.

## D-22: Rule priority is the outer loop; time proximity is the inner loop

**Accepted.**

```go
for _, r := range rules {          // strongest relationship first
    for j := i - 1; j >= 0; j-- {  // then nearest in time
        if r.links(...) { return j }
    }
}
```

**Why.** Transposed, "first match" silently becomes *nearest candidate matching
any rule* where it should mean *strongest rule matching any candidate*. In 05
that would let a weak same-workload link to the background node-2 eviction beat
the node-condition link that's actually correct.

## D-24: Union-find: skeleton adopted, algorithm rejected

**Rejected.**

Union-find's physical form (`parent []int` with `find()` walking to a root) is
exactly the shape a causal Forest Data Structure takes, and its native question
("are these the
same set?") is exactly 05's question. Both of its optimisations disqualify it:

- **Path compression** repoints each node directly at its root. The intermediate
  hops *are the product*. `BackOff → OOMKilling → origin` compressed to
  `BackOff → origin` keeps the verdict and throws away the diagnosis.
- **Union by rank** picks whichever parent balances the tree. The requirement is
  the parent that's *true*. Unrelated criteria.

Union-find minus rank minus path compression is a Forest Data Structure. At
n≈10 there's
nothing left for the algorithm to contribute.

## D-25: Fixed-width time buckets derive shape; they never link findings

**Accepted.**

Buckets summarise **one** finding's behaviour over time. They must never
associate two findings, which is D-21 restated at the implementation level.

Buckets are computed on demand from retained occurrence timestamps and stored
nowhere, because a second copy of a derivable fact is a second source of truth.

## D-31: The causal structure is a Forest Data Structure over findings

**Accepted.** Un-parks D-18, narrows D-17.

```go
type Forest struct {
    Findings []group.Finding // ascending by FirstSeen
    Edges    []Edge          // parallel; Edges[i] describes Findings[i]
}
type Edge struct { Parent int; Kind Kind; Evidence string }
```

One `int` per finding. `Parent == -1` is a root.

**Invariant: `Edges[i].Parent < i`**, because `Build` only scans backwards from
`i`. Everything safety-related falls out of it. Cycles are structurally
impossible, so there's no visited set, no cycle check and no error path.
`Findings` is already a topological order, because sorting by time *is* the
topological sort. `Children(i)` need only scan forward from `i+1`.

**Grain: finding→finding, not finding→record.** D-18 was parked as superseded by
D-17's trail-on-demand. The simulation showed it was the right shape at the
wrong grain. Both now survive at different jobs: the Forest Data Structure is
the causal
structure, and raw-record queries remain the *evidence* mechanism for quoting
things like 02's OOM interval.

**Why `Forest` wraps both slices** rather than `Build` returning a bare
`[]Edge`. The two are index-coupled, and handing them back separately makes
"same length, same order" an unenforced convention that one misplaced
`sort.Slice` breaks silently. The failure mode would be a *wrong diagnosis*
rather than a crash. Only `Build` constructs the pair.

## D-32: Rule priority is dimension specificity: pod > node > workload

**Accepted.**

Rule priority is the outer loop, time proximity the inner (D-22).

**The fight.** The alternative was ordering by *parent kind*, putting
cause-shaped parents (deploy markers, node conditions) before symptom parents.
It loses 03's depth. `BackOff`[image-pull-retry] matches both the same-pod rule
(against `Failed`) and the deploy rule (against `ScalingReplicaSet`). Under
specificity ordering the chain is three deep:

```
ScalingReplicaSet → Failed[image-pull] → BackOff[retry]
```

Under kind ordering it flattens to two siblings under the deploy, losing the
claim that the retry is caused by the pull failure rather than by the rollout.

**Known limitation, recorded rather than hidden.** The same-pod rule links to
*any* earlier finding sharing a pod, so an unrelated earlier symptom on that pod
could become its parent. Checked against the corpus and it doesn't occur: 05's
`batch-reporter` Unhealthy (pod `19cb12bab-7143a`) and its eviction (pod
`820090759-b420b`) are different instances, so the node-condition rule correctly
wins. Guarding it needs a reason-precedence table, which is inflation for a case
with no evidence behind it.

## D-33: A single 300-second window, derived from the data

**Accepted.** Closes O-03.

Measured across all six captures:

| | value |
|---|---|
| every true edge | +4.4, +4.7, +7.3, +7.4, +10.4, +15.4, +25.8, +48.7, +85.4, +88.6, **+97.9s** |
| nearest false candidate | 06's `LALALALA`, **+600s** after the data-pipeline rollout |

Any window in **(98s, 600s)** produces identical results. 300s sits mid-gap: 3×
the largest true edge, half the smallest false one.

**One constant, not three.** Per-rule windows would be tunable to the corpus and
therefore overfitted to it. A single number with a stated margin is honest about
how much evidence backs it.

## D-34: Roots are classified by what they are, not by when they are

**Accepted.** Supersedes D-27's time-based truncation test.

| Root | Meaning | Fires on |
|---|---|---|
| deploy marker or node condition | **explained**, a cause was reached | 03, 04, 05, 06 |
| a failure *with* children | **partially explained**, proximate cause found and nothing upstream | 02 (`OOMKilling`) |
| a failure with *no* children | **unexplained** | 01's three, and every background finding |

**Why it beats D-27.** That version tested whether the root sat within seconds
of the capture start. 02's first `OOMKilling` is 156 seconds in, so the
heuristic would have mis-called it, and it needed a threshold nothing justified.
This version needs **no threshold at all** and produces the honest sentence
directly: *"the crash-loop is explained by OOM kills; what drove the memory
growth is not in this capture."*

"Unexplained and transient" is also exactly the suppression predicate the
diagnosis layer needs to silence `01-healthy` without deleting 05.

## D-35: An edge must be provable from record text or object identity

**Accepted.** Strengthens D-21.

Co-occurrence is never sufficient. Every rule points at something a human can
read in the data:

| Rule | Proof | Strength |
|---|---|---|
| same pod | **identity**, same `k8s.object.name` | inferred (adjacency) |
| node condition | **named**, `NodeHas<X>` ↔ child body contains `[<X>]` | stated |
| deploy marker | **named**, deploy body gives `<workload>-<hash>`; child pods are `<that>-<suffix>` | stated |

**Evidence, 05-test-b:**

```
10:15:00.000  node-4  NodeHasDiskPressure  "Node node-4 status is now: NodeHasDiskPressure"
10:15:15.359  node-4  Evicted              "The node had condition: [DiskPressure]."   ×10
10:02:32.345  node-2  Evicted              "The node was low on resource: memory. ..."  ← control
```

The evicted pod's own record names the condition that evicted it. The node-2
eviction is the control that proves the rule discriminates: same `Evicted`
reason, different named cause, correctly unlinked, and it would still be
rejected even if it were on node-4.

**Why this matters beyond correctness.** The brief asks for *"Evidence: which
events from your tool's output led you there? Quote specific findings."* A
stated-cause edge yields a verbatim quote. A co-occurrence edge yields a
hand-wave.

**Honest asymmetry.** The same-pod rule is the *weakest* of the three.
`OOMKilling → BackOff` is identity plus adjacency; the `BackOff` body says
"Back-off restarting failed container" and never names the OOM. It still
outranks the others for the depth reason in D-32, and the difference must flow
into the confidence the diagnosis layer reports.

## D-36: The deploy rule keys on the named ReplicaSet, not the workload

**Accepted.** Supersedes the same-workload rule specified earlier in this pass.

The deploy body names the exact ReplicaSet it scaled. Failing pods must belong
to **that** ReplicaSet rather than merely to the same service.

```
03: "Scaled up replica set payment-service-9e3f1a2b8 to 3"
    failing:    payment-service-9e3f1a2b8-{005e2,45cbb,b8c2e}      3/3 from that RS
    background: auth-service-df386e8ed-…, data-pipeline-c7a123947-…  different RS
04: 5/5 from checkout-service-7d4f8b9c5        06: 6/6 from data-pipeline-3c7d2e1a9
```

**Why.** "Same workload" links a service that merely *happened* to be deployed
recently. "Pods from the ReplicaSet this rollout created" is the actual causal
claim, and it's the difference between *"the deploy caused this"* and *"this
service was deployed at some point"*.

## D-37: Two edge tiers, not a confidence score

**Accepted.**

`Caused` means the record text names the link, or it's the same object.
`MayRelate` means a shared dimension inside the window with nothing in the text
connecting them.

**Why not a numeric score.** A score is a model, and a model needs calibration
data that doesn't exist here. Two tiers are each defensible from the rule that
fired.

**The `MayRelate` tier is empty on this corpus and stays empty.** Nothing sits
on node-4 in 05 but the condition and its ten evictions, and every deploy edge
is textually provable. Loosening a rule to populate the tier would manufacture
exactly the weak edges D-21 exists to prevent.

## D-38: Complexity, scale, and why no graph library

**Accepted.**

**Time and space, measured rather than estimated.** `go test -bench . -benchmem`,
Linux, 16 logical cores.

`link.Build`, findings synthesised in the shape of a real capture (a rollout
followed by failures on its pods, so about half acquire a parent):

| n (findings) | ns/op | B/op | allocs/op |
|---|---|---|---|
| **10** (the real working point) | 29,868 | 3,634 | 51 |
| 100 | 1,411,987 | 130,530 | 1,561 |
| 1,000 | 32,289,829 | 1,497,458 | 17,766 |

Read side at n=1000: `Roots` 2,801 ns / 10 allocs; `RootOf` **2.3 ns**, zero
allocations, an integer loop over a slice already in cache.

Whole pipeline, capture read into memory and replayed from a `bytes.Reader` so
disk is excluded:

| Capture | ns/op | throughput | B/op | allocs/op |
|---|---|---|---|---|
| 01-healthy | 140,288,461 | 118 MB/s | 15,154,132 | 338,688 |
| 04-test-a (heaviest, 227 reportable) | 138,623,705 | 120 MB/s | 15,368,683 | 339,221 |
| 05-test-b | 140,015,896 | 118 MB/s | 15,163,105 | 338,797 |

**~140 ms against the brief's 5-second budget: 35× headroom.** Allocation is ~17
per record, dominated by JSON unmarshalling into the wire struct. At this margin
there's nothing worth reclaiming.

**Two corrections to what this entry first claimed**, both from estimates I
never measured:

- It said n=1,000 would be "a few ms". It's **32 ms**, roughly ten times slower.
  Each comparison is a function call doing slice intersection and string work
  rather than a bare integer compare, and the estimate assumed the latter.
- It extrapolated n=5,000 at "~100ms". Measured scaling puts it nearer 300 ms.
  The figure is dropped rather than re-estimated. If that size ever matters it
  should be measured.

**The scaling is sub-quadratic in practice.** Ten times the findings costs 47×
then 23×, rather than the 100× a true `O(n²)` would. The backwards scan stops at
the first match and most findings find a parent within a few steps, so the inner
loop rarely runs to completion. The worst case stays quadratic; the observed
case doesn't.

**Why n stays small.** n counts *findings*, not records. Findings grow with the
number of distinct `(workload, namespace, reason, rule)` tuples, meaning
distinct failure modes, and a cluster emitting ten times the events has roughly
the same number of those. Observed: 20,000 records → 3 to 10 findings. The
quadratic term is over a quantity that doesn't track input size.

**Why overlap-based structures are wrong rather than merely unnecessary.**
Interval trees and sweep lines answer "which intervals overlap". Measured
against the corpus, that's the wrong question in both directions:

| Quadrant | Corpus example | Overlap-based verdict |
|---|---|---|
| overlapping, **unrelated** | 02: `order-service/Unhealthy` 10:17:02–14 on node-6 sits **inside** `OOMKilling` 10:02:37–10:28:20 which spans node-6 | false positive |
| overlapping, related | 02 `OOMKilling`/`BackOff`; 03 `Failed`/`BackOff` | correct |
| **non-overlapping, related** | every deploy edge and the whole 05 incident, since causes are *instants* and effects begin after, giving **zero** overlap | **all missed** |
| non-overlapping, unrelated | the background floor | correct |

Overlap would miss the four best diagnoses in the corpus and invent a link
between a memory leak and an unrelated readiness probe. The real criterion is
**ordering plus a named shared dimension**, and ordering is one scalar
comparison, which is precisely why a sorted slice suffices.

**Time enters in exactly two places, never as a proposer.** As ordering (a
parent must be strictly earlier, which is also what makes cycles impossible),
and as a veto window that can only reject a candidate a dimension already
proposed.

**The one case union-find's shape would address**, named as the boundary: an
undirected merge, meaning *"these are one incident but the cause is not in the
data."* If 05's capture had begun at 10:15:10 the `NodeHasDiskPressure` record
would be absent, leaving six orphan evictions all naming `[DiskPressure]` on
node-4: obviously one incident, six roots. Cost to handle: ~20 lines grouping
orphans by (stated cause, shared dimension) under a synthetic root, which is **a
map rather than a library**. Doesn't occur in this corpus, and Step 2's workload
rollup already merges the within-workload version. Deferred, not forgotten.

**What would force a real graph.** Multiple parents (joint causation, D-19, no
capture has it), cycles (impossible when parents are strictly earlier),
reachability or shortest-path at scale (n ≤ 10), or incremental update as
records stream (this is a batch tool that reads a file and exits). None apply.

## D-50: Edges carry the rule that produced them

**Accepted.**

`link.Edge` gains `Rule string`, the name of the causal rule that fired.

It was already present as a field on the rule table and thrown away. Two callers
need it, and both were about to re-derive it from the evidence string, which is
display text:

1. The tree collapses sibling children that share an explanation (D-51), and
   "same explanation" means *same causal rule*, not *same evidence text*. The
   six evictions in 05 all fired `node-condition-named` but their evidence
   strings differ in elapsed time.
2. `ANALYSIS.md` needs to say which relationships the tool asserted, and
   counting rule names is not the same as grepping prose.

Parsing a sentence to recover a decision that was made in code is the failure
this prevents.

---

# Layer 6: diagnosis

## D-26: Shape, not reason, discriminates real problems from background

**Accepted.**

**Evidence: every capture, including 01-healthy, carries the same background
floor.**

| Capture | Background warnings |
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
before capture end, against 222 events on 5 pods over 8 minutes still firing at
capture end. Shape only exists once records are grouped (D-16).

## D-27: The trail must be able to report that it ran out of data

**Accepted**, then superseded by D-34's non-temporal test.

Two distinct terminations, reported differently. *Origin found*, meaning a
cause-shaped record with nothing before it. *Trail truncated*, meaning the head
of the chain sits within seconds of the capture start, with the tool naming what
would raise confidence.

**Evidence.** 02 has no deploy marker. The capture opens 10:00:01 and the first
`OOMKilling` is 10:02:37. The honest origin is *"earliest evidence at 10:02:37,
the cause predates this capture window"*, materially different from 04 where the
origin is a real causal event.

**Why it matters.** The brief requires a confidence level per scenario and asks
what would raise it. A tool that emits that has done on-call reasoning. A tool
whose author writes it by hand afterwards hasn't.

## D-40: The pattern is a diagnose-stage verdict, not a field on `Finding`

**Accepted.** Corrects the plan in `handover.md` §8.1.

The plan said "add a `Pattern` to `group.Finding`". Wrong on two counts, both
only visible once the Forest Data Structure existed:

1. `group`'s own prohibition is that it *"does not interpret shape over time,
   rank findings, or decide what caused what"*. A pattern is exactly the first
   of those.
2. Two of the five patterns, deploy-correlated and node issue, are read off
   `RootOf(i)`. `group` runs before `link` and has no Forest Data Structure to
   consult. The
   field would have to be filled in later by someone else, which is a mutable
   hole in a value the rest of the pipeline treats as final.

So a fifth package, `internal/diagnose`, consuming a Forest Data Structure and
producing a
verdict per finding:

```go
type Chart struct {
	link.Forest              // embedded: Findings, Edges, Roots, Children, RootOf
	Diagnoses []Diagnosis    // parallel to Forest.Findings
}
```

Embedded rather than a field, so `Chart` is usable everywhere a `Forest` was and
the three parallel slices travel as one value. Same discipline D-31 applied to
`Findings`/`Edges`, for the same reason: index-coupled slices that can be
separated will eventually be sorted apart, and the failure mode is a wrong
diagnosis rather than a crash.

**Rejected:** returning `[]Diagnosis` on its own and letting `triage` hold three
slices. It compiles, it's one type fewer, and it makes the misalignment
possible.

## D-41: The pattern vocabulary is the brief's, verbatim

**Accepted.**

The brief, item 4: *"surface the pattern (sustained crash-loop, transient blip,
deploy-correlated failure, capacity issue, node issue, etc.)"*, and its example
output has a `Pattern:` line. Those five, in those words.

**The fight.** There's a real argument for a different vocabulary. Two of the
five, deploy-correlated and node issue, are pure restatements of *"my root is a
deploy marker"* and *"my root is a node condition"*, which the Forest Data
Structure already
says with evidence in far more detail:

> `rollout created replica set payment-service-9e3f1a2b8 4.4s earlier; all 3
> affected pods belong to it`

Next to that, `Pattern: deploy-correlated failure` adds nothing. A vocabulary
describing **shape only** (transient / sustained / point) would carve the space
along an axis the Forest Data Structure doesn't already cover, and would be
defensible.

**Why it lost.** The brief names its five and asks for them by name. A grader
reading for their own vocabulary should find their own vocabulary. Inventing a
cleaner taxonomy and making them translate is scoring points against yourself.
The redundancy is real and harmless: the pattern is the one-word category, the
edge is the evidence, and they sit on different lines.

## D-42: Patterns are a priority table; the mechanism outranks the trigger

**Accepted.** Row 6's guard strengthened by D-46.

06's `FailedScheduling` is genuinely both. Its body reads `0/6 nodes are
available: 6 Insufficient cpu`, which is a capacity issue, and its root is
`data-pipeline`'s rollout 4.7s earlier, which is deploy-correlated. One finding,
two true labels, and `Pattern` is one value.

The table, first match wins:

| # | Pattern | Fires when | Fires on |
|---|---|---|---|
| 0 | *(none)* | the finding is a deploy marker | 03, 04, 06 rollouts |
| 1 | capacity issue | rule is `failed-scheduling` **and** a body names `Insufficient` | 06 |
| 2 | sustained crash-loop | rule is `backoff/crash-loop` or `oom-killed`, and not transient | 02 ×2 |
| 3 | node issue | the root is a Node-kind finding | 05 ×7 |
| 4 | deploy-correlated failure | the root is a deploy marker | 03 ×2, 04 |
| 5 | transient blip | transient by shape (D-43) | every background finding |
| 6 | *(none)* | anything else, including unrecognised reasons | 06's `LALALALA` |

**Ordering principle: a pattern that names the failure *mechanism* outranks one
that names its *trigger*,** because the trigger is already stated by the causal
edge and the mechanism isn't stated anywhere else. So 06 reads *"capacity
issue"*, and the rollout that provoked it appears one line below as the edge.

**Two deliberate abstentions.**

*A deploy marker gets no pattern.* Rule 4 as written would fire on the rollout
itself, since `RootOf(i) == i` and it is a deploy marker, labelling a successful
deployment a "deploy-correlated failure". A rollout is an anchor that did no
damage of its own. Row 0 catches it first.

*An unrecognised reason gets no pattern.* 06's `LALALALA` is transient by every
shape measure and row 5 would call it a transient blip. Handing a diagnosis
label to a reason the taxonomy couldn't interpret is precisely the confident
wrongness the conservative fallback exists to avoid. It's reported
uninterpreted, exactly as classification left it.

**Rejected:** a set of patterns per finding rather than one. It's more truthful,
since 06 really is both, and it makes the output a bag of labels to read rather
than a category to scan, in a report whose entire premise is a reader under time
pressure. The full truth is still recoverable: it's the pattern line plus the
edge line.

## D-43: "Transient" is a conjunction of three named bounds

**Accepted.** Closes half of O-02.

Measured across all six captures, per finding:

| | count | pods | span |
|---|---|---|---|
| background findings (3 in every file) | 1–3 | 1 | 0–16s |
| real problems (02, 03, 04, 06) | 18–222 | 3–6 | 7m49s–26m6s |

```go
transientCount = 10   // gap is (3, 18); mid-gap
transientPods  = 1    // a boundary, not a threshold: one instance, or the workload
transientSpan  = 60s  // gap is (16s, 4m31s)
```

**Why a conjunction and not the count alone.** Count alone separates the corpus
with a 6× margin and would be simpler. But 05's
`data-pipeline/Evicted[disk-pressure]` is `n=3` across **3 pods** over
**4m31s**. Count alone calls that a blip, and it's part of a node-wide incident.
Blast radius and duration are independent axes from volume, and a definition of
"transient" that reads only volume is wrong on its face.

**`transientPods = 1` isn't a tuned number.** It's the boundary between one
instance misbehaving and the workload misbehaving. The comparison is `<= 1`
rather than `== 1` because a Node or Deployment finding carries no pods at all,
and node-4's condition has to be able to be transient by shape. It's kept by
D-44's second clause rather than by pretending it's large.

**`transientSpan` is inert on this corpus and included anyway.** No finding in
any capture is decided by it: nothing with `count <= 10` and `pods <= 1` has a
span over 16s. It's here because the alternative is a tool that prints
"transient blip" beside a pod that has failed once a minute for twenty minutes.
Its bounds are still observed rather than invented: 3.75× above the background
ceiling and 4.5× below the shortest real problem.

**Measured and dropped: occupancy.** The plan called for "minutes occupied",
meaning distinct wall-clock minutes containing at least one event, to separate a
dense crash-loop from a sparse trickle. Computed over the corpus it's 1–2 for
every background finding and 5–22 for every real one, which is a clean
separation that `span` already makes identically. A fourth metric that never
changes an answer is a fourth metric to explain. Dropped.

## D-44: Suppression is `issue ∧ transient ∧ unexplained ∧ non-explanatory`

**Accepted.** Closes O-02. First clause strengthened by D-46.

The controlling evidence, three findings from the corpus:

| | n | pods | span | parent | children |
|---|---|---|---|---|---|
| 01 `data-pipeline/Evicted[memory-pressure]` | 1 | 1 | 0s | −1 | 0 |
| 05 `auth-service/Evicted[disk-pressure]` | 1 | 1 | 0s | **3** | 0 |
| 05 `node-4/NodeHasDiskPressure` | 1 | 0 | 0s | −1 | **6** |

**Identical on every shape metric, and they need three different verdicts.** The
first is background noise that must not be reported. The second is a pod killed
by a dying node. The third is the single most important line in that capture. No
threshold on count, pods, span or occupancy can separate them, because there's
nothing there to separate. Only the Forest Data Structure can.

```
suppress(i) := Category == Issue
             ∧ transient(Findings[i])          // D-43 — it is small
             ∧ Edges[i].Parent == noParent     // nothing explains it
             ∧ len(Children(i)) == 0           // and it explains nothing
```

**The fourth clause isn't decoration.** Without it node-4's condition (`n=1`,
`pods=0`, `span=0`, a root) is suppressed, and with it goes the entire
`05-test-b` incident, since every one of its six evictions hangs off it.

**Why `Category == Issue` guards the whole thing.** 06's `LALALALA` is
transient, unexplained and childless, and would be suppressed. But
`CategoryUnclassified` exists precisely so that a reason we can't interpret is
*surfaced demoted rather than buried*, and suppressing it re-buries it. Deploy
markers are excluded by the same clause, which is correct for a different
reason: a marker with no children is a rollout that broke nothing, and there's
no shape argument for hiding it.

**Result across the corpus:**

| Capture | findings | suppressed | reported |
|---|---|---|---|
| 01-healthy | 3 | **3** | **0** |
| 02-memory-leak | 5 | 3 | 2 |
| 03-image-pull-failure | 6 | 3 | 3 |
| 04-test-a | 5 | 3 | 2 |
| 05-test-b | 10 | 3 | 7 |
| 06-test-c | 5 † | 3 | 2 |

† 06-test-c is **5 findings, 2 reported** as the file ships. Line 20,001 is a
hand-added record with reason `LALALALA`, commented out with a leading `// `, so
the decoder counts it as `skipped 1`. Uncomment it and 06 reads 6 findings and 3
reported, with the extra one surfaced unlabelled and never suppressed (D-46).
Demonstrated in [`analysis/06-test-c.md`](../analysis/06-test-c.md#the-extra-line-at-the-end-of-this-file).

**Exactly three per capture, in every capture**, and the same three shapes each
time (an `Evicted[memory-pressure]`, a `FailedMount`, an `Unhealthy` ×3). The
brief plants an identical background floor in all six files. A predicate tuned
to one file wouldn't land on the same three in the other five, so this is
corroboration rather than a fit.

`01-healthy` reports nothing. That was the point.

## D-45: Suppressed findings are counted and disclosed, never deleted

**Accepted.**

`Suppressed` is a property of the diagnosis rather than a filter applied inside
`diagnose`. The header states the count, the same way it already states skipped
and unrecognised records:

```
20,000 records, 19,772 filtered as noise, 2 findings, 3 suppressed as background
```

This is the rule the pipeline already follows twice: D-06 keeps noise records in
`Result.Records`, and the decoder discloses its skip count so a truncated
capture can't look like a clean one. It's what makes an aggressive threshold
safe. The worst case for a mis-tuned `transientCount` is a line the reader has
to ask about, rather than a fact that silently left the building.

It also keeps `report` honest under its own prohibition (render what you're
given, decide nothing) while leaving room for a `--all` flag to render the
suppressed rows without any stage having to re-derive them.

## D-46: An unrecognised reason is never labelled and never suppressed

**Accepted.** Strengthens the `Category == Issue` guard in D-42 and D-44.

**How it surfaced.** `06-test-c.jsonl`'s planted record was internally
inconsistent: `severity_text: "Normal"` alongside `severity_number: 13`, which
is Warning. Corrected to Warning, it stops taking the fallback's Normal branch
(`CategoryUnclassified`) and takes the Warning branch, which promotes it to
`CategoryIssue` with `Recognised: false`.

That walked it straight past both guards as written. One occurrence, one pod,
zero span, a root with no children: **transient, unexplained, unexplanatory, and
now an issue.** D-44's predicate would have suppressed it and D-42's table would
have labelled it a transient blip.

The record argues against both, in its own body:

> *"In case there are some new events that we haven't really recognized and
> handled, we'd much rather surface it, instead of burying it"*

**The principle, which is what actually changed.** Suppressing a finding is a
claim to understand it well enough to know it doesn't matter. Naming its pattern
is a claim to know what kind of thing it is. Neither claim can be made about a
reason that isn't in the taxonomy, which is the entire premise of the
conservative fallback, and letting shape override it re-buries exactly what the
fallback exists to surface.

So `Recognised` is carried from `classify.Classification` through
`group.Finding` to the diagnose stage, and one predicate governs both questions:

```go
func diagnosable(f group.Finding) bool {
	return f.Category == classify.CategoryIssue && f.Recognised
}
```

One function rather than two checks, because the two must not drift. **A finding
we decline to categorise is exactly a finding we may not dismiss.**

**Why this beats the guard it replaces.** `Category == Issue` excluded
unrecognised reasons only by side effect, since they happen to sit in a
different category. The moment severity moved, the side effect stopped holding.
Keying on `Recognised` states the actual reason and covers both fallback
branches at once.

**Consequence for coverage.** No capture now exercises the
`CategoryUnclassified` branch. `classify`'s own tests do, and the triage test
says so explicitly rather than leaving the gap silent.

## D-48: The "why" is a signature over record bodies

**Accepted.**

The patterns answer *what kind of failure this is*. They don't answer *why*, and
the brief asks for both. Its output spec calls for *"evidence: event counts,
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
build whose `/healthcheck` path no longer exists**, which is the actual
diagnosis, the actual remediation, and one substring away from being free.

**Why a signature and not a quoted body.** A finding holds up to 222 records and
their bodies differ in pod name, IP and UID. Quoting the first is arbitrary and
hides that 04 has *three distinct symptoms*. Normalising the volatile tokens and
counting collapses them:

| Finding | records | signatures |
|---|---|---|
| 04 `Unhealthy` | 222 | 3 |
| 06 `FailedScheduling` | 177 | 3 |
| 02 `BackOff` | 91 | 1 |
| 03 `Failed` | 24 | 3 |

**Normalised:** pod names (`<pod>`), IPv4 addresses (`<ip>`), and parenthesised
UIDs (dropped). Nothing else. In particular **numbers with units are never
touched**. `512Mi`, `404`, `8080` and `0/6` are the answer, and a normaliser
that ate them would delete exactly what this feature exists to surface.

**Why this belongs in `diagnose` and not in `report`.** Reducing 222 bodies to 3
ranked signatures is an interpretation of shape across a finding's records,
which is this stage's remit and which `report` is explicitly forbidden to do.
`report` renders the list it's handed.

## D-49: Signatures rank by specificity, then by frequency

**Accepted.**

Frequency alone is the wrong sort key, and 03 is the proof:

```
Error: ImagePullBackOff                                          18 of 24
Failed to pull image "registry.internal/payment-service:v2.14.0-rc3"
  ... manifest ... not found                                      3 of 24
Error: ErrImagePull                                               3 of 24
```

The **rarest** signature is the **only** one that says anything. `Error:
ImagePullBackOff` restates the REASON column; the 3-occurrence line names the
tag that doesn't exist. Leading with the common one buries the answer under a
paraphrase of the question.

**The rule.** A signature is *specific* if, after normalisation, it contains a
**quoted string** or a **digit**. Specific signatures sort above generic ones,
and within each group frequency decides.

**Why that predicate.** Normalisation has already removed the volatile digits
(IPs, UIDs, pod hashes), so a digit that survives is a fact about the failure: a
status code, a resource amount, a port, a node tally. A quoted string is a name
the cluster chose to quote: an image ref, a volume, a configmap.

**Verified against every multi-signature finding in the corpus:**

| Finding | Leads with | Correct? |
|---|---|---|
| 03 `Failed` | `Failed to pull image "…v2.14.0-rc3"` (quoted, 3×) | yes, the bad tag |
| 04 `Unhealthy` | `HTTP probe failed with statuscode: 404` (digit, 178×) | yes, the missing endpoint |
| 06 `FailedScheduling` | `0/6 nodes are available: 6 Insufficient cpu` (digit, 118×) | yes |
| 05 `Evicted` | `The node had condition: [DiskPressure].` (sole signature) | n/a |

**Rejected: longest-first.** It gets 03 and 04 right, and it gets them right by
accident, because length is a proxy for nothing. A rule that happens to work is
a rule that will stop working without telling you.

**Rejected: a taxonomy of "interesting" tokens per reason.** More accurate and
more fitted to this corpus. Specificity is a property of a sentence rather than
of a Kubernetes reason, and the general rule is the one that survives an event
type we've never seen.

**Binary, not a score.** A ranking function with weights would need every weight
justified, and the corpus supports exactly one distinction: does this line carry
a concrete noun. Frequency is a real tiebreak. Anything finer would be invented.

## D-53: Remediation is a column in the taxonomy, not a rules engine

**Accepted.**

The brief asks the analysis report for *"remediation for the next five
minutes"*, and a triage tool that names a cause without naming a next move stops
one step short of useful. Each taxonomy row gains a `fix`, rendered as
`RECOMMENDED` at the foot of the incident it belongs to.

**Keyed on the mechanism's rule, not the root's.** 03's root is a rollout, and
the fix for a rollout is nothing. The fix belongs to what actually broke, which
is the same node the verdict quotes.

**Templated, so it's copy-pasteable.** `{workload}`, `{namespace}`, `{node}` and
`{pod}` are substituted from the finding, because a command an on-call engineer
has to hand-edit at 3am is a command they'll get wrong.

**Diagnostic before destructive.** Where both exist, the safe command comes
first. `kubectl logs --previous` before `kubectl set resources`. `kubectl
describe node` before `kubectl cordon`. The tool is confident about what it
observed and has no business being confident about what to change.

**Explicitly not a rules engine.** No conditionals, no severity-dependent
advice, no synthesis across findings. One string per taxonomy row, the same
shape as `cause`. Adding remediation for a new failure mode is filling in a
column, and a row with no `fix` renders nothing rather than something vague.

**A limitation stated rather than hidden.** The fix table covers only the
fourteen reasons the taxonomy covers, which are the reasons that occur in these
six captures. See O-04.

## D-54: `Incident` is the unit the report renders, and it's built in `diagnose`

**Accepted.** Supersedes the flat ordering sketched in `handover.md` §8.2.

The renderer needs, per incident: its severity, its blast radius, which node
explains it, which node paged, and where it sits in priority order. Every one of
those is a derived fact and `report` is forbidden to derive. So they're computed
once, in `diagnose`, and handed over:

```go
type Incident struct {
	Root, Mechanism, Paged int   // Paged is -1 when several symptoms tie
	Severity   event.Severity    // MAX over the tree, never the root's
	Members    []int             // root and descendants, in time order
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

The verdict first quoted the deepest node. In 03 that's `BackOff`, meaning
"repeated image-pull retry", which is a consequence, where the shallowest
failure is `Failed`, which names the tag that doesn't exist. Depth is the right
axis for *what paged you* and the wrong one for *what went wrong*.

**`Paged` is -1 on a tie.** 05 has six equally-bad leaves and no way to know
which one raised the page. Marking the first is a fabrication, so the root
carries the incident instead.

**Blast radius counts Pod-kind findings only.** Counting all members made 05
read *"7 workloads across 4 namespaces"*. node-4 is not a workload, and
`default` is a namespace where nothing happened. Six workloads, three
namespaces.

**Ordering: severity, then blast radius, then time.** This is the brief's
"prioritised summary" applied to incidents rather than findings, so an incident
is never split across the ranking.

**`StillFailing` is `capture end - Last <= 2m`.** Derived: across the corpus
every real problem ends 0–100s before the capture does, and every background
finding ends 600–1730s before. Any value in that gap behaves identically, and
two minutes sits inside it while reading as a round number rather than a tuned
one. It's the difference between *"this is happening now"* and *"this
happened"*, which is the first thing an on-call reader needs and the last thing
a flat report tells them. `diagnose.Build` therefore takes the capture end as a
parameter, since the stage can't say whether something is ongoing without
knowing when the observation stopped.

## D-57: Signature-level meanings, because the body decides what a probe failure is

**Accepted.** Narrows the rule-level `meaning` of D-53.

`meaning` is keyed on the taxonomy rule, and for probe failures that's the wrong
key. One reason, `Unhealthy`, carries three different situations:

| Body | What it proves | What to do |
|---|---|---|
| `connection refused` | TCP RST, **nothing is listening** | the process is not up; it's being restarted |
| `context deadline exceeded` | connection not refused, **no answer in time** | the process is up but stuck or saturated |
| `HTTP statuscode: 404` | server up and routing, **path does not exist** | a deploy defect |

The rule-level sentence, *"the application is running but not answering its
health check"*, is wrong for two of the three. For 404 the application **is**
answering. For a refused connection it isn't running at all.

**Rejected: splitting the taxonomy rule by body.** `Failed` already does exactly
this (`failed/image-pull` against `failed/sandbox-creation`), so it's the
obvious move, and it's wrong here because `Rule` is in the coalescing key.
04-test-a's 222 records would fragment into three findings, and that's one
broken deployment rather than three problems. Same trap as D-12.

**Accepted: a second, smaller interpretation table keyed on the signature.** The
taxonomy interprets *reasons*; this interprets *signature shapes*. It doesn't
touch grouping, it's data rather than a switch, and a signature that matches no
row simply carries no reading, which is the same abstention the classifier makes
for a reason it can't read.

**Rows are added only for shapes the corpus actually contains:** the three probe
outcomes above, and the scheduler's CPU / memory / both distinction in 06. No
row for `500`, `503`, `no route to host`, or TCP-probe variants, because none
appears in any capture and a row written from documentation is a body matcher
nobody has ever seen fire. `OOMKilling` already proved that trap: the brief's
own example body text would never have matched a real record.

**What it buys, on 04-test-a.** The three probe modes interleave for the whole
7m49s rather than the 404s following a startup phase:

```
checkout-service/404       n=178  10:22:07.319 -> 10:29:56.262
checkout-service/REFUSED   n=24   10:22:34.806 -> 10:29:40.242
checkout-service/TIMEOUT   n=20   10:22:40.925 -> 10:29:55.424
```

The 404 arrives first, 7.3s after the rollout, and never stops. So the reading
is that the 404 is the defect and the other two are churn it causes. Without
per-signature meanings the report shows three counts and leaves that inference
to the reader.

## D-58: Cadence is measured per object, and the trend threshold is conservative

**Accepted.** Corrects a claim made in D-05.

A finding that fired 26 times over 25 minutes has a rhythm, and the rhythm is
the diagnosis. *One OOM kill per pod every four minutes* says more about a
memory leak than the count and the span together.

**Measured per object, then pooled, never across the finding as a whole.**
Interleaving makes the finding-wide gap meaningless:

| Finding | finding-wide median | per-pod median | what the per-pod number is |
|---|---|---|---|
| 04 `Unhealthy` | 1.4s | **10.5s** | the probe period |
| 06 `FailedScheduling` | 1.9s | **20.4s** | the scheduler's retry interval |

1.4s is an artefact of five pods being probed independently. 10.5s is the
`periodSeconds` on the probe, which is a real fact about the deployment.

**Measured, all six captures, intervals pooled per pod:**

| Finding | pods | intervals | median | early→late ratio |
|---|---|---|---|---|
| 02 `OOMKilling` | 4 | 22 | **4m9.8s** | 0.79 |
| 02 `BackOff` | 4 | 87 | 10.8s | 0.70 |
| 03 `Failed` | 3 | 21 | 40.5s | **11.49** |
| 03 `BackOff` | 3 | 15 | 80.4s | **5.13** |
| 04 `Unhealthy` | 5 | 217 | 10.5s | 1.01 |
| 06 `FailedScheduling` | 6 | 171 | 20.4s | 0.99 |

**Thresholds:** easing at ratio ≥ 3, tightening at ≤ 1/3. Symmetric in ratio
terms, and the observed gap is (1.01, 5.13). 3.0 is close to its geometric
midpoint of 2.28 and leaves 3× margin on the steady side against 217 and 171
samples.

**This corrects D-05.** That entry cites `Started -> Killing` at *"4m41s
contracting to 4m12s"* as the memory leak's fill rate accelerating, and the
claim got repeated since. It's **two intervals on one pod**. Across all 22 OOM
intervals the ratio is 0.79, a mild contraction well inside the noise of a
series whose intervals already range from 169s to 326s. The corpus doesn't
support "accelerating", so the tool won't say it. 02 reports **steady**, and the
underlying numbers are published so a reader can see the 0.79 and draw their own
conclusion.

The unambiguous signal in this corpus runs the other way. 03's retries ease from
10.6s to 2m25s, which is Kubernetes' exponential backoff, visible at 5.13× and
11.49×, and worth naming because it tells an on-call engineer the gaps will keep
growing whatever they do until the image is fixed.

**Minimums: 6 intervals for a cadence, 8 for a trend.** Below those the answer
would be arithmetic on noise. A finding under the minimum reports no cadence
rather than a confident one.

---

# Layer 7: output

## D-29: lipgloss table, no bubbletea and no spinner

**Accepted.**

`charmbracelet/lipgloss` renders a styled findings table. `bubbletea` and
`bubbles` are **not** taken as dependencies, and there's no spinner.

**Why.** The pipeline completes a 20,000-record capture in 131–140 ms. A spinner
over that renders roughly two frames and reads as a flicker. bubbletea isn't a
spinner library either. It's an event loop that takes over the terminal, which
sits badly beside the "no CLI framework" non-goal.

Output clarity is 10% of the grade and the table is where that's won. The
spinner would have bought a dependency, an event loop and a lifecycle, for
something nobody would see.

**Alternative considered: a spinner above a duration threshold.** Start one only
if the pipeline is still running after ~300 ms, so it never appears on these
captures but would on a genuinely large one. Honest, and it's machinery for a
case that doesn't exist in the submission. Revisit if input sizes grow.

## D-30: Styling is TTY-conditional and emphasis only

**Accepted.**

Two constructors. `report.New` styles only when its writer is a character device
and `NO_COLOR` is unset, so a pipe, a file and a test buffer all render plain.
`report.NewStyled` always styles, and exists so the property below is testable
at all.

**Why.** The brief requires captured stdout files for scenarios 04, 05 and 06,
saying *"This is required, not optional"*, and a grader opening a file full of
escape sequences is reading noise. `engineering-standards.md` §5 states the same
rule independently.

**The property, and how it's enforced.** Strip the CSI sequences from the styled
rendering and it's byte-identical to the plain one, so no fact is ever carried
by colour alone. `TestStylingIsEmphasisOnly` asserts exactly that, and
`TestRenderPlainHasNoEscapeSequences` asserts the plain path emits no `ESC` at
all. Layout is computed identically in both modes, and the only branch is
whether a style is applied to an already-padded string.

Verified end to end: piped output contains **0** escape sequences, and the same
command under a pty contains **14** styled lines.

**Implementation note worth keeping.** The colour profile must be forced with
`renderer.SetColorProfile(termenv.ANSI)` *after* construction.
`lipgloss.NewRenderer(w, termenv.WithProfile(...))` doesn't work, because
lipgloss re-detects from the writer and overrides it back to Ascii, so
`NewStyled` would silently become identical to `New` and the emphasis-only test
would pass vacuously. Measured: the option path yields termenv profile 3, which
is Ascii. termenv numbers `TrueColor` 0 down to `Ascii` 3, so a larger number is
*less* colour, which is easy to misread.

## D-47: The healthy result is a rendered answer, not an empty table

**Accepted.**

`no issues detected` becomes:

```
✓  ALL CLEAR — no issues detected

   Nothing here needs an on-call response.
   3 transient blips held back as background -- each explained by nothing,
   and explaining nothing.
```

**Why it earns the space.** A clean capture is a real answer, and it's the
answer a reader at 3am most needs to trust at a glance. It's also the single
output most likely to be misread, because a tool that prints nothing is
indistinguishable from a tool that failed.

**Why the second line isn't optional.** "No findings" from a tool that quietly
suppressed three things is a claim the reader can't check. D-45 rests on the
suppressed count staying visible, and the all-clear is the one output where
there's nothing else on screen to carry it. Where nothing was held back it says
so instead: silence because there was nothing, rather than silence because we
filtered.

**On the colour.** Green is the palette's fourth entry and the only one that
isn't a severity, which nominally weakens the "three conventional colours and
nothing else" rule the table follows. It's admitted because it appears on
exactly one line and that line never coexists with a table, so no scan is made
harder by it. The emphasis-only contract still holds and is tested on this path
specifically.

## D-51: Siblings that share an explanation collapse to one line each

**Accepted.**

05 renders six children of one node condition, each repeating an identical `why`
and a near-identical `caused`:

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
information that *is* per-child: which workloads, which namespaces, how far
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

**The collapse condition is a fact rather than a judgement.** Every child shares
the same edge rule (D-50), the same reason, and the same leading signature. Any
child that differs on any of the three renders in full beside its collapsed
siblings, so the collapse can never hide a child telling a different story.

**Rejected: a `--verbose` escape hatch.** A second rendering path and a second
set of golden files, to restore text that's identical by construction. The one
case where the detail matters, a child whose explanation differs, isn't
collapsed in the first place.

**Rejected: keeping every child in full** so each node is independently quotable
into `ANALYSIS.md`. The report is read under time pressure before it's quoted,
and a reader who can't see six workloads in one glance is worse off than one who
has to look up a line.

## D-52: One `PAGED HERE` marker per incident, on the worst leaf

**Accepted.**

The north star is pulling on the thread of a page and walking back to the
origin, so the tree has to show where the thread starts. The marker goes on the
**deepest node of highest severity**, the symptom that would actually have
raised the alert, with the root labelled as the cause it traces back to.

```
10:17:59.977  ▸ ScalingReplicaSet on payment-service     ◀── ROOT CAUSE
│
└── 10:18:04.412  Failed on payment-service  ×24
    │
    └── 10:18:14.858  BackOff on payment-service  ×18   ◀── PAGED HERE
```

**Rejected: marking every leaf.** In 05 that's all six evictions, and a marker
on most of the tree marks nothing.

**Deferred, then built as D-59: `--trace <workload>`.** Naming the service you
were paged for and dimming the rest is closer to the real 3am workflow than any
default can be. It's genuinely better *and* it doesn't remove the need for a
default, since the captured output files the brief requires are produced without
arguments.

**Severity of an incident is the maximum over its tree, never its root's.** 03's
root is a rollout at INFO and the outage beneath it is CRITICAL. Taking the
root's severity labelled the whole incident INFO and would have sorted it below
a readiness blip.

## D-55: The JSON is the audit trail, not a second summary

**Accepted.**

`--json` emits a document whose organising principle is that **every verdict
sits next to the inputs that produced it**. A conclusion on its own is something
you have to trust. A conclusion beside its evidence and the thresholds that were
applied is something you can check, and disagree with, using nothing but the
file.

Each finding carries:

| | Why it's there |
|---|---|
| the coalescing identity | why these records are one fact rather than several |
| **every member record, verbatim** | so every count and time range is *recomputable* rather than merely asserted |
| the causal edge, with its rule and evidence | so an asserted link can be checked against the capture |
| the diagnosis, **with each suppression clause separately** | so `suppressed: true` can be traced to which clause decided it |

The document header carries the causal window and every threshold, because **a
threshold nobody can see is a threshold nobody can challenge.**

**Suppressed findings are included, flagged.** Excluding them would make the
document agree with the tool by construction, which is the opposite of the
point. The first thing a sceptical reader wants is the list of things that were
held back.

**Lifecycle noise is counted but not reproduced.** It's ~19,800 of 20,000
records, it's already in the input file byte for byte, and duplicating it would
make the document larger than its own source while adding nothing the source
doesn't hold. The rule drawn: *everything the tool concluded is here, and
everything it read is in the file it read.* Sizes under that rule: 7.5 KB
(01-healthy) to 102 KB (04-test-a), against a 16 MB input.

**Rejected: nesting findings inside incidents.** It reads better and duplicates
every finding that belongs to a tree. Incidents index into `findings` instead,
so there's exactly one copy of each and a consumer can walk either structure.

**Rejected: emitting durations as nanosecond integers.** `60000000000` is a
number a reader has to decode. `"1m0s"` is one they can act on. This document is
read by a person writing an analysis report at least as often as by a program.

**On ownership.** `diagnose.Config` and `diagnose.Suppression` carry no struct
tags. How a verdict is spelled on a wire is the renderer's business, and a tag
in a diagnosis stage would make that stage own a file format. `report` maps them
into its own wire types.

## D-56: A verdict is derived from its reasons, never stored beside them

**Accepted.** Found by the test written for D-55.

`Diagnosis` held both `Suppressed bool` and the four clauses behind it. Writing
the JSON test that asserts *"the published outcome must follow from the
published clauses"* failed immediately, on the test fixtures, which set the
outcome and left the clauses zeroed.

That's a fixture bug and a design bug. Two fields that must agree, and which
nothing forces to agree, will eventually disagree, and here the disagreement
would be published as an audit trail, which is worse than not publishing one.

`Suppressed` is now a method:

```go
func (d Diagnosis) Suppressed() bool { return d.Because.holds() }
```

There's exactly one place that says what suppression means, it reads the four
values the document publishes, and a hand-built chart can no longer claim an
outcome its own reasons contradict.

**The general rule this is an instance of:** where a value and its justification
are both exported, derive the value. A stored conclusion is a second source of
truth, and the audit trail is only worth anything if it can't be inconsistent
with the thing it audits.

## D-59: `--trace` narrows to one workload and marks one node

**Accepted.** Un-parks the option deferred by D-52.

The north star is pulling on the thread of a page and walking back to the
origin. `--trace <workload>` is that path directly: name the service you were
paged for, get the incident it belongs to, and see where in the chain you are.

```
TRACING auth-service — showing 1 of 1 incident

   ROOT CAUSE    node-4 reported NodeHasDiskPressure  at 10:15:00.000
     ├─ 10:16:25.429  Evicted  auth-service · production  ×1 · 1 pod  +1m25.4s   YOU ARE HERE
```

`auth-service` did nothing wrong. The trail runs from the pod that paged you to
a node that filled its disk, through two namespaces you don't own.

**One marker, not every match.** The first implementation marked every finding
whose workload matched, which in 03-image-pull-failure marks all three nodes of
the tree, since every one of them is `payment-service`, and says nothing. The
mark goes on the **last matching member in time**: the most recent thing your
service did, which is what you were looking at when you were paged.

**It narrows, and says what it narrowed.** Incidents not involving the workload
are counted in the header rather than dropped, and a workload with no incident
gets a sentence saying so rather than an empty report that reads like a clean
cluster. Same rule as the suppressed count in D-45: a filter that doesn't
disclose what it filtered can mislead by omission.

**It implies the incident view**, at the API and not only at the CLI. The table
has no notion of a trail, so narrowing it to one workload would merely hide
rows, and a caller who asked to follow a thread wants the thread.

**Matches a pod instance as well as a workload**, because the name on a page is
often the pod.

---

# Open questions

## O-01: Package layout

`engineering-standards.md` §1.1 names `group` / `diagnose` / `correlate` /
`report`. The repository has `otel` / `event` / `classify` / `group` / `link` /
`diagnose` / `report` / `triage`. D-17 collapsed correlation into an on-demand
function and D-31 gave it a package after all, so the gap is narrower than it
was. Still needs an ADR to close.

## O-02: Pattern thresholds *(closed by D-43 and D-44)*

D-26 required numeric thresholds for the pattern set, each a named constant with
its derivation recorded (`engineering-standards.md` §3.2: *"numbers are never
magic"*).

Settled at three constants rather than six. Only "transient" needs numbers at
all. Every other pattern is decided by a rule ID, a body substring, or the root
of the incident, and the suppression those thresholds feed is a conjunction with
the Forest Data Structure rather than a threshold on its own.

## O-03: Deploy-correlation window *(closed by D-33)*

D-17's `window` parameter. Observed deploy→first-symptom deltas: 03 = 4.4s,
04 = 7.3s, 06 = 4.7s. Settled at 300s with the margin measured in D-33.

## O-04: The taxonomy covers only the reasons these captures contain

**Open. This is the tool's biggest real limitation.**

Fourteen reasons are classified, and they're exactly the reasons that occur in
the six provided files. A real cluster emits many more:
`CreateContainerConfigError`, `FailedAttachVolume`, `NetworkNotReady`,
`Preempted`, `FailedKillPod`, `ImageInspectError`, `NodeHasMemoryPressure`,
`NodeHasPIDPressure`, `TaintManagerEviction` among them.

Every one of them lands on the conservative fallback: surfaced as an issue if
it's a Warning, never labelled with a pattern, never suppressed, no remediation.
That's the *correct* failure mode, since nothing is silently dropped and nothing
is confidently mislabelled, and the tool is still measurably less useful on a
cluster that isn't one of these six files. Saying so is more honest than a
taxonomy that looks complete.

The behaviour is demonstrated end to end in
[`analysis/06-test-c.md`](../analysis/06-test-c.md#the-extra-line-at-the-end-of-this-file),
using a hand-added record with reason `LALALALA`.

Two questions this corpus can't settle:

1. **How far to extend the taxonomy without evidence.** Every row added from
   documentation rather than from observed records is a row whose body matchers
   are guesses. D-57 records one case where the brief's own example body text
   would never have matched a real record (`OOMKilling`).
2. **Whether the causal rules generalise.** All three were derived from edges
   visible in these captures. `NodeHasMemoryPressure` would flow through
   `node-condition-named` unchanged. A `Preempted` pod naming its preemptor
   would need a rule that doesn't exist.

---

# Reversals and corrections

Every place this ledger changed its mind, in one table. Kept because a decision
that was overturned tells you more about the shape of the problem than one that
was right first time.

| Entry | What it claimed | What overturned it | Why |
|---|---|---|---|
| **D-11 draft** | `Node` belongs in the group key | **D-12** | Fixed 05, fragmented 02/03/04. A memory leak belongs to the workload |
| **D-23** | 05's impure eviction finding should be disclosed, not split | **D-28** | The bodies name two different causes, so the taxonomy rule already separates them |
| **D-18/D-20** | Precomputed Forest Data Structure, two-arena layout | **D-17** | Solving a performance problem that doesn't exist at n≤227 |
| **D-17** | A trail is only an on-demand query | **D-31** | Right shape, wrong grain. Both survive at different jobs |
| **D-27** | Truncation is detected by proximity to capture start | **D-34** | 02's first OOM is 156s in, so the heuristic mis-calls it, and it needed a threshold nothing justified |
| **D-05** | 02's memory leak is *accelerating* | **D-58** | Two intervals on one pod. Across all 22 the ratio is 0.79, which supports nothing |
| **D-38 draft** | n=1,000 costs "a few ms"; n=5,000 "~100ms" | **the benchmark** | 32 ms and nearer 300 ms. Both were estimates I never measured |
| **D-42/D-44 guards** | `Category == Issue` excludes unrecognised reasons | **D-46** | It only did so by side effect, and the side effect stopped holding when severity moved |
| **D-54 draft** | The verdict quotes the deepest failing node | **D-54** | In 03 that's `BackOff`, a consequence. Split into Mechanism and Paged |
| **D-56 draft** | `Suppressed` is a stored field beside its reasons | **D-56** | Two fields that must agree and nothing forces to agree will disagree, and this one gets published as an audit trail |
