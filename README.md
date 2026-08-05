# daiquiri

A triage tool for Kubernetes events shipped as OpenTelemetry logs. It reads a
20,000-line JSONL capture, throws away the lifecycle noise, coalesces what's
left into findings, works out what caused what, and tells you in one screen what
broke, why, how bad it is, and what to do next.

```
   ROOT CAUSE    rollout of checkout-service  at 10:21:59.977
                 readiness probe failing; the pod is being kept out of service
                 → the application is running but not answering its health check, so it
                   is being kept out of the load balancer and serving no traffic
                 │ Readiness probe failed: HTTP probe failed with statuscode: 404

   IMPACT        222 events · 5 pods · 7m56s · STILL FAILING at capture end
```

That `404` is the whole diagnosis. The rollout shipped a build whose
`/healthcheck` path no longer exists. It's one substring out of 20,000 records,
and finding it is the job.

## Deliverables

**The three test scenarios**, each covering what's broken, how to spot it in the
tool's output, what to do in the next five minutes, and confidence with what
would raise it:

| | Verdict |
|---|---|
| **[04-test-a](analysis/04-test-a.md)** | `checkout-service` rolled out a build whose health path 404s. All 5 replicas fail readiness, so the Service has zero endpoints. |
| **[05-test-b](analysis/05-test-b.md)** | `node-4` filled its disk and evicted 10 pods across 6 workloads in 3 namespaces. One node fault wearing six disguises. |
| **[06-test-c](analysis/06-test-c.md)** | `data-pipeline` scaled to 12 replicas the cluster can't fit. 177 scheduling failures, 6 pods Pending. |

**[ANALYSIS.md](ANALYSIS.md)** indexes those three, adds write-ups of the three
provided scenarios ([01-healthy](analysis/01-healthy.md),
[02-memory-leak](analysis/02-memory-leak.md),
[03-image-pull-failure](analysis/03-image-pull-failure.md)) checked against the
answers the brief states for them, and maps every acceptance criterion to where
it holds.

**[DESIGN.md](DESIGN.md)** is why it's built this way: the north star, the
thread, the Forest Data Structure, and what I'd do differently.

Raw captured output for all six is in [`analysis/`](analysis/) as `.txt` and
`.json`, with terminal screenshots in [`screenshots/`](screenshots/).

## Build and run

Go 1.26 or newer. No external services, no network access, no configuration.

```sh
go build -o daiquiri ./cmd/triage
./daiquiri testdata/04-test-a.jsonl
```

Or without building:

```sh
go run ./cmd/triage testdata/04-test-a.jsonl
```

**Give it a wide window.** The output is laid out for a terminal, and a narrow
one will wrap it into a mess. Measured across these six captures:

| View | Needs | Why |
|---|---|---|
| `--tree` | ~105 columns | text wraps to a fixed 92-column frame, and the indent and glyphs sit outside it |
| `--table` | up to ~145 columns | columns size to their content, so a longer workload name makes it wider |
| the `RECOMMENDED` line | up to 160 columns | it's a `kubectl` command and is deliberately never wrapped, because a wrapped command can't be pasted |

**160 columns clears everything here.** If your window is narrower, use `-tree`,
which is bounded and stays readable, or pipe to a file and open it in an editor.
Nothing is lost either way, since the layout is the only thing that suffers.

Or through the Makefile, which stamps the version from `git describe`:

```sh
make build        # -> bin/daiquiri
make run ARGS=testdata/04-test-a.jsonl
make help         # every target, one line each
```

### Docker

If you'd rather not install a Go toolchain. The captures are ~16 MB each and
stay out of the image, so mount the directory and pass a path inside it:

```sh
docker build -t daiquiri .
docker run --rm -t -v "$PWD/testdata:/data:ro" daiquiri /data/04-test-a.jsonl
```

`-t` keeps stdout a terminal, which is what turns colour on. Drop it and you get
plain text with no escape sequences, which is what you want when redirecting:

```sh
docker run --rm -v "$PWD/testdata:/data:ro" daiquiri -json /data/05-test-b.jsonl > run.json
```

Multi-stage build. `golang:1.26-alpine` compiles a static `CGO_ENABLED=0` binary
(3.5 MB, `-trimpath -s -w`), and the final stage is `alpine:3.21` running as uid
65532. **17.4 MB image**, builds in ~30s from cold.
`make docker-run ARGS=04-test-a.jsonl` wraps both commands.

