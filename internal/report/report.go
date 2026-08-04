// Package report renders a triage result for a human reading it under time
// pressure.
package report

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/troglodytto/daiquiri/internal/diagnose"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/triage"
)

// Column layout.
//
// gap is the run of spaces between columns: two is enough to separate them
// without the eye losing the row on a wide terminal. timeLayout drops the date
// because every record in a capture shares one, and milliseconds are kept
// because 05-test-b distinguishes evictions that are seconds apart.
const (
	gap        = "  "
	timeLayout = "15:04:05.000"
)

// palette holds the styles for one renderer.
//
// Per-renderer because lipgloss decides whether a
// style emits anything from the colour profile of the renderer that built it.
// Severity colours are the conventional three and nothing else is coloured: a
// table where every column is tinted is a table nobody can scan.
type palette struct {
	header, rule, meta          lipgloss.Style
	critical, warning, infoDull lipgloss.Style
	healthy                     lipgloss.Style

	// Incident-view styles. The tree carries more kinds of text than the table
	// does; a diagnosis, a quoted record, prose evidence, two badges; and
	// they need to be distinguishable without any of them shouting.
	pattern, signature, evidence, edge lipgloss.Style
	rootTag, pagedTag, tracedTag       lipgloss.Style
}

func paletteFrom(lr *lipgloss.Renderer) palette {
	return palette{
		header:   lr.NewStyle().Bold(true),
		rule:     lr.NewStyle().Faint(true),
		meta:     lr.NewStyle().Faint(true),
		critical: lr.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		warning:  lr.NewStyle().Foreground(lipgloss.Color("3")),
		infoDull: lr.NewStyle().Faint(true),

		// The fourth colour, and the only one that is not a severity. It appears
		// on exactly one line, which never coexists with a table; so the "three
		// conventional colours" rule the palette follows is not weakened by it.
		healthy: lr.NewStyle().Bold(true).Foreground(lipgloss.Color("2")),

		// The diagnosis is italic: it is a label, and the
		// row's colour is already spent on severity.
		pattern: lr.NewStyle().Italic(true).Foreground(lipgloss.Color("5")),

		// A quoted record body is the most literal thing on screen, so it is the
		// least decorated; plain, at full brightness.
		signature: lr.NewStyle().Foreground(lipgloss.Color("7")),

		// Our prose about the data, as opposed to the data. Dimmed and italic so
		// the eye can tell an assertion from a quotation at a glance.
		evidence: lr.NewStyle().Faint(true).Italic(true),

		// Structure glyphs only, never text.
		edge: lr.NewStyle().Foreground(lipgloss.Color("6")),

		// The two badges are inverted: they are the
		// only things in the report a reader should be able to find without
		// reading, and inversion survives a colour scheme that mangles hues.
		rootTag:  lr.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3")),
		pagedTag: lr.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("1")),

		// The traced workload is marked in a third colour, because it answers a
		// different question from the other two: not "what broke" or "what
		// paged", but "where am I in this".
		tracedTag: lr.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")),
	}
}

// Renderer writes a Result to a writer.
type Renderer struct {
	w      io.Writer
	styled bool
	style  palette

	// view selects which of the two renderings to produce.
	view View

	// trace narrows the incident view to a named workload and marks it.
	trace string
}

// Trace narrows the report to incidents involving one workload, and marks it
// wherever it appears.
//
// This is the 3am path: you are paged for one service, and what you need is not
// every incident in the cluster but the trail from your symptom back to its
// origin; which in 05-test-b runs from an evicted pod to a node that filled
// its disk, through nothing your service did.
func (r *Renderer) Trace(workload string) *Renderer {
	r.trace = workload

	// Tracing implies the incident view. The table has no notion of a trail, so
	// narrowing it to one workload would just hide rows; and a caller who
	// asked to follow a thread wants the thread.
	r.view = ViewTree

	return r
}

// View selects what the renderer produces.
type View uint8

// Views. The zero value renders both, so a Renderer built with New is complete.
const (
	ViewBoth View = iota
	ViewTable
	ViewTree
)

// Only narrows the renderer to a single view.
func (r *Renderer) Only(v View) *Renderer {
	r.view = v

	return r
}

// shows reports whether v is part of what this renderer produces.
func (r *Renderer) shows(v View) bool {
	return r.view == ViewBoth || r.view == v
}

