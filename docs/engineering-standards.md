# Engineering Standards

This is the binding document for daiquiri. It governs every change, by any
author, human or agent. Deviations are not resolved by disclosure — a deviation
is a blocking defect, not a note in a PR description.

`AGENTS.md` is the short always-loaded summary of this file. Where they appear
to disagree, this file wins, and the discrepancy is itself a bug to fix.

---

## 1. Architecture

### 1.1 The pipeline

daiquiri is a batch pipeline. Data flows one way and never back:

```
JSONL bytes
   ↓  internal/otel        decode wire records (streaming, per-record resilient)
   ↓  internal/event       normalize to the domain model
   ↓  internal/classify    noise predicate + severity, from a table
   ↓  internal/group       coalesce into findings (non-windowed key, timestamps retained)
   ↓  internal/diagnose    shape over time + likely cause, per finding
   ↓  internal/correlate   shared root cause, across findings
   ↓  internal/report      order, render (text | json)
stdout
```

`internal/triage` wires the stages. `cmd/triage` parses flags, calls the
orchestrator, and maps errors to exit codes. Neither contains domain logic.

### 1.2 Dependency rule

Dependencies point one direction, down the list above. `internal/event` is the
leaf and imports nothing internal. A cycle is a build failure by construction;
an *upward* import that compiles is still a defect.

Each package states its responsibility, its permitted dependencies, and its
explicit prohibitions in its `doc.go`. Those prohibitions are normative. Before
adding code to a package, read its `doc.go` and confirm the code belongs there.
If it does not, the answer is to put it elsewhere, not to widen the doc.

### 1.3 SOLID, as it actually applies in Go

Applied where it earns its place. Cited as justification only when the concrete
benefit can be named.

- **Single responsibility.** One package, one reason to change. The test: can
  you state the package's job in one sentence with no "and"? `group` answers
  *what is one thing*; `diagnose` answers *what is wrong with it*; `correlate`
  answers *are these the same incident*. Three sentences, three packages.
- **Open/closed.** The event taxonomy is data. Adding a reason means adding a
  table row. Any change that requires editing a `switch` to support a new
  Kubernetes reason has failed this test and must be restructured.
- **Liskov.** The `text` and `json` renderers are interchangeable behind one
  interface: same inputs, same information, no renderer-specific preconditions.
  If a caller must know which renderer it holds, the abstraction is wrong.
- **Interface segregation.** Interfaces are one to three methods. A stage
  consumer declares exactly what it calls and nothing more.
- **Dependency inversion.** **Consumers declare interfaces; producers return
  concrete types.** This is the Go convention and it is not negotiable here.
  There is no `interfaces.go`. `internal/triage` declares the narrow interfaces
  it needs; each stage package exports a struct. A stage is faked in tests with
  a few lines defined at the test site.

### 1.4 Non-goals

Recorded so that no future change reintroduces them as "improvements". Each is
a decision already made, with its reason.

- **No concurrency or fan-out.** A 20k-record capture decodes sequentially in
  well under the 5s budget. Goroutines would add coordination complexity and
  race surface for no measurable gain. Benchmarks justify this; if a benchmark
  ever contradicts it, change the benchmark's verdict first, then the code.
- **No SIMD, no vectorization, no custom JSON parser.** Same reason.
- **No second implementation of anything.** One decoder, one grouper, one text
  renderer. No parallel "alternative" packages, no commented-out second
  approach, no `v2` beside `v1`. Exploratory work happens outside the repo and
  never enters the submission.
- **No persistence, no server, no daemon.** It is a CLI that reads a file and
  exits.
- **No configuration file.** Flags only.

---

## 2. Test discipline

### 2.1 Test-first

RED before GREEN, with evidence. A failing test is written and *observed
failing* before the implementation exists. "I know it would fail" is not
evidence. The observed failure output goes in the commit body or the task note.

A test that has never been seen to fail is not a test — it is an assertion that
the code does what the code does.

### 2.2 Form

- **Table-driven subtests.** `t.Run` per case, named for the behavior, not
  numbered. Failure messages state expected versus actual.
- **testify** for assertions: `require` for preconditions that make the rest of
  the test meaningless, `assert` for independent checks that should all report.
