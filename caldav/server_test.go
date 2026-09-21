package caldav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/internal"
)

var propFindSupportedCalendarComponentRequest = `
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop>
     <c:supported-calendar-component-set />
  </d:prop>
</d:propfind>
`

var testPropFindSupportedCalendarComponentCases = map[*Calendar][]string{
	{Path: "/user/calendars/cal"}:                                                     {"VEVENT"},
	{Path: "/user/calendars/cal", SupportedComponentSet: []string{"VTODO"}}:           {"VTODO"},
	{Path: "/user/calendars/cal", SupportedComponentSet: []string{"VEVENT", "VTODO"}}: {"VEVENT", "VTODO"},
}

func TestPropFindSupportedCalendarComponent(t *testing.T) {
	for calendar, expected := range testPropFindSupportedCalendarComponentCases {
		req := httptest.NewRequest("PROPFIND", calendar.Path, nil)
		req.Body = io.NopCloser(strings.NewReader(propFindSupportedCalendarComponentRequest))
		req.Header.Set("Content-Type", "application/xml")
		w := httptest.NewRecorder()
		handler := Handler{Backend: &testBackend{calendars: []Calendar{*calendar}}}
		handler.ServeHTTP(w, req)

		res := w.Result()
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Error(err)
		}
		resp := string(data)
		for _, comp := range expected {
			// Would be nicer to do a proper XML-decoding here, but this is probably good enough for now.
			if !strings.Contains(resp, comp) {
				t.Errorf("Expected component: %v not found in response:\n%v", comp, resp)
			}
		}
	}
}

var propFindCalendarRequest = `
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop>
	 <d:displayname/>
	 <c:calendar-description/>
     <c:calendar-timezone />
	 <n:calendar-color 
                xmlns:n="http://apple.com/ns/ical/"/>
  </d:prop>
</d:propfind>
`

var calendarTimezoneData = `BEGIN:VCALENDAR
PRODID:-//Example Corp.//CalDAV Client//EN
VERSION:2.0
BEGIN:VTIMEZONE
TZID:US-Eastern
LAST-MODIFIED:19870101T000000Z
BEGIN:STANDARD
DTSTART:19671029T020000
RRULE:FREQ=YEARLY;BYDAY=-1SU;BYMONTH=10
TZOFFSETFROM:-0400
TZOFFSETTO:-0500
TZNAME:Eastern Standard Time (US & Canada)
END:STANDARD
BEGIN:DAYLIGHT
DTSTART:19870405T020000
RRULE:FREQ=YEARLY;BYDAY=1SU;BYMONTH=4
TZOFFSETFROM:-0500
TZOFFSETTO:-0400
TZNAME:Eastern Daylight Time (US & Canada)
END:DAYLIGHT
END:VTIMEZONE
END:VCALENDAR
`

func TestPropFindCalendar(t *testing.T) {
	timezone, err := decodeCalendarTimezone(calendarTimezoneData)
	if err != nil {
		t.Fatalf("Unexpected error in decodeCalendarTimezone: %s", err)
	}

	calendar := Calendar{
		Path:        "/user/calendars/cal",
		Name:        "Test Calendar",
		Description: "This is a test calendar",
		Timezone:    timezone,
		Color:       "#DEADBEEF",
	}

	req := httptest.NewRequest("PROPFIND", calendar.Path, nil)
	req.Body = io.NopCloser(strings.NewReader(propFindCalendarRequest))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	handler := Handler{Backend: &testBackend{calendars: []Calendar{calendar}}}
	handler.ServeHTTP(w, req)

	resp := w.Result()

	var ms internal.MultiStatus
	err = xml.NewDecoder(resp.Body).Decode(&ms)
	if err != nil {
		t.Fatalf("Unexpcted error in xml.NewDecoder: %s", err)
	}
	if len(ms.Responses) != 1 {
		t.Fatalf("Found %d multi status responses, expected 1", len(ms.Responses))
	}
	if len(ms.Responses[0].PropStats) != 1 {
		t.Fatalf("Found %d prop stats, expected 1", len(ms.Responses[0].PropStats))
	}
	if ms.Responses[0].PropStats[0].Status.Code != 200 {
		t.Fatalf("Received %d prop stat status, expected 200", ms.Responses[0].PropStats[0].Status.Code)
	}
	if len(ms.Responses[0].PropStats[0].Prop.Raw) != 4 {
		t.Fatalf("Found %d props, expected 4", len(ms.Responses[0].PropStats[0].Prop.Raw))
	}

	rawDisplayName := ms.Responses[0].PropStats[0].Prop.Get(internal.DisplayNameName)
	rawCalendarDescription := ms.Responses[0].PropStats[0].Prop.Get(calendarDescriptionName)
	rawTimezone := ms.Responses[0].PropStats[0].Prop.Get(calendarTimezoneName)
	rawColor := ms.Responses[0].PropStats[0].Prop.Get(calendarColorName)
	if rawDisplayName == nil {
		t.Fatal("Got unexpected nil rawDisplayName")
	}
	if rawCalendarDescription == nil {
		t.Fatal("Got unexpected nil rawCalendarDescription")
	}
	if rawTimezone == nil {
		t.Fatal("Got unexpected nil rawTimezone")
	}
	if rawColor == nil {
		t.Fatal("Got unexpected nil rawColor")
	}

	v0 := internal.DisplayName{}
	err = rawDisplayName.Decode(&v0)
	if err != nil {
		t.Fatalf("Unexpcted error in rawDisplayName.Decode: %s", err)
	}
	if calendar.Name != v0.Name {
		t.Fatalf("Calendar name is '%s', expected '%s'", calendar.Name, v0.Name)
	}

	v1 := calendarDescription{}
	err = rawCalendarDescription.Decode(&v1)
	if err != nil {
		t.Fatalf("Unexpcted error in rawCalendarDescription.Decode: %s", err)
	}
	if calendar.Description != v1.Description {
		t.Fatalf("Calendar description is '%s', expected '%s'", calendar.Description, v1.Description)
	}

	v2 := calendarTimezone{}
	err = rawTimezone.Decode(&v2)
	if err != nil {
		t.Fatalf("Unexpected error in rawTimezone.Decode: %s", err)
	}
	gotTimezone, err := decodeCalendarTimezone(v2.Timezone)
	if err != nil {
		t.Fatalf("Calendar timezone is not a valid iCalendar object: %s", err)
	}
	if tzid := gotTimezone.Children[0].Props.Get(ical.PropTimezoneID); tzid == nil || tzid.Value != "US-Eastern" {
		t.Fatalf("Calendar timezone is '%v', expected 'US-Eastern'", tzid)
	}

	v3 := calendarColor{}
	err = rawColor.Decode(&v3)
	if err != nil {
		t.Fatalf("Unexpcted error in rawColor.Decode: %s", err)
	}
	if calendar.Color != v3.Color {
		t.Fatalf("Calendar color is '%s', expected '%s'", calendar.Color, v3.Color)
	}
}

