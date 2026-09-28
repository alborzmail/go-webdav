package carddav

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

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
