package classify

import (
	"strings"

	"github.com/troglodytto/daiquiri/internal/event"
)

// Rule identifiers that are read outside this package.
const (
	// RuleCrashLoop is a Warning-severity BackOff: a container restarting.
	RuleCrashLoop = "backoff/crash-loop"
	// RuleOOMKilled is a container killed by the kernel for exceeding its limit.
	RuleOOMKilled = "oom-killed"
	// RuleFailedScheduling is a pod the scheduler could not place.
	RuleFailedScheduling = "failed-scheduling"
)

// rule is one row of the taxonomy.
type rule struct {
	// whenSeverity, when set, requires the event's severity to match. This is
	// what separates a crash-looping BackOff from an image-pull-retry BackOff.
	whenSeverity *event.Severity
	// whenBodyHas, when non-empty, requires the body to contain one of these
	// substrings. Kubernetes emits fixed strings here, so matching is
	// case-sensitive.
	whenBodyHas []string

	// id is a short stable identifier for this row.
	//
	// Grouping keys on it, so that two events sharing a reason but matching
	// different rules become different findings: an Evicted for disk pressure
	// and an Evicted for memory pressure are not one fact about a workload.
	// It exists so grouping never keys on cause, which is display text; and
	// rewording a sentence must not silently change how records coalesce.
	id string

	category Category
	severity event.Severity
	cause    string

	// meaning is what the failure amounts to for a developer who doesn't operate
	// Kubernetes.
	//
	// Separate from cause. cause is what the cluster did ("container exceeded
	// its memory limit and was killed by the kernel"). meaning is what that says
	// about your software ("the process is using more memory than it is allowed,
	// which usually means a leak").
	//
	// Written to be read aloud to whoever owns the service. One sentence, no
	// kubectl, no Kubernetes nouns where a plain one exists.
	meaning string

	// fix is the next move, rendered as RECOMMENDED under the incident. Empty
	// renders nothing.
	//
	// {workload}, {namespace}, {node} and {pod} are substituted from the finding
	// so the command can be pasted at 3am instead of hand-edited. Where a safe
	// check and a real change both apply, the check comes first.
	fix string
}

// warning is an addressable copy for use in whenSeverity.
var warning = event.SeverityWarning

// imagePullMarkers are the body substrings that identify an image-pull failure.
var imagePullMarkers = []string{"ErrImagePull", "ImagePullBackOff", "Failed to pull image"}

// noiseRule is the shared verdict for normal lifecycle events.
var noiseRule = rule{id: "lifecycle", category: CategoryNoise, severity: event.SeverityInfo}