// New returns a Renderer that decides for itself whether to style, based on
// whether w is a terminal.
func New(w io.Writer) *Renderer {
	if !isTerminal(w) || os.Getenv("NO_COLOR") != "" {
		return &Renderer{w: w}
	}

	return NewStyled(w)
}

// NewStyled returns a Renderer that always styles, whatever w is.
func NewStyled(w io.Writer) *Renderer {
	// The profile is forced. lipgloss inspects its output
	// to decide whether colour is supported, and a test buffer or a pipe always
	// answers no; which would make this constructor silently identical to the
	// plain one and leave the emphasis-only property untestable. Callers who
	// want detection use New.
	//
	// Set after construction, not via termenv.WithProfile: that option is
	// consumed when the output is built, and lipgloss then re-detects from the
	// writer and overrides it back to Ascii. Verified; the option path yields
	// termenv profile 3 (Ascii, since termenv numbers TrueColor 0 down to Ascii
	// 3) and emits no escape sequences at all.
	lr := lipgloss.NewRenderer(w)
	lr.SetColorProfile(termenv.ANSI)

	return &Renderer{w: w, styled: true, style: paletteFrom(lr)}
}

// isTerminal reports whether w is a character device.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// column is one table column: a heading, and the values beneath it.
type column struct {
	head   string
	rows   []string
	rightA bool
}

// width returns the widest cell, heading included.
func (c column) width() int {
	w := lipgloss.Width(c.head)
	for _, r := range c.rows {
		if n := lipgloss.Width(r); n > w {
			w = n
		}
	}

	return w
}

// Render writes the full report.
func (r *Renderer) Render(res triage.Result) error {
	var b strings.Builder

	r.writeHeader(&b, res)

	reported := res.Chart.Reported()
	if len(reported) == 0 {
		r.writeAllClear(&b, res)
		_, err := io.WriteString(r.w, b.String())

		return err
	}

	if r.shows(ViewTable) {
		r.writeTable(&b, res.Chart, reported)
	}

	if r.shows(ViewTree) {
		r.writeIncidents(&b, res.Chart)
	}

	_, err := io.WriteString(r.w, b.String())

	return err
}

// writeHeader writes the title and the ingest counters.
func (r *Renderer) writeHeader(b *strings.Builder, res triage.Result) {
	title := "KUBERNETES CLUSTER EVENT TRIAGE"
	if res.Cluster != "" {
		title += "  " + res.Cluster
	}

	fmt.Fprintln(b, r.paint(r.style.header, title))

	meta := fmt.Sprintf("%s records, %s filtered as noise, %d findings",
		commas(res.Ingested), commas(res.Noise), len(res.Chart.Reported()))

	// Disclosed for the same reason as the two counters below it: a number the
	// reader can ask about beats a fact that silently left.
	if n := res.Chart.SuppressedCount(); n > 0 {
		meta += fmt.Sprintf(", %d suppressed as background", n)
	}

	if res.Skipped > 0 {
		meta += fmt.Sprintf(", skipped %d", res.Skipped)
	}

	if res.Unrecognised > 0 {
		meta += fmt.Sprintf(", unrecognised %d", res.Unrecognised)
	}

	meta += fmt.Sprintf(" (%s)", res.Elapsed.Round(1e6))

	fmt.Fprintln(b, r.paint(r.style.meta, meta))
}

// writeAllClear writes the healthy-cluster result.
func (r *Renderer) writeAllClear(b *strings.Builder, res triage.Result) {
	// The brief names this string outright; "your tool should output 'no issues
	// detected' and exit 0"; so it is present verbatim rather than paraphrased,
	// and the emphasis is built around it instead of replacing it.
	fmt.Fprintf(b, "\n%s\n\n", r.paint(r.style.healthy, "✓  ALL CLEAR — no issues detected"))
	fmt.Fprintln(b, "   Nothing here needs an on-call response.")

	if n := res.Chart.SuppressedCount(); n > 0 {
		fmt.Fprintf(b, "   %s held back as background -- each explained by nothing,\n   and explaining nothing.\n",
			plural(n, "transient blip"))

		return
	}

	fmt.Fprintln(b, "   Nothing was held back.")
}

// plural renders a count with its noun, so a line reads as a sentence rather
// than as a counter.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}

// eventKindNode is event.KindNode, aliased so tree.go can ask what kind a
// finding is without this package importing the event model for one constant.
const eventKindNode = event.KindNode