var propFindUserPrincipal = `
<?xml version="1.0" encoding="UTF-8"?>
<A:propfind xmlns:A="DAV:">
  <A:prop>
    <A:current-user-principal/>
    <A:principal-URL/>
    <A:resourcetype/>
  </A:prop>
</A:propfind>
`

func TestPropFindRoot(t *testing.T) {
	req := httptest.NewRequest("PROPFIND", "/", strings.NewReader(propFindUserPrincipal))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	calendar := &Calendar{}
	handler := Handler{Backend: &testBackend{calendars: []Calendar{*calendar}}}
	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	resp := string(data)
	if !strings.Contains(resp, `<current-user-principal xmlns="DAV:"><href>/user/</href></current-user-principal>`) {
		t.Errorf("No user-principal returned when doing a PROPFIND against root, response:\n%s", resp)
	}
}

const TestMkCalendarReq = `
<?xml version="1.0" encoding="UTF-8"?>
<B:mkcalendar xmlns:B="urn:ietf:params:xml:ns:caldav">
  <A:set xmlns:A="DAV:">
    <A:prop>
      <B:calendar-timezone>BEGIN:VCALENDAR&#13;
VERSION:2.0&#13;
PRODID:-//Apple Inc.//iPhone OS 18.1.1//EN&#13;
CALSCALE:GREGORIAN&#13;
BEGIN:VTIMEZONE&#13;
TZID:Europe/Paris&#13;
BEGIN:DAYLIGHT&#13;
TZOFFSETFROM:+0100&#13;
RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU&#13;
DTSTART:19810329T020000&#13;
TZNAME:UTC+2&#13;
TZOFFSETTO:+0200&#13;
END:DAYLIGHT&#13;
BEGIN:STANDARD&#13;
TZOFFSETFROM:+0200&#13;
RRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU&#13;
DTSTART:19961027T030000&#13;
TZNAME:UTC+1&#13;
TZOFFSETTO:+0100&#13;
END:STANDARD&#13;
END:VTIMEZONE&#13;
END:VCALENDAR&#13;
</B:calendar-timezone>
      <D:calendar-order xmlns:D="http://apple.com/ns/ical/">2</D:calendar-order>
      <B:supported-calendar-component-set>
        <B:comp name="VEVENT"/>
      </B:supported-calendar-component-set>
      <D:calendar-color xmlns:D="http://apple.com/ns/ical/" symbolic-color="red">#FF2968</D:calendar-color>
      <A:displayname>test calendar</A:displayname>
      <B:calendar-free-busy-set>
        <NO/>
      </B:calendar-free-busy-set>
    </A:prop>
  </A:set>
</B:mkcalendar>
`

const propFindTest2 = `
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop>
     <d:resourcetype/>
     <c:supported-calendar-component-set/>
     <d:displayname/>
     <c:max-resource-size/>
     <c:calendar-description/>
  </d:prop>
</d:propfind>
`

func TestMkCalendar(t *testing.T) {
	handler := Handler{Backend: &testBackend{
		calendars: []Calendar{},
		objectMap: map[string][]CalendarObject{},
	}}

	req := httptest.NewRequest("MKCALENDAR", "/user/calendars/default/", strings.NewReader(TestMkCalendarReq))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res := w.Result()
	if e := res.Body.Close(); e != nil {
		t.Fatal(e)
	} else if loc := res.Header.Get("Location"); loc != "/user/calendars/default/" {
		t.Fatalf("unexpected location: %s", loc)
	} else if sc := res.StatusCode; sc != http.StatusCreated {
		t.Fatalf("unexpected status code: %d", sc)
	}

	req = httptest.NewRequest("PROPFIND", "/user/calendars/default/", strings.NewReader(propFindTest2))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Depth", "0")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res = w.Result()
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp := string(data)
	if !strings.Contains(resp, fmt.Sprintf("<href>%s</href>", "/user/calendars/default/")) {
		t.Fatalf("want calendar href in response")
	} else if !strings.Contains(resp, "<resourcetype xmlns=\"DAV:\">") {
		t.Fatalf("want resource type in response")
	} else if !strings.Contains(resp, "<collection xmlns=\"DAV:\"></collection>") {
		t.Fatalf("want collection resource type in response")
	} else if !strings.Contains(resp, "<calendar xmlns=\"urn:ietf:params:xml:ns:caldav\"></calendar>") {
		t.Fatalf("want calendar resource type in response")
	} else if !strings.Contains(resp, "<displayname xmlns=\"DAV:\">test calendar</displayname>") {
		t.Fatalf("want display name in response")
	} else if !strings.Contains(resp, "<supported-calendar-component-set xmlns=\"urn:ietf:params:xml:ns:caldav\"><comp xmlns=\"urn:ietf:params:xml:ns:caldav\" name=\"VEVENT\"></comp></supported-calendar-component-set>") {
		t.Fatalf("want supported-calendar-component-set in response")
	}
}

func TestMkCalendarBody(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body          string
		contentLength int64
		wantName      string
	}{
		{name: "empty", body: ""},
		{name: "empty-chunked", body: "", contentLength: -1},
		{name: "chunked", body: TestMkCalendarReq, contentLength: -1, wantName: "test calendar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &testBackend{}
			req := httptest.NewRequest("MKCALENDAR", "/user/calendars/default/", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/xml")
			if tc.contentLength != 0 {
				req.ContentLength = tc.contentLength
			}
			w := httptest.NewRecorder()
			// http.MaxBytesReader answers a zero-length read with no error
			req.Body = http.MaxBytesReader(w, req.Body, 1<<20)
			(&Handler{Backend: backend}).ServeHTTP(w, req)

			if sc := w.Result().StatusCode; sc != http.StatusCreated {
				t.Fatalf("unexpected status code: %d", sc)
			} else if len(backend.calendars) != 1 || backend.calendars[0].Name != tc.wantName {
				t.Errorf("unexpected calendars: %+v", backend.calendars)
			}
		})
	}
}

func TestMkCalendarProps(t *testing.T) {
	backend := &testBackend{}
	handler := Handler{Backend: backend, Prefix: "/dav"}

	req := httptest.NewRequest("MKCALENDAR", "/dav/user/calendars/default/", strings.NewReader(TestMkCalendarReq))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if sc := w.Result().StatusCode; sc != http.StatusCreated {
		t.Fatalf("unexpected status code: %d", sc)
	} else if len(backend.calendars) != 1 {
		t.Fatalf("want 1 calendar, got %d", len(backend.calendars))
	}
	cal := backend.calendars[0]
	if cal.Name != "test calendar" {
		t.Errorf("unexpected name: %q", cal.Name)
	}
	if cal.Color != "#FF2968" {
		t.Errorf("unexpected color: %q", cal.Color)
	}
	if cal.Timezone == nil {
		t.Errorf("want a timezone")
	}
	if len(cal.SupportedComponentSet) != 1 || cal.SupportedComponentSet[0] != "VEVENT" {
		t.Errorf("unexpected component set: %v", cal.SupportedComponentSet)
	}
}