## Usage

```
usage: triage [flags] <filename.jsonl>

flags:
  -table   render only the prioritised summary table
  -tree    render only the causal incident view
  -json    emit the whole run as JSON
  -trace   narrow to one workload or pod and mark it
```

**With no flags you get both views**, in the order you need them. The table is
the inventory: everything wrong, prioritised, at a glance. The tree is the
argument for what caused it.

The flags narrow, they don't enable. Pass `-table` when you want one line per
finding for scanning or diffing, and `-tree` when you want the causal trail on
its own. Passing both is an error, so it can't silently do nothing.

```sh
./daiquiri testdata/05-test-b.jsonl           # table, then incidents
./daiquiri -table testdata/05-test-b.jsonl    # just the table
./daiquiri -tree  testdata/05-test-b.jsonl    # just the incidents
./daiquiri -tree  testdata/05-test-b.jsonl > analysis/05.txt   # plain text, no escapes
```

### `-trace`, the 3am path

You were paged for one service. You don't want every incident in the cluster.
You want the trail from your symptom back to its origin.

```sh
./daiquiri -trace auth-service testdata/05-test-b.jsonl
```

```
TRACING auth-service — showing 1 of 1 incident

   ROOT CAUSE    node-4 reported NodeHasDiskPressure  at 10:15:00.000
   ...
     ├─ 10:16:25.429  Evicted  auth-service · production  ×1 · 1 pod  +1m25.4s   YOU ARE HERE
```

`auth-service` did nothing wrong. The trail runs from the pod you were paged for
back to a node that filled its disk, through two namespaces you don't own.

`-trace` takes a workload name or a pod instance, implies the incident view, and
marks exactly one node: the last thing your service did, rather than every
finding that happens to share its name. Incidents that don't involve it are
counted in the header instead of dropped, because a filtered report that stays
quiet about the filter can mislead by omission.

`-json` replaces the human views rather than adding to them, so the output pipes
straight into `jq`. Combining it with `-table` or `-tree` is an error.

Exit codes: `0` success, `1` the file couldn't be read or decoded, `2` bad
usage.

## JSON output

`-json` is built for one purpose: **letting you disagree with the tool.** Every
verdict sits next to the inputs that produced it, so nothing has to be taken on
trust.

```sh
./daiquiri -json testdata/03-image-pull-failure.jsonl > run.json
```

```jsonc
{
  "schema": 1,
  "tool": {
    "causal_window": "5m0s",
    "thresholds": {
      "transient_max_count": 10,
      "transient_max_pods": 1,
      "transient_max_span": "1m0s",
      "still_failing_within": "2m0s"
    }
  },
  "capture":   { "records_ingested": 20000, "records_skipped": 0, ... },
  "incidents": [ { "root": 3, "mechanism": 4, "paged": 5, "members": [3,4,5], ... } ],
  "findings":  [ { "index": 0, "identity": {...}, "verdict": {...}, "shape": {...},
                   "diagnosis": {...}, "signatures": [...], "edge": {...},
                   "records": [ ...every member record, verbatim... ] } ]
}
```

What that buys you:

- **Every count is recomputable.** `shape.occurrences` sits beside the records it
  was counted from, so you can check it rather than believe it.
- **Every threshold is visible.** The numbers that decided each verdict travel
  with the verdicts.
- **Every suppression is explained.** `diagnosis.because` publishes each clause
  separately, so you can see which one decided it:

  ```json
  "because": {
    "is_a_recognised_failure": true,
    "is_small_on_every_axis":  true,
    "nothing_explains_it":     true,
    "it_explains_nothing":     true
  }
  ```

- **Suppressed findings are included**, flagged. Leaving them out would make the
  document agree with the tool by construction.
- **`paged` is `null`** when several symptoms are equally bad. A number there
  would be a fabrication.

Incidents index into `findings` rather than nesting them, so no finding is
duplicated and you can walk either structure:

```sh
jq '.findings[] | select(.diagnosis.suppressed) | .identity'          run.json
jq '.incidents[0] as $i | .findings[$i.mechanism].signatures'         run.json
jq '.findings[] | select(.edge) | {from: .edge.parent, why: .edge.evidence}' run.json
```

