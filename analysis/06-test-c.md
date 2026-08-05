# 06-test-c: data-pipeline was scaled past what the cluster can fit

Run it yourself:

```sh
go run ./cmd/triage testdata/06-test-c.jsonl
```

![daiquiri output for 06-test-c](../screenshots/06-test-c.png)

Raw output: [`06-test-c.txt`](06-test-c.txt) · [`06-test-c.json`](06-test-c.json)

## What's broken

`data-pipeline` was scaled to 12 replicas at 10:19:59.977 and the cluster can't
place them. At least 6 pods are stuck `Pending`, with 177 scheduling failures
over 9m55s, still failing when the capture ends.

Nothing is crash-looping. No pod that did start has failed. This is a capacity
problem, and the fix is arithmetic rather than debugging.

## How to identify it

**1. The scheduler tells you outright.** No inference needed:

```
▪ 0/6 nodes are available: 6 Insufficient cpu.                        118/177
  → no node had enough spare CPU for these pods
▪ 0/6 nodes are available: 3 Insufficient cpu, 3 Insufficient memory.  44/177
  → no node had enough of either CPU or memory for these pods
```

`0/6 nodes are available` means every node in the cluster was evaluated and
rejected. That's the whole search space, and every node in it said no.

**2. Read which resource binds.** CPU is the constraint on all 6 nodes in the
common case. On 3 of them memory binds too. So adding CPU alone won't place
every pod, and you'd find that out the slow way after the next scale-up.

**3. The cadence is flat.**

```
capacity issue  ·  every ~20.4s per pod
```

20 seconds is the scheduler's retry interval. It'll keep doing this forever.
Compare with 03, where the interval eases from 13s to 2m35s because Kubernetes
backs off image pulls. A flat retry means nothing is self-resolving and nothing
will.

**4. Look for what isn't there.** No `BackOff`, no `OOMKilling`, no `Unhealthy`.
Pods that scheduled are fine. Only the ones that couldn't be placed are
complaining, which points at the cluster rather than the workload.

## What to do

Next 5 minutes, get back to a size the cluster held:

```sh
kubectl scale deployment/data-pipeline -n data --replicas=<previous>
kubectl get pods -n data --field-selector=status.phase=Pending   # should empty out
```

Then decide properly, because you've got two real options and they cost
different things:

```sh
kubectl describe node | grep -A5 'Allocated resources'   # how much headroom exists
kubectl get deploy data-pipeline -n data -o yaml | grep -A4 resources
```

Either add nodes, or lower the pods' CPU requests if they're over-asking. Check
actual usage before you cut requests, or you'll trade Pending pods for throttled
ones.

## Confidence: high

The scheduler states its own reason and names how many nodes it rejected. Not
much room for a different reading.

What would raise it: the deployment's resource requests next to the nodes'
allocatable capacity. That turns "insufficient" into an exact shortfall, which
is the number you need to size the fix.

## The extra line at the end of this file

Line 20,001 of `testdata/06-test-c.jsonl` is mine. I appended it to test two
things the six shipped captures never exercise: what happens when the decoder
hits a line it can't read, and what happens when the taxonomy meets a reason it
has never seen.

It's a well-formed OTel record carrying a made-up reason, commented out with a
leading `// ` so it's unparseable as it ships:

```
// {"timestamp":"2024-06-01T10:29:59.677Z","severity_text":"Warning", ...
//  "body":"In case there are some new events that we haven't really recognized
//   and handled, we'd much rather surface it, instead of burying it",
//  "attributes":{"k8s.event.reason":"LALALALA", ...
```

### As it ships: the decoder can't parse it

```
20,000 records, 19,817 filtered as noise, 2 findings, 3 suppressed as background, skipped 1 (133ms)
```

`skipped 1`. The decode loop is per-record resilient, so one bad line is counted
and stepped over instead of aborting the run. The count is printed in the header
because a capture the tool couldn't fully read should never be able to
masquerade as a clean one. The diagnosis doesn't depend on that line, and you
can see that it doesn't because the number is right there.

### Uncomment it: the taxonomy has never seen `LALALALA`

Strip the `// ` and run it again:

```
20,001 records, 19,817 filtered as noise, 3 findings, 3 suppressed as background, unrecognised 1 (140ms)

10:29:59.677  WARNING   data-pipeline  data  LALALALA  1  1  instant  -  -
```

Three things happen, and each of them is a deliberate decision:

**It gets surfaced.** An unknown reason at `Warning` severity is treated as an
issue. The alternative is dropping anything the taxonomy doesn't recognise,
which turns every gap in my table into silence on your cluster.

**It gets no pattern.** The `PATTERN` column shows `-`. The tool has no basis to
call an unrecognised reason a crash-loop or a capacity issue, so it declines
rather than guessing. Same for remediation: no `RECOMMENDED` line, because a
made-up fix is worse than none.

**It is never suppressed as background.** Suppression needs
`recognised failure ∧ small on every axis ∧ nothing explains it ∧ it explains
nothing`. This finding is small, parentless and childless, so three of the four
clauses hold. The first one doesn't, and that's what keeps it visible. Without
that clause, every unknown reason would be quietly filed as background exactly
when you most need to see it.

In the incident view it gets its own block, honestly labelled:

```
━━ INCIDENT 2 of 2 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ WARNING · unexplained ━━

   ROOT CAUSE    unknown — data-pipeline reported LALALALA  at 10:29:59.677
                 unrecognised warning reason; classified conservatively
                 ⚠ unexplained — nothing in this capture explains what came before
```

The header count changes too: `skipped 1` becomes `unrecognised 1`. Those are
different failures and they get different words. One means the tool couldn't
read a line. The other means it read the line fine and doesn't know what the
reason means.

This is `O-04` in the decision ledger, and it's the tool's biggest real
limitation. The taxonomy covers the 14 reasons these six captures contain. A
real cluster emits `CreateContainerConfigError`, `FailedAttachVolume`,
`NetworkNotReady`, `Preempted` and more. Every one of them would come out
looking like this line: visible, unlabelled, and honest about not knowing.
