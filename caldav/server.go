package caldav

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/internal"
)

// TODO if nothing more Caldav-specific needs to be added this should be merged with carddav.PutAddressObjectOptions
type PutCalendarObjectOptions struct {
	// IfNoneMatch indicates that the client does not want to overwrite
	// an existing resource.
	IfNoneMatch webdav.ConditionalMatch
	// IfMatch provides the ETag of the resource that the client intends
	// to overwrite, can be ""
	IfMatch webdav.ConditionalMatch
	// Raw is the request body as the client sent it, set by the server only.
	// A backend storing it unchanged can return a strong ETag (RFC 4791
	// section 5.3.4).
	Raw []byte
}

// Backend is a CalDAV server backend.
type Backend interface {
	CalendarHomeSetPath(ctx context.Context) (string, error)

	CreateCalendar(ctx context.Context, calendar *Calendar) error
	ListCalendars(ctx context.Context) ([]Calendar, error)
	GetCalendar(ctx context.Context, path string) (*Calendar, error)

	GetCalendarObject(ctx context.Context, path string, req *CalendarCompRequest) (*CalendarObject, error)
	ListCalendarObjects(ctx context.Context, path string, req *CalendarCompRequest) ([]CalendarObject, error)
	QueryCalendarObjects(ctx context.Context, path string, query *CalendarQuery) ([]CalendarObject, error)
	PutCalendarObject(ctx context.Context, path string, calendar *ical.Calendar, opts *PutCalendarObjectOptions) (*CalendarObject, error)
	DeleteCalendarObject(ctx context.Context, path string) error

	webdav.UserPrincipalBackend
}

// ConditionalDeleteBackend is an optional interface a Backend can implement to
// check the If-Match header of a DELETE request and delete the object in one
// step. Without it the server compares the ETag itself, then deletes.
type ConditionalDeleteBackend interface {
	DeleteCalendarObjectIfMatch(ctx context.Context, path string, ifMatch webdav.ConditionalMatch) error
}

// UpdateBackend is an optional interface a Backend can implement to support
// PROPPATCH on calendars.
type UpdateBackend interface {
	UpdateCalendar(ctx context.Context, path string, update *CalendarUpdate) error
}

// MultiGetBackend is an optional interface a Backend can implement to fetch
// the objects of a calendar-multiget in one call. A path missing from the
// result is reported as not found.
type MultiGetBackend interface {
	GetCalendarObjects(ctx context.Context, paths []string, req *CalendarCompRequest) ([]CalendarObject, error)
}

