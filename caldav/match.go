package caldav

import (
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// Filter returns the filtered list of calendar objects matching the provided query.
// A nil query will return the full list of calendar objects.
func Filter(query *CalendarQuery, cos []CalendarObject) ([]CalendarObject, error) {
	if query == nil {
		// FIXME: should we always return a copy of the provided slice?
		return cos, nil
	}

	var out []CalendarObject
	for _, co := range cos {
		ok, err := Match(query.CompFilter, &co)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		// TODO properties are not currently filtered even if requested
		out = append(out, co)
	}
	return out, nil
}

// Match reports whether the provided CalendarObject matches the query.
func Match(query CompFilter, co *CalendarObject) (matched bool, err error) {
	if co.Data == nil || co.Data.Component == nil {
		panic("request to process empty calendar object")
	}
	return match(query, co.Data.Component, nil)
}

func match(filter CompFilter, comp *ical.Component, siblings []*ical.Component) (bool, error) {
	if comp.Name != filter.Name {
		return filter.IsNotDefined, nil
	}

	if !filter.Start.IsZero() || !filter.End.IsZero() {
		match, err := matchCompTimeRange(filter.Start, filter.End, comp, siblings)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	for _, compFilter := range filter.Comps {
		match, err := matchCompFilter(compFilter, comp)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	for _, propFilter := range filter.Props {
		match, err := matchPropFilter(propFilter, comp)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}

func matchCompFilter(filter CompFilter, comp *ical.Component) (bool, error) {
	var matches []*ical.Component
	for _, child := range comp.Children {
		match, err := match(filter, child, comp.Children)
		if err != nil {
			return false, err
		} else if match {
			matches = append(matches, child)
		}
	}
	if len(matches) == 0 {
		return filter.IsNotDefined, nil
	}
	return true, nil
}

func matchPropFilter(filter PropFilter, comp *ical.Component) (bool, error) {
	// TODO: this only matches first field, there can be multiple
	field := comp.Props.Get(filter.Name)
	if field == nil {
		return filter.IsNotDefined, nil
	}

	for _, paramFilter := range filter.ParamFilter {
		if !matchParamFilter(paramFilter, field) {
			return false, nil
		}
	}

	var zeroDate time.Time
	if filter.Start != zeroDate {
		match, err := matchPropTimeRange(filter.Start, filter.End, field)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	} else if filter.TextMatch != nil {
		if !matchTextMatch(*filter.TextMatch, field.Value) {
			return false, nil
		}
		return true, nil
	}
	// empty prop-filter, property exists
	return true, nil
}

func matchCompTimeRange(start, end time.Time, comp *ical.Component, siblings []*ical.Component) (bool, error) {
	// See https://datatracker.ietf.org/doc/html/rfc4791#section-9.9
	// The "start" attribute specifies the inclusive start of the time range,
	// and the "end" attribute specifies the non-inclusive end of the time range.
	// Both attributes MUST be specified as "date with UTC time" value.

	// Siblings with a RECURRENCE-ID replace instances of the master's rule;
	// one with RANGE=THISANDFUTURE replaces that instance and every later
	// one with its own, shifted by as much as it moves its own instance
	// (RFC 5545 section 3.8.4.4).
	var master *ical.Component
	var moved, futures []time.Time
	var from time.Time
	for _, sibling := range siblings {
		if sibling.Name != comp.Name {
			continue
		}
		prop := sibling.Props.Get(ical.PropRecurrenceID)
		if prop == nil {
			master = sibling
			continue
		}
		rid, err := prop.DateTime(time.UTC)
		if err != nil {
			return false, err
		}
		moved = append(moved, rid)
		if prop.Params.Get(ical.ParamRange) == "THISANDFUTURE" {
			futures = append(futures, rid)
			if sibling == comp {
				from = rid
			}
		}
	}
	rule, shift := comp, time.Duration(0)
	if !from.IsZero() && master != nil {
		dtstart, err := comp.Props.DateTime(ical.PropDateTimeStart, time.UTC)
		if err != nil {
			return false, err
		}
		rule, shift = master, dtstart.Sub(from)
	}
	var until time.Time
	for _, rid := range futures {
		if rid.After(from) && (until.IsZero() || rid.Before(until)) {
			until = rid
		}
	}

	rset, err := rule.RecurrenceSet(time.UTC)
	if err != nil {
		// A rule that cannot be expanded here, such as one counted in another
		// calendar (RFC 7529), may recur into any range. Keep the object for
		// the client rather than fail the query for every other one.
		return true, nil
	}
	overlaps, err := instanceOverlap(start, end, comp)
	if err != nil || overlaps == nil {
		return false, err
	}
	if rset == nil {
		dtstart, err := comp.Props.DateTime(ical.PropDateTimeStart, time.UTC)
		if err != nil {
			return false, err
		}
		return overlaps(dtstart), nil
	}

	for _, rid := range moved {
		if !rid.Equal(from) {
			rset.ExDate(rid)
		}
	}
	next := rset.Iterator()
	for t, ok := next(); ok && (until.IsZero() || t.Before(until)); t, ok = next() {
		if t.Before(from) {
			continue
		}
		s := t.Add(shift)
		// No instance starting after end can overlap the range.
		if !end.IsZero() && s.After(end) {
			break
		}
		if overlaps(s) {
			return true, nil
		}
	}
	return false, nil
}

// oneDay is the +P1D that RFC 4791 section 9.9 gives a DATE value.
const oneDay = 24 * time.Hour

// instanceOverlap returns RFC 4791 section 9.9's test of whether an instance
// of comp starting at the given time overlaps [start, end), where a zero end
// is open. It returns nil for components the section does not cover.
func instanceOverlap(start, end time.Time, comp *ical.Component) (func(time.Time) bool, error) {
	endsAfter := func(t time.Time) bool { return end.IsZero() || end.After(t) }
	endsAtOrAfter := func(t time.Time) bool { return end.IsZero() || !end.Before(t) }
	constant := func(ok bool) func(time.Time) bool { return func(time.Time) bool { return ok } }
	dtstart := comp.Props.Get(ical.PropDateTimeStart)

	switch comp.Name {
	case ical.CompEvent:
		event := ical.Event{Component: comp}
		eventStart, err := event.DateTimeStart(time.UTC)
		if err != nil {
			return nil, err
		}
		eventEnd, err := event.DateTimeEnd(time.UTC)
		if err != nil {
			return nil, err
		}
		if d := eventEnd.Sub(eventStart); d > 0 {
			return func(s time.Time) bool { return start.Before(s.Add(d)) && endsAfter(s) }, nil
		}
		return func(s time.Time) bool { return !start.After(s) && endsAfter(s) }, nil

	case ical.CompToDo:
		todoStart, err := comp.Props.DateTime(ical.PropDateTimeStart, time.UTC)
		if err != nil {
			return nil, err
		}
		due, err := comp.Props.DateTime(ical.PropDue, time.UTC)
		if err != nil {
			return nil, err
		}
		completed, err := comp.Props.DateTime(ical.PropCompleted, time.UTC)
		if err != nil {
			return nil, err
		}
		created, err := comp.Props.DateTime(ical.PropCreated, time.UTC)
		if err != nil {
			return nil, err
		}
		duration := comp.Props.Get(ical.PropDuration)

		switch {
		case dtstart != nil && duration != nil:
			d, err := duration.Duration()
			if err != nil {
				return nil, err
			}
			return func(s time.Time) bool {
				e := s.Add(d)
				return !start.After(e) && (endsAfter(s) || endsAtOrAfter(e))
			}, nil
		case dtstart != nil && !due.IsZero():
			d := due.Sub(todoStart)
			return func(s time.Time) bool {
				e := s.Add(d)
				return (start.Before(e) || !start.After(s)) && (endsAfter(s) || endsAtOrAfter(e))
			}, nil
		case dtstart != nil:
			return func(s time.Time) bool { return !start.After(s) && endsAfter(s) }, nil
		case !due.IsZero():
			return constant(start.Before(due) && endsAtOrAfter(due)), nil
		case !completed.IsZero() && !created.IsZero():
			return constant((!start.After(created) || !start.After(completed)) &&
				(endsAtOrAfter(created) || endsAtOrAfter(completed))), nil
		case !completed.IsZero():
			return constant(!start.After(completed) && endsAtOrAfter(completed)), nil
		case !created.IsZero():
			return constant(endsAfter(created)), nil
		}
		return constant(true), nil

	case ical.CompJournal:
		switch {
		case dtstart == nil:
			return constant(false), nil
		case dtstart.ValueType() == ical.ValueDate:
			return func(s time.Time) bool { return start.Before(s.Add(oneDay)) && endsAfter(s) }, nil
		}
		return func(s time.Time) bool { return !start.After(s) && endsAfter(s) }, nil
	}
	return nil, nil
}

func matchPropTimeRange(start, end time.Time, field *ical.Prop) (bool, error) {
	// See https://datatracker.ietf.org/doc/html/rfc4791#section-9.9

	ptime, err := field.DateTime(start.Location())
	if err != nil {
		return false, err
	}
	if ptime.After(start) && (end.IsZero() || ptime.Before(end)) {
		return true, nil
	}
	return false, nil
}

func matchParamFilter(filter ParamFilter, field *ical.Prop) bool {
	// TODO there can be multiple values
	value := field.Params.Get(filter.Name)
	if value == "" {
		return filter.IsNotDefined
	} else if filter.IsNotDefined {
		return false
	}
	if filter.TextMatch != nil {
		return matchTextMatch(*filter.TextMatch, value)
	}
	return true
}

func matchTextMatch(txt TextMatch, value string) bool {
	// TODO: handle text-match collation attribute
	match := strings.Contains(value, txt.Text)
	if txt.NegateCondition {
		match = !match
	}
	return match
}