// taxonomy maps an event reason to its ordered rules.
var taxonomy = map[string][]rule{
	// --- Critical -----------------------------------------------------------

	// One reason, two failures. Severity is what separates them, and neither is
	// noise: a Normal-severity BackOff is a repeated image-pull retry.
	"BackOff": {
		{
			id:           RuleCrashLoop,
			whenSeverity: &warning,
			category:     CategoryIssue,
			severity:     event.SeverityCritical,
			meaning:      "the container starts, dies, and is restarted over and over -- it is failing during startup or crashing immediately after it",
			cause:        "container is crash-looping and repeatedly failing to start",
			fix:          "read the previous container's exit: kubectl logs {pod} -n {namespace} --previous",
		},
		{
			id:       "backoff/image-pull-retry",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			meaning:  "Kubernetes keeps retrying a download that cannot succeed, and will keep waiting longer between attempts until the image is fixed",
			cause:    "repeated image-pull retry; the image cannot be fetched",
			fix:      "same fix as the pull failure above; the retry stops when the image resolves",
		},
	},

	// ImagePullBackOff and ErrImagePull live in the body, not the reason.
	"Failed": {
		{
			id:          "failed/image-pull",
			whenBodyHas: imagePullMarkers,
			category:    CategoryIssue,
			severity:    event.SeverityCritical,
			meaning:     "the image this deployment asks for does not exist where it is looking -- most often a tag that was never pushed, or a typo in the version",
			cause:       "image pull failed; the tag or registry credentials are likely wrong",
			fix:         "confirm the tag exists, then roll back: kubectl rollout undo deploy/{workload} -n {namespace}",
		},
		{
			id:       "failed/sandbox-creation",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			meaning:  "the pod could not be created at all, so the application never got the chance to start; this is below your code, in the node runtime or its networking",
			cause:    "container or pod sandbox creation failed",
			fix:      "check the node's container runtime and CNI: kubectl describe pod {pod} -n {namespace}",
		},
	},
	"FailedCreatePodSandBox": {
		{
			id:       "failed-sandbox",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			meaning:  "the node could not build the container environment, so nothing inside the pod ever ran -- an infrastructure fault rather than an application one",
			cause:    "pod sandbox creation failed; the pod never reached a running state",
			fix:      "check the node's container runtime and CNI: kubectl describe node {node}",
		},
	},

	// Classified on reason alone. The brief's table gives the body as "Memory
	// cgroup out of memory ...", but the captures actually carry "Container
	// <name> in pod <pod> exceeded memory limit (512Mi)". A matcher built from
	// the brief's example text would never fire, and the reason is unambiguous
	// on its own.
	"OOMKilling": {
		{
			id:       RuleOOMKilled,
			category: CategoryIssue,
			severity: event.SeverityCritical,
			meaning:  "the process is using more memory than it is allowed, which usually means a leak or a limit set below what the service actually needs",
			cause:    "container exceeded its memory limit and was killed by the kernel",
			fix:      "read the previous run's logs for the growth, then raise the limit: kubectl logs {pod} -n {namespace} --previous",
		},
	},

	"NodeNotReady": {
		{
			id:       "node/not-ready",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			meaning:  "a machine in the cluster stopped reporting healthy, so everything running on it is at risk of being moved or lost",
			cause:    "node went unhealthy; workloads on it are at risk",
			fix:      "check the kubelet on {node}, then drain if it stays down: kubectl describe node {node}",
		},
	},
	"NodeHasDiskPressure": {
		{
			id:       "node/disk-pressure",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			meaning:  "a machine in the cluster is running out of disk, and Kubernetes has started killing pods on it to reclaim space",
			cause:    "node is under disk pressure and will evict pods",
			fix:      "free disk on {node} and stop scheduling onto it: kubectl describe node {node}; kubectl cordon {node}",
		},
	},

	// --- Warning ------------------------------------------------------------

	"FailedScheduling": {
		{
			id:       RuleFailedScheduling,
			category: CategoryIssue,
			severity: event.SeverityWarning,
			meaning:  "there is nowhere to put these pods -- the cluster does not have enough free CPU or memory for what was asked for",
			cause:    "pod cannot be placed; insufficient resources or unsatisfied taints",
			fix:      "the cluster cannot fit this; scale down or add capacity: kubectl scale deploy/{workload} -n {namespace} --replicas=<fewer>",
		},
	},
	"Unhealthy": {
		{
			id:          "unhealthy/liveness",
			whenBodyHas: []string{"Liveness probe failed"},
			category:    CategoryIssue,
			severity:    event.SeverityWarning,
			meaning:     "the application stopped answering the check that proves it is alive, so Kubernetes is restarting it",
			cause:       "liveness probe failing; the kubelet will restart the container",
			fix:         "confirm the probe path exists in the new image; compare against the previous revision",
		},
		{
			id:       "unhealthy/readiness",
			category: CategoryIssue,
			severity: event.SeverityWarning,
			meaning:  "the application is running but not answering its health check, so it is being kept out of the load balancer and serving no traffic",
			cause:    "readiness probe failing; the pod is being kept out of service",
			fix:      "confirm the probe path exists in the new image: kubectl rollout history deploy/{workload} -n {namespace}",
		},
	},
	"FailedMount": {
		{
			id:       "failed-mount",
			category: CategoryIssue,
			severity: event.SeverityWarning,
			meaning:  "the pod is waiting for a configmap or secret that does not exist, so it cannot start until that config is created",
			cause:    "volume mount failed; a referenced configmap or secret is likely missing",
			fix:      "create the missing configmap or secret, or fix its name in the pod spec",
		},
	},
	"Evicted": {
		{
			id:          "evicted/disk-pressure",
			whenBodyHas: []string{"[DiskPressure]"},
			category:    CategoryIssue,
			severity:    event.SeverityWarning,
			meaning:     "this pod was killed to protect a machine that was running out of disk -- the pod is the victim, not the cause",
			cause:       "pod evicted because its node was under disk pressure",
			fix:         "fix the node condition first; the pods will reschedule once {node} recovers",
		},
		{
			id:       "evicted/memory-pressure",
			category: CategoryIssue,
			severity: event.SeverityWarning,
			meaning:  "this pod was killed to protect a machine running low on memory, usually because it asked for less than it actually uses",
			cause:    "pod evicted because its node was under resource pressure",
			fix:      "set or raise the memory request for {workload} so the scheduler reserves what it uses",
		},
	},

	// --- Ignore -------------------------------------------------------------

	"Pulled":           {noiseRule},
	"Created":          {noiseRule},
	"Started":          {noiseRule},
	"Scheduled":        {noiseRule},
	"Killing":          {noiseRule},
	"SuccessfulCreate": {noiseRule},
	"SuccessfulDelete": {noiseRule},

	// Extension, as the brief permits ("you may extend this list"). Pulling is
	// the counterpart to Pulled and accounts for 17,175 records across the six
	// captures. Omitting it leaves ~2,860 noise records per file surviving the
	// filter, which would make 01-healthy report 2,884 issues instead of none.
	"Pulling": {noiseRule},

	// Extension. Not reportable alone, but retained:
	// in all three captures where it appears it lands minutes before that
	// capture's incident, making it the anchor for deploy-correlated failure.
	"ScalingReplicaSet": {
		{
			id:       "deploy/scaled",
			category: CategoryDeployMarker,
			severity: event.SeverityInfo,
			cause:    "deployment scaled; a rollout occurred here",
		},
	},
}

// matches reports whether the rule applies to e.
func (r rule) matches(e event.Event) bool {
	if r.whenSeverity != nil && *r.whenSeverity != e.Severity {
		return false
	}

	for _, marker := range r.whenBodyHas {
		if strings.Contains(e.Body, marker) {
			return true
		}
	}

	return len(r.whenBodyHas) == 0
}
