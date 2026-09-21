package carddav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
)

type testBackend struct {
	addressBooks []AddressBook
}

type contextKey string

var (
	aliceData = `BEGIN:VCARD
VERSION:4.0
UID:urn:uuid:4fbe8971-0bc3-424c-9c26-36c3e1eff6b1
FN;PID=1.1:Alice Gopher
N:Gopher;Alice;;;
EMAIL;PID=1.1:alice@example.com
CLIENTPIDMAP:1;urn:uuid:53e374d9-337e-4727-8803-a1e9c14e0551
END:VCARD`
	alicePath = "urn:uuid:4fbe8971-0bc3-424c-9c26-36c3e1eff6b1.vcf"

	currentUserPrincipalKey = contextKey("test:currentUserPrincipal")
	homeSetPathKey          = contextKey("test:homeSetPath")
	addressBookPathKey      = contextKey("test:addressBookPath")
)

func (*testBackend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	r := ctx.Value(currentUserPrincipalKey).(string)
	return r, nil
}

func (*testBackend) AddressBookHomeSetPath(ctx context.Context) (string, error) {
	r := ctx.Value(homeSetPathKey).(string)
	return r, nil
}

func (*testBackend) ListAddressBooks(ctx context.Context) ([]AddressBook, error) {
	p := ctx.Value(addressBookPathKey).(string)
	return []AddressBook{
		AddressBook{
			Path:                 p,
			Name:                 "My contacts",
			Description:          "Default address book",
			MaxResourceSize:      1024,
			SupportedAddressData: nil,
		},
	}, nil
}

func (b *testBackend) GetAddressBook(ctx context.Context, path string) (*AddressBook, error) {
	abs, err := b.ListAddressBooks(ctx)
	if err != nil {
		panic(err)
	}
	for _, ab := range abs {
		if ab.Path == path {
			return &ab, nil
		}
	}
	return nil, webdav.NewHTTPError(404, fmt.Errorf("Not found"))
}

func (b *testBackend) CreateAddressBook(ctx context.Context, ab *AddressBook) error {
	b.addressBooks = append(b.addressBooks, *ab)
	return nil
}

func (*testBackend) UpdateAddressBook(ctx context.Context, path string, update *AddressBookUpdate) error {
	panic("TODO: implement")
}

func (*testBackend) DeleteAddressBook(ctx context.Context, path string) error {
	panic("TODO: implement")
}

func (*testBackend) GetAddressObject(ctx context.Context, path string, req *AddressDataRequest) (*AddressObject, error) {
	if path == alicePath {
		card, err := vcard.NewDecoder(strings.NewReader(aliceData)).Decode()
		if err != nil {
			return nil, err
		}
		return &AddressObject{
			Path: path,
			Card: card,
		}, nil
	} else {
		return nil, webdav.NewHTTPError(404, fmt.Errorf("Not found"))
	}
}

func (b *testBackend) GetAddressObjects(ctx context.Context, paths []string, req *AddressDataRequest) ([]AddressObject, error) {
	addresses := make([]AddressObject, 0)
	for _, path := range paths {
		if ao, err := b.GetAddressObject(ctx, path, req); err == nil {
			addresses = append(addresses, *ao)
		}
	}
	return addresses, nil
}

func (b *testBackend) ListAddressObjects(ctx context.Context, path string, req *AddressDataRequest) ([]AddressObject, error) {
	p := ctx.Value(addressBookPathKey).(string)
	if !strings.HasPrefix(path, p) {
		return nil, webdav.NewHTTPError(404, fmt.Errorf("Not found"))
	}
	alice, err := b.GetAddressObject(ctx, alicePath, req)
	if err != nil {
		return nil, err
	}

	return []AddressObject{*alice}, nil
}

func (*testBackend) QueryAddressObjects(ctx context.Context, path string, query *AddressBookQuery) ([]AddressObject, error) {
	panic("TODO: implement")
}

func (*testBackend) PutAddressObject(ctx context.Context, path string, card vcard.Card, opts *PutAddressObjectOptions) (*AddressObject, error) {
	panic("TODO: implement")
}

func (*testBackend) DeleteAddressObject(ctx context.Context, path string) error {
	panic("TODO: implement")
}

func TestAddressBookDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		prefix               string
		currentUserPrincipal string
		homeSetPath          string
		addressBookPath      string
	}{
		{
			name:                 "simple",
			prefix:               "",
			currentUserPrincipal: "/test/",
			homeSetPath:          "/test/contacts/",
			addressBookPath:      "/test/contacts/private",
		},
		{
			name:                 "prefix",
			prefix:               "/dav",
			currentUserPrincipal: "/dav/test/",
			homeSetPath:          "/dav/test/contacts/",
			addressBookPath:      "/dav/test/contacts/private",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			h := Handler{&testBackend{}, tc.prefix}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				ctx = context.WithValue(ctx, currentUserPrincipalKey, tc.currentUserPrincipal)
				ctx = context.WithValue(ctx, homeSetPathKey, tc.homeSetPath)
				ctx = context.WithValue(ctx, addressBookPathKey, tc.addressBookPath)
				r = r.WithContext(ctx)
				(&h).ServeHTTP(w, r)
			}))
			defer ts.Close()

			// client supports .well-known discovery if explicitly pointed to it
			startURL := ts.URL
			if tc.currentUserPrincipal != "/" {
				startURL = ts.URL + "/.well-known/carddav"
			}

			client, err := NewClient(nil, startURL)
			if err != nil {
				t.Fatalf("error creating client: %s", err)
			}
			cup, err := client.FindCurrentUserPrincipal(ctx)
			if err != nil {
				t.Fatalf("error finding user principal url: %s", err)
			}
			if cup != tc.currentUserPrincipal {
				t.Fatalf("Found current user principal URL '%s', expected '%s'", cup, tc.currentUserPrincipal)
			}
			hsp, err := client.FindAddressBookHomeSet(ctx, cup)
			if err != nil {
				t.Fatalf("error finding home set path: %s", err)
			}
			if hsp != tc.homeSetPath {
				t.Fatalf("Found home set path '%s', expected '%s'", hsp, tc.homeSetPath)
			}
			abs, err := client.FindAddressBooks(ctx, hsp)
			if err != nil {
				t.Fatalf("error finding address books: %s", err)
			}
			if len(abs) != 1 {
				t.Fatalf("Found %d address books, expected 1", len(abs))
			}
			if abs[0].Path != tc.addressBookPath {
				t.Fatalf("Found address book at %s, expected %s", abs[0].Path, tc.addressBookPath)
			}
		})
	}
}

var mkcolRequestBody = `
<?xml version="1.0" encoding="utf-8" ?>
   <D:mkcol xmlns:D="DAV:"
                 xmlns:C="urn:ietf:params:xml:ns:carddav">
     <D:set>
       <D:prop>
         <D:resourcetype>
           <D:collection/>
           <C:addressbook/>
         </D:resourcetype>
         <D:displayname>Lisa's Contacts</D:displayname>
         <C:addressbook-description xml:lang="en"
   >My primary address book.</C:addressbook-description>
       </D:prop>
     </D:set>
   </D:mkcol>`

func TestCreateAddressbookMinimalBody(t *testing.T) {
	tb := testBackend{
		addressBooks: nil,
	}
	b := backend{
		Backend: &tb,
		Prefix:  "/dav",
	}
	req := httptest.NewRequest("MKCOL", "/dav/addressbooks/user0/test-addressbook", strings.NewReader(mkcolRequestBody))
	req.Header.Set("Content-Type", "application/xml")

	err := b.Mkcol(req)
	if err != nil {
		t.Fatalf("Unexpcted error in Mkcol: %s", err)
	}
	if len(tb.addressBooks) != 1 {
		t.Fatalf("Found %d address books, expected 1", len(tb.addressBooks))
	}
	c := tb.addressBooks[0]
	if c.Name != "Lisa's Contacts" {
		t.Fatalf("Address book name is '%s', expected 'Lisa's Contacts'", c.Name)
	}
	if c.Path != "/dav/addressbooks/user0/test-addressbook" {
		t.Fatalf("Address book path is '%s', expected '/dav/addressbooks/user0/test-addressbook'", c.Path)
	}
	if c.Description != "My primary address book." {
		t.Fatalf("Address book sdscription is '%s', expected 'My primary address book.'", c.Description)
	}
}

type deadPropsBackend struct {
	testBackend
	update *AddressBookUpdate
}

func (b *deadPropsBackend) GetAddressBook(ctx context.Context, path string) (*AddressBook, error) {
	return &b.addressBooks[0], nil
}

func (b *deadPropsBackend) UpdateAddressBook(ctx context.Context, path string, update *AddressBookUpdate) error {
	b.update = update
	return nil
}