// Handler handles CalDAV HTTP requests. It can be used to create a CalDAV
// server.
type Handler struct {
	Backend Backend
	Prefix  string
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Backend == nil {
		http.Error(w, "caldav: no backend available", http.StatusInternalServerError)
		return
	}

	if r.URL.Path == "/.well-known/caldav" {
		principalPath, err := h.Backend.CurrentUserPrincipal(r.Context())
		if err != nil {
			http.Error(w, "caldav: failed to determine current user principal", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, principalPath, http.StatusPermanentRedirect)
		return
	}

	var err error
	switch r.Method {
	case "REPORT":
		err = h.handleReport(w, r)
	case "MKCALENDAR":
		err = h.handleMkCalendar(w, r)
	default:
		b := backend{
			Backend: h.Backend,
			Prefix:  strings.TrimSuffix(h.Prefix, "/"),
		}
		hh := internal.Handler{Backend: &b}
		hh.ServeHTTP(w, r)
	}

	if err != nil {
		internal.ServeError(w, err)
	}
}

func (h *Handler) handleMkCalendar(w http.ResponseWriter, r *http.Request) error {
	b := backend{
		Backend: h.Backend,
		Prefix:  strings.TrimSuffix(h.Prefix, "/"),
	}
	if b.resourceTypeAtPath(r.URL.Path) != resourceTypeCalendar {
		return internal.HTTPErrorf(http.StatusForbidden, "caldav: calendar creation not allowed at given location")
	}

	cal := Calendar{
		Path: r.URL.Path,
	}

	if !internal.IsRequestBodyEmpty(r) {
		var m mkcalendarReq
		if err := internal.DecodeXMLRequest(r, &m); err != nil {
			return internal.HTTPErrorf(http.StatusBadRequest, "caldav: error parsing mkcalendar request: %s", err.Error())
		}

		if err := decodeMkcolProp(&m.Set.Prop, &cal); err != nil {
			return err
		}
	}

	if err := h.Backend.CreateCalendar(r.Context(), &cal); err != nil {
		return err
	}

	w.Header().Add("Location", cal.Path)
	w.Header().Add("Content-Length", "0")
	w.WriteHeader(http.StatusCreated)
	//
	// If a response body for a successful request is included, it MUST
	// be a CALDAV:mkcalendar-response XML element.
	// 	<!ELEMENT mkcalendar-response ANY>
	//
	return nil
}

func (h *Handler) handleReport(w http.ResponseWriter, r *http.Request) error {
	var report reportReq
	if err := internal.DecodeXMLRequest(r, &report); err != nil {
		return err
	}

	if report.Query != nil {
		return h.handleQuery(r, w, report.Query)
	} else if report.Multiget != nil {
		return h.handleMultiget(r.Context(), w, report.Multiget)
	}
	return internal.HTTPErrorf(http.StatusBadRequest, "caldav: expected calendar-query or calendar-multiget element in REPORT request")
}

func decodeParamFilter(el *paramFilter) (*ParamFilter, error) {
	pf := &ParamFilter{Name: el.Name}
	if el.IsNotDefined != nil {
		if el.TextMatch != nil {
			return nil, fmt.Errorf("caldav: failed to parse param-filter: if is-not-defined is provided, text-match can't be provided")
		}
		pf.IsNotDefined = true
	}
	if el.TextMatch != nil {
		pf.TextMatch = &TextMatch{Text: el.TextMatch.Text}
	}
	return pf, nil
}

func decodePropFilter(el *propFilter) (*PropFilter, error) {
	pf := &PropFilter{Name: el.Name}
	if el.IsNotDefined != nil {
		if el.TextMatch != nil || el.TimeRange != nil || len(el.ParamFilter) > 0 {
			return nil, fmt.Errorf("caldav: failed to parse prop-filter: if is-not-defined is provided, text-match, time-range, or param-filter can't be provided")
		}
		pf.IsNotDefined = true
	}
	if el.TextMatch != nil {
		pf.TextMatch = &TextMatch{Text: el.TextMatch.Text}
	}
	if el.TimeRange != nil {
		pf.Start = time.Time(el.TimeRange.Start)
		pf.End = time.Time(el.TimeRange.End)
	}
	for _, paramEl := range el.ParamFilter {
		paramFi, err := decodeParamFilter(&paramEl)
		if err != nil {
			return nil, err
		}
		pf.ParamFilter = append(pf.ParamFilter, *paramFi)
	}
	return pf, nil
}

func decodeCompFilter(el *compFilter) (*CompFilter, error) {
	cf := &CompFilter{Name: el.Name}
	if el.IsNotDefined != nil {
		if el.TimeRange != nil || len(el.PropFilters) > 0 || len(el.CompFilters) > 0 {
			return nil, fmt.Errorf("caldav: failed to parse comp-filter: if is-not-defined is provided, time-range, prop-filter, or comp-filter can't be provided")
		}
		cf.IsNotDefined = true
	}
	if el.TimeRange != nil {
		cf.Start = time.Time(el.TimeRange.Start)
		cf.End = time.Time(el.TimeRange.End)
	}
	for _, pfEl := range el.PropFilters {
		pf, err := decodePropFilter(&pfEl)
		if err != nil {
			return nil, err
		}
		cf.Props = append(cf.Props, *pf)
	}
	for _, childEl := range el.CompFilters {
		child, err := decodeCompFilter(&childEl)
		if err != nil {
			return nil, err
		}
		cf.Comps = append(cf.Comps, *child)
	}
	return cf, nil
}

func decodeComp(comp *comp) (*CalendarCompRequest, error) {
	if comp == nil {
		return nil, internal.HTTPErrorf(http.StatusBadRequest, "caldav: unexpected empty calendar-data in request")
	}
	if comp.Allprop != nil && len(comp.Prop) > 0 {
		return nil, internal.HTTPErrorf(http.StatusBadRequest, "caldav: only one of allprop or prop can be specified in calendar-data")
	}
	if comp.Allcomp != nil && len(comp.Comp) > 0 {
		return nil, internal.HTTPErrorf(http.StatusBadRequest, "caldav: only one of allcomp or comp can be specified in calendar-data")
	}

	req := &CalendarCompRequest{
		AllProps: comp.Allprop != nil,
		AllComps: comp.Allcomp != nil,
	}
	for _, p := range comp.Prop {
		req.Props = append(req.Props, p.Name)
	}
	for _, c := range comp.Comp {
		comp, err := decodeComp(&c)
		if err != nil {
			return nil, err
		}
		req.Comps = append(req.Comps, *comp)
	}
	return req, nil
}

func decodeCalendarDataReq(calendarData *calendarDataReq) (*CalendarCompRequest, error) {
	if calendarData.Comp == nil {
		return &CalendarCompRequest{
			AllProps: true,
			AllComps: true,
		}, nil
	}
	return decodeComp(calendarData.Comp)
}

func (h *Handler) handleQuery(r *http.Request, w http.ResponseWriter, query *calendarQuery) error {
	var q CalendarQuery
	// TODO: calendar-data in query.Prop
	cf, err := decodeCompFilter(&query.Filter.CompFilter)
	if err != nil {
		return err
	}
	q.CompFilter = *cf

	cos, err := h.Backend.QueryCalendarObjects(r.Context(), r.URL.Path, &q)
	if err != nil {
		return err
	}

	var resps []internal.Response
	for _, co := range cos {
		b := backend{
			Backend: h.Backend,
			Prefix:  strings.TrimSuffix(h.Prefix, "/"),
		}
		propfind := internal.PropFind{
			XMLName:  query.XMLName,
			Prop:     query.Prop,
			AllProp:  query.AllProp,
			PropName: query.PropName,
		}
		resp, err := b.propFindCalendarObject(r.Context(), &propfind, &co)
		if err != nil {
			return err
		}
		resps = append(resps, *resp)
	}

	ms := internal.NewMultiStatus(resps...)

	return internal.ServeMultiStatus(w, ms)
}

func (h *Handler) handleMultiget(ctx context.Context, w http.ResponseWriter, multiget *calendarMultiget) error {
	var dataReq CalendarCompRequest
	if multiget.Prop != nil {
		var calendarData calendarDataReq
		if err := multiget.Prop.Decode(&calendarData); err != nil && !internal.IsNotFound(err) {
			return err
		}
		decoded, err := decodeCalendarDataReq(&calendarData)
		if err != nil {
			return err
		}
		dataReq = *decoded
	}

	// Prefetch all objects and index by path for quick lookup in response generation.
	var lookups map[string]*CalendarObject
	if mb, ok := h.Backend.(MultiGetBackend); ok {
		paths := make([]string, len(multiget.Hrefs))
		for i, href := range multiget.Hrefs {
			paths[i] = href.Path
		}
		objects, err := mb.GetCalendarObjects(ctx, paths, &dataReq)
		if err != nil {
			return err
		}
		lookups = make(map[string]*CalendarObject, len(objects))
		for i := range objects {
			lookups[objects[i].Path] = &objects[i]
		}
	}

	// response preperation with lookup
	var resps []internal.Response
	for _, href := range multiget.Hrefs {
		var co *CalendarObject
		var err error
		if lookups == nil {
			co, err = h.Backend.GetCalendarObject(ctx, href.Path, &dataReq)
		} else if co = lookups[href.Path]; co == nil {
			err = internal.HTTPErrorf(http.StatusNotFound, "Couldn't find calendar object at: %s", href.Path)
		}
		if err != nil {
			resp := internal.NewErrorResponse(href.Path, err)
			resps = append(resps, *resp)
			continue
		}

		b := backend{
			Backend: h.Backend,
			Prefix:  strings.TrimSuffix(h.Prefix, "/"),
		}
		propfind := internal.PropFind{
			XMLName:  multiget.XMLName,
			Prop:     multiget.Prop,
			AllProp:  multiget.AllProp,
			PropName: multiget.PropName,
		}
		resp, err := b.propFindCalendarObject(ctx, &propfind, co)
		if err != nil {
			return err
		}
		resps = append(resps, *resp)
	}

	ms := internal.NewMultiStatus(resps...)
	return internal.ServeMultiStatus(w, ms)
}

type backend struct {
	Backend Backend
	Prefix  string
}

type resourceType int

const (
	resourceTypeRoot resourceType = iota
	resourceTypeUserPrincipal
	resourceTypeCalendarHomeSet
	resourceTypeCalendar
	resourceTypeCalendarObject
)

func (b *backend) resourceTypeAtPath(reqPath string) resourceType {
	p := path.Clean(reqPath)
	p = strings.TrimPrefix(p, b.Prefix)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p == "/" {
		return resourceTypeRoot
	}
	return resourceType(len(strings.Split(p, "/")) - 1)
}

func (b *backend) Options(r *http.Request) (caps []string, allow []string, err error) {
	caps = []string{"calendar-access"}

	if b.resourceTypeAtPath(r.URL.Path) != resourceTypeCalendarObject {
		return caps, []string{http.MethodOptions, "PROPFIND", "PROPPATCH", "REPORT", "DELETE", "MKCOL"}, nil
	}

	var dataReq CalendarCompRequest
	_, err = b.Backend.GetCalendarObject(r.Context(), r.URL.Path, &dataReq)
	if httpErr, ok := err.(*internal.HTTPError); ok && httpErr.Code == http.StatusNotFound {
		return caps, []string{http.MethodOptions, http.MethodPut}, nil
	} else if err != nil {
		return nil, nil, err
	}

	return caps, []string{
		http.MethodOptions,
		http.MethodHead,
		http.MethodGet,
		http.MethodPut,
		http.MethodDelete,
		"PROPFIND",
	}, nil
}

func (b *backend) HeadGet(w http.ResponseWriter, r *http.Request) error {
	var dataReq CalendarCompRequest
	if r.Method != http.MethodHead {
		dataReq.AllProps = true
	}
	co, err := b.Backend.GetCalendarObject(r.Context(), r.URL.Path, &dataReq)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", ical.MIMEType)
	if co.ContentLength > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(co.ContentLength, 10))
	}
	if co.ETag != "" {
		w.Header().Set("ETag", internal.ETag(co.ETag).String())
	}
	if !co.ModTime.IsZero() {
		w.Header().Set("Last-Modified", co.ModTime.UTC().Format(http.TimeFormat))
	}

	if r.Method != http.MethodHead {
		return ical.NewEncoder(w).Encode(co.Data)
	}
	return nil
}