var reportCalendarData = `
<?xml version="1.0" encoding="UTF-8"?>
<B:calendar-multiget xmlns:A="DAV:" xmlns:B="urn:ietf:params:xml:ns:caldav">
  <A:prop>
    <B:calendar-data/>
  </A:prop>
  <A:href>%s</A:href>
</B:calendar-multiget>
`

func TestMultiCalendarBackend(t *testing.T) {
	calendarB := Calendar{Path: "/user/calendars/b", SupportedComponentSet: []string{"VTODO"}}
	calendars := []Calendar{
		{Path: "/user/calendars/a"},
		calendarB,
	}
	eventSummary := "This is a todo"
	event := ical.NewEvent()
	event.Name = ical.CompToDo
	event.Props.SetText(ical.PropUID, "46bbf47a-1861-41a3-ae06-8d8268c6d41e")
	event.Props.SetDateTime(ical.PropDateTimeStamp, time.Now())
	event.Props.SetText(ical.PropSummary, eventSummary)
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//xyz Corp//NONSGML PDA Calendar Version 1.0//EN")
	cal.Children = []*ical.Component{
		event.Component,
	}
	object := CalendarObject{
		Path: "/user/calendars/b/test.ics",
		Data: cal,
	}
	req := httptest.NewRequest("PROPFIND", "/user/calendars/", strings.NewReader(propFindUserPrincipal))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	handler := Handler{Backend: &testBackend{
		calendars: calendars,
		objectMap: map[string][]CalendarObject{
			calendarB.Path: {object},
		},
	}}
	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	resp := string(data)
	for _, calendar := range calendars {
		if !strings.Contains(resp, fmt.Sprintf(`<response xmlns="DAV:"><href>%s</href>`, calendar.Path)) {
			t.Errorf("Calendar: %v not returned in PROPFIND, response:\n%s", calendar, resp)
		}
	}

	// Now do a PROPFIND for the last calendar
	req = httptest.NewRequest("PROPFIND", calendarB.Path, strings.NewReader(propFindSupportedCalendarComponentRequest))
	req.Header.Set("Content-Type", "application/xml")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res = w.Result()
	defer res.Body.Close()
	data, err = io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	resp = string(data)
	if !strings.Contains(resp, "VTODO") {
		t.Errorf("Expected component: VTODO not found in response:\n%v", resp)
	}
	if !strings.Contains(resp, object.Path) {
		t.Errorf("Expected calendar object: %v not found in response:\n%v", object, resp)
	}

	// Now do a REPORT to get the actual data for the event
	req = httptest.NewRequest("REPORT", calendarB.Path, strings.NewReader(fmt.Sprintf(reportCalendarData, object.Path)))
	req.Header.Set("Content-Type", "application/xml")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res = w.Result()
	defer res.Body.Close()
	data, err = io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	resp = string(data)
	if !strings.Contains(resp, fmt.Sprintf("SUMMARY:%s", eventSummary)) {
		t.Errorf("ICAL content not properly returned in response:\n%v", resp)
	}
}

var propFindAllProp = `
<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:">
  <D:allprop/>
</D:propfind>
`

var reportTest1 = `
<?xml version="1.0" encoding="utf-8"?>
<C:calendar-query xmlns:C="urn:ietf:params:xml:ns:caldav">
    <D:prop xmlns:D="DAV:">
      <D:getetag/>
      <D:getcontenttype/>
      <D:getcontentlength/>
      <D:getlastmodified/>
      <C:calendar-data/>
    </D:prop>
    <C:filter>
        <C:comp-filter name="VCALENDAR">
          <C:comp-filter name="VEVENT"/>
        </C:comp-filter>
    </C:filter>
</C:calendar-query>
`

var propFindTest1 = `
<?xml version="1.0" encoding="UTF-8"?>                                                                 
<A:propfind xmlns:A="DAV:">
  <A:prop>
    <B:calendar-home-set xmlns:B="urn:ietf:params:xml:ns:caldav"/>
    <B:calendar-user-address-set xmlns:B="urn:ietf:params:xml:ns:caldav"/>
    <B:max-attendees-per-instance xmlns:B="urn:ietf:params:xml:ns:caldav"/>
    <A:principal-collection-set/>
    <A:principal-URL/>
    <A:resource-id/>
    <A:supported-report-set/>
    <B:supported-calendar-component-set xmlns:B="urn:ietf:params:xml:ns:caldav"/>
    <B:max-resource-size xmlns:B="urn:ietf:params:xml:ns:caldav"/>
    <B:calendar-timezone xmlns:B="urn:ietf:params:xml:ns:caldav"/>
    <A:current-user-principal/>
    <A:displayname/>    
    <B:calendar-description xmlns:B="urn:ietf:params:xml:ns:caldav"/>
    <B:calendar-data xmlns:B="urn:ietf:params:xml:ns:caldav"/>
    <A:resourcetype/>
    <A:getcontenttype/>
    <A:getetag/>
  </A:prop>
</A:propfind>
`

var calendarTestData1 = `
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Example Corp.//CalDAV Client//EN
BEGIN:VTIMEZONE
LAST-MODIFIED:20040110T032845Z
TZID:US/Eastern
BEGIN:DAYLIGHT
DTSTART:20000404T020000
RRULE:FREQ=YEARLY;BYDAY=1SU;BYMONTH=4
TZNAME:EDT
TZOFFSETFROM:-0500
TZOFFSETTO:-0400
END:DAYLIGHT
BEGIN:STANDARD
DTSTART:20001026T020000
RRULE:FREQ=YEARLY;BYDAY=-1SU;BYMONTH=10
TZNAME:EST
TZOFFSETFROM:-0400
TZOFFSETTO:-0500
END:STANDARD
END:VTIMEZONE
BEGIN:VEVENT
ATTENDEE;PARTSTAT=ACCEPTED;ROLE=CHAIR:mailto:cyrus@example.com
ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:lisa@example.com
DTSTAMP:20060206T001220Z
DTSTART;TZID=US/Eastern:20060104T100000
DURATION:PT1H
LAST-MODIFIED:20060206T001330Z
ORGANIZER:mailto:cyrus@example.com
SEQUENCE:1
STATUS:TENTATIVE
SUMMARY:Event #3
UID:DC6C50A017428C5216A2F1CD@example.com
X-ABC-GUID:E1CX5Dr-0007ym-Hz@example.com
END:VEVENT
END:VCALENDAR
`

