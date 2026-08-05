# Decision ledger

What was decided, and what else was on the table.

Each entry is **Landed** (what's in force) and **Weighed** (the alternatives,
and the evidence that killed them). A decision with no recorded alternative was
never really made. Numbers are stable and quoted from source, so an entry that
was superseded keeps its number and points at whatever replaced it.

The narrative version is [`../DESIGN.md`](../DESIGN.md). Evidence comes from the
six captures in `testdata/`.

| Layer | Package | Decisions |
|---|---|---|
| [0. Scope and process](#layer-0-scope-and-process) | — | D-01, D-02, D-39 |
| [1. The product](#layer-1-the-product) | — | D-03, D-04 |
| [2. Ingest](#layer-2-ingest) | `otel`, `event` | D-08, D-10 |
| [3. Classification](#layer-3-classification) | `classify` | D-05, D-06, D-07, D-09 |
| [4. Coalescing](#layer-4-coalescing) | `group` | D-11 – D-16, D-23, D-28 |
| [5. Causality](#layer-5-causality) | `link` | D-17 – D-22, D-24, D-25, D-31 – D-38, D-50 |
| [6. Diagnosis](#layer-6-diagnosis) | `diagnose` | D-26, D-27, D-40 – D-46, D-48, D-49, D-53, D-54, D-57, D-58 |
| [7. Output](#layer-7-output) | `report` | D-29, D-30, D-47, D-51, D-52, D-55, D-56, D-59 |
| [Open](#open-questions) | — | O-01 – O-04 |

---

# Layer 0: scope and process

## D-01: Rebuild by hand; take only the standards document

**Landed.** `~/Projects/daiquiri-ai` is an abandoned AI-built attempt at this
challenge. Nothing is inherited from it except `engineering-standards.md`, which
encodes the author's own standard.

**Weighed:** reusing its code or package layout. The brief grades reasoning, and
reasoning you didn't do is reasoning you can't defend in a review.

**Amended since.** The inherited document described a repository it didn't
govern, so four claims were corrected against reality: §1.1's pipeline named a
`correlate` package that never existed (closes O-01), `doc.go` files were
required and absent (eight written), the Liskov example described renderers
nothing abstracts over (deleted), and godoc was said to be enforced by `revive`,
which never runs (replaced with what each gate actually catches). §3.1 is
amended separately by D-39.

## D-02: Design is argued in the open before code is written

**Landed.** Structure, data model and thresholds settled in writing, against
fixture evidence, before implementation.

**Weighed:** designing while building. Three decisions here (D-11, D-14, D-21)
were reversed by looking at the data. Each would have been a rewrite if found
after implementing.

## D-39: No per-package LLDs

**Landed.** Amends `engineering-standards.md` §3.1, which required
`docs/lld/<package>.md` before each package. The ledger carries what an LLD is
for: D-31 through D-38 are the `link` LLD in everything but filename.

**Weighed:** writing the three missing LLDs retroactively, which documents what
got built where §3.1's "before" exists to design what will be. And accepting the
deviation silently, which leaves a binding document that is untrue.

---

# Layer 1: the product

## D-03: The deliverable is a causal trail

**Landed.** North star: an engineer paged at 3am pulls the thread of the alert
and walks back to where the failure started. All five failing captures resolve
to a 2 or 3 hop trail.

**Weighed:** a flat prioritised finding list. The brief puts 35% on the analysis
report and warns that *"a summary that lists counts without an interpretation is
half the work"*, and that *"seemingly unconnected findings are sometimes side
effects of the same underlying issue."*

## D-04: Prioritised list on top, trail underneath, each headline its own origin

**Landed.** Incidents ordered by severity, each headed by its **origin** rather
than its symptom, so the first line read is already the root cause.

**Weighed:** severity ordering alone, which loses chronology. And chronology
alone, which loses the brief's prioritised summary.

---

# Layer 2: ingest

## D-08: Node identity is normalised at decode

**Landed.**

```go
if e.Object.Kind == "Node" && e.Node == "" { e.Node = e.Object.Name }
```

**Weighed:** normalising at each use site. The one `NodeHasDiskPressure` record
in the corpus carries `k8s.object.name = "node-4"` and no `k8s.node.name`, so
without this every same-node rule fails to match the exact event it exists to
find.

## D-10: Occurrence count is `max(count)` per event UID, summed

**Landed.** Covered by a synthetic test.

**Weighed:** `+= count`, which is simpler and double-counts under a
count-incrementing pipeline. `k8s.event.count` is `1` for every record in this
corpus, so the harder path can't be exercised by it, and the brief requires
handling it anyway.

---

# Layer 3: classification

## D-05: Noise is retained rather than counted and dropped

**Landed.** Noise becomes a retained slice. ~20,000 events ≈ 1.6 MB against a
16 MB input.

**Weighed:** `res.Noise++` and discard, which was the implementation at the
time. Lifecycle records are the endpoints of 02's `Started → Killing` interval,
so discarding them removes the evidence behind the strongest quantitative claim
the tool can make.

> D-58 corrects this entry: it read two intervals on one pod as an accelerating
> leak. Across all 22 the ratio is 0.79, and the tool reports steady.

## D-06: Noise is retained but never narrated

**Landed.** Every record kept; only issues, markers and unknowns create a
finding.

**Weighed:** letting noise create findings, which adds ~2,900 `Pulled` findings
per capture.

## D-07: `Workload()` derives the owner, kind-dispatched, conservative fallback

**Landed.** A method on `event.Object`. Strips the ReplicaSet hash and pod
suffix for `Pod`, the hash for `ReplicaSet`, and returns anything unmatched
unchanged. Measured: 94,686 Pod records and 25,311 ReplicaSet records match
their shape 100%, and no workload name itself ends in a hash-shaped segment.

**Weighed:** parsing in the grouper, which gives a coalescing package a working
knowledge of Kubernetes naming. And failing loudly on an unmatched name: the
conservative fallback makes the failure mode "no rollup" instead of "wrong
rollup", which matters because real clusters use other hash alphabets.

## D-09: Unrecognised `Normal` events get their own category

**Landed.** `CategoryUnclassified`. They group normally and are reported,
demoted. Unrecognised **Warning** stays an issue.

**Weighed:** routing them to noise, which was the existing fallback and buries
06's planted `LALALALA` record. And a special case instead of a category:
routing through normal grouping means 2,000 unknown records collapse to one
finding per reason.

---

# Layer 4: coalescing

## D-11: Group key is `(Kind, Workload, Namespace, Reason)`

**Landed.** Extended by D-28, which adds `Rule`.

**Weighed:** keying on `Object.Name`. 04's 225 `Unhealthy` records span 5 pod
instances, so that is 5 findings where the cluster has 1 problem. The brief's
"same object" means the thing that broke.

## D-12: `Node` is **not** in the group key

**Landed.** Reverses an earlier decision in the same design pass.

**Weighed:** including it. Justified by 05 alone, where `data-pipeline` is
evicted on node-2 at 10:02:32 and on node-4 from 10:15:25. Merged, `FirstSeen`
predates the `NodeHasDiskPressure` that caused half of them, so the causal link
is correctly refused. Then measured against the rest:

| Capture | Without `Node` | With `Node` |
|---|---|---|
| 02 recommendation-service | 2 findings | **8** |
| 03 payment-service | 2 | **6** |
| 04 checkout-service | 1 | **3** |
| 05 data-pipeline | 1 impure | 2 clean |

Fixes one capture, fragments four. D-28 fixes 05 properly.

## D-13: Findings sort on a **total** order

**Landed.** `FirstSeen`, then `Kind`, `Workload`, `Namespace`, `Reason`.

**Weighed:** sorting on `FirstSeen` alone. Go randomises map iteration and 05
has evictions inside the same second, so ties resolve by map order and golden
tests fail *intermittently*.

## D-14: Markers and unknowns group through the identical path

**Landed.** A point event is a span of zero duration.

**Weighed:** special-casing them. Routing markers through grouping makes them
available as causal candidates for free.

## D-15: Grouping is conservative; linking does the joining

**Landed.** Never merge across a dimension that can't be shown irrelevant. An
incident is a set of related findings.

**Weighed:** merging aggressively. 04's `Unhealthy` spans three nodes and
merging is right there; 05's `Evicted` spans two and merging is wrong there. No
grouping key can tell those apart, and merging destroys information
irreversibly.

## D-16: Grouping is non-windowed; analysis is windowed

**Landed.** One finding per key over the whole capture, every occurrence
timestamp retained.

**Weighed:** storing `count + first + last`. That makes 01's `Unhealthy` (n=3
over 13s) and 02's `BackOff` (n=91 over 26m) the same shape.

## D-23: 05's impure eviction finding is disclosed rather than split

**Weighed and lost.** Superseded by D-28. It assumed the two evictions were one
failure mode seen twice. The bodies say otherwise: `low on resource: memory` on
node-2, `condition: [DiskPressure]` on node-4, and the taxonomy already
separates them.

Also weighed here and still rejected: **episode splitting on a temporal gap.**
Splitting where the inter-occurrence gap exceeds some multiple of the median
would separate these two, and needs a constant nothing in the data justifies.

## D-28: The fired taxonomy rule is part of the group key

**Landed.** Final key `(Kind, Workload, Namespace, Reason, Rule)`. Supersedes
D-23. Costs nothing elsewhere: every other group in the corpus matches a single
rule, and only 05's count changes, from 9 findings to 10.

**Weighed:** keying on `Cause` instead. `Cause` is display text, so rewording a
sentence would silently change how records coalesce. `Rule` is an identifier
whose only job is identity.

---

# Layer 5: causality

## D-17: A trail is a filtered, time-sorted slice of the raw records

**Landed.** Three OR'd conditions, one linear scan, computed on demand. Reverses
D-18 through D-20; narrowed later by D-31.

**Weighed:** everything in D-18 to D-20, all of which solve a performance
problem that doesn't exist. After classification the working set is ≤227
records, not 20,000.

## D-18: A precomputed Forest Data Structure over a time-sorted entry array

**Weighed, then adopted.** Parked by D-17 as unnecessary at n≤227, un-parked by
D-31 at finding grain. Content lives in D-31.

## D-19: Forest, not a general DAG: at most one parent

**Landed.** Mechanism is D-31.

**Weighed:** a general DAG, which is what real causality is. Two parents turns
"pull the thread" into three leads and no answer.

**Stated cost:** joint causation can't be expressed. No capture in this corpus
exhibits it.

## D-20: Two-arena physical layout

**Weighed and lost.** Superseded by D-17. Index ranges into flat occurrence
arrays plus a binary-searched lifecycle arena: a cache-locality optimisation for
a working set that fits in ~30 KB regardless.

## D-21: Time is a veto, never a proposal

**Landed.** Every rule needs a shared pod, node or ReplicaSet first, and time is
applied afterwards only to reject.

**Weighed:** proximity-based linking. 04 has 3 baseline `Unhealthy` events on
`data-pipeline` in the same window as 222 on `checkout-service`. Same reason, no
relationship. Proximity reports one incident where there are two.

## D-22: Rule priority is the outer loop; time proximity is the inner loop

**Landed.**

**Weighed:** transposing them, which silently turns "first match" into *nearest
candidate matching any rule* instead of *strongest rule matching any candidate*.
In 05 that lets a weak same-workload link to the background node-2 eviction beat
the node-condition link.

## D-24: Union-find

**Weighed and lost.** Its physical form is the same `parent []int` and its
native question is exactly 05's question. Both optimisations disqualify it: path
compression deletes the intermediate hops, which *are the product*, and union by
rank picks the parent that balances the tree when the requirement is the parent
that's true. Stripped of both it is the Forest Data Structure.

## D-25: Fixed-width time buckets derive shape; they never link findings

**Landed.** Computed on demand, stored nowhere.

**Weighed:** using bucket co-occupancy to associate findings, which is D-21
violated at the implementation level.

## D-31: The causal structure is a Forest Data Structure over findings

**Landed.** Un-parks D-18, narrows D-17.

```go
type Forest struct {
    Findings []group.Finding // ascending by FirstSeen
    Edges    []Edge          // parallel; Edges[i] describes Findings[i]
}
```

`Build` only scans backwards, so `Edges[i].Parent < i` holds by construction:
cycles impossible, slice already topologically ordered, `Children(i)` scans
forward, `RootOf` is an integer loop at 2.3 ns.

**Weighed:** returning a bare `[]Edge` and letting the caller hold the findings.
The two are index-coupled, and separating them makes "same length, same order"
an unenforced convention whose failure mode is a wrong diagnosis rather than a
crash.

## D-32: Rule priority is dimension specificity: pod > node > workload

**Landed.**

**Weighed:** ordering by parent kind, putting cause-shaped parents first. It
loses 03's depth: `ScalingReplicaSet → Failed → BackOff` flattens to two
siblings under the deploy, dropping the claim that the retry is caused by the
pull failure.

**Stated cost:** the same-pod rule links to any earlier finding sharing a pod.
Checked against the corpus and it doesn't occur. Guarding it needs a
reason-precedence table with no evidence behind it.

## D-33: A single 300-second window

**Landed.** Closes O-03. Every true edge in the corpus lands between +4.4s and
+97.9s; the nearest false candidate is at +600s. Anything in (98s, 600s) gives
identical results.

**Weighed:** per-rule windows, which would be tunable to this corpus and
therefore overfitted to it.

## D-34: Roots are classified by what they are, not by when they are

**Landed.** Supersedes D-27.

| Root | Confidence |
|---|---|
| deploy marker or node condition | explained |
| a failure *with* children | partially explained |
| a failure with *no* children | unexplained |

**Weighed:** D-27's time-based test. 02's first `OOMKilling` is 156s into the
capture, so a "within seconds of the start" heuristic mis-calls it, and it needs
a threshold nothing justifies. This version needs no threshold at all.

## D-35: An edge must be provable from record text or object identity

**Landed.** Strengthens D-21.

| Rule | Proof | Strength |
|---|---|---|
| same pod | identity | inferred |
| node condition | `NodeHas<X>` ↔ child body contains `[<X>]` | stated |
| deploy marker | deploy body names `<workload>-<hash>`; child pods are `<that>-<suffix>` | stated |

**Weighed:** co-occurrence. A stated-cause edge yields a verbatim quote for the
analysis report; a co-occurrence edge yields a hand-wave. The node-2 eviction in
05 is the control: same reason, different named cause, correctly unlinked.

**Stated asymmetry:** same-pod is the weakest of the three and still outranks
them for the depth reason in D-32. That flows into reported confidence.

## D-36: The deploy rule keys on the named ReplicaSet, not the workload

**Landed.** Failing pods must belong to the ReplicaSet the rollout body names.

**Weighed:** same-workload matching, which links a service that merely happened
to be deployed recently.

## D-37: Two edge tiers, not a confidence score

**Landed.** `Caused` (the text names the link, or it's the same object) and
`MayRelate` (a shared dimension inside the window, nothing in the text).
`MayRelate` is empty on this corpus and stays empty.

**Weighed:** a numeric score, which is a model needing calibration data that
doesn't exist here. And loosening a rule to populate the second tier, which
manufactures exactly the weak edges D-21 prevents.

## D-38: Complexity, scale, and why no graph library

**Landed.** Sorted slice, linear scan. Measured:

| n (findings) | ns/op | allocs/op |
|---|---|---|
| **10** (real working point) | 29,868 | 51 |
| 100 | 1,411,987 | 1,561 |
| 1,000 | 32,289,829 | 17,766 |

Whole pipeline ~140 ms against a 5-second budget. n counts distinct failure
modes, so 10× the events gives roughly the same n.

**Weighed:**

- **A graph library.** n is 3 to 10; the dependency exceeds the structure.
- **Interval tree or sweep line.** They answer "which intervals overlap", which
  is wrong in both directions. Causes are instants and effects begin after, so
  every deploy edge and the whole 05 incident has **zero** overlap. Overlap
  misses the four best diagnoses in the corpus and invents a link between a
  memory leak and an unrelated readiness probe.
- **Union-find.** See D-24.

**Two corrections from estimates never measured:** n=1,000 was published as "a
few ms" and is 32 ms; n=5,000 was extrapolated at "~100ms" and measures nearer
300 ms. The second figure is dropped rather than re-estimated.

**What would force a real graph:** multiple parents, cycles, reachability at
scale, or streaming updates. None apply.

**Deferred, not forgotten:** orphan merging. Had 05 begun at 10:15:10 the node
condition would be absent, leaving six evictions naming `[DiskPressure]`:
obviously one incident, six roots. ~20 lines and a map.

## D-50: Edges carry the rule that produced them

**Landed.** `link.Edge` gains `Rule string`.

**Weighed:** re-deriving it from the evidence string, which is display prose.
D-51's collapse means *same causal rule*, and 05's six evictions all fired one
rule while every evidence string differs in the elapsed time it quotes.

---

# Layer 6: diagnosis

## D-26: Shape, not reason, discriminates real problems from background

**Landed.** Every capture, including 01-healthy, carries the same background
floor: 1 `Evicted`, 1 `FailedMount`, 3 `Unhealthy`, with the workload randomised
per file.

**Weighed:** reason-based filtering. These are genuine `Warning` records the
taxonomy correctly calls issues, so *"no false positives on 01-healthy"* cannot
be met by filtering on reason.

## D-27: The trail must be able to report that it ran out of data

**Weighed and superseded** by D-34's non-temporal test. The principle survives:
*origin found* and *trail truncated* are different answers and get different
words.

## D-40: The pattern is a diagnose-stage verdict, not a field on `Finding`

**Landed.** A fifth package, `internal/diagnose`, embedding the Forest Data
Structure.

**Weighed:** adding `Pattern` to `group.Finding`, which was the recorded plan.
It breaks `group`'s own prohibition against interpreting shape over time, and
two of the five patterns are read off `RootOf(i)`, which `group` runs too early
to see. Also weighed returning a bare `[]Diagnosis`, which compiles and makes
misalignment possible.

## D-41: The pattern vocabulary is the brief's, verbatim

**Landed.** The brief's five words, unchanged.

**Weighed:** a shape-only vocabulary (transient / sustained / point), which
carves an axis the Forest doesn't already cover and is defensible as design. It
lost because the brief names its five and asks for them by name, and making a
grader translate is scoring points against yourself.

## D-42: Patterns are a priority table; the mechanism outranks the trigger

**Landed.** First match wins. 06 is truthfully both capacity and
deploy-correlated, and reads as capacity because the trigger is already stated
by the causal edge in more detail.

| # | Pattern | Fires when |
|---|---|---|
| 0 | *(none)* | the finding is a deploy marker |
| 1 | capacity issue | rule `failed-scheduling` **and** a body names `Insufficient` |
| 2 | sustained crash-loop | rule `backoff/crash-loop` or `oom-killed`, not transient |
| 3 | node issue | root is a Node-kind finding |
| 4 | deploy-correlated failure | root is a deploy marker |
| 5 | transient blip | transient by shape (D-43) |
| 6 | *(none)* | anything else, including unrecognised reasons |

**Weighed:** a set of patterns per finding. More truthful, and it turns the
output into a bag of labels to read rather than a category to scan. The full
truth stays recoverable as pattern line plus edge line.

**Two abstentions:** a deploy marker gets no pattern, since row 4 would label a
successful rollout a failure. An unrecognised reason gets no pattern either.

## D-43: "Transient" is a conjunction of three named bounds

**Landed.** `count ≤ 10`, `pods ≤ 1`, `span < 60s`, all three holding.
Background findings measure 1–3 / 1 / 0–16s; real problems 18–222 / 3–6 /
7m49s–26m6s.

**Weighed:** count alone, which separates the corpus with a 6× margin and calls
05's `data-pipeline/Evicted[disk-pressure]` (n=3 across **3 pods** over
**4m31s**) a blip. Blast radius and duration are independent axes from volume.

**Measured and dropped:** occupancy, meaning distinct wall-clock minutes
containing an event. It separates the corpus cleanly and identically to `span`,
so it is a fourth metric that never changes an answer.

## D-44: Suppression is `issue ∧ transient ∧ unexplained ∧ non-explanatory`

**Landed.** Closes O-02. Three findings, identical on every shape metric,
needing three different verdicts:

| | n | pods | span | parent | children | verdict |
|---|---|---|---|---|---|---|
| 01 `data-pipeline/Evicted[memory]` | 1 | 1 | 0s | −1 | 0 | suppress |
| 05 `auth-service/Evicted[disk]` | 1 | 1 | 0s | **3** | 0 | keep |
| 05 `node-4/NodeHasDiskPressure` | 1 | 0 | 0s | −1 | **6** | lead with it |

**Weighed:** any threshold on count, pods, span or occupancy. There is nothing
there to separate; only the Forest can. Dropping the fourth clause suppresses
node-4's condition and takes the entire 05 incident with it.

**Corroboration:** exactly 3 findings held back in every capture, always the
same three shapes. A predicate tuned to one file wouldn't land on the same three
in the other five.

## D-45: Suppressed findings are counted and disclosed, never deleted

**Landed.** The header states the count.

**Weighed:** filtering inside `diagnose`. Disclosure is what makes an aggressive
threshold safe: the worst case for a mis-tuned `transientCount` is a line the
reader asks about, instead of a fact that silently left the building.

## D-46: An unrecognised reason is never labelled and never suppressed

**Landed.** One predicate governs both questions:

```go
func diagnosable(f group.Finding) bool {
	return f.Category == classify.CategoryIssue && f.Recognised
}
```

**Weighed:** the `Category == Issue` guard it replaces, which excluded
unrecognised reasons only by side effect. When 06's planted record was corrected
from `Normal` to `Warning` it changed category, the side effect stopped holding,
and the record would have been both labelled a transient blip and suppressed.

Also weighed two separate checks instead of one function. They must not drift: a
finding we decline to categorise is exactly a finding we may not dismiss.

## D-48: The "why" is a signature over record bodies

**Landed.** Volatile tokens normalised (pod names, IPs, parenthesised UIDs) and
the remainder counted. 222 records become 3 signatures. **Numbers with units are
never touched**, because `512Mi`, `404`, `8080` and `0/6` are the answer.

**Weighed:** quoting the first body, which is arbitrary and hides that 04 has
three distinct symptoms. And putting this in `report`, which is forbidden to
interpret.

## D-49: Signatures rank by specificity, then by frequency

**Landed.** A signature is *specific* if, after normalisation, it contains a
quoted string or a digit. In 03 the most common line is `Error:
ImagePullBackOff` at 18 of 24 and says nothing; the line naming the missing tag
occurs 3 times and leads.

**Weighed:** frequency alone, which buries the answer under a paraphrase of the
question. Longest-first, which gets 03 and 04 right by accident since length
proxies nothing. A per-reason table of interesting tokens, which is more fitted
to this corpus, where specificity is a property of a sentence. And a weighted
score, where every weight would need justifying and the corpus supports one
distinction.

## D-53: Remediation is a column in the taxonomy, not a rules engine

**Landed.** One `fix` string per taxonomy row, templated on `{workload}`,
`{namespace}`, `{node}` and `{pod}`, keyed on the **mechanism's** rule rather
than the root's. Diagnostic command before destructive one. A row with no `fix`
renders nothing.

**Weighed:** a rules engine with conditionals and severity-dependent advice. The
tool is confident about what it observed and has no business being confident
about what to change.

## D-54: `Incident` is the unit the report renders, built in `diagnose`

**Landed.** Three distinct nodes, because conflating them was a real bug:

| | Which node | Answers |
|---|---|---|
| `Root` | nothing explains it | what set this off |
| `Mechanism` | **shallowest** failure of max severity | what actually broke |
| `Paged` | **deepest** leaf of max severity | what raised the alert |

`Paged` is `-1` on a tie. Blast radius counts Pod-kind findings only. Ordering
is severity, then blast radius, then time. `StillFailing` is
`capture end - Last ≤ 2m`: every real problem ends 0–100s before the capture and
every background finding 600–1730s before, so any value in that gap behaves
identically.

**Weighed:** quoting the deepest node as the verdict, which was the first
implementation. In 03 that is `BackOff`, meaning "repeated image-pull retry", a
consequence, where the shallowest failure names the tag that doesn't exist. And
counting all members for blast radius, which made 05 read "7 workloads across 4
namespaces" when node-4 is not a workload and nothing happened in the fourth
namespace.

## D-57: Signature-level meanings, because the body decides what a probe failure is

**Landed.** A second, smaller interpretation table keyed on the signature.

| Body | What it proves |
|---|---|
| `connection refused` | nothing is listening |
| `context deadline exceeded` | connected, no answer in time |
| `HTTP statuscode: 404` | up and routing, path does not exist |

The rule-level sentence is wrong for two of the three.

**Weighed:** splitting the taxonomy rule by body, which `Failed` already does.
Wrong here because `Rule` is in the coalescing key, so 04's 222 records would
fragment into three findings. Same trap as D-12.

**Rows only for shapes the corpus contains.** No row for `500`, `503` or `no
route to host`. A row written from documentation is a body matcher nobody has
seen fire, and `OOMKilling` already proved that trap: the brief's own example
body text would never have matched a real record.

## D-58: Cadence is measured per object, and the trend threshold is conservative

**Landed.** Intervals measured per object, then pooled. Easing at ratio ≥ 3,
tightening at ≤ 1/3, with minimums of 6 intervals for a cadence and 8 for a
trend.

| Finding | finding-wide median | per-pod median | what the per-pod number is |
|---|---|---|---|
| 04 `Unhealthy` | 1.4s | **10.5s** | the probe period |
| 06 `FailedScheduling` | 1.9s | **20.4s** | the scheduler's retry interval |

**Weighed:** measuring across the finding, which reports an artefact of the
replica count. And a tighter trend threshold: the observed gap is (1.01, 5.13),
so 3.0 sits near its geometric midpoint with 3× margin on the steady side.

**This corrects D-05**, which read `Started → Killing` at "4m41s contracting to
4m12s" as acceleration. That is two intervals on one pod. Across all 22 the
ratio is 0.79, inside the noise of a series ranging 169s to 326s. The tool
reports **steady** and publishes both means.

---

# Layer 7: output

## D-29: lipgloss table, no bubbletea and no spinner

**Landed.** `lipgloss` only.

**Weighed:** bubbletea plus a spinner. The pipeline finishes in ~140 ms, so a
spinner renders about two frames and reads as a flicker, and bubbletea is an
event loop that takes over the terminal. Also weighed a spinner above a ~300 ms
threshold: honest, and machinery for a case that doesn't exist here.

## D-30: Styling is TTY-conditional and emphasis only

**Landed.** `report.New` styles only when its writer is a character device and
`NO_COLOR` is unset. `report.NewStyled` always styles, so the property is
testable. Strip the CSI sequences from styled output and it is byte-identical to
plain; `TestStylingIsEmphasisOnly` asserts it.

**Weighed:** always styling. The brief requires captured stdout files, and a
grader opening a file of escape sequences is reading noise.

## D-47: The healthy result is a rendered answer, not an empty table

**Landed.** `✓ ALL CLEAR`, plus a line stating what was held back.

**Weighed:** printing nothing, which is indistinguishable from a tool that
failed. And dropping the second line: "no findings" from a tool that quietly
suppressed three things is a claim the reader can't check.

## D-51: Siblings that share an explanation collapse to one line each

**Landed.** The collapse condition is a fact: same edge rule, same reason, same
leading signature. Any child differing on any of the three renders in full
beside its collapsed siblings.

**Weighed:** keeping every child in full, which is thirty lines to say one thing
six times and hides the per-child information. And a `--verbose` escape hatch: a
second rendering path and a second set of goldens, to restore text that is
identical by construction.

## D-52: One `PAGED HERE` marker per incident, on the worst leaf

**Landed.** Deepest node of highest severity. Incident severity is the maximum
over its tree, never the root's: 03's root is a rollout at INFO above a CRITICAL
outage.

**Weighed:** marking every leaf, which in 05 marks all six evictions and
therefore marks nothing. And `--trace` as a replacement, deferred here and built
as D-59; it doesn't remove the need for a default, since the captured files are
produced without arguments.

## D-55: The JSON is the audit trail, not a second summary

**Landed.** Every verdict sits next to the inputs that produced it: the
coalescing identity, every member record verbatim, the causal edge with its rule
and evidence, and each suppression clause separately. Thresholds in the header,
suppressed findings included and flagged. 7.5 KB to 102 KB against a 16 MB
input.

**Weighed:** nesting findings inside incidents, which reads better and
duplicates every finding in a tree, so incidents index instead. Emitting
durations as nanosecond integers, where `60000000000` is a number a reader has
to decode. Reproducing lifecycle noise, which is ~19,800 records already in the
input file. And excluding suppressed findings, which makes the document agree
with the tool by construction.

**On ownership:** `diagnose.Config` and `Suppression` carry no struct tags. How
a verdict is spelled on a wire is the renderer's business.

## D-56: A verdict is derived from its reasons, never stored beside them

**Landed.**

```go
func (d Diagnosis) Suppressed() bool { return d.Because.holds() }
```

**Weighed:** the stored `Suppressed bool` it replaces. The JSON test asserting
*"the published outcome must follow from the published clauses"* failed on its
own fixtures, which set the outcome and left the clauses zeroed. Two fields that
must agree, and which nothing forces to agree, will disagree, and here the
disagreement gets published as an audit trail.

## D-59: `--trace` narrows to one workload and marks one node

**Landed.** Marks the **last matching member in time**, implies the incident
view at the API rather than only at the CLI, matches a pod instance as well as a
workload, and counts the incidents it left out.

**Weighed:** marking every matching finding, which was the first implementation
and marks all three nodes of 03's tree, since every one is `payment-service`.
And dropping non-matching incidents silently, which is D-45's rule violated: a
filter that won't say what it filtered can mislead by omission.

---

# Open questions

## O-01: Package layout *(closed by D-01's amendment)*

The standard named `group` / `diagnose` / `correlate` / `report`; the repository
has `otel` / `event` / `classify` / `group` / `link` / `diagnose` / `triage` /
`report`. Settled in favour of the repository. §1.1 now states the real chain
and §1.2 publishes the dependency graph.

## O-02: Pattern thresholds *(closed by D-43 and D-44)*

Settled at three constants rather than six. Only "transient" needs numbers;
every other pattern is decided by a rule ID, a body substring, or the root.

## O-03: Deploy-correlation window *(closed by D-33)*

Settled at 300s, with the margin measured.

## O-04: The taxonomy covers only the reasons these captures contain

**Open. The tool's biggest real limitation.**

Fourteen reasons are classified, and they are exactly the reasons in the six
provided files. `CreateContainerConfigError`, `FailedAttachVolume`,
`NetworkNotReady`, `Preempted`, `NodeHasMemoryPressure` and others all land on
the conservative fallback: surfaced if Warning, never labelled, never
suppressed, no remediation. Nothing is dropped in silence and nothing is
confidently mislabelled, and the tool is measurably less useful on a cluster it
hasn't seen.

Demonstrated end to end in
[`../analysis/06-test-c.md`](../analysis/06-test-c.md#the-extra-line-at-the-end-of-this-file).

**Two questions this corpus can't settle:**

1. **How far to extend the taxonomy without evidence.** Every row added from
   documentation is a row whose body matchers are guesses. D-57 records a case
   where the brief's own example body text would never have matched.
2. **Whether the causal rules generalise.** `NodeHasMemoryPressure` flows
   through `node-condition-named` unchanged; a `Preempted` pod naming its
   preemptor needs a rule that doesn't exist.

---

# Overturned

Every place this ledger changed its mind.

| Entry | Claimed | Overturned by | Why |
|---|---|---|---|
| **D-11 draft** | `Node` belongs in the group key | D-12 | fixed 05, fragmented 02/03/04 |
| **D-23** | 05's impure finding should be disclosed, not split | D-28 | the bodies name two different causes |
| **D-18/D-20** | precomputed forest, two-arena layout | D-17 | no performance problem at n≤227 |
| **D-17** | a trail is only an on-demand query | D-31 | right shape, wrong grain; both survive |
| **D-27** | truncation detected by proximity to capture start | D-34 | 02's first OOM is 156s in, and it needed a threshold nothing justified |
| **D-05** | 02's leak is *accelerating* | D-58 | two intervals on one pod; across all 22 the ratio is 0.79 |
| **D-38 draft** | n=1,000 costs "a few ms" | the benchmark | 32 ms; the figure was never measured |
| **D-42/D-44 guards** | `Category == Issue` excludes unrecognised reasons | D-46 | only by side effect, which stopped holding when severity moved |
| **D-54 draft** | the verdict quotes the deepest failing node | D-54 | in 03 that is a consequence; split into Mechanism and Paged |
| **D-56 draft** | `Suppressed` is stored beside its reasons | D-56 | nothing forced them to agree, and the disagreement gets published |
| **§1.1 of the standard** | pipeline includes a `correlate` package | the repository | it never existed |
