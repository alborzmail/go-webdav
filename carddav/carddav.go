// Package carddav provides a client and server CardDAV implementation.
//
// CardDAV is defined in RFC 6352.
package carddav

import (
	"encoding/xml"
	"time"

	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/internal"
)

var CapabilityAddressBook = webdav.Capability("addressbook")

func NewAddressBookHomeSet(path string) webdav.BackendSuppliedHomeSet {
	return &addressbookHomeSet{Href: internal.Href{Path: path}}
}

type AddressDataType struct {
	ContentType string
	Version     string
}

type AddressBook struct {
	Path                 string
	Name                 string
	Description          string
	MaxResourceSize      int64
	SupportedAddressData []AddressDataType
	// ReadOnly reports that the current user may only read this address book.
	// It controls the DAV:current-user-privilege-set reported by the server.
	ReadOnly bool
	// CTag, when set, is reported as the CalendarServer getctag property: an
	// opaque token that must change whenever the address book's contents change.
	CTag string
	// DeadProperties are the properties stored for clients, see
	// AddressBookUpdate. The server reports them as they are.
	DeadProperties []webdav.DeadProperty
}

type AddressBookUpdate struct {
	Name        *string
	Description *string
	// RemovedDeadProperties are dropped, then DeadProperties are stored,
	// each replacing the one of the same name. A backend which can't store
	// them must return an error.
	RemovedDeadProperties []xml.Name
	DeadProperties        []webdav.DeadProperty
}

func (ab *AddressBook) SupportsAddressData(contentType, version string) bool {
	if len(ab.SupportedAddressData) == 0 {
		return contentType == "text/vcard" && version == "3.0"
	}
	for _, t := range ab.SupportedAddressData {
		if t.ContentType == contentType && t.Version == version {
			return true
		}
	}
	return false
}

type AddressBookQuery struct {
	DataRequest AddressDataRequest

	PropFilters []PropFilter
	FilterTest  FilterTest // defaults to FilterAnyOf

	Limit int // <= 0 means unlimited
}

type AddressDataRequest struct {
	Props   []string
	AllProp bool
}

// IsEmpty reports whether no part of an address object is requested.
func (req *AddressDataRequest) IsEmpty() bool {
	return !req.AllProp && len(req.Props) == 0
}

type PropFilter struct {
	Name string
	Test FilterTest // defaults to FilterAnyOf

	// if IsNotDefined is set, TextMatches and Params need to be unset
	IsNotDefined bool
	TextMatches  []TextMatch
	Params       []ParamFilter
}

type ParamFilter struct {
	Name string

	// if IsNotDefined is set, TextMatch needs to be unset
	IsNotDefined bool
	TextMatch    *TextMatch
}

type TextMatch struct {
	Text            string
	NegateCondition bool
	MatchType       MatchType // defaults to MatchContains
	Collation       string    // defaults to CollationUnicodeCasemap
}

type FilterTest string

const (
	FilterAnyOf FilterTest = "anyof"
	FilterAllOf FilterTest = "allof"
)

type MatchType string

const (
	MatchEquals     MatchType = "equals"
	MatchContains   MatchType = "contains"
	MatchStartsWith MatchType = "starts-with"
	MatchEndsWith   MatchType = "ends-with"
)

// Collations a server must support (RFC 6352 section 8.3).
const (
	CollationOctet          = internal.CollationOctet
	CollationASCIICasemap   = internal.CollationASCIICasemap
	CollationUnicodeCasemap = internal.CollationUnicodeCasemap
)

type AddressBookMultiGet struct {
	Paths       []string
	DataRequest AddressDataRequest
}

// AddressObject is an address object resource. Its ETag is an opaque-tag or
// an entity-tag; the client keeps it as the server sent it, weak or strong,
// fit for an If-Match header.
type AddressObject struct {
	Path          string
	ModTime       time.Time
	ContentLength int64
	ETag          string
	Card          vcard.Card
	// Raw, when set by a backend, is the object as stored. The server sends
	// it instead of encoding Card wherever the whole object is asked for, so
	// that ETag names the bytes a client gets. Card can then be left nil.
	// The client sets it to the object as the server sent it in a REPORT,
	// and leaves Card nil where that cannot be parsed.
	Raw []byte
}

// SyncQuery is the query struct represents a sync-collection request
type SyncQuery struct {
	DataRequest AddressDataRequest
	SyncToken   string
	Limit       int // <= 0 means unlimited
}

// SyncResponse contains the returned sync-token for next time
type SyncResponse struct {
	SyncToken string
	// Truncated reports that the server sent only part of the changes (RFC
	// 6578 section 3.6); a sync from SyncToken brings the rest.
	Truncated bool
	Updated   []AddressObject
	Deleted   []string
}
