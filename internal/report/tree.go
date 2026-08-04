package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/diagnose"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/link"
)

// Layout constants for the incident view.
const (
	// frame is the rendered width. 92 columns fits an 80-column terminal with a
	// little overflow rather than wrapping catastrophically, and leaves room for
	// the deepest chain in the corpus (three levels) without the text column
	// collapsing.
	frame = 92

	// label is the width of the ROOT CAUSE / IMPACT / RECOMMENDED gutter, so the
	// three read as one column rather than three indents.
	label = 15

	// maxSignatures is how many distinct signatures a node shows before the rest
	// are summarised. Two covers every finding in the corpus except three, and
	// those three are told how many they are hiding rather than left to imply
	// there is nothing more.
	maxSignatures = 2
)

// Tree glyphs. Box-drawing rather than ASCII because the structure has to
// survive being read at speed, and because it is characters rather than escape
// sequences it is identical in the plain and styled renderings.
const (
	glyphFinding  = "●"
	glyphMarker   = "▸"
	glyphSpecific = "▪"
	glyphEdge     = "⤷"
	glyphShared   = "▾"
	glyphQuote    = "│"
)

// writeIncidents renders every incident, most urgent first.
//
// When a workload is being traced, only incidents touching it are rendered, and
// the rest are accounted for in one line rather than dropped -- a filtered
// report that does not say what it filtered is a report that can mislead by
// omission.
func (r *Renderer) writeIncidents(b *strings.Builder, c diagnose.Chart) {
	all := c.Incidents()

	incidents := all
	if r.trace != "" {
		incidents = incidents[:0:0]

		for _, in := range all {
			if touches(c, in, r.trace) {
				incidents = append(incidents, in)
			}
		}

		fmt.Fprintf(b, "\n%s %s\n",
			r.paint(r.style.header, "TRACING"),
			r.paint(r.style.meta, fmt.Sprintf("%s — showing %d of %s",
				r.trace, len(incidents), plural(len(all), "incident"))))

		if len(incidents) == 0 {
			fmt.Fprintf(b, "\n  %s\n",
				r.paint(r.style.warning, "no incident in this capture involves "+r.trace))

			return
		}
	}

	for n, in := range incidents {
		b.WriteByte('\n')
		r.writeIncidentRule(b, n, len(incidents), in)
		b.WriteByte('\n')
		r.writeVerdict(b, c, in)
		b.WriteByte('\n')

		fmt.Fprintln(b, "  "+r.paint(r.style.rule, "── how we got there "+strings.Repeat("─", frame-24)))
		b.WriteByte('\n')

		r.writeNode(b, c, in, in.Root, "  ", 0)

		if fix := c.Remediation(in); fix != "" {
			b.WriteByte('\n')
			r.writeLabelled(b, "RECOMMENDED", r.style.healthy, fix, r.style.header)
		}

		b.WriteByte('\n')
	}
}

// touches reports whether any member of an incident concerns the named
// workload, matched against the workload name and the pod instances beneath it.
func touches(c diagnose.Chart, in diagnose.Incident, name string) bool {
	for _, i := range in.Members {
		if matches(c.Findings[i], name) {
			return true
		}
	}

	return false
}

// writeIncidentRule writes the severity-coloured banner that opens an incident.
func (r *Renderer) writeIncidentRule(b *strings.Builder, n, total int, in diagnose.Incident) {
	title := fmt.Sprintf("━━ INCIDENT %d of %d ", n+1, total)
	meta := fmt.Sprintf(" %s · %s ━━", in.Severity, in.Confidence)

	fill := frame - lipgloss.Width(title) - lipgloss.Width(meta)
	if fill < 1 {
		fill = 1
	}

	st := r.severityStyle(in.Severity)

	fmt.Fprintln(b, r.paint(st, title)+r.paint(r.style.rule, strings.Repeat("━", fill))+r.paint(st, meta))
}

