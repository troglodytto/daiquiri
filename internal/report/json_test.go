package report_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/diagnose"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/link"
	"github.com/troglodytto/daiquiri/internal/report"
	"github.com/troglodytto/daiquiri/internal/triage"
)

// tiedSymptoms mirrors 05-test-b: one node condition and two evictions of equal
// severity, where no single symptom can be said to have paged anyone.
func tiedSymptoms() triage.Result {
	condition := group.Finding{
		Kind: event.KindNode, Workload: "node-4", Namespace: "default",
		Reason: "NodeHasDiskPressure", Rule: "node/disk-pressure",
		Category: classify.CategoryIssue, Severity: event.SeverityCritical, Recognised: true,
		Count: 1, FirstSeen: at("10:15:00.000"), LastSeen: at("10:15:00.000"),
		Nodes: []string{"node-4"},
	}
	condition.Cause = "node is under disk pressure and will evict pods"

	first := finding(event.SeverityWarning, "auth-service", "production", "Evicted", 1,
		[]string{"auth-service-cb5f88109-57949"}, []string{"node-4"},
		at("10:15:15.359"), at("10:15:15.359"))
	second := finding(event.SeverityWarning, "batch-reporter", "data", "Evicted", 1,
		[]string{"batch-reporter-19cb12bab-7143a"}, []string{"node-4"},
		at("10:15:25.779"), at("10:15:25.779"))

	findings := []group.Finding{condition, first, second}

	return triage.Result{
		Ingested: 20000, Noise: 19984, Cluster: "prod-us-east-1",
		Elapsed: 131 * time.Millisecond,
		Chart: diagnose.Chart{
			Forest: link.Forest{
				Findings: findings,
				Edges: []link.Edge{
					{Parent: -1},
					{Parent: 0, Kind: link.KindCaused, Rule: "node-condition-named", Evidence: "node-4 reported DiskPressure 15.4s earlier"},
					{Parent: 0, Kind: link.KindCaused, Rule: "node-condition-named", Evidence: "node-4 reported DiskPressure 25.8s earlier"},
				},
			},
			CaptureEnd: at("10:29:59.677"),
			Diagnoses: []diagnose.Diagnosis{
				{Pattern: diagnose.PatternNodeIssue, Confidence: diagnose.ConfidenceExplained,
					Because: diagnose.Suppression{Diagnosable: true, Transient: true, Root: true}},
				{Pattern: diagnose.PatternNodeIssue, Confidence: diagnose.ConfidenceExplained,
					Because: diagnose.Suppression{Diagnosable: true, Transient: true, Childless: true}},
				{Pattern: diagnose.PatternNodeIssue, Confidence: diagnose.ConfidenceExplained,
					Because: diagnose.Suppression{Diagnosable: true, Transient: true, Childless: true}},
			},
		},
	}
}

// emit renders a result as JSON and decodes it back into a generic form, so the
// tests below assert on the document a consumer actually receives, and never on
// the Go structs behind it.
func emit(t *testing.T, res triage.Result) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, res, "v1.2.3"))

	var doc map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), "output must be valid JSON")

	return doc
}

func TestJSONGolden(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, deployFailure(), "v1.2.3"))

	golden(t, "deploy-failure.json", buf.Bytes())
}

// TestJSONCarriesEveryRecord is the property the whole document rests on: a
// count you cannot recompute is a count you have to trust.
func TestJSONCarriesEveryRecord(t *testing.T) {
	doc := emit(t, deployFailure())

	findings := doc["findings"].([]any)
	require.Len(t, findings, 3)

	for _, raw := range findings {
		f := raw.(map[string]any)
		records := f["records"].([]any)

		shape := f["shape"].(map[string]any)
		assert.Equal(t, shape["occurrences"], float64(len(records)),
			"a watch-based capture emits one record per occurrence, so these must agree")
	}
}

// TestJSONIncludesSuppressedFindings: excluding them would make the document
// agree with the tool by construction, which is the opposite of the point.
func TestJSONIncludesSuppressedFindings(t *testing.T) {
	doc := emit(t, healthy())

	findings := doc["findings"].([]any)
	require.Len(t, findings, 3, "01-healthy's three background findings are all present")

	assert.Empty(t, doc["incidents"], "and none of them is an incident")

	for _, raw := range findings {
		d := raw.(map[string]any)["diagnosis"].(map[string]any)
		assert.True(t, d["suppressed"].(bool))
	}
}

// TestJSONExplainsEverySuppressionDecision is what makes a verdict arguable:
// the outcome is true exactly when all four clauses are, and both are
// published.
func TestJSONExplainsEverySuppressionDecision(t *testing.T) {
	for _, res := range []triage.Result{deployFailure(), healthy(), nodePressure()} {
		doc := emit(t, res)

		for _, raw := range doc["findings"].([]any) {
			d := raw.(map[string]any)["diagnosis"].(map[string]any)
			because := d["because"].(map[string]any)

			all := because["is_a_recognised_failure"].(bool) &&
				because["is_small_on_every_axis"].(bool) &&
				because["nothing_explains_it"].(bool) &&
				because["it_explains_nothing"].(bool)

			assert.Equal(t, d["suppressed"], all,
				"the published outcome must follow from the published clauses")
		}
	}
}

