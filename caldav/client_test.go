package caldav

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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
	backend.calendars[0].SupportedRScaleSet = []string{"GREGORIAN", "PERSIAN"}
	cals, err := c.FindCalendars(ctx, "/user/calendars/")
	if err != nil {
		t.Fatalf("FindCalendars: %v", err)
	}
	if len(cals) != 1 {
		t.Fatalf("listed %d calendars, want 1", len(cals))
	}
	if cal := cals[0]; cal.Name != want.Name || cal.Color != want.Color || !cal.ReadOnly || cal.CTag != "7" ||
		!reflect.DeepEqual(cal.SupportedComponentSet, want.SupportedComponentSet) ||
		!reflect.DeepEqual(cal.SupportedRScaleSet, []string{"GREGORIAN", "PERSIAN"}) {
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

// TestSyncCollection verifies a sync hands over objects as the server sent
// them, deletions, and the token a truncated answer continues from.
func TestSyncCollection(t *testing.T) {
	const data = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//t//EN\r\nEND:VCALENDAR\r\n"
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:response><D:href>/cal/a.ics</D:href><D:propstat><D:prop><D:getetag>W/"a1"</D:getetag>
  <C:calendar-data>BEGIN:VCALENDAR&#13;
VERSION:2.0&#13;
PRODID:-//t//t//EN&#13;
END:VCALENDAR&#13;
</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>
 <D:response><D:href>/cal/b.ics</D:href><D:status>HTTP/1.1 404 Not Found</D:status></D:response>
 <D:response><D:href>/cal/</D:href><D:status>HTTP/1.1 507 Insufficient Storage</D:status></D:response>
 <D:sync-token>t2</D:sync-token>
</D:multistatus>`)
	}))
	defer srv.Close()

	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got, err := c.SyncCollection(context.Background(), "/cal/", &SyncQuery{SyncToken: "t1"})
	if err != nil {
		t.Fatalf("SyncCollection: %v", err)
	}
	if !strings.Contains(body, "<sync-token>t1</sync-token>") {
		t.Errorf("request did not carry the token:\n%s", body)
	}
	if got.SyncToken != "t2" || !got.Truncated || !reflect.DeepEqual(got.Deleted, []string{"/cal/b.ics"}) {
		t.Errorf("sync = %+v, want token t2, truncated, /cal/b.ics deleted", got)
	}
	if len(got.Updated) != 1 {
		t.Fatalf("updated %d objects, want 1", len(got.Updated))
	}
	if o := got.Updated[0]; o.Path != "/cal/a.ics" || o.ETag != `W/"a1"` || string(o.Raw) != data || o.Data == nil {
		t.Errorf("updated %+v", o)
	}
}
