package triage

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
)

// Finding is one coalesced group: every raw Event sharing the same
// (ObjectKind, ObjectName, Namespace, Reason) tuple, folded into a single
// reportable unit.
type Finding struct {
	Key        string // Event.GroupKey() of the members
	ObjectKind string
	ObjectName string
	Namespace  string
	Reason     string

	NodeNames map[string]bool // usually one node, but a Pod's events can span
	// a reschedule onto a different node — don't
	// assume singular

	FirstSeen  time.Time
	LastSeen   time.Time
	TotalCount int           // sum of Event.Count across all members, not len(members)
	Members    []event.Event // sorted ascending by Timestamp — the "vine"

	Classification classify.Classification // computed once per group, not per event
}

func Group(events []event.Event) {
	byKey := make(map[string]*Finding)

	for _, e := range events {
		key := e.GroupKey()
		f, ok := byKey[key]
		if !ok {
			f = &Finding{
				Key: key, ObjectKind: e.Object.Kind, ObjectName: e.Object.Name,
				Namespace: e.Namespace, Reason: e.Reason,
				NodeNames: make(map[string]bool),
				FirstSeen: e.Timestamp, LastSeen: e.Timestamp,
			}
			byKey[key] = f
		}

		if e.Timestamp.Before(f.FirstSeen) {
			f.FirstSeen = e.Timestamp
		}
		if e.Timestamp.After(f.LastSeen) {
			f.LastSeen = e.Timestamp
		}
		f.TotalCount += e.Count
		f.Members = append(f.Members, e)
		if e.Node != "" {
			f.NodeNames[e.Node] = true
		}
	}

	// Indent with 2 or 4 spaces
	b, _ := json.MarshalIndent(byKey, "", "  ")
	fmt.Println(string(b))
}
