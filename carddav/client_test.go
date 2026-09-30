package carddav

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
)

type collectionsBackend struct {
	deadPropsBackend
}

func (*collectionsBackend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	return "/dav/principals/user0/", nil
}

func (*collectionsBackend) AddressBookHomeSetPath(ctx context.Context) (string, error) {
	return "/dav/addressbooks/user0/", nil
}

func (b *collectionsBackend) ListAddressBooks(ctx context.Context) ([]AddressBook, error) {
	return b.addressBooks, nil
}

// TestClientAddressBookCollections verifies an address book made, listed and
// changed through the client arrives at the server's backend as it was given.
func TestClientAddressBookCollections(t *testing.T) {
	backend := &collectionsBackend{}
	srv := httptest.NewServer(&Handler{Backend: backend, Prefix: "/dav"})
	defer srv.Close()
	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx := context.Background()

	colorName := xml.Name{Space: "http://inf-it.com/ns/ab/", Local: "addressbook-color"}
	color := webdav.DeadProperty{Name: colorName, XML: []byte(`<addressbook-color xmlns="http://inf-it.com/ns/ab/">#ff0000</addressbook-color>`)}
	want := AddressBook{
		Path:           "/dav/addressbooks/user0/friends/",
		Name:           "Friends & family",
		Description:    "People",
		DeadProperties: []webdav.DeadProperty{color},
	}
	if err := c.CreateAddressBook(ctx, &want); err != nil {
		t.Fatalf("CreateAddressBook: %v", err)
	}
	if len(backend.addressBooks) != 1 {
		t.Fatalf("backend has %d address books, want 1", len(backend.addressBooks))
	}
	if got := backend.addressBooks[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("created %+v, want %+v", got, want)
	}

	backend.addressBooks[0].ReadOnly = true
	backend.addressBooks[0].CTag = "7"
	abs, err := c.FindAddressBooks(ctx, "/dav/addressbooks/user0/", colorName)
	if err != nil {
		t.Fatalf("FindAddressBooks: %v", err)
	}
	if len(abs) != 1 {
		t.Fatalf("listed %d address books, want 1", len(abs))
	}
	if ab := abs[0]; ab.Name != want.Name || !ab.ReadOnly || ab.CTag != "7" || !reflect.DeepEqual(ab.DeadProperties, want.DeadProperties) {
		t.Errorf("listed %+v", ab)
	}
	ab, err := c.FindAddressBook(ctx, want.Path)
	if err != nil {
		t.Fatalf("FindAddressBook: %v", err)
	}
	if ab.Name != want.Name || ab.CTag != "7" || ab.DeadProperties != nil {
		t.Errorf("found %+v", ab)
	}

	name, desc := "Renamed", ""
	err = c.UpdateAddressBook(ctx, want.Path, &AddressBookUpdate{Name: &name, Description: &desc, RemovedDeadProperties: []xml.Name{colorName}})
	if err != nil {
		t.Fatalf("UpdateAddressBook: %v", err)
	}
	update := backend.update
	if update == nil || update.Name == nil || *update.Name != name || update.Description == nil || *update.Description != "" ||
		!reflect.DeepEqual(update.RemovedDeadProperties, []xml.Name{colorName}) {
		t.Errorf("update %+v, want a rename, the description and the color removed", update)
	}
}

// TestSyncCollection verifies a sync hands over cards as the server sent
// them, deletions, and the token a truncated answer continues from.
func TestSyncCollection(t *testing.T) {
	const data = "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Alice\r\nEND:VCARD\r\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:" xmlns:A="urn:ietf:params:xml:ns:carddav">
 <D:response><D:href>/book/a.vcf</D:href><D:propstat><D:prop><D:getetag>"a1"</D:getetag>
  <A:address-data>BEGIN:VCARD&#13;
VERSION:4.0&#13;
FN:Alice&#13;
END:VCARD&#13;
</A:address-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>
 <D:response><D:href>/book/b.vcf</D:href><D:status>HTTP/1.1 404 Not Found</D:status></D:response>
 <D:response><D:href>/book/</D:href><D:status>HTTP/1.1 507 Insufficient Storage</D:status></D:response>
 <D:sync-token>t2</D:sync-token>
</D:multistatus>`)
	}))
	defer srv.Close()

	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got, err := c.SyncCollection(context.Background(), "/book/", &SyncQuery{SyncToken: "t1"})
	if err != nil {
		t.Fatalf("SyncCollection: %v", err)
	}
	if got.SyncToken != "t2" || !got.Truncated || !reflect.DeepEqual(got.Deleted, []string{"/book/b.vcf"}) {
		t.Errorf("sync = %+v, want token t2, truncated, /book/b.vcf deleted", got)
	}
	if len(got.Updated) != 1 {
		t.Fatalf("updated %d cards, want 1", len(got.Updated))
	}
	if o := got.Updated[0]; o.Path != "/book/a.vcf" || o.ETag != `"a1"` || string(o.Raw) != data || o.Card.PreferredValue(vcard.FieldFormattedName) != "Alice" {
		t.Errorf("updated %+v", o)
	}
}

func TestQueryAddressBookUnreadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
 <D:response><D:href>/book/a.vcf</D:href><D:propstat><D:prop>
  <C:address-data>BEGIN:VCARD&#13;
VERSION:4.0&#13;
FN:Ada&#13;
END:VCARD&#13;
</C:address-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>
 <D:response><D:href>/book/b.vcf</D:href><D:propstat><D:prop>
  <C:address-data>BEGIN:VCARD&#13;
VERSION:4.0&#13;
FN:Bob&#13;
</C:address-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>
</D:multistatus>`)
	}))
	defer srv.Close()

	c, err := NewClient(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got, err := c.QueryAddressBook(context.Background(), "/book/", &AddressBookQuery{})
	if err != nil {
		t.Fatalf("QueryAddressBook: %v", err)
	}
	if len(got) != 2 || got[0].Card == nil || got[1].Card != nil || !strings.HasSuffix(string(got[1].Raw), "FN:Bob\r\n") {
		t.Fatalf("objects = %+v, want a.vcf parsed and b.vcf kept unparsed", got)
	}
	query := &AddressBookQuery{
		DataRequest: AddressDataRequest{Props: []string{vcard.FieldFormattedName}},
		PropFilters: []PropFilter{{Name: vcard.FieldFormattedName, TextMatches: []TextMatch{{Text: "Ada"}}}},
	}
	if kept, err := Filter(query, got); err != nil || len(kept) != 2 {
		t.Fatalf("Filter = %d objects, %v; want both kept", len(kept), err)
	}
}