Lifecycle noise is counted but not reproduced. It's ~19,800 of 20,000 records
and it's already in the input file. Everything the tool *concluded* is in the
document, and everything it *read* is in the file it read. Output runs 7 KB to
100 KB against a 16 MB input.

## What the output means

### The header

```
20,000 records, 19,772 filtered as noise, 2 findings, 3 suppressed as background (134ms)
```

Every number that could hide something is disclosed. `suppressed as background`
counts the real-but-meaningless findings the tool held back, like a single
eviction or a three-event probe blip, and it's printed so that "2 findings" is a
claim you can check. A capture with a line the decoder couldn't parse also says
`skipped N`, so a truncated file can never masquerade as a clean one.

### The table

One row per finding, ordered by time, with a `PATTERN` column naming the kind of
failure: `sustained crash-loop`, `deploy-correlated failure`, `capacity issue`,
`node issue`, `transient blip`. A dash means the tool declined to categorise. A
rollout is an anchor and does no damage of its own, and a reason outside the
taxonomy is one we have no basis to label.

### The incident view

One block per causal tree, worst first. Each opens with a verdict you can stop
at:

| Line | Answers |
|---|---|
| `ROOT CAUSE` | what set this off, and when |
| the line below it | what actually broke, in the cluster's terms |
| `→` | what that means for whoever owns the service, in plain English |
| `│` | the cluster's own words, quoted verbatim |
| `IMPACT` | how wide, how long, and whether it's still happening |
| `RECOMMENDED` | the next move, with the command filled in |

Below that, `how we got there` shows the chain:

```
  ▸ 10:17:59.977  ScalingReplicaSet  payment-service · production
     ▪ Scaled up replica set payment-service-9e3f1a2b8 to 3  1/1

     ╰─ ● 10:18:04.412  Failed  payment-service · production      ×24 · 3 pods · 10m15s
           deploy-correlated failure
           ▪ Failed to pull image "…/payment-service:v2.14.0-rc3": manifest…   3/24
             Error: ImagePullBackOff                                          18/24
           ⤷ rollout created replica set payment-service-9e3f1a2b8 4.4s earlier;
             all 3 affected pods belong to it
```

| Symbol | Meaning |
|---|---|
| `●` | a finding |
| `▸` | a deploy marker, an anchor with no damage of its own |
| `▪` | a **specific** signature: it names an image, an amount, a status code |
| *(dim, unmarked)* | a generic signature that only restates the reason |
| `x/y` | how many of the finding's records carry that exact line |
| `→` | what that particular body *proves*, stated as evidence and no further |
| `⤷` | why the causal edge is believed, so you can check it against the capture |
| `▾` | one explanation shared by every child below it |
| `ROOT CAUSE` | where the trail ends |
| `PAGED HERE` | the symptom that would have raised the alert |

`PAGED HERE` is absent when several symptoms are equally bad. `05-test-b` has
six evictions of equal severity and there's no way to know which one paged you,
so the tool doesn't pretend otherwise.

### Signatures

A finding can hold 222 records whose bodies differ only in which replica was
probed. The tool normalises the volatile parts (pod names, IP addresses, object
UIDs) and counts what remains, so 222 records become three distinct symptoms
with counts.

Numbers with units are never normalised. `512Mi`, `404`, `8080` and `0/6 nodes`
are the answer.

Signatures rank by **specificity first, frequency second**. In
`03-image-pull-failure` the most common line is `Error: ImagePullBackOff` at 18
of 24, which only restates the reason. The line naming the tag that doesn't
exist occurs 3 times and leads anyway.

Where the body proves something the reason alone can't, the signature carries a
reading. One `Unhealthy` covers three different situations:

```
▪ Readiness probe failed: HTTP probe failed with statuscode: 404       178/222
  → the server answered the probe, with a status saying this path is
    not what it wants
▪ Readiness probe failed: ... connect: connection refused               24/222
  → nothing was listening on that port when the probe fired
```

Those prove opposite things about the process. One is running and answering, the
other isn't running at all. The readings state what the evidence establishes and
stop there. You debug, the tool just sharpens the lens.

Readings exist only for shapes these captures contain. Anything else carries no
reading rather than a plausible-sounding guess.