func (b *backend) PropFind(r *http.Request, propfind *internal.PropFind, depth internal.Depth) (*internal.MultiStatus, error) {
	resType := b.resourceTypeAtPath(r.URL.Path)

	var dataReq CalendarCompRequest
	var resps []internal.Response

	switch resType {
	case resourceTypeRoot:
		resp, err := b.propFindRoot(r.Context(), propfind)
		if err != nil {
			return nil, err
		}
		resps = append(resps, *resp)
	case resourceTypeUserPrincipal:
		principalPath, err := b.Backend.CurrentUserPrincipal(r.Context())
		if err != nil {
			return nil, err
		}
		if r.URL.Path == principalPath {
			resp, err := b.propFindUserPrincipal(r.Context(), propfind)
			if err != nil {
				return nil, err
			}
			resps = append(resps, *resp)
			if depth != internal.DepthZero {
				resp, err := b.propFindHomeSet(r.Context(), propfind)
				if err != nil {
					return nil, err
				}
				resps = append(resps, *resp)
				if depth == internal.DepthInfinity {
					resps_, err := b.propFindAllCalendars(r.Context(), propfind, true)
					if err != nil {
						return nil, err
					}
					resps = append(resps, resps_...)
				}
			}
		}
	case resourceTypeCalendarHomeSet:
		homeSetPath, err := b.Backend.CalendarHomeSetPath(r.Context())
		if err != nil {
			return nil, err
		}
		if r.URL.Path == homeSetPath {
			resp, err := b.propFindHomeSet(r.Context(), propfind)
			if err != nil {
				return nil, err
			}
			resps = append(resps, *resp)
			if depth != internal.DepthZero {
				recurse := depth == internal.DepthInfinity
				resps_, err := b.propFindAllCalendars(r.Context(), propfind, recurse)
				if err != nil {
					return nil, err
				}
				resps = append(resps, resps_...)
			}
		}
	case resourceTypeCalendar:
		ab, err := b.Backend.GetCalendar(r.Context(), r.URL.Path)
		if err != nil {
			return nil, err
		}
		resp, err := b.propFindCalendar(r.Context(), propfind, ab)
		if err != nil {
			return nil, err
		}
		resps = append(resps, *resp)
		if depth != internal.DepthZero {
			resps_, err := b.propFindAllCalendarObjects(r.Context(), propfind, ab)
			if err != nil {
				return nil, err
			}
			resps = append(resps, resps_...)
		}
	case resourceTypeCalendarObject:
		ao, err := b.Backend.GetCalendarObject(r.Context(), r.URL.Path, &dataReq)
		if err != nil {
			return nil, err
		}

		resp, err := b.propFindCalendarObject(r.Context(), propfind, ao)
		if err != nil {
			return nil, err
		}
		resps = append(resps, *resp)
	}

	return internal.NewMultiStatus(resps...), nil
}