// writeTable writes the reported findings, one row each, in the order given.
func (r *Renderer) writeTable(b *strings.Builder, c diagnose.Chart, reported []int) {
	cols := columnsOf(c, reported)

	widths := make([]int, len(cols))
	total := 0

	for i, c := range cols {
		widths[i] = c.width()
		total += widths[i]
	}

	total += len(gap) * (len(cols) - 1)

	b.WriteByte('\n')

	heads := make([]string, len(cols))
	for i, c := range cols {
		heads[i] = pad(c.head, widths[i], c.rightA)
	}

	fmt.Fprintln(b, r.paint(r.style.header, strings.TrimRight(strings.Join(heads, gap), " ")))
	fmt.Fprintln(b, r.paint(r.style.rule, strings.Repeat("─", total)))

	for row, at := range reported {
		cells := make([]string, len(cols))
		for i, col := range cols {
			cells[i] = pad(col.rows[row], widths[i], col.rightA)
		}

		// The whole row takes the severity colour, because at a glance the eye is looking for the critical line, not for a
		// critical word.
		fmt.Fprintln(b, r.paint(r.severityStyle(c.Findings[at].Severity), strings.TrimRight(strings.Join(cells, gap), " ")))
	}

	fmt.Fprintln(b, r.paint(r.style.rule, strings.Repeat("─", total)))
}

// columnsOf builds the table's columns from the reported findings.
func columnsOf(c diagnose.Chart, reported []int) []column {
	cols := []column{
		{head: "TIME"},
		{head: "SEVERITY"},
		{head: "WORKLOAD"},
		{head: "NAMESPACE"},
		{head: "REASON"},
		{head: "COUNT", rightA: true},
		{head: "PODS", rightA: true},
		{head: "WINDOW"},
		{head: "NODES"},
		{head: "PATTERN"},
	}

	for _, at := range reported {
		f := c.Findings[at]
		values := []string{
			f.FirstSeen.UTC().Format(timeLayout),
			f.Severity.String(),
			f.Workload,
			f.Namespace,
			f.Reason,
			fmt.Sprint(f.Count),
			scope(f),
			window(f),
			nodes(f),
			pattern(c.Diagnoses[at]),
		}
		for i := range cols {
			cols[i].rows = append(cols[i].rows, values[i])
		}
	}

	return cols
}

// pattern renders the diagnosis, or a dash where naming one would be a guess.
func pattern(d diagnose.Diagnosis) string {
	if d.Pattern == diagnose.PatternNone {
		return "-"
	}

	return d.Pattern.String()
}

// window renders how long the finding lasted.
//
// A point event; a deploy marker, a node condition; has no duration, and
// showing "0s" for it invites reading it as a measurement.
func window(f group.Finding) string {
	d := f.LastSeen.Sub(f.FirstSeen)
	if d <= 0 {
		return "instant"
	}

	return d.Round(1e9).String()
}

// scope renders how many pod instances the finding spans.
func scope(f group.Finding) string {
	if len(f.Pods) == 0 {
		return "-"
	}

	return fmt.Sprint(len(f.Pods))
}

// nodes renders the blast radius across nodes.
func nodes(f group.Finding) string {
	if len(f.Nodes) == 0 {
		return "-"
	}

	return strings.Join(f.Nodes, ",")
}

// severityStyle maps a finding's severity onto its row colour.
func (r *Renderer) severityStyle(s event.Severity) lipgloss.Style {
	switch s {
	case event.SeverityCritical:
		return r.style.critical

	case event.SeverityWarning:
		return r.style.warning

	default:
		return r.style.infoDull
	}
}

// paint applies a style, or returns the string untouched when not styling.
//
// The branch keeps the plain path provably plain. lipgloss is never
// called at all, so no escape sequence can reach a captured file however the
// terminal profile is detected.
func (r *Renderer) paint(st lipgloss.Style, s string) string {
	if !r.styled {
		return s
	}

	return st.Render(s)
}

// pad left- or right-aligns s in a field of n columns.
func pad(s string, n int, right bool) string {
	fill := n - lipgloss.Width(s)
	if fill <= 0 {
		return s
	}

	if right {
		return strings.Repeat(" ", fill) + s
	}

	return s + strings.Repeat(" ", fill)
}

// commas groups an integer in threes, so a reader can tell 20,000 from 2,000 at
// a glance instead of counting digits.
func commas(n int) string {
	s := fmt.Sprint(n)

	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}

	return b.String()
}