func TestPropFindAllPropAndQuery(t *testing.T) {
	calendar := Calendar{
		Description:           "This is a description which SHOULD NOT be returned in allprop",
		Path:                  "/user/calendars/default/",
		SupportedComponentSet: []string{"VEVENT", "VTODO"},
	}
	cal, err := ical.NewDecoder(strings.NewReader(calendarTestData1)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	object := CalendarObject{
		Path: "/user/calendars/default/DC6C50A017428C5216A2F1CD.ics",
		Data: cal,
		ETag: "191382932849",
	}
	handler := Handler{Backend: &testBackend{
		calendars: []Calendar{calendar},
		objectMap: map[string][]CalendarObject{
			calendar.Path: []CalendarObject{object},
		},
	}}

	req := httptest.NewRequest("PROPFIND", "/user/calendars/default/", strings.NewReader(propFindAllProp))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Depth", "0")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp := string(data)
	if !strings.Contains(resp, "<resourcetype xmlns=\"DAV:\">") {
		t.Fatalf("want resourcetype prop in allprop")
	} else if !strings.Contains(resp, "<collection xmlns=\"DAV:\">") {
		t.Fatalf("want collection resourcetype")
	} else if !strings.Contains(resp, "<calendar xmlns=\"urn:ietf:params:xml:ns:caldav\">") {
		t.Fatalf("expect calendar resourcetype")
	} else if strings.Contains(resp, "<calendar-description xmlns=\"urn:ietf:params:xml:ns:caldav\">") {
		t.Fatalf("do not want calendar-description in allprop")
	} else if strings.Contains(resp, "DC6C50A017428C5216A2F1CD.ics") {
		t.Fatalf("do not want children if Depth: 0")
	}

	req = httptest.NewRequest("PROPFIND", "/user/calendars/default/", strings.NewReader(propFindAllProp))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Depth", "1")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res = w.Result()
	defer res.Body.Close()
	data, err = io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp = string(data)
	if !strings.Contains(resp, fmt.Sprintf("<href>%s</href>", object.Path)) {
		t.Fatalf("want child href in allprop")
	} else if !strings.Contains(resp, object.ETag) {
		t.Fatalf("want child ETag in allprop")
	} else if !strings.Contains(resp, "<getcontenttype xmlns=\"DAV:\">text/calendar</getcontenttype>") {
		t.Fatalf("want child getcontenttype in allprop")
	} else if strings.Contains(resp, "<calendar-data xmlns=\"urn:ietf:params:xml:ns:caldav\">") {
		t.Fatalf("do not want calendar-data in allprop")
	}

	req = httptest.NewRequest("REPORT", "/user/calendars/default/", strings.NewReader(reportTest1))
	req.Header.Set("Content-Type", "application/xml")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res = w.Result()
	defer res.Body.Close()
	data, err = io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp = string(data)
	if !strings.Contains(resp, fmt.Sprintf("<href>%s</href>", object.Path)) {
		t.Fatalf("want child href in REPORT")
	} else if !strings.Contains(resp, object.ETag) {
		t.Fatalf("want child ETag in REPORT")
	} else if !strings.Contains(resp, "<getcontenttype xmlns=\"DAV:\">text/calendar</getcontenttype>") {
		t.Fatalf("want child getcontenttype in REPORT")
	} else if !strings.Contains(resp, "<calendar-data xmlns=\"urn:ietf:params:xml:ns:caldav\">") {
		t.Fatalf("do want calendar-data in REPORT")
	} else if !strings.Contains(resp, "UID:DC6C50A017428C5216A2F1CD@example.com") {
		t.Fatalf("calendar-data improperly returned")
	}

	req = httptest.NewRequest("PROPFIND", object.Path, strings.NewReader(propFindTest1))
	req.Header.Set("Content-Type", "application/xml")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res = w.Result()
	defer res.Body.Close()
	data, err = io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp = string(data)
	if strings.Contains(resp, "UID:DC6C50A017428C5216A2F1CD@example.com") {
		t.Fatalf("do not want calendar data in PROPFIND")
	} else if !strings.Contains(resp, object.ETag) {
		t.Fatalf("want child ETag in PROPFIND")
	} else if !strings.Contains(resp, "<getcontenttype xmlns=\"DAV:\">text/calendar</getcontenttype>") {
		t.Fatalf("want child getcontenttype in PROPFIND")
	}
}

var calendarTestData2 = `
BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Example Corp.//CalDAV Client//EN
BEGIN:VTIMEZONE
LAST-MODIFIED:20040110T032845Z
TZID:US/Eastern
BEGIN:DAYLIGHT
DTSTART:20000404T020000
RRULE:FREQ=YEARLY;BYDAY=1SU;BYMONTH=4
TZNAME:EDT
TZOFFSETFROM:-0500
TZOFFSETTO:-0400
END:DAYLIGHT
BEGIN:STANDARD
DTSTART:20001026T020000
RRULE:FREQ=YEARLY;BYDAY=-1SU;BYMONTH=10
TZNAME:EST
TZOFFSETFROM:-0400
TZOFFSETTO:-0500
END:STANDARD
END:VTIMEZONE
BEGIN:VEVENT
DTSTAMP:20060206T001102Z
DTSTART;TZID=US/Eastern:20060102T100000
DURATION:PT1H
SUMMARY:Event #1
Description:Go Steelers!
UID:74855313FA803DA593CD579A@example.com
END:VEVENT
END:VCALENDAR
`

var multigetTest1 = `
<?xml version="1.0" encoding="utf-8" ?>
   <C:calendar-multiget xmlns:D="DAV:"
                    xmlns:C="urn:ietf:params:xml:ns:caldav">
     <D:prop>
       <D:getetag/>
       <C:calendar-data/>
     </D:prop>
     <D:href>/user/calendars/default/74855313FA803DA593CD579A.ics</D:href>
     <D:href>/user/calendars/default/DC6C50A017428C5216A2F1CD.ics</D:href>
   </C:calendar-multiget>
`

func TestFindMultiget(t *testing.T) {
	calendar := Calendar{
		Description:           "This is a description which SHOULD NOT be returned in allprop",
		Path:                  "/user/calendars/default/",
		SupportedComponentSet: []string{"VEVENT"},
	}
	cal, err := ical.NewDecoder(strings.NewReader(calendarTestData1)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	object1 := CalendarObject{
		Path: "/user/calendars/default/DC6C50A017428C5216A2F1CD.ics",
		Data: cal,
		ETag: "191382932849",
	}
	cal, err = ical.NewDecoder(strings.NewReader(calendarTestData2)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	object2 := CalendarObject{
		Path: "/user/calendars/default/74855313FA803DA593CD579A.ics",
		Data: cal,
		ETag: "191382932850",
	}
	handler := Handler{Backend: &testBackend{
		calendars: []Calendar{calendar},
		objectMap: map[string][]CalendarObject{
			calendar.Path: []CalendarObject{object1, object2},
		},
	}}

	req := httptest.NewRequest("REPORT", "/user/calendars/default/", strings.NewReader(multigetTest1))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Depth", "1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp := string(data)
	if !strings.Contains(resp, "UID:DC6C50A017428C5216A2F1CD@example.com") {
		t.Fatalf("want object1 in multiget report")
	} else if !strings.Contains(resp, "UID:74855313FA803DA593CD579A@example.com") {
		t.Fatalf("want object2 in multiget report")
	} else if !strings.Contains(resp, object1.ETag) {
		t.Fatalf("want object1 ETag in multiget report")
	} else if !strings.Contains(resp, object2.ETag) {
		t.Fatalf("want object2 ETag in multiget report")
	}
}

func TestMultigetWithoutMultiGetBackend(t *testing.T) {
	cal, err := ical.NewDecoder(strings.NewReader(calendarTestData1)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	object := CalendarObject{
		Path: "/user/calendars/default/DC6C50A017428C5216A2F1CD.ics",
		Data: cal,
		ETag: "191382932849",
	}
	// Embedding the interface hides GetCalendarObjects
	handler := Handler{Backend: struct{ Backend }{&testBackend{
		objectMap: map[string][]CalendarObject{
			"/user/calendars/default/": []CalendarObject{object},
		},
	}}}

	req := httptest.NewRequest("REPORT", "/user/calendars/default/", strings.NewReader(multigetTest1))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp := string(data)
	if !strings.Contains(resp, "UID:DC6C50A017428C5216A2F1CD@example.com") {
		t.Fatalf("want object in multiget report:\n%v", resp)
	} else if !strings.Contains(resp, "find calendar object at: /user/calendars/default/74855313FA803DA593CD579A.ics") {
		t.Fatalf("want the backend's error for the missing object:\n%v", resp)
	}
}

var mkcolRequestData = `
<?xml version='1.0' encoding='UTF-8' ?>
<mkcol
    xmlns="DAV:"
    xmlns:CAL="urn:ietf:params:xml:ns:caldav"
    xmlns:CARD="urn:ietf:params:xml:ns:carddav">
    <set>
        <prop>
            <resourcetype>
                <collection />
                <CAL:calendar />
            </resourcetype>
            <displayname>Test calendar</displayname>
            <CAL:calendar-description>A calendar for testing</CAL:calendar-description>
            <n0:calendar-color
                xmlns:n0="http://apple.com/ns/ical/">#009688FF
            </n0:calendar-color>
            <CAL:calendar-timezone>
                <![CDATA[BEGIN:VCALENDAR
PRODID:-//Example Corp.//CalDAV Client//EN
VERSION:2.0
BEGIN:VTIMEZONE
TZID:Europe/Berlin
LAST-MODIFIED:20230104T023643Z
TZURL:https://www.tzurl.org/zoneinfo/Europe/Berlin
X-LIC-LOCATION:Europe/Berlin
X-PROLEPTIC-TZNAME:LMT
BEGIN:STANDARD
TZNAME:CET
TZOFFSETFROM:+005328
TZOFFSETTO:+0100
DTSTART:18930401T000632
END:STANDARD
BEGIN:DAYLIGHT
TZNAME:CEST
TZOFFSETFROM:+0100
TZOFFSETTO:+0200
DTSTART:19160430T230000
RDATE:19400401T020000
RDATE:19430329T020000
RDATE:19460414T020000
RDATE:19470406T030000
RDATE:19480418T020000
RDATE:19490410T020000
RDATE:19800406T020000
END:DAYLIGHT
BEGIN:STANDARD
TZNAME:CET
TZOFFSETFROM:+0200
TZOFFSETTO:+0100
DTSTART:19161001T010000
RDATE:19421102T030000
RDATE:19431004T030000
RDATE:19441002T030000
RDATE:19451118T030000
RDATE:19461007T030000
END:STANDARD
BEGIN:DAYLIGHT
TZNAME:CEST
TZOFFSETFROM:+0100
TZOFFSETTO:+0200
DTSTART:19170416T020000
RRULE:FREQ=YEARLY;UNTIL=19180415T010000Z;BYMONTH=4;BYDAY=3MO
END:DAYLIGHT
BEGIN:STANDARD
TZNAME:CET
TZOFFSETFROM:+0200
TZOFFSETTO:+0100
DTSTART:19170917T030000
RRULE:FREQ=YEARLY;UNTIL=19180916T010000Z;BYMONTH=9;BYDAY=3MO
END:STANDARD
BEGIN:DAYLIGHT
TZNAME:CEST
TZOFFSETFROM:+0100
TZOFFSETTO:+0200
DTSTART:19440403T020000
RRULE:FREQ=YEARLY;UNTIL=19450402T010000Z;BYMONTH=4;BYDAY=1MO
END:DAYLIGHT
BEGIN:DAYLIGHT
TZNAME:CEMT
TZOFFSETFROM:+0200
TZOFFSETTO:+0300
DTSTART:19450524T010000
RDATE:19470511T020000
END:DAYLIGHT
BEGIN:DAYLIGHT
TZNAME:CEST
TZOFFSETFROM:+0300
TZOFFSETTO:+0200
DTSTART:19450924T030000
RDATE:19470629T030000
END:DAYLIGHT
BEGIN:STANDARD
TZNAME:CET
TZOFFSETFROM:+0100
TZOFFSETTO:+0100
DTSTART:19460101T000000
RDATE:19800101T000000
END:STANDARD
BEGIN:STANDARD
TZNAME:CET
TZOFFSETFROM:+0200
TZOFFSETTO:+0100
DTSTART:19471005T030000
RRULE:FREQ=YEARLY;UNTIL=19491002T010000Z;BYMONTH=10;BYDAY=1SU
END:STANDARD
BEGIN:STANDARD
TZNAME:CET
TZOFFSETFROM:+0200
TZOFFSETTO:+0100
DTSTART:19800928T030000
RRULE:FREQ=YEARLY;UNTIL=19950924T010000Z;BYMONTH=9;BYDAY=-1SU
END:STANDARD
BEGIN:DAYLIGHT
TZNAME:CEST
TZOFFSETFROM:+0100
TZOFFSETTO:+0200
DTSTART:19810329T020000
RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU
END:DAYLIGHT
BEGIN:STANDARD
TZNAME:CET
TZOFFSETFROM:+0200
TZOFFSETTO:+0100
DTSTART:19961027T030000
RRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU
END:STANDARD
END:VTIMEZONE
END:VCALENDAR
]]>
            </CAL:calendar-timezone>
            <CAL:supported-calendar-component-set>
                <CAL:comp name="VEVENT" />
                <CAL:comp name="VTODO" />
                <CAL:comp name="VJOURNAL" />
            </CAL:supported-calendar-component-set>
        </prop>
    </set>
</mkcol>`

func TestCreateCalendar(t *testing.T) {
	tb := testBackend{
		calendars: nil,
		objectMap: nil,
	}
	b := backend{
		Backend: &tb,
		Prefix:  "/dav",
	}
	req := httptest.NewRequest("MKCOL", "/dav/calendars/user0/test-calendar", strings.NewReader(mkcolRequestData))
	req.Header.Set("Content-Type", "application/xml")

	err := b.Mkcol(req)
	if err != nil {
		t.Fatalf("Unexpcted error in Mkcol: %s", err)
	}
	if len(tb.calendars) != 1 {
		t.Fatalf("Found %d calendars, expected 1", len(tb.calendars))
	}
	c := tb.calendars[0]
	if c.Name != "Test calendar" {
		t.Fatalf("Calendar name is '%s', expected 'Test calendar'", c.Name)
	}
	expectedPath := "/dav/calendars/user0/test-calendar"
	if c.Path != expectedPath {
		t.Fatalf("Calendar path is '%s', expected '%s'", c.Path, expectedPath)
	}
	expectedDescription := "A calendar for testing"
	if c.Description != expectedDescription {
		t.Fatalf("Calendar description is '%s', expected '%s'", c.Description, expectedDescription)
	}
	expectedColor := "#009688FF"
	if c.Color != expectedColor {
		t.Fatalf("Calendar color is '%s', expected '%s'", c.Color, expectedColor)
	}
	if c.Timezone == nil {
		t.Fatal("Got unexpected nil calendar timezone")
	}
	if n := len(c.Timezone.Children); n != 1 {
		t.Fatalf("Found %d calendar timezone components, expected 1", n)
	}
	if name := c.Timezone.Children[0].Name; name != ical.CompTimezone {
		t.Fatalf("Calendar timezone component is '%s', expected '%s'", name, ical.CompTimezone)
	}
	expectedTimezone := "Europe/Berlin"
	if tzid := c.Timezone.Children[0].Props.Get(ical.PropTimezoneID); tzid == nil || tzid.Value != expectedTimezone {
		t.Fatalf("Calendar timezone is '%v', expected '%s'", tzid, expectedTimezone)
	}
	if len(c.SupportedComponentSet) != 3 {
		t.Fatalf("Found %d SupportedComponentSet, expected 3", len(c.SupportedComponentSet))
	}
	if c.SupportedComponentSet[0] != "VEVENT" {
		t.Fatalf("Calendar 0.SupportedComponentSet is '%s', expected '%s'", c.SupportedComponentSet[0], "VEVENT")
	}
	if c.SupportedComponentSet[1] != "VTODO" {
		t.Fatalf("Calendar 1.SupportedComponentSet is '%s', expected '%s'", c.SupportedComponentSet[1], "VTODO")
	}
	if c.SupportedComponentSet[2] != "VJOURNAL" {
		t.Fatalf("Calendar 2.SupportedComponentSet is '%s', expected '%s'", c.SupportedComponentSet[2], "VJOURNAL")
	}
}

var mkcolRequestDataMinimalBody = `
<?xml version='1.0' encoding='UTF-8' ?>
<mkcol
    xmlns="DAV:"
    xmlns:CAL="urn:ietf:params:xml:ns:caldav"
    xmlns:CARD="urn:ietf:params:xml:ns:carddav">
    <set>
        <prop>
            <resourcetype>
                <collection />
                <CAL:calendar />
            </resourcetype>
            <displayname>Test calendar</displayname>
        </prop>
    </set>
</mkcol>`

func TestCreateCalendarMinimalBody(t *testing.T) {
	tb := testBackend{
		calendars: nil,
		objectMap: nil,
	}
	b := backend{
		Backend: &tb,
		Prefix:  "/dav",
	}
	req := httptest.NewRequest("MKCOL", "/dav/calendars/user0/test-calendar", strings.NewReader(mkcolRequestDataMinimalBody))
	req.Header.Set("Content-Type", "application/xml")

	err := b.Mkcol(req)
	if err != nil {
		t.Fatalf("Unexpcted error in Mkcol: %s", err)
	}
	if len(tb.calendars) != 1 {
		t.Fatalf("Found %d calendars, expected 1", len(tb.calendars))
	}
	c := tb.calendars[0]
	if c.Name != "Test calendar" {
		t.Fatalf("Calendar name is '%s', expected 'Test calendar'", c.Name)
	}
	expectedPath := "/dav/calendars/user0/test-calendar"
	if c.Path != expectedPath {
		t.Fatalf("Calendar path is '%s', expected '%s'", c.Path, expectedPath)
	}
	expectedDescription := ""
	if c.Description != expectedDescription {
		t.Fatalf("Calendar description is '%s', expected '%s'", c.Description, expectedDescription)
	}
	expectedColor := ""
	if c.Color != expectedColor {
		t.Fatalf("Calendar color is '%s', expected '%s'", c.Color, expectedColor)
	}
	if c.Timezone != nil {
		t.Fatalf("Calendar timezone is '%v', expected none", c.Timezone)
	}
	if len(c.SupportedComponentSet) != 0 {
		t.Fatalf("Found %d SupportedComponentSet, expected 0", len(c.SupportedComponentSet))
	}
}

var mkcolRequestDataForeignColor = `
<?xml version='1.0' encoding='UTF-8' ?>
<mkcol
    xmlns="DAV:"
    xmlns:CAL="urn:ietf:params:xml:ns:caldav">
    <set>
        <prop>
            <resourcetype>
                <collection />
                <CAL:calendar />
            </resourcetype>
            <displayname>Test calendar</displayname>
            <CAL:calendar-color>#009688FF</CAL:calendar-color>
        </prop>
    </set>
</mkcol>`

func TestCreateCalendarForeignColor(t *testing.T) {
	tb := testBackend{}
	b := backend{
		Backend: &tb,
		Prefix:  "/dav",
	}
	req := httptest.NewRequest("MKCOL", "/dav/calendars/user0/test-calendar", strings.NewReader(mkcolRequestDataForeignColor))
	req.Header.Set("Content-Type", "application/xml")

	if err := b.Mkcol(req); err != nil {
		t.Fatalf("Unexpected error in Mkcol: %s", err)
	}
	if len(tb.calendars) != 1 {
		t.Fatalf("Found %d calendars, expected 1", len(tb.calendars))
	}
	if color := tb.calendars[0].Color; color != "" {
		t.Fatalf("Calendar color is '%s', expected none", color)
	}
}

var invalidCalendarTimezoneCases = []struct {
	name string
	data string
}{
	{"not an iCalendar object", "Europe/Berlin"},
	{"no component", "BEGIN:VCALENDAR\nEND:VCALENDAR\n"},
	{"another component", `BEGIN:VCALENDAR
BEGIN:VEVENT
UID:test
END:VEVENT
END:VCALENDAR
`},
	{"two VTIMEZONE components", `BEGIN:VCALENDAR
BEGIN:VTIMEZONE
TZID:US-Eastern
END:VTIMEZONE
BEGIN:VTIMEZONE
TZID:Europe/Berlin
END:VTIMEZONE
END:VCALENDAR
`},
}

func TestDecodeCalendarTimezone(t *testing.T) {
	if _, err := decodeCalendarTimezone(calendarTimezoneData); err != nil {
		t.Errorf("Unexpected error for a valid calendar timezone: %s", err)
	}

	for _, tc := range invalidCalendarTimezoneCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeCalendarTimezone(tc.data); err == nil {
				t.Error("Invalid calendar timezone accepted")
			}
		})
	}
}

var propPatchRequest = `
<?xml version="1.0" encoding="utf-8" ?>
<D:propertyupdate xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:A="http://apple.com/ns/ical/">
  <D:set>
    <D:prop>
      <D:displayname>Renamed</D:displayname>
      <A:calendar-color>#FF2968</A:calendar-color>%s
    </D:prop>
  </D:set>
  <D:remove>
    <D:prop>
      <C:calendar-description/>
    </D:prop>
  </D:remove>
</D:propertyupdate>
`

func TestPropPatchCalendar(t *testing.T) {
	backend := &testBackend{}
	handler := Handler{Backend: backend}

	req := httptest.NewRequest("PROPPATCH", "/user/calendars/default/", strings.NewReader(fmt.Sprintf(propPatchRequest, "")))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp := string(data)
	if res.StatusCode != http.StatusMultiStatus {
		t.Fatalf("unexpected status code: %d", res.StatusCode)
	} else if strings.Count(resp, "<status>") != 1 || !strings.Contains(resp, "<status>HTTP/1.1 200 OK</status>") {
		t.Fatalf("want a single 200 propstat:\n%v", resp)
	}
	update := backend.updates["/user/calendars/default/"]
	if update == nil {
		t.Fatalf("want the calendar updated")
	} else if update.Name == nil || *update.Name != "Renamed" {
		t.Errorf("unexpected name: %v", update.Name)
	} else if update.Color == nil || *update.Color != "#FF2968" {
		t.Errorf("unexpected color: %v", update.Color)
	} else if update.Description == nil || *update.Description != "" {
		t.Errorf("want the description removed: %v", update.Description)
	} else if update.Timezone != nil {
		t.Errorf("want the timezone unchanged")
	}
}

func TestPropPatchCalendarFailedDependency(t *testing.T) {
	backend := &testBackend{}
	handler := Handler{Backend: backend}

	body := fmt.Sprintf(propPatchRequest, "<D:getetag>1</D:getetag>")
	req := httptest.NewRequest("PROPPATCH", "/user/calendars/default/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp := string(data)
	if strings.Contains(resp, "200 OK") || !strings.Contains(resp, "424 Failed Dependency") {
		t.Fatalf("want the other properties to fail:\n%v", resp)
	} else if len(backend.updates) != 0 {
		t.Fatalf("want no update applied")
	}
}

func TestDeadProperties(t *testing.T) {
	backend := &testBackend{}
	handler := Handler{Backend: backend}
	serve := func(method, body string) string {
		req := httptest.NewRequest(method, "/user/calendars/default/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Depth", "0")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		data, err := io.ReadAll(w.Result().Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	order := xml.Name{Space: "http://apple.com/ns/ical/", Local: "calendar-order"}

	serve("MKCALENDAR", TestMkCalendarReq)
	if len(backend.calendars) != 1 {
		t.Fatalf("want 1 calendar, got %d", len(backend.calendars))
	}
	var found bool
	for _, dead := range backend.calendars[0].DeadProperties {
		if dead.Name == order {
			found = true
		} else if dead.Name == calendarColorName || dead.Name == internal.DisplayNameName {
			t.Errorf("want %v handled by the server", dead.Name)
		}
	}
	if !found {
		t.Fatalf("want calendar-order kept: %v", backend.calendars[0].DeadProperties)
	}

	resp := serve("PROPFIND", propFindAllProp)
	if !strings.Contains(resp, `<calendar-order xmlns="http://apple.com/ns/ical/">2</calendar-order>`) {
		t.Fatalf("want calendar-order in allprop response:\n%v", resp)
	}

	resp = serve("PROPPATCH", `<D:propertyupdate xmlns:D="DAV:" xmlns:A="http://apple.com/ns/ical/">
		<D:set><D:prop><A:calendar-order>3</A:calendar-order></D:prop></D:set>
		<D:remove><D:prop><A:refreshrate/></D:prop></D:remove>
	</D:propertyupdate>`)
	update := backend.updates["/user/calendars/default/"]
	if update == nil {
		t.Fatalf("want the calendar updated:\n%v", resp)
	} else if len(update.DeadProperties) != 1 || update.DeadProperties[0].Name != order {
		t.Errorf("unexpected dead properties: %v", update.DeadProperties)
	} else if string(update.DeadProperties[0].XML) != `<calendar-order xmlns="http://apple.com/ns/ical/">3</calendar-order>` {
		t.Errorf("unexpected XML: %s", update.DeadProperties[0].XML)
	} else if len(update.RemovedDeadProperties) != 1 || update.RemovedDeadProperties[0].Local != "refreshrate" {
		t.Errorf("unexpected removed dead properties: %v", update.RemovedDeadProperties)
	}
}

func TestEncodeCompFilterIsNotDefined(t *testing.T) {
	filter := CompFilter{
		Name: "VCALENDAR",
		Comps: []CompFilter{
			{Name: "VTODO", Props: []PropFilter{
				{Name: "STATUS", IsNotDefined: true},
				{Name: "ATTENDEE", ParamFilter: []ParamFilter{{Name: "PARTSTAT", IsNotDefined: true}}},
			}},
			{Name: "VEVENT", IsNotDefined: true},
		},
	}

	b, err := xml.Marshal(encodeCompFilter(&filter))
	if err != nil {
		t.Fatal(err)
	}
	var el compFilter
	if err := xml.Unmarshal(b, &el); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCompFilter(&el)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*decoded, filter) {
		t.Errorf("want %+v, got %+v", filter, *decoded)
	}
}

func TestRawObject(t *testing.T) {
	// The encoder would sort the properties
	const raw = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//raw//EN\r\nBEGIN:VEVENT\r\nUID:raw\r\n" +
		"DTSTAMP:20060206T001102Z\r\nDTSTART:20060102T100000Z\r\nSUMMARY:Raw\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	const path = "/user/calendars/default/raw.ics"
	cal, err := ical.NewDecoder(strings.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	backend := &testBackend{objectMap: map[string][]CalendarObject{
		"/user/calendars/default/": []CalendarObject{{Path: path, ETag: "1", Raw: []byte(raw)}},
	}}
	serve := func(method, body string) string {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/xml")
		w := httptest.NewRecorder()
		(&Handler{Backend: backend}).ServeHTTP(w, req)
		data, err := io.ReadAll(w.Result().Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	multiget := func(calendarData string) string {
		return serve("REPORT", `<C:calendar-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
			<D:prop>`+calendarData+`</D:prop><D:href>`+path+`</D:href></C:calendar-multiget>`)
	}

	if resp := serve(http.MethodGet, ""); resp != raw {
		t.Errorf("want the object as stored, got:\n%s", resp)
	}
	if resp := multiget(`<C:calendar-data/>`); !strings.Contains(resp, "VERSION:2.0&#xD;&#xA;PRODID:-//raw//EN") {
		t.Errorf("want the object as stored in calendar-data:\n%s", resp)
	}

	backend.objectMap["/user/calendars/default/"][0].Data = cal
	resp := multiget(`<C:calendar-data><C:comp name="VCALENDAR"><C:prop name="VERSION"/></C:comp></C:calendar-data>`)
	if strings.Contains(resp, "VERSION:2.0&#xD;&#xA;PRODID:-//raw//EN") || !strings.Contains(resp, "PRODID:-//raw//EN&#xD;&#xA;VERSION:2.0") {
		t.Errorf("want Data encoded for a partial calendar-data:\n%s", resp)
	}
}

func TestPutRaw(t *testing.T) {
	// Lines are folded where the encoder wouldn't
	body := strings.Replace(calendarTestData1, "SUMMARY:", "SUMMARY:\r\n ", 1)
	backend := &testBackend{}
	handler := Handler{Backend: backend}

	req := httptest.NewRequest(http.MethodPut, "/user/calendars/default/event.ics", strings.NewReader(body))
	req.Header.Set("Content-Type", ical.MIMEType)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if sc := w.Result().StatusCode; sc != http.StatusCreated {
		t.Fatalf("unexpected status code: %d", sc)
	} else if string(backend.put) != body {
		t.Errorf("want the body as sent, got:\n%s", backend.put)
	}

	req = httptest.NewRequest(http.MethodPut, "/user/calendars/default/event.ics", strings.NewReader(body))
	req.Header.Set("Content-Type", ical.MIMEType)
	w = httptest.NewRecorder()
	req.Body = http.MaxBytesReader(w, req.Body, 16)
	handler.ServeHTTP(w, req)
	if sc := w.Result().StatusCode; sc != http.StatusRequestEntityTooLarge {
		t.Errorf("unexpected status code for a body past the limit: %d", sc)
	}
}

type conditionalDeleteBackend struct {
	*testBackend
	ifMatch webdav.ConditionalMatch
}

func (b *conditionalDeleteBackend) DeleteCalendarObjectIfMatch(ctx context.Context, path string, ifMatch webdav.ConditionalMatch) error {
	b.ifMatch = ifMatch
	return nil
}

func TestDeleteIfMatch(t *testing.T) {
	const path = "/user/calendars/default/event.ics"
	backend := &testBackend{objectMap: map[string][]CalendarObject{
		"/user/calendars/default/": []CalendarObject{{Path: path, ETag: "1"}},
	}}
	serve := func(b Backend, ifMatch string) int {
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		req.Header.Set("If-Match", ifMatch)
		w := httptest.NewRecorder()
		(&Handler{Backend: b}).ServeHTTP(w, req)
		return w.Result().StatusCode
	}

	if sc := serve(backend, `"2"`); sc != http.StatusPreconditionFailed {
		t.Errorf("unexpected status code for a stale ETag: %d", sc)
	} else if len(backend.deleted) != 0 {
		t.Errorf("want the object kept")
	}
	if sc := serve(backend, `"2", "1"`); sc != http.StatusNoContent {
		t.Errorf("unexpected status code for the current ETag: %d", sc)
	} else if len(backend.deleted) != 1 {
		t.Errorf("want the object deleted")
	}

	conditional := &conditionalDeleteBackend{testBackend: &testBackend{}}
	if sc := serve(conditional, `"2"`); sc != http.StatusNoContent {
		t.Errorf("unexpected status code: %d", sc)
	} else if conditional.ifMatch != `"2"` {
		t.Errorf("want If-Match handed to the backend, got %q", conditional.ifMatch)
	} else if len(conditional.deleted) != 0 {
		t.Errorf("want DeleteCalendarObject left alone")
	}
}

type testBackend struct {
	calendars []Calendar
	objectMap map[string][]CalendarObject
	updates   map[string]*CalendarUpdate
	deleted   []string
	put       []byte
}

func (t *testBackend) UpdateCalendar(ctx context.Context, path string, update *CalendarUpdate) error {
	if t.updates == nil {
		t.updates = make(map[string]*CalendarUpdate)
	}
	t.updates[path] = update
	return nil
}

func (t *testBackend) CreateCalendar(ctx context.Context, calendar *Calendar) error {
	t.calendars = append(t.calendars, *calendar)
	return nil
}

func (t *testBackend) ListCalendars(ctx context.Context) ([]Calendar, error) {
	return t.calendars, nil
}

func (t *testBackend) GetCalendar(ctx context.Context, path string) (*Calendar, error) {
	for _, cal := range t.calendars {
		if cal.Path == path {
			return &cal, nil
		}
	}
	return nil, fmt.Errorf("calendar for path: %s not found", path)
}

func (t *testBackend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	return "/user/calendars/", nil
}

func (t *testBackend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	return "/user/", nil
}

func (t *testBackend) DeleteCalendarObject(ctx context.Context, path string) error {
	t.deleted = append(t.deleted, path)
	return nil
}

func (t *testBackend) GetCalendarObject(ctx context.Context, path string, req *CalendarCompRequest) (*CalendarObject, error) {
	for _, objs := range t.objectMap {
		for _, obj := range objs {
			if obj.Path == path {
				return &obj, nil
			}
		}
	}
	return nil, fmt.Errorf("couldn't find calendar object at: %s", path)
}

func (t *testBackend) GetCalendarObjects(ctx context.Context, paths []string, req *CalendarCompRequest) ([]CalendarObject, error) {
	objs := make([]CalendarObject, 0)
	for _, path := range paths {
		if obj, err := t.GetCalendarObject(ctx, path, req); err == nil {
			objs = append(objs, *obj)
		}
	}
	return objs, nil
}

func (t *testBackend) PutCalendarObject(ctx context.Context, path string, calendar *ical.Calendar, opts *PutCalendarObjectOptions) (*CalendarObject, error) {
	t.put = opts.Raw
	return &CalendarObject{Path: path}, nil
}

func (t *testBackend) ListCalendarObjects(ctx context.Context, path string, req *CalendarCompRequest) ([]CalendarObject, error) {
	return t.objectMap[path], nil
}

func (t *testBackend) QueryCalendarObjects(ctx context.Context, path string, query *CalendarQuery) ([]CalendarObject, error) {
	if cos, err := t.ListCalendarObjects(ctx, path, nil); err != nil {
		return nil, err
	} else {
		return Filter(query, cos)
	}
}
