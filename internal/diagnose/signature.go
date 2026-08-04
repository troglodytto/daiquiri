package diagnose

import (
	"regexp"
	"sort"
	"strings"

	"github.com/troglodytto/daiquiri/internal/group"
)

// Volatile tokens: the parts of a record body that identify *which* occurrence
// rather than *what happened*.
//
// Deliberately three, and deliberately not more. Numbers with units are never
// touched -- 512Mi, 404, 8080 and "0/6 nodes" are the answer this whole feature
// exists to surface, and a normaliser that ate them would delete the diagnosis
// along with the noise.
var (
	// podToken matches <workload>-<9 hex>-<5 alnum>, the pod instance name.
	podToken = regexp.MustCompile(`[a-z][a-z0-9-]*-[0-9a-f]{9}-[0-9a-z]{5}`)

	// ipToken matches a dotted IPv4 address. Ports are left alone: 8080 says
	// which service was probed, where the address says which replica.
	ipToken = regexp.MustCompile(`\d{1,3}(?:\.\d{1,3}){3}`)

	// uidToken matches a parenthesised object UID, as BackOff bodies carry.
	// Anchored on all-hex-and-hyphen so that "(512Mi)" cannot match.
	uidToken = regexp.MustCompile(`\([0-9a-f][0-9a-f-]{7,}\)`)

	// whitespace collapses runs left behind by the substitutions above.
	whitespace = regexp.MustCompile(`\s+`)

	// concreteNoun decides specificity: a quoted string, or a surviving digit.
	//
	// After normalisation the volatile digits are gone, so a digit that remains
	// is a fact about the failure -- a status code, a resource amount, a port, a
	// node tally. A quoted string is a name the cluster chose to quote: an image
	// reference, a volume, a configmap.
	concreteNoun = regexp.MustCompile(`"[^"]+"|\d`)
)

// Signature is one distinct thing a finding's records say, and how often.
type Signature struct {
	// Text is the record body with volatile tokens replaced.
	Text string

	// Count is how many of the finding's records carry it.
	Count int

	// Specific marks a signature that names something concrete. Generic
	// signatures restate the reason -- "Error: ImagePullBackOff" beside a
	// finding whose reason is already Failed -- and are rendered as such.
	Specific bool

	// Means is what this particular body proves, where the finding's own
	// meaning is too coarse to say. Empty when we have nothing to add.
	//
	// It states what the evidence establishes and stops there. The engineer
	// debugs; this tool sharpens the lens they debug through, and a reading that
	// slid into instructions would be guessing at a system it cannot see.
	Means string
}

// reading is one row of the signature interpretation table.
//
// The taxonomy interprets reasons; this interprets signature shapes. It exists
// because one reason can carry situations that mean opposite things: an
// Unhealthy body reading "connection refused" proves nothing was listening,
// while one reading "statuscode: 404" proves the server was listening and
// answering. The finding-level meaning cannot be right for both.
//
// Splitting the taxonomy rule instead would have been the obvious move -- Failed
// already does it -- but Rule is in the coalescing key, so it would fragment
// 04-test-a's one broken deployment into three findings.
//
// First match wins, so narrower rows come first. A signature matching no row
// carries no reading, which is the same abstention the classifier makes for a
// reason it cannot read.
type reading struct {
	name  string
	all   []string // every substring must be present
	means string
}

// readings covers only shapes the captures actually contain.
//
// No row for HTTP 500 or 503, for "no route to host", or for TCP-probe
// failures: none appears in any capture, and a body matcher nobody has ever
// seen fire is a guess wearing a rule's clothes. OOMKilling already proved that
// trap -- the brief's own example body text would never have matched a real
// record.
var readings = []reading{
	// The three readiness outcomes in 04-test-a, which interleave for the whole
	// 7m49s rather than resolving into phases.
	{
		name:  "probe/http-status",
		all:   []string{"probe failed", "statuscode:"},
		means: "the server answered the probe -- with a status saying this path is not what it wants",
	},
	{
		name:  "probe/refused",
		all:   []string{"probe failed", "connection refused"},
		means: "nothing was listening on that port when the probe fired",
	},
	{
		name:  "probe/timeout",
		all:   []string{"probe failed", "deadline exceeded"},
		means: "the connection was not refused, and no answer arrived inside the probe's timeout",
	},

	// 06-test-c's scheduler bodies name which resource ran out, and the two are
	// not the same problem to have.
	{
		name:  "scheduling/cpu-and-memory",
		all:   []string{"Insufficient cpu", "Insufficient memory"},
		means: "no node had enough of either CPU or memory for these pods",
	},
	{
		name:  "scheduling/cpu",
		all:   []string{"Insufficient cpu"},
		means: "no node had enough spare CPU for these pods",
	},
	{
		name:  "scheduling/memory",
		all:   []string{"Insufficient memory"},
		means: "no node had enough spare memory for these pods",
	},
}

// readingOf returns what a signature's text proves, or empty.
func readingOf(text string) string {
	for _, r := range readings {
		matched := true

		for _, token := range r.all {
			if !strings.Contains(text, token) {
				matched = false

				break
			}
		}

		if matched {
			return r.means
		}
	}

	return ""
}

// signaturesOf reduces a finding's records to their distinct normalised bodies,
// ranked by specificity and then by frequency.
//
// Frequency alone is the wrong order. In 03-image-pull-failure the ranking is:
//
//	Error: ImagePullBackOff                                   18 of 24
//	Failed to pull image "...:v2.14.0-rc3": manifest not found  3 of 24
//
// The rarest line is the only one that says anything; the common one is a
// paraphrase of the REASON column. Leading with it buries the answer under a
// restatement of the question.
func signaturesOf(f group.Finding) []Signature {
	counts := make(map[string]int, len(f.Events))
	for _, e := range f.Events {
		counts[normalise(e.Body)]++
	}

	out := make([]Signature, 0, len(counts))
	for text, n := range counts {
		if text == "" {
			continue
		}

		out = append(out, Signature{
			Text:     text,
			Count:    n,
			Specific: concreteNoun.MatchString(text),
			Means:    readingOf(text),
		})
	}

	// Text is the final tiebreak so the order is total: Go randomises map
	// iteration, and two signatures can share both specificity and count.
	sort.Slice(out, func(i, j int) bool {
		switch {
		case out[i].Specific != out[j].Specific:
			return out[i].Specific
		case out[i].Count != out[j].Count:
			return out[i].Count > out[j].Count
		default:
			return out[i].Text < out[j].Text
		}
	})

	return out
}

// normalise strips the tokens that vary between occurrences of one failure.
func normalise(body string) string {
	body = podToken.ReplaceAllString(body, "<pod>")
	body = ipToken.ReplaceAllString(body, "<ip>")
	body = uidToken.ReplaceAllString(body, "")

	return strings.TrimSpace(whitespace.ReplaceAllString(body, " "))
}
