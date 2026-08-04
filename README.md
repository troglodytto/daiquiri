# daiquiri

A triage tool for Kubernetes events shipped as OpenTelemetry logs. It reads a
20,000-line JSONL capture, throws away the lifecycle noise, coalesces what is
left into findings, works out what caused what, and tells you — in one screen —
what broke, why, how bad it is, and what to do next.

```
   ROOT CAUSE    rollout of checkout-service  at 10:21:59.977
                 readiness probe failing; the pod is being kept out of service
                 → the application is running but not answering its health check, so it
                   is being kept out of the load balancer and serving no traffic
                 │ Readiness probe failed: HTTP probe failed with statuscode: 404

   IMPACT        222 events · 5 pods · 7m56s · STILL FAILING at capture end
```

That `404` is the whole diagnosis: the rollout shipped a build whose
`/healthcheck` path no longer exists. It is one substring out of 20,000 records,
and finding it is the job.

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

## Usage

```
usage: triage [flags] <filename.jsonl>

flags:
  -table   render only the prioritised summary table
  -tree    render only the causal incident view
```

**With no flags you get both**, in the order you need them: the table is the
inventory — everything that is wrong, prioritised, at a glance — and the tree is
the argument for what caused it.

The flags **narrow**; they do not enable. Pass `-table` when you want the
one-line-per-finding view for scanning or diffing, and `-tree` when you want the
causal trail on its own. Passing both is an error rather than a silent no-op.

```sh
./daiquiri testdata/05-test-b.jsonl           # table, then incidents
./daiquiri -table testdata/05-test-b.jsonl    # just the table
./daiquiri -tree  testdata/05-test-b.jsonl    # just the incidents
./daiquiri -tree  testdata/05-test-b.jsonl > analysis/05.txt   # plain text, no escapes
```

Exit codes: `0` success, `1` the file could not be read or decoded, `2` bad
usage.

## What the output means

### The header

```
20,000 records, 19,772 filtered as noise, 2 findings, 3 suppressed as background (134ms)
```

Every number that could hide something is disclosed. `suppressed as background`
is the count of real-but-meaningless findings the tool held back — a single
eviction, a three-event probe blip — and it is printed so that "2 findings" is a
claim you can check rather than one you have to trust. A capture with a line the
decoder could not parse also says `skipped N`, so a truncated file can never
masquerade as a clean one.

### The table

One row per finding, ordered by time, with a `PATTERN` column naming the kind of
failure: `sustained crash-loop`, `deploy-correlated failure`, `capacity issue`,
`node issue`, `transient blip`. A dash means the tool declined to categorise —
a rollout is an anchor rather than a failure, and a reason not in the taxonomy is
one we have no basis to label.

### The incident view

One block per causal tree, most urgent first. Each opens with a verdict you can
stop at:

| Line | Answers |
|---|---|
| `ROOT CAUSE` | what set this off, and when |
| the line below it | what actually broke, in the cluster's terms |
| `→` | what that means for whoever owns the service, in plain English |
| `│` | the cluster's own words, quoted verbatim |
| `IMPACT` | how wide, how long, and whether it is still happening |
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
| `▸` | a deploy marker — an anchor, not a failure |
| `▪` | a **specific** signature: it names an image, an amount, a status code |
| *(dim, unmarked)* | a generic signature that only restates the reason |
| `x/y` | how many of the finding's records carry that exact line |
| `⤷` | why the causal edge is believed — check it against the capture |
| `▾` | one explanation shared by every child below it |
| `ROOT CAUSE` | where the trail ends |
| `PAGED HERE` | the symptom that would have raised the alert |

`PAGED HERE` is absent when several symptoms are equally bad — `05-test-b` has
six evictions of equal severity and there is no way to know which one paged you,
so the tool does not pretend otherwise.

### Signatures

A finding can hold 222 records whose bodies differ only in which replica was
probed. The tool normalises the volatile parts — pod names, IP addresses, object
UIDs — and counts what remains, so 222 records become three distinct symptoms
with counts.

Numbers with units are never normalised. `512Mi`, `404`, `8080` and `0/6 nodes`
are the answer, not noise.

Signatures are ranked by **specificity first, frequency second**. In
`03-image-pull-failure` the most common line is `Error: ImagePullBackOff` (18 of
24), which only restates the reason; the line naming the tag that does not exist
occurs 3 times and leads anyway.

## Colour

Colour is emphasis only. Strip the escape sequences from the styled output and
it is byte-identical to the plain output, so no fact is ever carried by colour
alone — which is what makes a redirected file readable:

```sh
./daiquiri -tree testdata/05-test-b.jsonl | cat    # already plain
NO_COLOR=1 ./daiquiri testdata/05-test-b.jsonl     # plain on a terminal too
```

Styling is applied only when stdout is a terminal and `NO_COLOR` is unset.

## Development

```sh
make test     # go test ./... -race
make lint     # golangci-lint
make bench    # go test -bench . -benchmem
```

Golden files are regenerated with `go test ./internal/report -update`; read the
diff before committing it.

## Performance

A 16.5 MB, 20,000-record capture is processed in **~135 ms** — about 124 MB/s,
15 MB allocated. The brief's budget is 5 seconds, so there is roughly 37×
headroom. Every record is retained in memory, including the ~19,800 filtered as
noise, because lifecycle events are the evidence a trail is built from.

## Known limits

The taxonomy covers the fourteen event reasons that occur in the six provided
captures. A real cluster emits many more — `CreateContainerConfigError`,
`FailedAttachVolume`, `NetworkNotReady`, `Preempted` and others.

For any of those the tool falls back conservatively: an unrecognised Warning is
surfaced as an issue, never labelled with a pattern, never suppressed as
background, and offered no remediation. Nothing is silently dropped and nothing
is confidently mislabelled — but the tool is measurably less useful on a cluster
it has not seen, and that is a gap rather than a design choice. It is tracked as
`O-04` in the decision ledger.

## Documentation

| File | What it holds |
|---|---|
| `docs/decisions.md` | every decision, the alternatives that lost, and the fixture evidence — including reversals, kept in place with the reason they fell |
| `docs/hld.md` | the architecture |
| `docs/handover.md` | current state, design philosophy, acceptance data, next steps |
| `docs/engineering-standards.md` | the standard all of the above is held to |