// writeVerdict writes the block a reader can stop at.
//
// Four parts, in the order a question gets asked: what set it off, what that
// broke, the cluster's own words for it, and how bad it is. Nothing here is
// composed prose -- the trigger is templated from the root's kind, the
// explanation is the taxonomy's own sentence, and the quoted line is a record
// body with only its volatile tokens replaced.
func (r *Renderer) writeVerdict(b *strings.Builder, c diagnose.Chart, in diagnose.Incident) {
	root := c.Findings[in.Root]
	mech := c.Findings[in.Mechanism]

	r.writeLabelled(b, "ROOT CAUSE", r.style.rootTag,
		trigger(root)+"  "+r.paint(r.style.meta, "at "+root.FirstSeen.UTC().Format(timeLayout)),
		r.style.header)

	gutter := strings.Repeat(" ", label+2)

	if mech.Cause != "" {
		for _, l := range wrapText(mech.Cause, frame-label-4) {
			fmt.Fprintln(b, gutter+r.paint(r.severityStyle(in.Severity), l))
		}
	}

	// What that means for whoever owns the service, in their vocabulary rather
	// than the cluster's. The line above says what Kubernetes did; this one says
	// what it tells you about your software, which is the inference a reader
	// would otherwise have to make unaided.
	if mech.Meaning != "" {
		for i, l := range wrapText("→ "+mech.Meaning, frame-label-4) {
			if i > 0 {
				l = "  " + l
			}

			fmt.Fprintln(b, gutter+r.paint(r.style.evidence, l))
		}
	}

	// Only a specific signature is quoted. A generic one restates the reason,
	// which the line above already gave, and quoting it would spend the most
	// prominent line in the report on a paraphrase.
	if s, ok := c.Diagnoses[in.Mechanism].Leading(); ok && s.Specific {
		for _, l := range wrapText(s.Text, frame-label-6) {
			fmt.Fprintln(b, gutter+r.paint(r.style.edge, glyphQuote+" ")+r.paint(r.style.signature, l))
		}
	}

	if in.Confidence != diagnose.ConfidenceExplained {
		fmt.Fprintln(b, gutter+r.paint(r.style.warning,
			"⚠ "+in.Confidence.String()+" — nothing in this capture explains what came before"))
	}

	b.WriteByte('\n')
	r.writeLabelled(b, "IMPACT", r.severityStyle(in.Severity), impact(in), r.style.header)
}

// writeLabelled writes one gutter-aligned row of the verdict block.
func (r *Renderer) writeLabelled(b *strings.Builder, tag string, tagStyle lipgloss.Style, text string, textStyle lipgloss.Style) {
	pad := label - lipgloss.Width(tag) - 2
	if pad < 0 {
		pad = 0
	}

	fmt.Fprintln(b, "  "+r.paint(tagStyle, " "+tag+" ")+strings.Repeat(" ", pad)+r.paint(textStyle, text))
}

// trigger phrases the root in the terms a reader thinks in.
//
// A table over the root's kind rather than a sentence per incident: a rollout,
// a node condition, an unreadable reason and a bare failure are four different
// openings, and there is no fifth.
func trigger(f group.Finding) string {
	switch {
	case f.Category == classify.CategoryDeployMarker:
		return "rollout of " + f.Workload

	case f.Kind == eventKindNode:
		return f.Workload + " reported " + f.Reason

	case !f.Recognised:
		return "unknown — " + f.Workload + " reported " + f.Reason

	default:
		return f.Workload + ", " + f.Reason
	}
}

// impact renders the blast radius as one scannable line.
func impact(in diagnose.Incident) string {
	parts := []string{plural(in.Events, "event")}

	if in.Pods > 0 {
		parts = append(parts, plural(in.Pods, "pod"))
	}
	if in.Workloads > 1 {
		parts = append(parts, plural(in.Workloads, "workload"))
	}
	if in.Namespaces > 1 {
		parts = append(parts, plural(in.Namespaces, "namespace"))
	}

	parts = append(parts, in.Span().Round(1e9).String())

	if in.StillFailing {
		return strings.Join(parts, " · ") + " · STILL FAILING at capture end"
	}

	return strings.Join(parts, " · ") + " · stopped before capture end"
}