func (b *backend) propFindRoot(ctx context.Context, propfind *internal.PropFind) (*internal.Response, error) {
	principalPath, err := b.Backend.CurrentUserPrincipal(ctx)
	if err != nil {
		return nil, err
	}

	props := map[xml.Name]internal.PropFindFunc{
		internal.ResourceTypeName: internal.PropFindValue(internal.NewResourceType(internal.CollectionName)),
	}
	if propfind.AllProp == nil {
		props[internal.CurrentUserPrincipalName] = internal.PropFindValue(&internal.CurrentUserPrincipal{
			Href: internal.Href{Path: principalPath},
		})
	}

	return internal.NewPropFindResponse(principalPath, propfind, props)
}

func (b *backend) propFindUserPrincipal(ctx context.Context, propfind *internal.PropFind) (*internal.Response, error) {
	principalPath, err := b.Backend.CurrentUserPrincipal(ctx)
	if err != nil {
		return nil, err
	}

	props := map[xml.Name]internal.PropFindFunc{
		internal.ResourceTypeName: internal.PropFindValue(internal.NewResourceType(internal.CollectionName, internal.PrincipalName)),
	}

	if propfind.AllProp == nil {
		props[internal.CurrentUserPrincipalName] = internal.PropFindValue(&internal.CurrentUserPrincipal{
			Href: internal.Href{Path: principalPath},
		})
		props[calendarHomeSetName] = func(*internal.RawXMLValue) (interface{}, error) {
			homeSetPath, err := b.Backend.CalendarHomeSetPath(ctx)
			if err != nil {
				return nil, err
			}
			return &calendarHomeSet{Href: internal.Href{Path: homeSetPath}}, nil
		}
	}

	return internal.NewPropFindResponse(principalPath, propfind, props)
}

