package caldav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
)

// minimalCalendar builds a single-VEVENT VCALENDAR valid enough to encode.
func minimalCalendar() *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//go-webdav//test//EN")
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, "test-uid")
	ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC))
	cal.Children = append(cal.Children, ev.Component)
	return cal
}

// TestPutCalendarObjectSendsIfMatch verifies the client forwards the
// PutCalendarObjectOptions.IfMatch ETag as an If-Match request header and
// returns the server's new ETag.
func TestPutCalendarObjectSendsIfMatch(t *testing.T) {
	var gotIfMatch string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotIfMatch = r.Header.Get("If-Match")
		w.Header().Set("ETag", `"new-etag"`)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	opts := &PutCalendarObjectOptions{IfMatch: webdav.ConditionalMatch(`"old-etag"`)}
	co, err := c.PutCalendarObject(context.Background(), "/cal/test-uid.ics", minimalCalendar(), opts)
	if err != nil {
		t.Fatalf("PutCalendarObject: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotIfMatch != `"old-etag"` {
		t.Errorf("If-Match header = %q, want %q", gotIfMatch, `"old-etag"`)
	}
	if co.ETag != `"new-etag"` {
		t.Errorf("returned ETag = %q, want %q", co.ETag, `"new-etag"`)
	}
}

// TestPutCalendarObjectNilOptsOmitsIfMatch verifies a nil opts (the
// unconditional write) sends no If-Match header.
func TestPutCalendarObjectNilOptsOmitsIfMatch(t *testing.T) {
	var hadIfMatch bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadIfMatch = r.Header["If-Match"]
		w.Header().Set("ETag", `"e"`)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.PutCalendarObject(context.Background(), "/cal/test-uid.ics", minimalCalendar(), nil); err != nil {
		t.Fatalf("PutCalendarObject: %v", err)
	}
	if hadIfMatch {
		t.Errorf("If-Match header present on unconditional PUT, want absent")
	}
}

// TestPutCalendarObjectPreconditionFailed verifies a 412 response surfaces as
// an error whose status code consumers can read via webdav.HTTPErrorCode.
func TestPutCalendarObjectPreconditionFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer srv.Close()

	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	opts := &PutCalendarObjectOptions{IfMatch: webdav.ConditionalMatch(`"stale"`)}
	_, err = c.PutCalendarObject(context.Background(), "/cal/test-uid.ics", minimalCalendar(), opts)
	if err == nil {
		t.Fatal("PutCalendarObject: want error on 412, got nil")
	}
	code, ok := webdav.HTTPErrorCode(err)
	if !ok {
		t.Fatalf("HTTPErrorCode: not an HTTP error: %v", err)
	}
	if code != http.StatusPreconditionFailed {
		t.Errorf("status code = %d, want %d", code, http.StatusPreconditionFailed)
	}
}

// TestGetCalendarObjectKeepsAWeakETag verifies a weak ETag, as a compressing
// proxy in front of Nextcloud sends, is returned as the server sent it.
func TestGetCalendarObjectKeepsAWeakETag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ical.MIMEType)
		w.Header().Set("ETag", `W/"e1"`)
		if err := ical.NewEncoder(w).Encode(minimalCalendar()); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()

	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	co, err := c.GetCalendarObject(context.Background(), "/cal/test-uid.ics")
	if err != nil {
		t.Fatalf("GetCalendarObject: %v", err)
	}
	if co.ETag != `W/"e1"` {
		t.Errorf("ETag = %q, want %q", co.ETag, `W/"e1"`)
	}
}

// TestMultiGetCalendarKeepsETags verifies getetag values are returned as the
// server wrote them, weak or strong.
func TestMultiGetCalendarKeepsETags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <D:response><D:href>/cal/a.ics</D:href><D:propstat><D:prop><D:getetag>"a1"</D:getetag>
  <C:calendar-data>BEGIN:VCALENDAR&#13;
VERSION:2.0&#13;
PRODID:-//t//t//EN&#13;
END:VCALENDAR&#13;
</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>
 <D:response><D:href>/cal/b.ics</D:href><D:propstat><D:prop><D:getetag>W/"b1"</D:getetag>
  <C:calendar-data>BEGIN:VCALENDAR&#13;
VERSION:2.0&#13;
PRODID:-//t//t//EN&#13;
END:VCALENDAR&#13;
</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>
</D:multistatus>`)
	}))
	defer srv.Close()

	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	objs, err := c.MultiGetCalendar(context.Background(), "/cal/", &CalendarMultiGet{Paths: []string{"/cal/a.ics", "/cal/b.ics"}})
	if err != nil {
		t.Fatalf("MultiGetCalendar: %v", err)
	}
	if len(objs) != 2 || objs[0].ETag != `"a1"` || objs[1].ETag != `W/"b1"` {
		t.Errorf("objects = %+v, want ETags %q and %q", objs, `"a1"`, `W/"b1"`)
	}
}
