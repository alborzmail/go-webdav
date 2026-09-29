package caldav

import (
	"errors"
	"strings"
	"time"

	"github.com/alborzmail/go-recur"
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

func match(filter CompFilter, comp, parent *ical.Component) (bool, error) {
	if comp.Name != filter.Name {
		return filter.IsNotDefined, nil
	}

	if !filter.Start.IsZero() || !filter.End.IsZero() {
		match, err := matchCompTimeRange(filter.Start, filter.End, comp, parent)
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
		match, err := match(filter, child, comp)
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

func matchCompTimeRange(start, end time.Time, comp, parent *ical.Component) (bool, error) {
	// See https://datatracker.ietf.org/doc/html/rfc4791#section-9.9
	// The "start" attribute specifies the inclusive start of the time range,
	// and the "end" attribute specifies the non-inclusive end of the time range.
	// Both attributes MUST be specified as "date with UTC time" value.

	overlaps, err := instanceOverlap(start, end, comp)
	if err != nil || overlaps == nil {
		return false, err
	}
	if comp.Props.Get(ical.PropDateTimeStart) == nil {
		return overlaps(time.Time{}, time.Time{}), nil
	}

	// A component is matched by the instances it has in its series, which
	// its RECURRENCE-ID siblings take from the master (RFC 5545 section
	// 3.8.4.4).
	uid, err := comp.Props.Text(ical.PropUID)
	if err != nil {
		return false, err
	}
	series, err := (&ical.Calendar{Component: parent}).Series(uid, time.UTC)
	if err != nil {
		return unreadable(err)
	}
	to := end
	if to.IsZero() {
		to = openEnd
	}
	// Instants are whole seconds, so a second either side holds every
	// instance that the section's inclusive bounds let in.
	instances, err := series.Between(start.Add(-time.Second), to.Add(time.Second))
	if err != nil {
		return unreadable(err)
	}
	for inst := range instances {
		if series.Component(inst) == comp && overlaps(inst.Start, inst.End) {
			return true, nil
		}
	}
	return false, nil
}

// unreadable keeps an object whose rule cannot be expanded here, such as one
// in a calendar scale not counted (RFC 7529): it may recur into any range,
// and one such object must not fail the query for every other one.
func unreadable(err error) (bool, error) {
	if errors.Is(err, recur.ErrSyntax) || errors.Is(err, recur.ErrScale) {
		return true, nil
	}
	return false, err
}

// openEnd stands for a time range without end: the first instant past
// the years an iCalendar date can write.
var openEnd = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)

// instanceOverlap returns RFC 4791 section 9.9's test of whether an instance
// of comp from s to e overlaps [start, end), where a zero end is open. It
// returns nil for components the section does not cover.
func instanceOverlap(start, end time.Time, comp *ical.Component) (func(s, e time.Time) bool, error) {
	endsAfter := func(t time.Time) bool { return end.IsZero() || end.After(t) }
	endsAtOrAfter := func(t time.Time) bool { return end.IsZero() || !end.Before(t) }
	constant := func(ok bool) func(s, e time.Time) bool { return func(s, e time.Time) bool { return ok } }
	dtstart := comp.Props.Get(ical.PropDateTimeStart)

	switch comp.Name {
	case ical.CompEvent:
		return func(s, e time.Time) bool {
			if e.After(s) {
				return start.Before(e) && endsAfter(s)
			}
			return !start.After(s) && endsAfter(s)
		}, nil

	case ical.CompToDo:
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
			return func(s, e time.Time) bool {
				return !start.After(e) && (endsAfter(s) || endsAtOrAfter(e))
			}, nil
		case dtstart != nil && !due.IsZero():
			return func(s, e time.Time) bool {
				return (start.Before(e) || !start.After(s)) && (endsAfter(s) || endsAtOrAfter(e))
			}, nil
		case dtstart != nil:
			return func(s, e time.Time) bool { return !start.After(s) && endsAfter(s) }, nil
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
			return func(s, e time.Time) bool { return start.Before(e) && endsAfter(s) }, nil
		}
		return func(s, e time.Time) bool { return !start.After(s) && endsAfter(s) }, nil
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