// writeNode renders one finding and its children.
func (r *Renderer) writeNode(b *strings.Builder, c diagnose.Chart, in diagnose.Incident, i int, pad string, depth int) {
	f, d := c.Findings[i], c.Diagnoses[i]

	glyph := glyphFinding
	if f.Category == classify.CategoryDeployMarker {
		glyph = glyphMarker
	}

	// The stem is what makes a chain read as a chain. The root has none: it is
	// what everything below hangs from, not a child of anything.
	stem := "╰─ "
	if depth == 0 {
		stem = ""
	}

	head := fmt.Sprintf("%s%s %s  %s  %s",
		stem, glyph,
		r.paint(r.style.meta, f.FirstSeen.UTC().Format(timeLayout)),
		r.paint(r.severityStyle(f.Severity), f.Reason),
		r.paint(r.style.header, f.Workload)+r.paint(r.style.meta, " · "+f.Namespace))

	// Emptiness is decided on the unstyled text. Painting an empty string still
	// emits the escape sequences that open and close the style, so a styled ""
	// is not "" -- and testing the painted value produced a marker row padded to
	// the frame in colour and unpadded in plain, which is exactly the divergence
	// the emphasis-only contract forbids.
	scope := scopeOf(f)

	var right string
	if scope != "" {
		right = r.paint(r.style.meta, scope)
	}

	if i == r.tracedNode(c, in) {
		if right != "" {
			right += "  "
		}

		right += r.paint(r.style.tracedTag, " YOU ARE HERE ")
	} else if i == in.Paged {
		if right != "" {
			right += "  "
		}

		right += r.paint(r.style.pagedTag, " PAGED HERE ")
	}

	// No trailing run of spaces where there is nothing to right-align: a
	// captured output file is diffed and grepped, and invisible padding is noise
	// in both.
	if right == "" {
		fmt.Fprintln(b, pad+head)
	} else {
		gap := frame - lipgloss.Width(pad+head) - lipgloss.Width(right)
		if gap < 2 {
			gap = 2
		}

		fmt.Fprintln(b, pad+head+strings.Repeat(" ", gap)+right)
	}

	inner := pad + strings.Repeat(" ", lipgloss.Width(stem)) + "   "

	if d.Pattern != diagnose.PatternNone {
		line := d.Pattern.String()
		if c := rhythm(d.Cadence, f); c != "" {
			line += "  ·  " + c
		}

		fmt.Fprintln(b, inner+r.paint(r.style.pattern, line))
	}

	r.writeSignatures(b, inner, d, f.Count)

	if e := c.Edges[i]; !c.IsRoot(i) {
		style := r.style.edge
		if e.Kind == link.KindMayRelate {
			style = r.style.warning
		}

		for j, l := range wrapText(e.Evidence, frame-lipgloss.Width(inner)-4) {
			mark := r.paint(style, glyphEdge+" ")
			if j > 0 {
				mark = "  "
			}

			fmt.Fprintln(b, inner+mark+r.paint(r.style.evidence, l))
		}
	}

	kids := c.Children(i)
	if len(kids) == 0 {
		return
	}

	if c.ShareExplanation(kids) {
		r.writeCollapsed(b, c, in, i, kids, pad+strings.Repeat(" ", lipgloss.Width(stem)))
		return
	}

	nest := pad + strings.Repeat(" ", lipgloss.Width(stem))
	for _, k := range kids {
		fmt.Fprintln(b, strings.TrimRight(nest+"   ", " "))
		r.writeNode(b, c, in, k, nest+"   ", depth+1)
	}
}

// writeSignatures writes what the records themselves said.
func (r *Renderer) writeSignatures(b *strings.Builder, inner string, d diagnose.Diagnosis, total int) {
	for n, s := range d.Signatures {
		if n == maxSignatures {
			fmt.Fprintln(b, inner+"  "+r.paint(r.style.meta,
				fmt.Sprintf("… %s not shown", plural(len(d.Signatures)-maxSignatures, "further signature"))))

			return
		}

		style, mark := r.style.meta, "  "
		if s.Specific {
			style, mark = r.style.signature, r.paint(r.style.edge, glyphSpecific)+" "
		}

		for j, l := range wrapText(s.Text, frame-lipgloss.Width(inner)-11) {
			prefix := mark
			count := r.paint(r.style.meta, fmt.Sprintf("  %d/%d", s.Count, total))

			if j > 0 {
				prefix, count = "  ", ""
			}

			fmt.Fprintln(b, inner+prefix+r.paint(style, l)+count)
		}

		// What this particular body proves, where the finding's own meaning is
		// too coarse to say. Stated as evidence, not as instructions.
		if s.Means == "" {
			continue
		}

		for j, l := range wrapText("→ "+s.Means, frame-lipgloss.Width(inner)-8) {
			if j > 0 {
				l = "  " + l
			}

			fmt.Fprintln(b, inner+"  "+r.paint(r.style.evidence, l))
		}
	}
}

// rhythm renders a finding's cadence, or empty where too few intervals existed.
//
// The trend is named only when it is not steady: "steady" beside a probe that
// fires on a fixed period says nothing, where "easing" beside a retry loop says
// the gaps will keep widening until the fault is fixed.
func rhythm(c diagnose.Cadence, f group.Finding) string {
	if !c.Known() {
		return ""
	}

	out := "every ~" + roughly(c.Median)
	if u := c.Unit(f); u != "" {
		out += " " + u
	}

	switch c.Trend {
	case diagnose.TrendEasing:
		out += ", easing " + roughly(c.Early) + " → " + roughly(c.Late)

	case diagnose.TrendTightening:
		out += ", tightening " + roughly(c.Early) + " → " + roughly(c.Late)
	}

	return out
}

// roughly renders a duration at the precision it was actually measured to.
//
// Tenths below a minute, whole seconds above. "4m11.2s" implies a
// measurement two decimal places finer than a median over 22 samples supports,
// and precision nobody has is precision nobody should print.
func roughly(d time.Duration) string {
	if d < time.Minute {
		return d.Round(100 * time.Millisecond).String()
	}

	return d.Round(time.Second).String()
}