// TestJSONIncidentIndicesResolve guards the one way this document can lie:
// incidents refer to findings by index, so a bad index would point a reader at
// the wrong failure.
func TestJSONIncidentIndicesResolve(t *testing.T) {
	doc := emit(t, deployFailure())

	findings := doc["findings"].([]any)
	incidents := doc["incidents"].([]any)
	require.Len(t, incidents, 1)

	in := incidents[0].(map[string]any)

	for _, key := range []string{"root", "mechanism"} {
		i := int(in[key].(float64))
		require.Less(t, i, len(findings), "%s index out of range", key)
		assert.Equal(t, float64(i), findings[i].(map[string]any)["index"])
	}

	require.NotNil(t, in["paged"], "this incident has one worst symptom")
	assert.Less(t, int(in["paged"].(float64)), len(findings))

	for _, m := range in["members"].([]any) {
		assert.Less(t, int(m.(float64)), len(findings))
	}
}

// TestJSONPagedIsNullOnATie: 05-test-b has six equally-bad evictions, and a
// number there would be a fabrication. Null says "we do not know", which is a
// different and honest claim.
func TestJSONPagedIsNullOnATie(t *testing.T) {
	doc := emit(t, tiedSymptoms())

	incidents := doc["incidents"].([]any)
	require.Len(t, incidents, 1)

	in := incidents[0].(map[string]any)
	paged, present := in["paged"]

	require.True(t, present, "the key is always present; its value carries the claim")
	assert.Nil(t, paged)
}

// TestJSONRootHasNoEdge: a root emits null. A zero-valued object would put
// Parent 0 on the wire, which reads as "explained by finding 0".
func TestJSONRootHasNoEdge(t *testing.T) {
	doc := emit(t, deployFailure())

	findings := doc["findings"].([]any)

	assert.Nil(t, findings[0].(map[string]any)["edge"], "the rollout is a root")
	require.NotNil(t, findings[1].(map[string]any)["edge"])

	e := findings[1].(map[string]any)["edge"].(map[string]any)
	assert.Equal(t, float64(0), e["parent"])
	assert.Equal(t, "caused", e["kind"])
	assert.Equal(t, "rollout-replicaset", e["rule"], "the rule that fired, not prose to be parsed")
	assert.NotEmpty(t, e["evidence"])
	assert.Equal(t, "4.435s", e["delay"])
}

// TestJSONPublishesTheThresholds. A threshold nobody can see is a threshold
// nobody can challenge, and every verdict in the document was decided by one.
func TestJSONPublishesTheThresholds(t *testing.T) {
	doc := emit(t, deployFailure())

	tool := doc["tool"].(map[string]any)
	assert.Equal(t, "v1.2.3", tool["version"], "the version is passed in, never guessed")
	assert.Equal(t, "5m0s", tool["causal_window"])

	th := tool["thresholds"].(map[string]any)
	assert.Equal(t, float64(10), th["transient_max_count"])
	assert.Equal(t, float64(1), th["transient_max_pods"])

	// Durations are strings. 60000000000 is a number a reader has to decode, and
	// a person writing an analysis report reads this document too.
	assert.Equal(t, "1m0s", th["transient_max_span"])
	assert.Equal(t, "2m0s", th["still_failing_within"])
}

// TestJSONDisclosesTheCountersEvenAtZero. "Absent" and "zero" are different
// claims, and only one of them says the tool read the whole file.
func TestJSONDisclosesTheCountersEvenAtZero(t *testing.T) {
	doc := emit(t, deployFailure())

	c := doc["capture"].(map[string]any)
	for _, key := range []string{
		"records_ingested", "records_skipped",
		"records_filtered_as_noise", "records_unrecognised",
	} {
		_, ok := c[key]
		assert.True(t, ok, "%s must be present even at zero", key)
	}

	assert.Equal(t, float64(0), c["records_skipped"])
	assert.Equal(t, float64(20000), c["records_ingested"])
	assert.Equal(t, schemaVersionForTest, doc["schema"])
}

// TestJSONDoesNotEscapeURLs; a probe address rendered with escaped angle
// brackets is unreadable in the one output whose purpose is to be read closely.
func TestJSONDoesNotEscapeURLs(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, nodePressure(), "dev"))

	assert.NotContains(t, buf.String(), `<`)
	assert.NotContains(t, buf.String(), `&`)
}

// TestJSONEmptySlicesAreNotNull: a consumer iterating pods should not have to
// special-case "this finding is not about pods".
func TestJSONEmptySlicesAreNotNull(t *testing.T) {
	doc := emit(t, deployFailure())

	rollout := doc["findings"].([]any)[0].(map[string]any)
	shape := rollout["shape"].(map[string]any)

	assert.NotNil(t, shape["pods"])
	assert.Empty(t, shape["pods"])
	assert.NotNil(t, shape["nodes"])
}

const schemaVersionForTest = float64(1)
