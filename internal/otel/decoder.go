package otel

import (
	"bufio"
	"io"

	"github.com/troglodytto/daiquiri/internal/event"
)

// Buffer sizing for the line scanner.
//
// The longest line across all six provided captures is 973 bytes, averaging
// 828. initialBufferBytes is ~65x that, so the scanner allocates once and never
// regrows. maxLineBytes bounds memory against a structurally corrupt capture
// containing no newlines at all, while still tolerating ~1000x the largest
// line observed; exceeding it surfaces as bufio.ErrTooLong through Err rather
// than as an out-of-memory kill.
const (
	initialBufferBytes = 64 * 1024
	maxLineBytes       = 1024 * 1024
)

// Stats reports what the decoder saw.
//
// Skipped is disclosed in the report rather than swallowed: a truncated or
// partially corrupt capture must not be able to masquerade as a clean one.
type Stats struct {
	// Ingested counts records successfully turned into events.
	Ingested int
	// Skipped counts records that could not be interpreted. Blank lines are not
	// counted -- they are formatting, not damage.
	Skipped int
}

// Decoder streams events from a JSONL capture, one record per line.
//
// It follows the bufio.Scanner idiom: call Next until it returns false, then
// check Err. That keeps the caller's loop flat and lets "skipped a bad line,
// kept going" be expressed without a callback or a partial-results slice.
//
// A malformed record is counted and skipped. Only a failure of the underlying
// stream ends decoding with an error, because a bad record is data we can
// survive without and report on, while a bad stream means we cannot know what
// we failed to read.
type Decoder struct {
	scanner *bufio.Scanner
	// rec is reused across every line to keep allocations flat. See
	// record.unmarshal for the clearing this makes necessary.
	rec   record
	stats Stats
	err   error
}

// New returns a Decoder reading JSONL records from r.
func New(r io.Reader) *Decoder {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, initialBufferBytes), maxLineBytes)
	return &Decoder{scanner: s}
}

// Next returns the next decoded event, or false when the stream is exhausted or
// has failed. Check Err after it returns false to tell those apart.
func (d *Decoder) Next() (event.Event, bool) {
	for d.scanner.Scan() {
		line := d.scanner.Bytes()
		if isBlank(line) {
			continue
		}
		if err := d.rec.unmarshal(line); err != nil {
			d.stats.Skipped++
			continue
		}
		ev, err := d.rec.toEvent()
		if err != nil {
			d.stats.Skipped++
			continue
		}
		d.stats.Ingested++
		return ev, true
	}
	d.err = d.scanner.Err()
	return event.Event{}, false
}

// Err returns the first stream-level error encountered, or nil at clean EOF.
// Malformed records are not errors; they are counted in Stats.
func (d *Decoder) Err() error { return d.err }

// Stats returns the running ingest counters.
func (d *Decoder) Stats() Stats { return d.stats }

// isBlank reports whether a line carries no content. Such lines are skipped
// silently rather than counted as malformed: a trailing newline is ordinary
// formatting, and counting it would make a clean capture look truncated.
func isBlank(line []byte) bool {
	for _, c := range line {
		if c != ' ' && c != '\t' && c != '\r' {
			return false
		}
	}
	return true
}