- **Golden files** in `testdata/golden/` for renderer output. Regenerated only
  by an explicit `-update` flag, and every golden diff is read before it is
  accepted. A blindly regenerated golden file is worse than no test.
- **Benchmarks** with `-benchmem` for the ingest and grouping paths. The 5s
  budget is an acceptance criterion, so it gets a benchmark, not a hope.
- **`-race` always.** `make test` runs it. Even single-threaded, it is free
  insurance against the day that stops being true.

### 2.3 Pristine output

A passing test run prints nothing but the pass lines. No stray logging, no
warnings, no skipped-test noise left unexplained. Noise in test output is how
real failures get missed.

### 2.4 What gets tested

Every package with logic carries tests before it is called done. Specifically:
the noise predicate against all reasons in the brief's table; the coalescing key
against records that must and must not merge; shape classification against
synthetic burst, sustained, and recurring distributions; renderer output against
goldens; and the end-to-end pipeline against all six fixtures, asserting the
brief's acceptance criteria directly — including no false positives on
`01-healthy.jsonl`.

---

## 3. Documentation rigour

Documentation is a deliverable, not an afterthought, and it is graded.

### 3.1 Levels

- **HLD** — `docs/hld.md`. System context, the pipeline, data flow, the shape of
  the output, and cross-cutting decisions. Updated when a stage is added,
  removed, or re-scoped.
- **Decision ledger** — `docs/decisions.md`. Every decision at the time it is
  made, with the alternatives that lost and the evidence behind it. A reversal
  keeps the original entry and states why it fell; deleting it hides the
  reasoning. This carries what per-package LLDs and separate ADRs would, in one
  place that cannot drift out of step with itself.
- **No per-package LLDs.** Dropped deliberately (`docs/decisions.md` D-39). The
  ledger covers types, algorithm, edge cases and failure modes; the package
  `doc.go` covers responsibility and prohibitions; the HLD covers the pipeline.
  A fourth document describing the same package is a copy to keep in sync, not
  a design aid.
- **Package docs** — every package has a `doc.go` stating responsibility,
  permitted dependencies, and prohibitions.
- **Godoc** — every exported identifier has a comment beginning with its own
  name. Enforced by `revive`.

### 3.2 Standard

Comments explain *why*, not *what*. A comment restating the code is deleted on
sight. A non-obvious decision without a comment explaining its reason is an
incomplete change.

Numbers are never magic. Every threshold, window size, and limit is a named
constant with a comment explaining how it was chosen and what would change it.

---

## 4. Go conventions

- `gofmt` clean, always. Enforced in `make verify`.
- No package-name stutter: `group.Grouper`, never `group.GroupGrouper`.
- `cmd/` and `internal/` only. No `pkg/`. (The widely-circulated
  "golang-standards/project-layout" repository is not official and is not
  followed here.)
- Errors wrapped with `%w` and enough context to name the stage and the record
  or finding involved. Sentinel errors where callers must branch; `errors.Is` /
  `errors.As` to inspect, never string matching.
- Accept interfaces, return structs.
- Zero values are useful where it costs nothing.
- No `panic` outside genuinely impossible states, and never in library code.
- No global mutable state.
- Exported surface is minimized. If nothing outside the package calls it, it is
  lowercase.

---

## 5. Output contract

The tool's stdout is a graded deliverable read by a human under time pressure.

- Styling is emphasis only, never information. Text and JSON outputs carry
  identical content.
- Output degrades to clean plain text when stdout is not a terminal, because
  the submission requires captured files. Golden tests pin the plain path.
- Findings are ordered by severity, then by blast radius. The reader should not
  have to scroll to find the worst thing.
- Correlations appear above the findings they explain: cause before symptoms.
- Every claim is grounded in evidence the tool can point at — counts,
  timestamps, signatures. A summary that lists counts without interpretation is
  explicitly called out in the brief as half the work.

---

## 6. Review gates

- **Important and above blocks.** No merge, no "done", no moving on.
- **Minor findings go to a ledger** and are triaged at final review. They are
  not silently dropped.
- **`make verify` passes clean** before any change is called complete. Claiming
  completion without running it is a process violation regardless of whether
  the code happens to work.
- **Evidence before assertions.** "Tests pass" requires pasted output. "It
  works" requires the command and its result.