func (b *backend) propFindHomeSet(ctx context.Context, propfind *internal.PropFind) (*internal.Response, error) {
	homeSetPath, err := b.Backend.CalendarHomeSetPath(ctx)
	if err != nil {
		return nil, err
	}

	// TODO anything else to return here?
	props := map[xml.Name]internal.PropFindFunc{
		internal.ResourceTypeName: internal.PropFindValue(internal.NewResourceType(internal.CollectionName)),
	}

	if propfind.AllProp == nil {
		props[internal.CurrentUserPrincipalName] = func(*internal.RawXMLValue) (interface{}, error) {
			path, err := b.Backend.CurrentUserPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			return &internal.CurrentUserPrincipal{Href: internal.Href{Path: path}}, nil
		}
	}

	return internal.NewPropFindResponse(homeSetPath, propfind, props)
}

func (b *backend) propFindCalendar(ctx context.Context, propfind *internal.PropFind, cal *Calendar) (*internal.Response, error) {
	props := map[xml.Name]internal.PropFindFunc{
		internal.ResourceTypeName: internal.PropFindValue(internal.NewResourceType(internal.CollectionName, calendarName)),
	}
	if propfind.AllProp == nil {
		props[internal.CurrentUserPrincipalName] = func(*internal.RawXMLValue) (interface{}, error) {
			path, err := b.Backend.CurrentUserPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			return &internal.CurrentUserPrincipal{Href: internal.Href{Path: path}}, nil
		}
		props[supportedCalendarDataName] = internal.PropFindValue(&supportedCalendarData{
			Types: []calendarDataType{
				{ContentType: ical.MIMEType, Version: "2.0"},
			},
		})
		props[supportedCalendarComponentSetName] = func(*internal.RawXMLValue) (interface{}, error) {
			components := []comp{}
			if cal.SupportedComponentSet != nil {
				for _, name := range cal.SupportedComponentSet {
					components = append(components, comp{Name: name})
				}
			} else {
				components = append(components, comp{Name: ical.CompEvent})
			}
			return &supportedCalendarComponentSet{
				Comp: components,
			}, nil
		}
		props[internal.CurrentUserPrivilegeSetName] = internal.PropFindValue(internal.NewCurrentUserPrivilegeSet(cal.ReadOnly))
		if cal.Description != "" {
			props[calendarDescriptionName] = internal.PropFindValue(&calendarDescription{
				Description: cal.Description,
			})
		}
		if cal.Color != "" {
			props[calendarColorName] = internal.PropFindValue(&calendarColor{
				Color: cal.Color,
			})
		}
		if cal.Timezone != nil {
			props[calendarTimezoneName] = func(*internal.RawXMLValue) (interface{}, error) {
				var buf bytes.Buffer
				if err := ical.NewEncoder(&buf).Encode(cal.Timezone); err != nil {
					return nil, err
				}
				return &calendarTimezone{
					Timezone: buf.String(),
				}, nil
			}
		}
		if cal.MaxResourceSize > 0 {
			props[maxResourceSizeName] = internal.PropFindValue(&maxResourceSize{
				Size: cal.MaxResourceSize,
			})
		}
		if cal.CTag != "" {
			props[internal.GetCTagName] = internal.PropFindValue(&internal.GetCTag{CTag: cal.CTag})
		}
	}

	if cal.Name != "" {
		props[internal.DisplayNameName] = internal.PropFindValue(&internal.DisplayName{
			Name: cal.Name,
		})
	}
	for _, dead := range cal.DeadProperties {
		if _, ok := props[dead.Name]; !ok {
			props[dead.Name] = internal.PropFindXML(dead.XML)
		}
	}

	// TODO: CALDAV:min-date-time, CALDAV:max-date-time, CALDAV:max-instances, CALDAV:max-attendees-per-instance

	return internal.NewPropFindResponse(cal.Path, propfind, props)
}

