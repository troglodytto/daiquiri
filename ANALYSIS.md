# Analysis Report

Three scenarios, diagnosed from the tool's output. Every quoted line is copied
from the captured runs in [`analysis/`](analysis/), reproducible with:

```sh
go run ./cmd/triage testdata/04-test-a.jsonl
```

---

## 04-test-a — checkout-service is serving no traffic after a bad rollout

**Diagnosis.** The `checkout-service` rollout at `10:21:59.977` shipped a build
whose readiness path returns **404**. All five replicas of the new replica set
`checkout-service-7d4f8b9c5` are failing readiness and none has ever passed, so
the Service has **zero ready endpoints** — `checkout-service` in `production` is
down, not degraded. Still failing when the capture ends, 7m49s in.

**Evidence.**

```
ROOT CAUSE  rollout of checkout-service at 10:21:59.977
IMPACT      222 events · 5 pods · 7m56s · STILL FAILING at capture end
▪ Readiness probe failed: HTTP probe failed with statuscode: 404      178/222
  → the server answered the probe -- with a status saying this path is not what it wants
⤷ rollout created replica set checkout-service-7d4f8b9c5 7.3s earlier;
  all 5 affected pods belong to it
```

First failure lands **7.3s after** the rollout with zero occurrences before it,
and the probe repeats `every ~10.5s per pod`. The 404 is decisive: the container
is running and answering — it is answering that this path does not exist. The
image is fine; the health endpoint moved or was removed.

**Remediation (next 5 minutes).**
`kubectl rollout undo deployment/checkout-service -n production`. Confirm
endpoints return with `kubectl get endpoints checkout-service -n production`,
then diff the probe path in the deployment spec against the new build's routes
before rolling forward again.

**Confidence: high**, for the rollout as cause and 404 as mechanism. Two things
would raise it. First, **44 of the 222 events are not 404s** — 24 `connection
refused` and 20 timeouts — and the event stream does not explain them: all five
pods `Started` between `10:22:02` and `10:22:24` and were **never killed**, so
these are not restart churn. Container logs would settle whether the process was
flapping internally. Second, the previous revision's probe path would confirm the
change rather than infer it.

---

## 05-test-b — node-4 ran out of disk and evicted six unrelated services

**Diagnosis.** `node-4` reported `NodeHasDiskPressure` at `10:15:00.000` and its
kubelet evicted **10 pods across 6 workloads in 3 namespaces** within 1m38s.
This is a single node-level fault, not six application failures. The evicted
workloads are victims and need no code change. The incident **stopped 10m
before the capture ends**, so the node either recovered or was drained.

**Evidence.**

```
ROOT CAUSE  node-4 reported NodeHasDiskPressure at 10:15:00.000
IMPACT      11 events · 10 pods · 6 workloads · 3 namespaces · 4m57s
            stopped before capture end
▾ evicted 6 workloads across 3 namespaces in 1m37.9s, each reporting:
  The node had condition: [DiskPressure].
```

The link is stated by the records themselves, not inferred: the node reports
`NodeHasDiskPressure` and every evicted pod's body names `[DiskPressure]`, on
that node, +15.4s to +1m37.9s later. Spanning `data`, `production` and
`staging` is what makes it read as six incidents in a flat report and one
incident here.

**The control case matters.** The capture also holds a `data-pipeline` eviction
at `10:02:32` on **node-2**, whose body reads `The node was low on resource:
memory` — different node, different cause, thirteen minutes earlier. The tool
keeps it separate and suppresses it as background. Folding it in would have
dragged the incident's start time back before the event that caused it.

**Remediation (next 5 minutes).** `kubectl cordon node-4` to stop scheduling
onto it, then `kubectl describe node node-4` and check disk usage — image cache
and container logs first. Uncordon once reclaimed.

**Confidence: high** that disk pressure on node-4 evicted these pods. **Low** on
*what filled the disk* — that is not in an event stream at all. Node-level disk
metrics, or `crictl imagefsinfo`, would answer it. Whether the node has since
recovered is also unresolved: the silence after `10:16:37` is consistent with
both recovery and a drained node.

---

## 06-test-c — data-pipeline was scaled beyond what the cluster can fit

**Diagnosis.** `data-pipeline` was scaled to **12 replicas** at `10:19:59.977`
and the cluster cannot place them. At least six pods are stuck `Pending` with
**177 scheduling failures over 9m55s**, still failing at capture end. This is a
capacity problem, not an application fault — nothing is crash-looping, and no
pod that did start has failed.

**Evidence.**

```
ROOT CAUSE  rollout of data-pipeline at 10:19:59.977
IMPACT      177 events · 6 pods · 10m0s · STILL FAILING at capture end
▪ Scaled up replica set data-pipeline-3c7d2e1a9 to 12                    1/1
▪ 0/6 nodes are available: 6 Insufficient cpu.                       118/177
  → no node had enough spare CPU for these pods
▪ 0/6 nodes are available: 3 Insufficient cpu, 3 Insufficient memory.  44/177
  → no node had enough of either CPU or memory for these pods
```

`0/6 nodes are available` is unambiguous — every node in the cluster was
evaluated and rejected. CPU is the binding constraint on all six nodes in the
common case; on three of them memory binds too, so adding CPU alone would not
place every pod. The scheduler retries `every ~20.4s per pod`, which will
continue indefinitely without intervention.

**Remediation (next 5 minutes).** Scale back to what the cluster held before —
`kubectl scale deployment/data-pipeline -n data --replicas=<previous>` — which
clears the Pending pods immediately. Then decide properly between adding nodes
and lowering the pods' CPU requests; `kubectl describe node` will show how much
headroom actually exists.

**Confidence: high.** The scheduler states its own reason and names the count of
nodes it rejected. What would raise it further: the deployment's resource
requests and the nodes' allocatable capacity, which together give the exact
shortfall rather than "insufficient".

**One data-quality note.** This capture contains **one malformed line** that the
tool could not parse, disclosed as `skipped 1` in the header. It is the last
line of the file. Twenty thousand records were read successfully and the
diagnosis does not depend on the missing one — but a capture the tool could not
fully read must never be presented as a clean one.

---

## What the tool did not decide for me

Worth stating plainly, since these are the judgements the output supports rather
than makes:

- **04 is an outage, not a warning.** Every individual `Unhealthy` event is
  Warning severity and the tool reports the incident as `WARNING`. What makes it
  critical is that **5 of 5** replicas are affected, which is in the blast radius
  rather than the severity. Severity is a property of an event type; scope is a
  property of an incident.
- **05's six findings are one page, not six.** The tool groups them under one
  root, but the decision that you fix the node rather than the six services is a
  human one.
- **In all three, the tool's confidence tracks what is *in* the capture.** It
  reports 04 and 06 as `explained` because a rollout precedes them, and it has no
  way to know whether the rollout itself was intentional.
