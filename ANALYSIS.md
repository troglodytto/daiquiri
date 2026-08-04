# Analysis report

Three unknown scenarios, diagnosed from the tool's own output. One file each,
covering what's broken, how to spot it, what to do about it, and how sure I am.

| Scenario | Verdict | Still failing? |
|---|---|---|
| [04-test-a](analysis/04-test-a.md) | `checkout-service` rollout shipped a build whose health path 404s. All 5 replicas fail readiness, so the Service has zero endpoints. | yes |
| [05-test-b](analysis/05-test-b.md) | `node-4` filled its disk and evicted 10 pods across 6 workloads in 3 namespaces. One node fault wearing six disguises. | no, stopped 10m before the capture ends |
| [06-test-c](analysis/06-test-c.md) | `data-pipeline` scaled to 12 replicas the cluster can't fit. 177 scheduling failures, 6 pods Pending. | yes |

Every quoted line comes from the captured runs in [`analysis/`](analysis/), with
screenshots in [`screenshots/`](screenshots/). Reproduce any of them with:

```sh
go run ./cmd/triage testdata/04-test-a.jsonl
```

## Three judgements the tool doesn't make for you

**04 is an outage, not a warning.** Every `Unhealthy` event is Warning severity
and the tool labels the incident `WARNING`. What makes it critical is that it's
5 of 5 replicas, which lives in the blast radius. Severity belongs to an event
type. Scope belongs to an incident. You need both.

**05's six findings are one page, not six.** The tool groups them under one root
and says so. Deciding to fix the node and leave the six services alone is still
your call.

**Confidence tracks what's in the capture, not what's true.** 04 and 06 read
`explained` because a rollout precedes them. The tool has no way to know whether
that rollout was intentional.