func TestDeadProperties(t *testing.T) {
	backend := &deadPropsBackend{}
	handler := Handler{Backend: backend, Prefix: "/dav"}
	serve := func(method, body string) string {
		req := httptest.NewRequest(method, "/dav/addressbooks/user0/test-addressbook", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Depth", "0")
		ctx := context.WithValue(req.Context(), currentUserPrincipalKey, "/dav/principals/user0/")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req.WithContext(ctx))
		data, err := io.ReadAll(w.Result().Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	color := xml.Name{Space: "http://inf-it.com/ns/ab/", Local: "addressbook-color"}
	colorProp := `<I:addressbook-color xmlns:I="http://inf-it.com/ns/ab/">#ff0000</I:addressbook-color>`

	serve("MKCOL", strings.Replace(mkcolRequestBody, "<D:displayname>", colorProp+"<D:displayname>", 1))
	if len(backend.addressBooks) != 1 {
		t.Fatalf("Found %d address books, expected 1", len(backend.addressBooks))
	}
	ab := backend.addressBooks[0]
	if ab.Name != "Lisa's Contacts" || ab.Description != "My primary address book." {
		t.Errorf("Unexpected address book: %+v", ab)
	}
	if len(ab.DeadProperties) != 1 || ab.DeadProperties[0].Name != color {
		t.Fatalf("Unexpected dead properties: %v", ab.DeadProperties)
	}

	resp := serve("PROPFIND", `<D:propfind xmlns:D="DAV:"><D:allprop/></D:propfind>`)
	if !strings.Contains(resp, `<addressbook-color xmlns="http://inf-it.com/ns/ab/">#ff0000</addressbook-color>`) {
		t.Fatalf("Expected addressbook-color in allprop response:\n%v", resp)
	}

	resp = serve("PROPPATCH", `<D:propertyupdate xmlns:D="DAV:"><D:remove><D:prop>`+colorProp+`</D:prop></D:remove></D:propertyupdate>`)
	if backend.update == nil {
		t.Fatalf("Expected the address book updated:\n%v", resp)
	} else if len(backend.update.RemovedDeadProperties) != 1 || backend.update.RemovedDeadProperties[0] != color {
		t.Errorf("Unexpected removed dead properties: %v", backend.update.RemovedDeadProperties)
	}

	backend.update = nil
	resp = serve("PROPPATCH", `<D:propertyupdate xmlns:D="DAV:"><D:set><D:prop>`+colorProp+
		`<D:resourcetype/></D:prop></D:set></D:propertyupdate>`)
	if !strings.Contains(resp, "403 Forbidden") || !strings.Contains(resp, "424 Failed Dependency") {
		t.Errorf("Expected the protected property to fail the others:\n%v", resp)
	} else if backend.update != nil {
		t.Errorf("Expected no update applied")
	}
}

type deleteBackend struct {
	testBackend
	deleted []string
	ifMatch webdav.ConditionalMatch
}

func (b *deleteBackend) GetAddressObject(ctx context.Context, path string, req *AddressDataRequest) (*AddressObject, error) {
	return &AddressObject{Path: path, ETag: "1"}, nil
}

func (b *deleteBackend) DeleteAddressObject(ctx context.Context, path string) error {
	b.deleted = append(b.deleted, path)
	return nil
}

type conditionalDeleteBackend struct {
	deleteBackend
}

func (b *conditionalDeleteBackend) DeleteAddressObjectIfMatch(ctx context.Context, path string, ifMatch webdav.ConditionalMatch) error {
	b.ifMatch = ifMatch
	return nil
}

func TestDeleteIfMatch(t *testing.T) {
	serve := func(b Backend, ifMatch string) int {
		req := httptest.NewRequest(http.MethodDelete, "/user/contacts/default/alice.vcf", nil)
		req.Header.Set("If-Match", ifMatch)
		w := httptest.NewRecorder()
		(&Handler{Backend: b}).ServeHTTP(w, req)
		return w.Result().StatusCode
	}

	backend := &deleteBackend{}
	if sc := serve(backend, `"2"`); sc != http.StatusPreconditionFailed {
		t.Errorf("Unexpected status code for a stale ETag: %d", sc)
	} else if len(backend.deleted) != 0 {
		t.Errorf("Expected the object kept")
	}
	if sc := serve(backend, `"2", "1"`); sc != http.StatusNoContent {
		t.Errorf("Unexpected status code for the current ETag: %d", sc)
	} else if len(backend.deleted) != 1 {
		t.Errorf("Expected the object deleted")
	}

	conditional := &conditionalDeleteBackend{}
	if sc := serve(conditional, `"2"`); sc != http.StatusNoContent {
		t.Errorf("Unexpected status code: %d", sc)
	} else if conditional.ifMatch != `"2"` {
		t.Errorf("Expected If-Match handed to the backend, got %q", conditional.ifMatch)
	} else if len(conditional.deleted) != 0 {
		t.Errorf("Expected DeleteAddressObject left alone")
	}
}
