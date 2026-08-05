# Engineering standards

The binding document for daiquiri. It governs every change, by any author,
human or agent.

A deviation is a blocking defect. Disclosing one in a PR description doesn't
resolve it. Either the code changes or this file changes, and changing this file
needs an entry in [`decisions.md`](decisions.md) saying why.

## 1. Architecture

### 1.1 The pipeline

A batch pipeline. Data flows one way and never back.

```
JSONL bytes
   ↓  internal/otel        decode wire records, streaming, per-record resilient
   ↓  internal/event       the domain model, and the leaf of the graph
   ↓  internal/classify    issue / marker / unclassified / noise, from a table
   ↓  internal/group       coalesce into findings, non-windowed, timestamps kept
   ↓  internal/link        the Forest Data Structure: one parent int per finding
   ↓  internal/diagnose    pattern, cadence, signatures, incidents
   ↓  internal/report      order and render: table | tree | json
stdout
```

`internal/triage` wires the stages and owns `Result`. `cmd/triage` parses flags,
calls the orchestrator, and maps errors to exit codes. Neither holds domain
logic.

### 1.2 Dependency rule

Dependencies point one way, down the list above. `internal/event` is the leaf
and imports nothing else in this repository. A cycle is a build failure by
construction. An _upward_ import that compiles is still a defect.

The graph as it actually stands, which `go list -deps` will confirm:

```
event     → (nothing)
otel      → event
classify  → event
group     → event classify
link      → event classify group
diagnose  → event classify group link
triage    → event classify group link diagnose otel
report    → event classify group link diagnose otel triage
```

Every package states its responsibility, its permitted dependencies and its
prohibitions in `doc.go`. Those prohibitions are normative and the ledger quotes
them. Before adding code to a package, read its `doc.go` and confirm the code
belongs there. If it doesn't, put it elsewhere; widening the `doc.go` is the
wrong fix.

The prohibitions earn their place. D-40 rejected putting `Pattern` on
`group.Finding` by quoting `group`'s own prohibition against interpreting shape
over time, and the alternative would have left a mutable hole in a value the
rest of the pipeline treats as final.

### 1.3 SOLID, where it earns its place

Cited as justification only when the concrete benefit can be named.

**Single responsibility.** One package, one reason to change. The test: can you
state the package's job in one sentence with no "and"? `group` answers _what is
one thing_. `link` answers _what explains what_. `diagnose` answers _what is
wrong and how bad_. Three sentences, three packages.

**Open/closed.** The event taxonomy is data. Adding a Kubernetes reason means
adding a table row. Any change that needs a `switch` edited to support a new
reason has failed this and gets restructured. The taxonomy is 14 rows of data
and the classifier that walks it has no per-reason branch in it.

**Interface segregation.** Interfaces are one to three methods. A consumer
declares exactly what it calls.

**Dependency inversion.** **Consumers declare interfaces; producers return
concrete types.** This is the Go convention and it isn't negotiable here. There
is no `interfaces.go`. `internal/triage` declares the narrow interfaces it
needs; each stage exports a struct. A stage is faked in tests with a few lines
defined at the test site.

Liskov gets no entry, because nothing here has two implementations behind one
interface and inventing one to satisfy an acronym is the failure mode this
section exists to prevent.

### 1.4 Non-goals

Decisions already taken, recorded so no future change reintroduces one as an
improvement.

- **No concurrency or fan-out.** Sequential decode is ~130 ms against a 5-second
  budget, which is over 35× of headroom. Goroutines would add coordination and race
  surface for no measurable gain. If a benchmark ever contradicts this, change
  the benchmark's verdict first, then the code.
- **No SIMD, no vectorisation, no custom JSON parser.** Same reason.
- **No graph library, no union-find, no interval tree.** n is the number of
  findings, which is 3 to 10 across all six captures. Argued out with
  measurements in D-38.
- **No second implementation of anything.** One decoder, one grouper, one
  renderer. No parallel "alternative" packages, no commented-out second
  approach, no `v2` beside `v1`. Exploratory work happens outside the repo.
- **No persistence, no server, no daemon.** It reads a file and exits.
- **No configuration file.** Flags only, stdlib `flag`, no CLI framework.

## 2. Test discipline

### 2.1 Test-first