func (b *backend) propFindAllCalendars(ctx context.Context, propfind *internal.PropFind, recurse bool) ([]internal.Response, error) {
	abs, err := b.Backend.ListCalendars(ctx)
	if err != nil {
		return nil, err
	}

	var resps []internal.Response
	for _, ab := range abs {
		resp, err := b.propFindCalendar(ctx, propfind, &ab)
		if err != nil {
			return nil, err
		}
		resps = append(resps, *resp)
		if recurse {
			resps_, err := b.propFindAllCalendarObjects(ctx, propfind, &ab)
			if err != nil {
				return nil, err
			}
			resps = append(resps, resps_...)
		}
	}
	return resps, nil
}

func (b *backend) propFindCalendarObject(ctx context.Context, propfind *internal.PropFind, co *CalendarObject) (*internal.Response, error) {
	props := map[xml.Name]internal.PropFindFunc{
		internal.GetContentTypeName: internal.PropFindValue(&internal.GetContentType{
			Type: ical.MIMEType,
		}),
	}

	if propfind.AllProp == nil {
		props[internal.CurrentUserPrincipalName] = func(*internal.RawXMLValue) (interface{}, error) {
			path, err := b.Backend.CurrentUserPrincipal(ctx)
			if err != nil {
				return nil, err
			}
			return &internal.CurrentUserPrincipal{Href: internal.Href{Path: path}}, nil
		}
	}

	if n := propfind.XMLName; n == calendarQueryName || n == calendarMultigetName {
		props[calendarDataName] = func(*internal.RawXMLValue) (interface{}, error) {
			var buf bytes.Buffer
			if err := ical.NewEncoder(&buf).Encode(co.Data); err != nil {
				return nil, err
			}

			return &calendarDataResp{Data: buf.Bytes()}, nil
		}
	}

	if co.ContentLength > 0 {
		props[internal.GetContentLengthName] = internal.PropFindValue(&internal.GetContentLength{
			Length: co.ContentLength,
		})
	}

	if !co.ModTime.IsZero() {
		props[internal.GetLastModifiedName] = internal.PropFindValue(&internal.GetLastModified{
			LastModified: internal.Time(co.ModTime),
		})
	}

	if co.ETag != "" {
		props[internal.GetETagName] = internal.PropFindValue(&internal.GetETag{
			ETag: internal.ETag(co.ETag),
		})
	}

	return internal.NewPropFindResponse(co.Path, propfind, props)
}

func (b *backend) propFindAllCalendarObjects(ctx context.Context, propfind *internal.PropFind, cal *Calendar) ([]internal.Response, error) {
	var dataReq CalendarCompRequest
	aos, err := b.Backend.ListCalendarObjects(ctx, cal.Path, &dataReq)
	if err != nil {
		return nil, err
	}

	var resps []internal.Response
	for _, ao := range aos {
		resp, err := b.propFindCalendarObject(ctx, propfind, &ao)
		if err != nil {
			return nil, err
		}
		resps = append(resps, *resp)
	}
	return resps, nil
}

func (b *backend) PropPatch(r *http.Request, update *internal.PropertyUpdate) (*internal.Response, error) {
	ub, ok := b.Backend.(UpdateBackend)
	if !ok || b.resourceTypeAtPath(r.URL.Path) != resourceTypeCalendar {
		return nil, internal.HTTPErrorf(http.StatusNotImplemented, "caldav: PropPatch not implemented")
	}

	var cu CalendarUpdate
	resp, ok, err := internal.NewPropPatchResponse(r.URL.Path, update, func(name xml.Name, raw *internal.RawXMLValue) error {
		return propPatchCalendar(&cu, name, raw)
	})
	if err != nil || !ok {
		return resp, err
	}
	if err := ub.UpdateCalendar(r.Context(), r.URL.Path, &cu); err != nil {
		return nil, err
	}
	return resp, nil
}

// protectedProps are the properties of a calendar the server computes.
var protectedProps = map[xml.Name]bool{
	internal.ResourceTypeName:            true,
	internal.GetContentLengthName:        true,
	internal.GetContentTypeName:          true,
	internal.GetLastModifiedName:         true,
	internal.GetETagName:                 true,
	internal.GetCTagName:                 true,
	internal.CurrentUserPrincipalName:    true,
	internal.CurrentUserPrivilegeSetName: true,
	supportedCalendarDataName:            true,
	supportedCalendarComponentSetName:    true,
	maxResourceSizeName:                  true,
}

