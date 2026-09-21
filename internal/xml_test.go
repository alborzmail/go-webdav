package internal

import (
	"bytes"
	"encoding/xml"
	"io"
	"testing"
)

const rawXML = `<?xml version="1.0" encoding="UTF-8"?>
<bookstore>
  <book category="COOKING">
    <title lang="en">Everyday Italian</title>
    <author>Giada De Laurentiis</author>
    <year>2005</year>
  </book>

  <book category="CHILDREN">
    <title lang="en">Harry Potter</title>
    <author>J K. Rowling</author>
    <year>2005</year>
  </book>
</bookstore>`

func TestRawXMLValue(t *testing.T) {
	// TODO: test XML namespaces too

	var rawValue RawXMLValue
	if err := xml.Unmarshal([]byte(rawXML), &rawValue); err != nil {
		t.Fatalf("xml.Unmarshal() = %v", err)
	}

	b, err := xml.Marshal(&rawValue)
	if err != nil {
		t.Fatalf("xml.Marshal() = %v", err)
	}

	s := xml.Header + string(b)
	if s != rawXML {
		t.Errorf("input doesn't match output:\n%v\nvs.\n%v", rawXML, s)
	}
}

func TestRawXMLValue_TokenReader(t *testing.T) {
	var rawValue RawXMLValue
	if err := xml.Unmarshal([]byte(rawXML), &rawValue); err != nil {
		t.Fatalf("xml.Unmarshal() = %v", err)
	}

	tr := rawValue.TokenReader()

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	for {
		tok, err := tr.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("TokenReader.Token() = %v", err)
		}

		if err := enc.EncodeToken(tok); err != nil {
			t.Fatalf("Encoder.EncodeToken() = %v", err)
		}
	}
	if err := enc.Flush(); err != nil {
		t.Fatalf("Encoder.Flush() = %v", err)
	}

	s := xml.Header + buf.String()
	if s != rawXML {
		t.Errorf("input doesn't match output:\n%v\nvs.\n%v", rawXML, s)
	}
}

func TestRawXMLValue_Bytes(t *testing.T) {
	const in = `<A:calendar-color xmlns:A="http://apple.com/ns/ical/" symbolic-color="red">#FF2968</A:calendar-color>`
	const want = `<calendar-color xmlns="http://apple.com/ns/ical/" symbolic-color="red">#FF2968</calendar-color>`

	var rawValue RawXMLValue
	if err := xml.Unmarshal([]byte(in), &rawValue); err != nil {
		t.Fatalf("xml.Unmarshal() = %v", err)
	}
	b, err := rawValue.Bytes()
	if err != nil {
		t.Fatalf("Bytes() = %v", err)
	} else if string(b) != want {
		t.Errorf("Bytes() = \n%s\n but want: \n%s", b, want)
	}

	// The namespace must not be declared twice once encoded again
	decoded, err := DecodeRawXMLBytes(b)
	if err != nil {
		t.Fatalf("DecodeRawXMLBytes() = %v", err)
	}
	b, err = xml.Marshal(decoded)
	if err != nil {
		t.Fatalf("xml.Marshal() = %v", err)
	} else if string(b) != want {
		t.Errorf("xml.Marshal() = \n%s\n but want: \n%s", b, want)
	}
}
