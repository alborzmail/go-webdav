package caldav

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/emersion/go-webdav"
)

// TestClientCalendarCollections verifies a calendar made, listed and changed
// through the client arrives at the server's backend as it was given.
func TestClientCalendarCollections(t *testing.T) {
	backend := &testBackend{}
	srv := httptest.NewServer(&Handler{Backend: backend})
	defer srv.Close()
	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx := context.Background()

	note := webdav.DeadProperty{
		Name: xml.Name{Space: "urn:example", Local: "note"},
		XML:  []byte(`<note xmlns="urn:example">kept</note>`),
	}
	want := Calendar{
		Path:                  "/user/calendars/work/",
		Name:                  "Work & play",
		Description:           "Meetings",
		Color:                 "#FF2968",
		SupportedComponentSet: []string{"VEVENT", "VTODO"},
		DeadProperties:        []webdav.DeadProperty{note},
	}
	if err := c.CreateCalendar(ctx, &want); err != nil {
		t.Fatalf("CreateCalendar: %v", err)
	}
	if len(backend.calendars) != 1 {
		t.Fatalf("backend has %d calendars, want 1", len(backend.calendars))
	}
	got := backend.calendars[0]
	if !reflect.DeepEqual(got, want) {
		t.Errorf("created %+v, want %+v", got, want)
	}

	backend.calendars[0].ReadOnly = true
	backend.calendars[0].CTag = "7"
	cals, err := c.FindCalendars(ctx, "/user/calendars/")
	if err != nil {
		t.Fatalf("FindCalendars: %v", err)
	}
	if len(cals) != 1 {
		t.Fatalf("listed %d calendars, want 1", len(cals))
	}
	if cal := cals[0]; cal.Name != want.Name || cal.Color != want.Color || !cal.ReadOnly || cal.CTag != "7" ||
		!reflect.DeepEqual(cal.SupportedComponentSet, want.SupportedComponentSet) {
		t.Errorf("listed %+v", cal)
	}

	name, color := "Renamed", ""
	if err := c.UpdateCalendar(ctx, want.Path, &CalendarUpdate{Name: &name, Color: &color}); err != nil {
		t.Fatalf("UpdateCalendar: %v", err)
	}
	update := backend.updates[want.Path]
	if update == nil || update.Name == nil || *update.Name != name || update.Color == nil || *update.Color != "" || update.Description != nil {
		t.Errorf("update %+v, want a rename and the color removed", update)
	}
}

// TestUpdateCalendarRefused verifies a PROPPATCH the server refuses in its
// multistatus fails with the refusal's status code.
func TestUpdateCalendarRefused(t *testing.T) {
	backend := &testBackend{calendars: []Calendar{{Path: "/user/calendars/work/"}}}
	srv := httptest.NewServer(&Handler{Backend: backend})
	defer srv.Close()
	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	protected := webdav.DeadProperty{
		Name: xml.Name{Space: "DAV:", Local: "getetag"},
		XML:  []byte(`<getetag xmlns="DAV:">"1"</getetag>`),
	}
	err = c.UpdateCalendar(context.Background(), "/user/calendars/work/", &CalendarUpdate{DeadProperties: []webdav.DeadProperty{protected}})
	if code, ok := webdav.HTTPErrorCode(err); !ok || code != http.StatusForbidden {
		t.Errorf("UpdateCalendar = %v, want a 403", err)
	}
	if len(backend.updates) != 0 {
		t.Errorf("backend updated: %v", backend.updates)
	}
}
