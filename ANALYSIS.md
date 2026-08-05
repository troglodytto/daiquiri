# Analysis report

One file per capture, each covering what's broken, how to spot it in the tool's
output, what to do about it, and how sure I am.

Every quoted line comes from the captured runs in [`analysis/`](analysis/), with
terminal screenshots in [`screenshots/`](screenshots/). Reproduce any of them:

```sh
go run ./cmd/triage testdata/04-test-a.jsonl
```

## The three test scenarios

| Scenario | Verdict | Still failing? |
|---|---|---|
| [04-test-a](analysis/04-test-a.md) | `checkout-service` rollout shipped a build whose health path 404s. All 5 replicas fail readiness, so the Service has zero endpoints. | yes |
| [05-test-b](analysis/05-test-b.md) | `node-4` filled its disk and evicted 10 pods across 6 workloads in 3 namespaces. One node fault wearing six disguises. | no, stopped 10m before the capture ends |
| [06-test-c](analysis/06-test-c.md) | `data-pipeline` scaled to 12 replicas the cluster can't fit. 177 scheduling failures, 6 pods Pending. | yes |

## The three provided scenarios

Development fixtures with known answers. I've written them up too, because how a
tool behaves on a capture you already know the answer to is how you find out
whether to trust it on one you don't.

| Scenario | Verdict | Matches the brief? |
|---|---|---|
| [01-healthy](analysis/01-healthy.md) | Nothing needs an on-call response. 3 genuine `Warning` events held back as background and disclosed in the header. | yes, and exits `0` |
| [02-memory-leak](analysis/02-memory-leak.md) | `recommendation-service` exceeds its 512Mi limit and is OOM-killed every ~4m11s per pod, 26 kills and 91 restarts across 4 pods. | yes, with one honest divergence on "leak" |
| [03-image-pull-failure](analysis/03-image-pull-failure.md) | `payment-service` rolled out with tag `v2.14.0-rc3`, which the registry says doesn't exist. 3 pods, backing off. | yes |

## Acceptance criteria

The brief's checklist, and where to see each one hold.

| Criterion | Evidence |
|---|---|
| Parses OTel JSONL at 20K scale | 120,001 records across six captures, `records_ingested` in every JSON |
| Filters normal lifecycle events | 19,772 to 19,995 per capture, counted in the header and **retained in memory** as trail evidence |
| Groups by object, namespace and reason | key is `(Kind, Workload, Namespace, Reason, Rule)`; `Workload` rather than the pod instance, so 04's 5 replicas are 1 finding |
| Counts and time ranges computed from the records | `max(count)` per event UID summed; `FirstSeen`/`LastSeen` are the true min/max of members |
| Outputs reason, objects, count, range, severity, cause | every column of the table, plus the cause line in the incident view |
| Severity matches the brief's table | all 11 rules match exactly: `BackOff`/`Failed`/`OOMKilling`/`NodeHasDiskPressure` → CRITICAL, `Evicted`/`FailedScheduling`/`Unhealthy`/`FailedMount` → WARNING, lifecycle → ignored |
| No false positives on `01-healthy` | `0 findings`, `3 suppressed as background`, exit `0` |
| Identifies the primary issue in 02 and 03 | `recommendation-service` OOM, `payment-service` image pull, both with the signature quoted |
| Distinguishes transient from sustained | `transient blip` needs `count ≤ 10 ∧ pods ≤ 1 ∧ span < 60s` and no causal edge in either direction |
| 20K records in under 5 seconds | ~130 ms measured, about 35× of headroom |
| Report covers 04, 05 and 06 | the three files above |

## Three judgements the tool leaves to you

**04 is an outage even though every event is a Warning.** `Unhealthy` is Warning
severity by the brief's own table, and the tool labels the incident `WARNING`.
What makes it critical is that it's 5 of 5 replicas, which lives in the blast
radius rather than the severity. Severity belongs to an event type. Scope
belongs to an incident. Read both.

**05's six findings are one page.** The tool groups them under one root and
quotes the edge. Deciding to fix the node and leave the six services alone is
still your call.

**Confidence tracks what's in the capture.** 04 and 06 read `explained` because
a rollout precedes them. Whether that rollout was intentional is outside the
event stream, and the tool has no way to know.

## One deliberate addition

Line 20,001 of `testdata/06-test-c.jsonl` is mine, appended to test two paths
the shipped captures never reach: an unparseable line, and a reason the taxonomy
has never seen. Both are written up in
[06-test-c.md](analysis/06-test-c.md#the-extra-line-at-the-end-of-this-file).
