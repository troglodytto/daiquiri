# 04-test-a: checkout-service is down after a bad rollout

Run it yourself:

```sh
go run ./cmd/triage testdata/04-test-a.jsonl
```

![daiquiri output for 04-test-a](../screenshots/04-test-a.png)

Raw output: [`04-test-a.txt`](04-test-a.txt) · [`04-test-a.json`](04-test-a.json)

## What's broken

The `checkout-service` rollout at 10:21:59.977 shipped a build whose readiness
path returns 404. All 5 replicas of the new replica set fail readiness, none has
ever passed, and it's still going when the capture ends 7m49s later.

So the Service has zero ready endpoints. `checkout-service` in `production`
isn't degraded, it's serving nothing.

## How to identify it

Three things in the output, in the order they matter.

**1. The 404.** This is the whole diagnosis:

```
▪ Readiness probe failed: HTTP probe failed with statuscode: 404   178/222
  → the server answered the probe -- with a status saying this path is not what it wants
```

A 404 means the container is up and routing. It's answering you, and what it's
answering is that the path doesn't exist. So the image pulled fine and the
process runs. Somebody moved or removed the health endpoint.

Compare that with `connection refused`, which would mean nothing was listening
at all. Same `Unhealthy` reason, completely different problem.

**2. The timing.** First failure lands 7.3 seconds after the rollout, with zero
occurrences before it:

```
⤷ rollout created replica set checkout-service-7d4f8b9c5 7.3s earlier;
  all 5 affected pods belong to it
```

Zero before, 222 after, every affected pod belonging to the new replica set.
That's about as clean as deploy correlation gets.

**3. The scope.** `5 pods` in the table, against a rollout that says `to 5`.
Every replica is affected, which is what turns this from a warning into an
outage. The tool prints `WARNING`, because that's the severity Kubernetes
assigns an `Unhealthy` event. The severity is per event type. The scope is per
incident. Read both.

## What to do

Next 5 minutes:

```sh
kubectl rollout undo deployment/checkout-service -n production
kubectl get endpoints checkout-service -n production   # should stop being empty
```

Then, before you roll forward again, diff the probe path in the deployment spec
against the routes the new build actually serves. `kubectl rollout history
deploy/checkout-service -n production` gives you the revision to compare.

Don't bother reading application logs for a crash. There isn't one. The process
is healthy and the deployment's idea of "healthy" is stale.

## Confidence: high

High on the rollout as cause and the 404 as mechanism. Both are quoted straight
out of the capture.

Two things sit outside what this data can settle.

**44 of the 222 events aren't 404s.** 24 are `connection refused`, 20 are
timeouts. Nothing in a Kubernetes event stream can tell you *why* a process
stopped answering on a port. That answer lives in container logs, a heap
profile, or node metrics, and none of those ship in a JSONL of events.

What the capture does settle is which kind of failure each one is, and the three
kinds want different things from you:

| Signature | n | What it proves | Where to look |
|---|---|---|---|
| `statuscode: 404` | 178 | up, listening, routing, and the path is gone | the deployment spec and the new build's routes |
| `connection refused` | 24 | nothing bound to that port when the probe fired | container start-up, crash logs |
| `i/o timeout` | 20 | the connection was accepted, the answer never came | the process is alive and stuck, or the network is |

I can rule out restart churn: all 5 pods `Started` between 10:22:02 and 10:22:24
and none was ever killed. So something is making the process intermittently
unavailable without the kubelet ever restarting it, and container logs are how
you'd find it.

**The previous revision's probe path.** I'm inferring the endpoint moved. The
old spec would confirm it.