// writeCollapsed writes children that share an explanation: the explanation
// once, then one line each for what differs.
func (r *Renderer) writeCollapsed(b *strings.Builder, c diagnose.Chart, in diagnose.Incident, parent int, kids []int, pad string) {
	inner := pad + "   "

	namespaces := map[string]bool{}
	for _, k := range kids {
		namespaces[c.Findings[k].Namespace] = true
	}

	span := c.Findings[kids[len(kids)-1]].FirstSeen.Sub(c.Findings[parent].FirstSeen)
	summary := fmt.Sprintf("%s %s across %s in %s, each reporting:",
		strings.ToLower(c.Findings[kids[0]].Reason),
		plural(len(kids), "workload"),
		plural(len(namespaces), "namespace"),
		span.Round(1e8))

	fmt.Fprintln(b, inner+r.paint(r.style.edge, glyphShared+" ")+r.paint(r.style.evidence, summary))

	if s, ok := c.Diagnoses[kids[0]].Leading(); ok {
		for _, l := range wrapText(s.Text, frame-lipgloss.Width(inner)-6) {
			fmt.Fprintln(b, inner+"  "+r.paint(r.style.signature, l))
		}
	}

	fmt.Fprintln(b, "")

	for n, k := range kids {
		stem := "├─ "
		if n == len(kids)-1 {
			stem = "╰─ "
		}

		kf := c.Findings[k]

		row := fmt.Sprintf("%s%s%s  %s  %s",
			inner, stem,
			r.paint(r.style.meta, kf.FirstSeen.UTC().Format(timeLayout)),
			r.paint(r.severityStyle(kf.Severity), kf.Reason),
			r.paint(r.style.header, kf.Workload)+r.paint(r.style.meta, " · "+kf.Namespace))

		right := r.paint(r.style.meta, scopeOf(kf)+"  +"+kf.FirstSeen.Sub(c.Findings[parent].FirstSeen).Round(1e8).String())
		if k == r.tracedNode(c, in) {
			right += "  " + r.paint(r.style.tracedTag, " YOU ARE HERE ")
		} else if k == in.Paged {
			right += "  " + r.paint(r.style.pagedTag, " PAGED HERE ")
		}

		gap := frame - lipgloss.Width(row) - lipgloss.Width(right)
		if gap < 2 {
			gap = 2
		}

		fmt.Fprintln(b, row+strings.Repeat(" ", gap)+right)
	}
}

// tracedNode returns the single finding to mark as where the reader is.
//
// The latest matching member, not every match. In 03 all three findings belong
// to payment-service, and marking all three says nothing -- the useful mark is
// the symptom you were looking at when you were paged, which is the last thing
// your service did. Returns -1 when nothing is being traced.
func (r *Renderer) tracedNode(c diagnose.Chart, in diagnose.Incident) int {
	if r.trace == "" {
		return -1
	}

	found := -1

	for _, i := range in.Members {
		if matches(c.Findings[i], r.trace) {
			found = i
		}
	}

	return found
}

// matches reports whether a finding concerns the named workload or pod.
func matches(f group.Finding, name string) bool {
	if f.Workload == name {
		return true
	}

	for _, p := range f.Pods {
		if p == name {
			return true
		}
	}

	return false
}

// scopeOf renders a finding's size for a collapsed row.
func scopeOf(f group.Finding) string {
	// A rollout is one deliberate act, and "×1" beside it invites reading it as
	// a measurement of something.
	if f.Category == classify.CategoryDeployMarker {
		return ""
	}

	s := fmt.Sprintf("×%d", f.Count)

	if n := len(f.Pods); n > 0 {
		s += " · " + plural(n, "pod")
	}
	if w := f.LastSeen.Sub(f.FirstSeen); w > 0 {
		s += " · " + w.Round(1e9).String()
	}

	return s
}

// wrapText breaks s to width, preferring a space and falling back to a
// punctuation boundary.
//
// The fallback matters: an image reference has no spaces, and splitting
// "payment-service:v2.14.0-rc3" mid-token produces a line nobody can paste into
// a shell or quote into an analysis report.
func wrapText(s string, w int) []string {
	if w < 20 {
		w = 20
	}

	var out []string

	for lipgloss.Width(s) > w {
		cut := strings.LastIndex(s[:w], " ")
		if cut < w/2 {
			cut = strings.LastIndexAny(s[:w], "/:,;")
		}
		if cut < w/3 {
			cut = w
		}

		out = append(out, strings.TrimRight(s[:cut], " "))
		s = strings.TrimSpace(s[cut:])
	}

	return append(out, s)
}