### Cadence

A repeating finding has a rhythm, and the rhythm is often the diagnosis:

```
sustained crash-loop  ·  every ~4m11s per pod
deploy-correlated failure  ·  every ~40.5s per pod, easing 13s → 2m35s
```

One OOM kill per pod every four minutes says more about a memory leak than the
count and the span together. `easing` is Kubernetes backing off, so the gaps
keep widening whatever you do until the image is fixed.

Intervals are measured **per pod and then pooled**, never across the finding.
04's five pods are probed independently, so the finding-wide gap is 1.4s where
the actual probe period is 10.5s. The first number is an artefact of the replica
count. The second is a fact about the deployment.

The trend is named only when it's unmistakable, meaning a 3× move between the
early and late halves. `02-memory-leak` contracts from 4m43s to 3m43s and is
reported **steady**: a 21% move across 22 samples that already range from 169s
to 326s isn't a claim this data supports. Both means are in the JSON so you can
judge it yourself.

## Colour

Colour is emphasis only. Strip the escape sequences from the styled output and
it's byte-identical to the plain output, so no fact is ever carried by colour
alone. That's what makes a redirected file readable:

```sh
./daiquiri -tree testdata/05-test-b.jsonl | cat    # already plain
NO_COLOR=1 ./daiquiri testdata/05-test-b.jsonl     # plain on a terminal too
```

Styling is applied only when stdout is a terminal and `NO_COLOR` is unset.

## Development

```sh
make verify   # the gate: vet, lint, race tests, gofmt check
make test     # go test ./... -race
make lint     # golangci-lint  (make tools installs it, pinned)
make bench    # go test -bench . -benchmem
make capture  # regenerate every file in analysis/
```

Golden files are regenerated with `go test ./internal/report -update`. Read the
diff before committing it.

`make hooks` points `core.hooksPath` at `.githooks/`, so gofmt, vet and lint run
on every commit.

Diagrams live in [`docs/diagrams/`](docs/diagrams/) as SVG plus editable
`.excalidraw` source, both written by `python3 docs/diagrams/generate.py`.

## Performance

A 16.5 MB, 20,000-record capture is processed in **~130 ms**, about 125 MB/s,
15 MB allocated. The brief's budget is 5 seconds, so there's over 35× of
headroom. Every record is retained in memory, including the ~19,800 filtered as
noise, because lifecycle events are the evidence a trail gets built from.

## Known limits

The taxonomy covers the fourteen event reasons that occur in the six provided
captures. A real cluster emits many more: `CreateContainerConfigError`,
`FailedAttachVolume`, `NetworkNotReady`, `Preempted` and others.

For any of those the tool falls back conservatively. An unrecognised Warning is
surfaced as an issue, never labelled with a pattern, never suppressed as
background, and offered no remediation. Nothing is dropped in silence and
nothing is confidently mislabelled. The tool is still measurably less useful on
a cluster it hasn't seen, and that's a gap rather than a design choice. It's
tracked as `O-04` in the decision ledger.

## What's in here

| Path | What it holds |
|---|---|
| [`DESIGN.md`](DESIGN.md) | the design decisions, why each one, and what I'd do differently |
| [`ANALYSIS.md`](ANALYSIS.md) | index of the three test-scenario diagnoses |
| [`analysis/*.md`](analysis/) | one diagnosis per scenario: what's broken, how to spot it, what to do, confidence |
| [`analysis/*.txt`](analysis/), `*.json` | raw captured output for all six scenarios, plus a `--trace` example |
| [`screenshots/`](screenshots/) | the terminal output for each scenario, rendered |
| [`docs/diagrams/`](docs/diagrams/) | pipeline, Forest Data Structure and thread diagrams |
| `cmd/triage` | flags and exit codes, nothing else |
| `internal/` | `otel` → `classify` → `group` → `link` → `diagnose` → `report` |
| `testdata/golden/` | rendered output, pinned |

## Documentation

| File | What it holds |
|---|---|
| `docs/decisions.md` | every decision, the alternatives that lost, and the fixture evidence, including reversals kept in place with the reason they fell |
| `docs/hld.md` | the architecture |
| `docs/handover.md` | current state, design philosophy, acceptance data, next steps |
| `docs/engineering-standards.md` | the standard all of the above is held to |
