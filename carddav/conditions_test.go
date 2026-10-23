package carddav

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emersion/go-webdav"
)

type preconditionBackend struct {
	testBackend
	etag           string
	exists         bool
	reads, deletes int
}

func (b *preconditionBackend) GetAddressObject(ctx context.Context, path string, req *AddressDataRequest) (*AddressObject, error) {
	b.reads++
	if !b.exists {
		return nil, webdav.NewHTTPError(http.StatusNotFound, errors.New("missing"))
	}
	return &AddressObject{Path: path, ETag: b.etag, Raw: []byte("synthetic object")}, nil
}
func (b *preconditionBackend) DeleteAddressObject(ctx context.Context, path string) error {
	b.deletes++
	b.exists = false
	return nil
}

func TestObjectETagConditions(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodDelete} {
		for _, tt := range []struct {
			name, etag, ifMatch, ifNoneMatch string
			readStatus, deleteStatus         int
		}{
			{"unconditional", "1", "", "", 200, 204},
			{"match none", "1", "", `"1"`, 304, 412},
			{"weak none", "1", "", `W/"1"`, 304, 412},
			{"weak resource", `W/"1"`, "", `"1"`, 304, 412},
			{"list none", "1", "", `"other", W/"1"`, 304, 412},
			{"wildcard none", "1", "", "*", 304, 412},
			{"wildcard without etag", "", "", "*", 304, 412},
			{"miss none", "1", "", `"other", "another"`, 200, 204},
			{"match", "1", `"1"`, "", 200, 204},
			{"list match", "1", `"other", "1"`, "", 200, 204},
			{"weak match fails", "1", `W/"1"`, "", 412, 412},
			{"weak resource match fails", `W/"1"`, `"1"`, "", 412, 412},
			{"wildcard match", "", "*", "", 200, 204},
			{"miss match", "1", `"other"`, "", 412, 412},
			{"match precedes none", "1", `"other"`, `"1"`, 412, 412},
			{"both match", "1", `"1"`, `"1"`, 304, 412},
			{"both pass", "1", `"1"`, `"other"`, 200, 204},
			{"malformed match", "1", `"broken`, "", 400, 400},
			{"malformed none", "1", "", `"broken`, 400, 400},
		} {
			t.Run(method+"/"+tt.name, func(t *testing.T) {
				b := &preconditionBackend{etag: tt.etag, exists: true}
				r := httptest.NewRequest(method, "/user/contacts/default/contact.vcf", nil)
				r.Header.Set("If-Match", tt.ifMatch)
				r.Header.Set("If-None-Match", tt.ifNoneMatch)
				w := httptest.NewRecorder()
				(&Handler{Backend: b}).ServeHTTP(w, r)
				want := tt.readStatus
				if method == http.MethodDelete {
					want = tt.deleteStatus
				}
				if w.Code != want {
					t.Fatalf("status = %d, want %d; body %q", w.Code, want, w.Body.String())
				}
				if method == http.MethodDelete {
					if want == 204 && (b.exists || b.deletes != 1) {
						t.Error("object not deleted")
					}
					if want != 204 && (!b.exists || b.deletes != 0) {
						t.Error("failed precondition deleted object")
					}
				}
				if want == 304 {
					if w.Body.Len() != 0 {
						t.Error("304 has a body")
					}
					if tt.etag != "" && w.Header().Get("ETag") == "" {
						t.Error("304 missing ETag")
					}
				}
				if method == http.MethodHead && want == 200 && w.Body.Len() != 0 {
					t.Error("HEAD has a body")
				}
				if method == http.MethodGet && want == 200 && w.Body.String() != "synthetic object" {
					t.Error("GET lost data")
				}
			})
		}
	}
}

type legacyPreconditionBackend struct {
	*preconditionBackend
	calls int
}

func (b *legacyPreconditionBackend) DeleteAddressObjectIfMatch(ctx context.Context, path string, match webdav.ConditionalMatch) error {
	b.calls++
	return webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("atomic match rejected"))
}

type optionsPreconditionBackend struct {
	*legacyPreconditionBackend
	options *webdav.RemoveAllOptions
}

func (b *optionsPreconditionBackend) DeleteAddressObjectWithOptions(ctx context.Context, path string, opts *webdav.RemoveAllOptions) error {
	b.options = opts
	return webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("atomic conditions rejected"))
}

func TestDeleteConditionsBackendDispatch(t *testing.T) {
	for _, headers := range [][2]string{{`"1"`, ""}, {"", "*"}, {`"1"`, `"other"`}} {
		b := &optionsPreconditionBackend{legacyPreconditionBackend: &legacyPreconditionBackend{preconditionBackend: &preconditionBackend{etag: "1", exists: true}}}
		r := httptest.NewRequest(http.MethodDelete, "/user/contacts/default/contact.vcf", nil)
		r.Header.Set("If-Match", headers[0])
		r.Header.Set("If-None-Match", headers[1])
		w := httptest.NewRecorder()
		(&Handler{Backend: b}).ServeHTTP(w, r)
		if w.Code != 412 {
			t.Errorf("status = %d, want backend's 412", w.Code)
		}
		if b.options == nil || string(b.options.IfMatch) != headers[0] || string(b.options.IfNoneMatch) != headers[1] {
			t.Errorf("lost options: %+v", b.options)
		}
		if b.reads != 0 || b.deletes != 0 || b.calls != 0 || !b.exists {
			t.Error("atomic backend bypassed")
		}
	}
	t.Run("legacy if-match remains atomic", func(t *testing.T) {
		b := &legacyPreconditionBackend{preconditionBackend: &preconditionBackend{etag: "1", exists: true}}
		r := httptest.NewRequest(http.MethodDelete, "/user/contacts/default/contact.vcf", nil)
		r.Header.Set("If-Match", `"1"`)
		w := httptest.NewRecorder()
		(&Handler{Backend: b}).ServeHTTP(w, r)
		if w.Code != 412 || b.calls != 1 || b.reads != 0 || b.deletes != 0 {
			t.Errorf("legacy dispatch failed: %+v status %d", b, w.Code)
		}
	})
	t.Run("legacy cannot bypass if-none-match", func(t *testing.T) {
		b := &legacyPreconditionBackend{preconditionBackend: &preconditionBackend{etag: "1", exists: true}}
		r := httptest.NewRequest(http.MethodDelete, "/user/contacts/default/contact.vcf", nil)
		r.Header.Set("If-Match", `"1"`)
		r.Header.Set("If-None-Match", "*")
		w := httptest.NewRecorder()
		(&Handler{Backend: b}).ServeHTTP(w, r)
		if w.Code != 412 || b.calls != 0 || b.reads != 1 || b.deletes != 0 || !b.exists {
			t.Errorf("fallback failed: %+v status %d", b, w.Code)
		}
	})
}