RED before GREEN, with evidence. A failing test is written and _observed
failing_ before the implementation exists. "I know it would fail" is not
evidence, and the observed failure output goes in the commit body.

A test that has never been seen to fail is an assertion that the code does what
the code does.

This caught a real one. The JSON test that asserts _"the published outcome must
follow from the published clauses"_ failed on its own fixtures, which set the
outcome and left the clauses zeroed. That's how `Suppressed` became a derived
method rather than a stored field (D-56).

### 2.2 Form

- **Table-driven subtests.** `t.Run` per case, named for the behaviour and never
  numbered. Failure messages state expected against actual.
- **testify.** `require` for preconditions that make the rest of the test
  meaningless, `assert` for independent checks that should all report.
- **Golden files** in `testdata/golden/` for rendered output. Regenerated only
  under an explicit `-update` flag, and every golden diff is read before it's
  accepted. A blindly regenerated golden is worse than no test.
- **Benchmarks** with `-benchmem` on ingest, grouping and linking. The 5-second
  budget is an acceptance criterion, so it gets a measurement.
- **`-race` always.** `make test` runs it. Even single-threaded it's free
  insurance against the day that stops being true.

### 2.3 Pristine output

A passing run prints nothing but pass lines. No stray logging, no warnings, no
unexplained skips. Noise in test output is how real failures get missed.

### 2.4 What gets tested

Every package with logic carries tests before it's called done. Specifically:

| Under test         | The case that matters                                                                                                                       |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------- |
| the taxonomy       | every reason in the brief's table, plus `BackOff` at both severities and `Failed` with and without an image-pull body                       |
| taxonomy totality  | a property test: every reason's final rule is unconditional, so classification can never fall through                                       |
| the coalescing key | records that must merge and records that must not; 02's four pods on four nodes stay one finding per reason                                 |
| determinism        | group the same input 100× and assert byte-identical output, which catches the map-ordering trap in D-13 that otherwise fails intermittently |
| count semantics    | synthetic, because `k8s.event.count` is 1 for every record in this corpus and the count-incrementing path can't be exercised by it (D-10)   |
| shape              | synthetic distributions rather than fixtures, so thresholds aren't pinned to one dataset                                                    |
| rendering          | goldens, plus `TestStylingIsEmphasisOnly` and `TestRenderPlainHasNoEscapeSequences`                                                         |
| end to end         | all six captures against the brief's acceptance criteria, including **no false positives on `01-healthy`**                                  |

## 3. Documentation rigour

Documentation is a graded deliverable.

### 3.1 Levels

- **HLD**, [`hld.md`](hld.md). System context, the pipeline, data flow, the
  shape of the output. Updated when a stage is added, removed or re-scoped.
- **Decision ledger**, [`decisions.md`](decisions.md). Every decision at the
  time it's made, with the alternatives that lost and the evidence behind it. A
  reversal keeps the original entry and states why it fell, because deleting it
  hides the reasoning. Layered by pipeline stage, with a reversals table at the
  bottom.
- **Design writeup**, [`../DESIGN.md`](../DESIGN.md). The readable version of
  the ledger, for someone who wants the argument without the full record.
- **No per-package LLDs.** Dropped deliberately (D-39). The ledger covers types,
  algorithm, edge cases and failure modes; `doc.go` covers responsibility and
  prohibitions; the HLD covers the pipeline. A fourth document describing the
  same package is a copy to keep in sync.
- **Package docs.** Every package has a `doc.go` stating responsibility,
  permitted dependencies and prohibitions.
- **Godoc.** Every exported identifier has a comment beginning with its own
  name.

### 3.2 What enforces what

Claiming a linter enforces something it doesn't is worse than claiming nothing.
As configured, `make verify` runs:

| Gate                                                                          | Catches                                              |
| ----------------------------------------------------------------------------- | ---------------------------------------------------- |
| `gofmt -l`                                                                    | formatting                                           |
| `go vet`                                                                      | suspicious constructs                                |
| `golangci-lint` (defaults: errcheck, govet, ineffassign, staticcheck, unused) | unchecked errors, dead code, ineffectual assignments |
| `go test -race`                                                               | behaviour, and data races                            |

Godoc coverage and the comment standard below are **not** machine-enforced.
They're review gates, and a review that waves one through is the defect.

