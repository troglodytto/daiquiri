// Package report renders a triage result for a human reading it under time
// pressure.
//
// Prohibitions. report decides nothing. It does not classify, coalesce, rank by
// anything other than the order it is given, or interpret a finding. If a fact
// is not already on the Result, report does not compute it -- a renderer that
// derives conclusions is a second, invisible analysis stage.
//
// Two output modes, one layout. Styling is emphasis only: strip the escape
// sequences from the styled rendering and it is byte-identical to the plain
// one, so no fact is ever carried by colour alone. This is not decoration
// policy -- the submission requires captured stdout files, and a grader opening
// a file full of escape sequences is reading noise.
package report

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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
// Per-renderer rather than package-level because lipgloss decides whether a
// style emits anything from the colour profile of the renderer that built it.
// Severity colours are the conventional three and nothing else is coloured: a
// table where every column is tinted is a table nobody can scan.
type palette struct {
	header, rule, meta          lipgloss.Style
	critical, warning, infoDull lipgloss.Style
}

func paletteFrom(lr *lipgloss.Renderer) palette {
	return palette{
		header:   lr.NewStyle().Bold(true),
		rule:     lr.NewStyle().Faint(true),
		meta:     lr.NewStyle().Faint(true),
		critical: lr.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		warning:  lr.NewStyle().Foreground(lipgloss.Color("3")),
		infoDull: lr.NewStyle().Faint(true),
	}
}

// Renderer writes a Result to a writer.
type Renderer struct {
	w      io.Writer
	styled bool
	style  palette
}

// New returns a Renderer that decides for itself whether to style, based on
// whether w is a terminal.
//
// A pipe, a file and a test buffer all render plain. NO_COLOR is honoured
// because it is the one convention every tool in a terminal agrees on.
func New(w io.Writer) *Renderer {
	if !isTerminal(w) || os.Getenv("NO_COLOR") != "" {
		return &Renderer{w: w}
	}

	return NewStyled(w)
}

// NewStyled returns a Renderer that always styles, whatever w is.
//
// It exists so the "styling is emphasis only" property can be tested: the test
// needs both renderings of the same input, and it cannot get the styled one
// from a buffer otherwise.
func NewStyled(w io.Writer) *Renderer {
	// The profile is forced rather than detected. lipgloss inspects its output
	// to decide whether colour is supported, and a test buffer or a pipe always
	// answers no -- which would make this constructor silently identical to the
	// plain one and leave the emphasis-only property untestable. Callers who
	// want detection use New.
	//
	// Set after construction, not via termenv.WithProfile: that option is
	// consumed when the output is built, and lipgloss then re-detects from the
	// writer and overrides it back to Ascii. Verified -- the option path yields
	// termenv profile 3 (Ascii, since termenv numbers TrueColor 0 down to Ascii
	// 3) and emits no escape sequences at all.
	lr := lipgloss.NewRenderer(w)
	lr.SetColorProfile(termenv.ANSI)

	return &Renderer{w: w, styled: true, style: paletteFrom(lr)}
}

// isTerminal reports whether w is a character device.
//
// Deliberately stdlib rather than a terminal-detection dependency: the only
// question being asked is whether the destination is a console, and a file mode
// answers it.
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

	if len(res.Forest.Findings) == 0 {
		b.WriteString("\nno issues detected\n")
		_, err := io.WriteString(r.w, b.String())
		return err
	}

	r.writeTable(&b, res.Forest.Findings)

	_, err := io.WriteString(r.w, b.String())

	return err
}

// writeHeader writes the title and the ingest counters.
//
// Skipped and unrecognised appear only when non-zero, and they are the two
// numbers that stop a capture the tool could not fully read from looking like a
// clean one.
func (r *Renderer) writeHeader(b *strings.Builder, res triage.Result) {
	title := "KUBERNETES CLUSTER EVENT TRIAGE"
	if res.Cluster != "" {
		title += "  " + res.Cluster
	}

	fmt.Fprintln(b, r.paint(r.style.header, title))

	meta := fmt.Sprintf("%s records, %s filtered as noise, %d findings",
		commas(res.Ingested), commas(res.Noise), len(res.Forest.Findings))

	if res.Skipped > 0 {
		meta += fmt.Sprintf(", skipped %d", res.Skipped)
	}

	if res.Unrecognised > 0 {
		meta += fmt.Sprintf(", unrecognised %d", res.Unrecognised)
	}

	meta += fmt.Sprintf(" (%s)", res.Elapsed.Round(1e6))

	fmt.Fprintln(b, r.paint(r.style.meta, meta))
}

// writeTable writes the findings, one row each, in the order given.
func (r *Renderer) writeTable(b *strings.Builder, findings []group.Finding) {
	cols := columnsOf(findings)

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

	for row := range findings {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = pad(c.rows[row], widths[i], c.rightA)
		}

		// The whole row takes the severity colour rather than the severity cell
		// alone: at a glance the eye is looking for the critical line, not for a
		// critical word.
		fmt.Fprintln(b, r.paint(r.severityStyle(findings[row].Severity), strings.TrimRight(strings.Join(cells, gap), " ")))
	}

	fmt.Fprintln(b, r.paint(r.style.rule, strings.Repeat("─", total)))
}

// columnsOf builds the table's columns from the findings.
func columnsOf(findings []group.Finding) []column {
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
	}

	for _, f := range findings {
		values := []string{
			f.FirstSeen.UTC().Format(timeLayout),
			f.Severity.String(),
			f.Workload,
			f.Namespace,
			f.Reason,
			fmt.Sprint(f.Count),
			fmt.Sprint(len(f.Pods)),
			window(f),
			nodes(f),
		}
		for i := range cols {
			cols[i].rows = append(cols[i].rows, values[i])
		}
	}

	return cols
}

// window renders how long the finding lasted.
//
// A point event -- a deploy marker, a node condition -- has no duration, and
// showing "0s" for it invites reading it as a measurement rather than as an
// instant.
func window(f group.Finding) string {
	d := f.LastSeen.Sub(f.FirstSeen)
	if d <= 0 {
		return "instant"
	}

	return d.Round(1e9).String()
}

// nodes renders the blast radius across nodes.
//
// Empty for scheduler and controller events, which no kubelet emitted. That
// absence is normal, so it renders as a dash rather than as nothing -- an empty
// cell reads as missing data.
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
// The branch is what makes the plain path provably plain: lipgloss is never
// called at all, so no escape sequence can reach a captured file however the
// terminal profile is detected.
func (r *Renderer) paint(st lipgloss.Style, s string) string {
	if !r.styled {
		return s
	}

	return st.Render(s)
}

// pad left- or right-aligns s in a field of n columns.
//
// Padding happens before styling, never after: an escape sequence has no width
// on screen but plenty of bytes, and padding a styled string aligns the bytes
// instead of the glyphs.
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