func decodeDeadProperty(name xml.Name, raw *internal.RawXMLValue) (*webdav.DeadProperty, error) {
	if protectedProps[name] {
		return nil, internal.HTTPErrorf(http.StatusForbidden, "caldav: %v is protected", name.Local)
	}
	b, err := raw.Bytes()
	if err != nil {
		return nil, err
	}
	return &webdav.DeadProperty{Name: name, XML: b}, nil
}

func propPatchCalendar(cu *CalendarUpdate, name xml.Name, raw *internal.RawXMLValue) error {
	// TODO handle all properties
	var (
		displayName internal.DisplayName
		desc        calendarDescription
		color       calendarColor
		tz          calendarTimezone
	)
	var v interface{}
	switch name {
	case internal.DisplayNameName:
		v, cu.Name = &displayName, &displayName.Name
	case calendarDescriptionName:
		v, cu.Description = &desc, &desc.Description
	case calendarColorName:
		v, cu.Color = &color, &color.Color
	case calendarTimezoneName:
		v, cu.Timezone = &tz, ical.NewCalendar()
	default:
		if raw == nil {
			cu.RemovedDeadProperties = append(cu.RemovedDeadProperties, name)
			return nil
		}
		dead, err := decodeDeadProperty(name, raw)
		if err != nil {
			return err
		}
		cu.DeadProperties = append(cu.DeadProperties, *dead)
		return nil
	}
	if raw == nil {
		return nil
	}
	if err := raw.Decode(v); err != nil {
		return &internal.HTTPError{http.StatusBadRequest, err}
	}
	if name == calendarTimezoneName {
		cal, err := decodeCalendarTimezone(strings.TrimSpace(tz.Timezone))
		if err != nil {
			return err
		}
		cu.Timezone = cal
	}
	return nil
}

func (b *backend) Put(w http.ResponseWriter, r *http.Request) error {
	ifNoneMatch := webdav.ConditionalMatch(r.Header.Get("If-None-Match"))
	ifMatch := webdav.ConditionalMatch(r.Header.Get("If-Match"))

	opts := PutCalendarObjectOptions{
		IfNoneMatch: ifNoneMatch,
		IfMatch:     ifMatch,
	}

	t, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return internal.HTTPErrorf(http.StatusBadRequest, "caldav: malformed Content-Type: %v", err)
	}
	if t != ical.MIMEType {
		// TODO: send CALDAV:supported-calendar-data error
		return internal.HTTPErrorf(http.StatusBadRequest, "caldav: unsupported Content-Type %q", t)
	}

	// TODO: check CALDAV:max-resource-size precondition
	opts.Raw, err = internal.ReadRequestBody(r)
	if err != nil {
		return err
	}
	cal, err := ical.NewDecoder(bytes.NewReader(opts.Raw)).Decode()
	if err != nil {
		// TODO: send CALDAV:valid-calendar-data error
		return internal.HTTPErrorf(http.StatusBadRequest, "caldav: failed to parse iCalendar: %v", err)
	}

	co, err := b.Backend.PutCalendarObject(r.Context(), r.URL.Path, cal, &opts)
	if err != nil {
		return err
	}

	if co.ETag != "" {
		w.Header().Set("ETag", internal.ETag(co.ETag).String())
	}
	if !co.ModTime.IsZero() {
		w.Header().Set("Last-Modified", co.ModTime.UTC().Format(http.TimeFormat))
	}
	if co.Path != "" {
		w.Header().Set("Location", co.Path)
	}

	// TODO: http.StatusNoContent if the resource already existed
	w.WriteHeader(http.StatusCreated)

	return nil
}

func (b *backend) Delete(r *http.Request) error {
	ifMatch := webdav.ConditionalMatch(r.Header.Get("If-Match"))
	if ifMatch.IsSet() && b.resourceTypeAtPath(r.URL.Path) == resourceTypeCalendarObject {
		if cb, ok := b.Backend.(ConditionalDeleteBackend); ok {
			return cb.DeleteCalendarObjectIfMatch(r.Context(), r.URL.Path, ifMatch)
		}

		var dataReq CalendarCompRequest
		co, err := b.Backend.GetCalendarObject(r.Context(), r.URL.Path, &dataReq)
		if err != nil {
			return err
		}
		if _, ok, err := ifMatch.MatchETag(co.ETag); err != nil {
			return &internal.HTTPError{http.StatusBadRequest, err}
		} else if !ok && !ifMatch.IsWildcard() {
			return internal.HTTPErrorf(http.StatusPreconditionFailed, "caldav: If-Match condition failed")
		}
	}
	return b.Backend.DeleteCalendarObject(r.Context(), r.URL.Path)
}