### 3.3 Comment standard

Comments explain _why_. A comment restating the code is deleted on sight, and a
non-obvious decision with no comment giving its reason is an incomplete change.

Numbers are never magic. Every threshold, window and limit is a named constant
with a comment saying how it was chosen and what would change it. `link`'s
300-second window carries the measurement that produced it: every true edge in
the corpus lands under +97.9s, the nearest false candidate is at +600s, and
anything in that gap behaves identically.

Prose in this repository follows the author's voice spec, kept outside the
repo at `~/career/career-ops/voice-dna.md`. In practice: no em dashes, no
negated-then-corrected framings, no puffery, sentence-case headings. A
mechanical sweep runs over every markdown file and every Go comment before a
docs change is called done.

## 4. Go conventions

- `gofmt` clean, always.
- No package-name stutter. `group.Finding`, never `group.GroupFinding`.
- `cmd/` and `internal/` only, no `pkg/`. The widely-circulated
  `golang-standards/project-layout` repository is not official and is not
  followed here.
- Errors wrapped with `%w`, carrying enough context to name the stage and the
  record involved. Sentinel errors where callers must branch, inspected with
  `errors.Is` / `errors.As` and never by string matching.
- Accept interfaces, return structs.
- Zero values are useful where that costs nothing.
- No `panic` outside genuinely impossible states, and never in library code.
- No global mutable state.
- Exported surface is minimised. If nothing outside the package calls it, it's
  lowercase.

## 5. Output contract

Stdout is a graded deliverable read by a human under time pressure.

- **Styling is emphasis only.** Strip the CSI sequences from the styled
  rendering and it's byte-identical to the plain one. No fact is ever carried by
  colour alone. `TestStylingIsEmphasisOnly` asserts exactly that.
- **Plain when piped.** Output degrades to clean text when stdout is not a
  terminal, because the submission requires captured files. Goldens pin the
  plain path.
- **Worst first.** Incidents are ordered by severity, then blast radius, then
  time. The reader should not have to scroll to find the worst thing.
- **Cause above symptom.** A root appears above the findings it explains.
- **Every number that could hide something is disclosed.** Records skipped,
  filtered as noise, held back as background, or dropped by `--trace` are all
  counted in the header. A filter that stays quiet about what it filtered can
  mislead by omission.
- **Every claim points at evidence the tool can quote.** Counts, timestamps,
  signatures. The brief calls a summary that lists counts without interpretation
  half the work.

## 6. Review gates

- **Important and above blocks.** No merge, no "done", no moving on.
- **Minor findings go to a ledger** and are triaged at final review. They are
  not silently dropped.
- **`make verify` passes clean** before any change is called complete. Claiming
  completion without running it is a process violation regardless of whether the
  code happens to work.
- **Evidence before assertions.** "Tests pass" requires pasted output. "It
  works" requires the command and its result. Two entries in the ledger record
  performance figures published from estimate rather than measurement, both
  corrected by the benchmark that should have run first (D-38).

## 7. Standing limitations

Declared, not defects. Anything that moves one of these gets a ledger entry.

**The taxonomy covers 14 event reasons**, which are exactly the reasons these
six captures contain. A real cluster emits `CreateContainerConfigError`,
`FailedAttachVolume`, `NetworkNotReady`, `Preempted` and more. Every one lands
on the conservative fallback: surfaced as an issue if it's a Warning, never
labelled with a pattern, never suppressed, no remediation offered. Nothing is
dropped in silence and nothing is confidently mislabelled, and the tool is still
measurably less useful on a cluster it hasn't seen. Tracked as **O-04**, and
demonstrated end to end in
[`../analysis/06-test-c.md`](../analysis/06-test-c.md#the-extra-line-at-the-end-of-this-file).

**Joint causation cannot be expressed.** At most one parent per finding, which
is what keeps "pull the thread" a walk instead of a branching interrogation. No
capture in this corpus exhibits joint causation. The boundary is real (D-19).

**The same-pod causal rule links to any earlier finding sharing a pod**, so an
unrelated earlier symptom on that pod could become its parent. Checked against
the corpus and it does not occur. Guarding it needs a reason-precedence table,
which is inflation for a case with no evidence behind it (D-32).
