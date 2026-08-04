package classify

import (
	"strings"

	"github.com/troglodytto/daiquiri/internal/event"
)

// rule is one row of the taxonomy.
//
// Rules for a reason are tried in order and the first match wins, so a reason's
// final rule must be unconditional -- that is what makes classification total
// and lets body-sensitive reasons tolerate an empty body.
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
	// It exists so grouping never keys on cause, which is display text -- and
	// rewording a sentence must not silently change how records coalesce.
	id string

	category Category
	severity event.Severity
	cause    string
}

// warning is an addressable copy for use in whenSeverity.
//
// Only Warning needs one: the sole severity-guarded rule is the crash-looping
// BackOff, and its Normal counterpart is that reason's unconditional final rule
// rather than a second guarded one.
var warning = event.SeverityWarning

// imagePullMarkers are the body substrings that identify an image-pull failure.
//
// ImagePullBackOff and ErrImagePull are not Kubernetes event reasons -- they
// appear only inside the body of Failed events -- so reading the body is the
// only way to tell an image-pull failure from a sandbox-creation failure. All
// three shapes occur in the provided captures.
var imagePullMarkers = []string{"ErrImagePull", "ImagePullBackOff", "Failed to pull image"}

// noiseRule is the shared verdict for normal lifecycle events.
var noiseRule = rule{id: "lifecycle", category: CategoryNoise, severity: event.SeverityInfo}

// taxonomy maps an event reason to its ordered rules.
//
// This is data, deliberately. The brief invites extending the list, and
// extending it here means adding a row -- never editing a switch statement.
//
// Severities follow the table in the challenge brief. Where this file departs
// from that table it is marked, with the reason.
var taxonomy = map[string][]rule{
	// --- Critical -----------------------------------------------------------

	// One reason, two failures. Severity is what separates them, and neither is
	// noise: a Normal-severity BackOff is a repeated image-pull retry.
	"BackOff": {
		{
			id:           "backoff/crash-loop",
			whenSeverity: &warning,
			category:     CategoryIssue,
			severity:     event.SeverityCritical,
			cause:        "container is crash-looping and repeatedly failing to start",
		},
		{
			id:       "backoff/image-pull-retry",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			cause:    "repeated image-pull retry; the image cannot be fetched",
		},
	},

	// ImagePullBackOff and ErrImagePull live in the body, not the reason.
	"Failed": {
		{
			id:          "failed/image-pull",
			whenBodyHas: imagePullMarkers,
			category:    CategoryIssue,
			severity:    event.SeverityCritical,
			cause:       "image pull failed; the tag or registry credentials are likely wrong",
		},
		{
			id:       "failed/sandbox-creation",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			cause:    "container or pod sandbox creation failed",
		},
	},
	"FailedCreatePodSandBox": {
		{
			id:       "failed-sandbox",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			cause:    "pod sandbox creation failed; the pod never reached a running state",
		},
	},

	// Classified on reason alone. The brief's table gives the body as "Memory
	// cgroup out of memory ...", but the captures actually carry "Container
	// <name> in pod <pod> exceeded memory limit (512Mi)". A matcher built from
	// the brief's example text would never fire, and the reason is unambiguous
	// on its own.
	"OOMKilling": {
		{
			id:       "oom-killed",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			cause:    "container exceeded its memory limit and was killed by the kernel",
		},
	},

	"NodeNotReady": {
		{
			id:       "node/not-ready",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			cause:    "node went unhealthy; workloads on it are at risk",
		},
	},
	"NodeHasDiskPressure": {
		{
			id:       "node/disk-pressure",
			category: CategoryIssue,
			severity: event.SeverityCritical,
			cause:    "node is under disk pressure and will evict pods",
		},
	},

	// --- Warning ------------------------------------------------------------

	"FailedScheduling": {
		{
			id:       "failed-scheduling",
			category: CategoryIssue,
			severity: event.SeverityWarning,
			cause:    "pod cannot be placed; insufficient resources or unsatisfied taints",
		},
	},
	"Unhealthy": {
		{
			id:          "unhealthy/liveness",
			whenBodyHas: []string{"Liveness probe failed"},
			category:    CategoryIssue,
			severity:    event.SeverityWarning,
			cause:       "liveness probe failing; the kubelet will restart the container",
		},
		{
			id:       "unhealthy/readiness",
			category: CategoryIssue,
			severity: event.SeverityWarning,
			cause:    "readiness probe failing; the pod is being kept out of service",
		},
	},
	"FailedMount": {
		{
			id:       "failed-mount",
			category: CategoryIssue,
			severity: event.SeverityWarning,
			cause:    "volume mount failed; a referenced configmap or secret is likely missing",
		},
	},
	"Evicted": {
		{
			id:          "evicted/disk-pressure",
			whenBodyHas: []string{"[DiskPressure]"},
			category:    CategoryIssue,
			severity:    event.SeverityWarning,
			cause:       "pod evicted because its node was under disk pressure",
		},
		{
			id:       "evicted/memory-pressure",
			category: CategoryIssue,
			severity: event.SeverityWarning,
			cause:    "pod evicted because its node was under resource pressure",
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

	// Extension. Not reportable on its own, but retained rather than discarded:
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