func (b *backend) Mkcol(r *http.Request) error {
	if b.resourceTypeAtPath(r.URL.Path) != resourceTypeCalendar {
		return internal.HTTPErrorf(http.StatusForbidden, "caldav: calendar creation not allowed at given location")
	}

	cal := Calendar{
		Path: r.URL.Path,
	}

	if !internal.IsRequestBodyEmpty(r) {
		var m mkcolReq
		if err := internal.DecodeXMLRequest(r, &m); err != nil {
			return internal.HTTPErrorf(http.StatusBadRequest, "caldav: error parsing mkcol request: %s", err.Error())
		}

		prop := m.Set.Prop
		if !prop.ResourceType.Is(internal.CollectionName) || !prop.ResourceType.Is(calendarName) {
			return internal.HTTPErrorf(http.StatusBadRequest, "caldav: unexpected resource type")
		}
		if err := decodeMkcolProp(&prop, &cal); err != nil {
			return err
		}
	}

	return b.Backend.CreateCalendar(r.Context(), &cal)
}

func decodeMkcolProp(prop *mkcolProp, cal *Calendar) error {
	cal.Name = prop.DisplayName
	cal.Description = prop.CalendarDescription
	cal.Color = strings.TrimSpace(prop.CalendarColor)

	if s := strings.TrimSpace(prop.CalendarTimezone); s != "" {
		tz, err := decodeCalendarTimezone(s)
		if err != nil {
			return err
		}
		cal.Timezone = tz
	}

	cal.SupportedComponentSet = make([]string, len(prop.SupportedCalendarComponentSet.Comp))
	for i, v := range prop.SupportedCalendarComponentSet.Comp {
		cal.SupportedComponentSet[i] = v.Name
	}

	for i := range prop.Raw {
		name, ok := prop.Raw[i].XMLName()
		if !ok {
			continue
		}
		dead, err := decodeDeadProperty(name, &prop.Raw[i])
		if err != nil {
			return err
		}
		cal.DeadProperties = append(cal.DeadProperties, *dead)
	}
	return nil
}

func decodeCalendarTimezone(s string) (*ical.Calendar, error) {
	cal, err := ical.NewDecoder(strings.NewReader(s)).Decode()
	if err != nil {
		return nil, NewPreconditionError(PreconditionValidCalendarData)
	}
	if len(cal.Children) != 1 || cal.Children[0].Name != ical.CompTimezone {
		return nil, NewPreconditionError(PreconditionValidCalendarData)
	}
	return cal, nil
}

func (b *backend) Copy(r *http.Request, dest *internal.Href, recursive, overwrite bool) (created bool, err error) {
	return false, internal.HTTPErrorf(http.StatusNotImplemented, "caldav: Copy not implemented")
}

func (b *backend) Move(r *http.Request, dest *internal.Href, overwrite bool) (created bool, err error) {
	return false, internal.HTTPErrorf(http.StatusNotImplemented, "caldav: Move not implemented")
}

// https://datatracker.ietf.org/doc/html/rfc4791#section-5.3.2.1
type PreconditionType string

const (
	PreconditionNoUIDConflict                PreconditionType = "no-uid-conflict"
	PreconditionSupportedCalendarData        PreconditionType = "supported-calendar-data"
	PreconditionSupportedCalendarComponent   PreconditionType = "supported-calendar-component"
	PreconditionValidCalendarData            PreconditionType = "valid-calendar-data"
	PreconditionValidCalendarObjectResource  PreconditionType = "valid-calendar-object-resource"
	PreconditionCalendarCollectionLocationOk PreconditionType = "calendar-collection-location-ok"
	PreconditionMaxResourceSize              PreconditionType = "max-resource-size"
	PreconditionMinDateTime                  PreconditionType = "min-date-time"
	PreconditionMaxDateTime                  PreconditionType = "max-date-time"
	PreconditionMaxInstances                 PreconditionType = "max-instances"
	PreconditionMaxAttendeesPerInstance      PreconditionType = "max-attendees-per-instance"
)

func NewPreconditionError(err PreconditionType) error {
	name := xml.Name{Space: "urn:ietf:params:xml:ns:caldav", Local: string(err)}
	elem := internal.NewRawXMLElement(name, nil, nil)
	return &internal.HTTPError{
		Code: 409,
		Err: &internal.Error{
			Raw: []internal.RawXMLValue{*elem},
		},
	}
}
