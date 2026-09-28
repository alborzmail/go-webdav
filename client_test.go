package webdav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReadDirWithoutContentLength verifies a member the server gives no
// getcontentlength for is still listed, as CalDAV servers are free to do.
func TestReadDirWithoutContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:">
 <D:response><D:href>/cal/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>
 <D:response><D:href>/cal/a.ics</D:href>
  <D:propstat><D:prop><D:resourcetype/><D:getetag>"a1"</D:getetag></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  <D:propstat><D:prop><D:getcontentlength/></D:prop><D:status>HTTP/1.1 404 Not Found</D:status></D:propstat>
 </D:response>
</D:multistatus>`)
	}))
	defer srv.Close()

	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	files, err := c.ReadDir(context.Background(), "/cal/", false)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(files) != 2 || files[1].Path != "/cal/a.ics" || files[1].ETag != `"a1"` {
		t.Errorf("files = %+v, want /cal/ and /cal/a.ics with ETag %q", files, `"a1"`)
	}
}
