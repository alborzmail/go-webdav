// Package conditional evaluates object ETag preconditions for DAV handlers.
package conditional

import (
	"net/http"

	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/internal"
)

// Check evaluates If-Match before If-None-Match for an existing object.
// notModified asks GET/HEAD handlers to send a bodyless 304 response.
// Wildcards test existence, including objects without an ETag.
func Check(r *http.Request, etag string) (notModified bool, err error) {
	match := webdav.ConditionalMatch(r.Header.Get("If-Match"))
	if match.IsSet() && !match.IsWildcard() {
		if _, ok, err := match.MatchETag(etag); err != nil {
			return false, &internal.HTTPError{http.StatusBadRequest, err}
		} else if !ok {
			return false, internal.HTTPErrorf(http.StatusPreconditionFailed, "webdav: If-Match condition failed")
		}
	}
	none := webdav.ConditionalMatch(r.Header.Get("If-None-Match"))
	_, matched, err := none.MatchETagWeak(etag)
	if err != nil {
		return false, &internal.HTTPError{http.StatusBadRequest, err}
	}
	if matched || none.IsWildcard() {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			return true, nil
		}
		return false, internal.HTTPErrorf(http.StatusPreconditionFailed, "webdav: If-None-Match condition failed")
	}
	return false, nil
}
