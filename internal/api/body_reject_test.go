package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestMalformedValuesAreRefused sends a body that breaks off or carries
// the wrong kind of token at each step of the reader. Every one must be
// refused as body{}, false, with nothing read before the fault leaking out.
func TestMalformedValuesAreRefused(t *testing.T) {
	shift := shiftFields(false)
	for _, c := range []struct {
		fields fieldSet
		in     string
	}{
		// The object does not close.
		{versionOnly, `{"version":1`},
		{versionOnly, `{"version":1]`},
		// nullableID: no value after the colon.
		{moveFields, `{"afterCueId":}`},
		{moveFields, `{"afterCueId":`},
		// optText: neither a string nor null.
		{editFields, `{"notePublic":5}`},
		{editFields, `{"notePublic":true}`},
		{editFields, `{"notePublic":{}}`},
		// optText: no value after the colon.
		{editFields, `{"notePublic":}`},
		{editFields, `{"notePublic":`},
		// timestamp: not a string, or not RFC 3339.
		{insertFields, `{"start":5}`},
		{insertFields, `{"start":null}`},
		{insertFields, `{"start":"2027-06-06 10:30"}`},
		// roles: not an array.
		{assignFields, `{"roles":{}}`},
		{assignFields, `{"roles":null}`},
		{assignFields, `{"roles":5}`},
		// roles: the array does not close.
		{assignFields, `{"roles":[`},
		{assignFields, `{"roles":[}`},
		{assignFields, `{"roles":[{"roleId":1,"roleNote":null}`},
		// roles: an item does not close.
		{assignFields, `{"roles":[{"roleId":1,"roleNote":null`},
		{assignFields, `{"roles":[{"roleId":1,"roleNote":null]}`},
		// newRoles: not an array.
		{importFields, `{"newRoles":{}}`},
		{importFields, `{"newRoles":null}`},
		{importFields, `{"newRoles":"x"}`},
		// newRoles: the array does not close.
		{importFields, `{"newRoles":[`},
		{importFields, `{"newRoles":[}`},
		{importFields, `{"newRoles":[{"name":"攝影","view":"crew","checksIn":true}`},
		// newRoles: an item is not an object.
		{importFields, `{"newRoles":[5]}`},
		{importFields, `{"newRoles":["攝影"]}`},
		{importFields, `{"newRoles":[[]]}`},
		// newRoles: an item does not close.
		{importFields, `{"newRoles":[{"name":"攝影","view":"crew","checksIn":true`},
		{importFields, `{"newRoles":[{"name":"攝影","view":"crew","checksIn":true]}`},
		// excluded: the array does not close.
		{shift, `{"fromCueId":1,"deltaMin":5,"excluded":[`},
		{shift, `{"fromCueId":1,"deltaMin":5,"excluded":[1`},
		{shift, `{"fromCueId":1,"deltaMin":5,"excluded":[1,2}`},
	} {
		if b, ok := read(c.fields, c.in); ok || !reflect.DeepEqual(b, body{}) {
			t.Errorf("%s: accepted or leaked: %v %+v", c.in, ok, b)
		}
	}
}

// decoderOver is a decoder set up as readBody sets up its own.
func decoderOver(s string) *json.Decoder {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	return dec
}

// TestUnknownFieldKindIsRefused: no fieldSet uses a kind outside the
// table, so readField is called directly with one.
func TestUnknownFieldKindIsRefused(t *testing.T) {
	var b body
	if readField(decoderOver(`1`), fieldKind(99), "version", &b) {
		t.Error("an unknown fieldKind was accepted")
	}
	if !reflect.DeepEqual(b, body{}) {
		t.Errorf("an unknown fieldKind wrote %+v", b)
	}
}

// TestUnmappedKeysHaveNoTarget: every fieldSet maps its keys, so the
// targets are called directly with names they do not know, the case
// matching exactly.
func TestUnmappedKeysHaveNoTarget(t *testing.T) {
	var b body
	for _, key := range []string{"unknown", "Version", "afterCueID", "durationMin"} {
		if p := intTarget(&b, key); p != nil {
			t.Errorf("intTarget(%q) is not nil", key)
		}
	}
	for _, key := range []string{"unknown", "CheckedIn", "on"} {
		if p := boolTarget(&b, key); p != nil {
			t.Errorf("boolTarget(%q) is not nil", key)
		}
	}
	for _, key := range []string{"unknown", "Title", "notePublic"} {
		if p := stringTarget(&b, key); p != nil {
			t.Errorf("stringTarget(%q) is not nil", key)
		}
	}
}

// TestAfterCueIDTarget: afterCueId is a nullableID in every fieldSet, so
// its positiveInt target is only reached by a direct call.
func TestAfterCueIDTarget(t *testing.T) {
	var b body
	if p := intTarget(&b, "afterCueId"); p != &b.afterCueID {
		t.Errorf("intTarget(afterCueId) = %p, want %p", p, &b.afterCueID)
	}
}

// TestUnmappedKeysAreNotWritten gives each reader a value it would accept
// under a key it does not map: it must refuse and leave body untouched.
func TestUnmappedKeysAreNotWritten(t *testing.T) {
	for _, c := range []struct {
		name string
		read fieldReader
		in   string
	}{
		{"readPositiveField", readPositiveField, `7`},
		{"readBoolField", readBoolField, `true`},
		{"readTextField", readTextField, `"x"`},
	} {
		var b body
		if c.read(decoderOver(c.in), "unknown", &b) {
			t.Errorf("%s accepted an unmapped key", c.name)
		}
		if !reflect.DeepEqual(b, body{}) {
			t.Errorf("%s wrote %+v", c.name, b)
		}
	}
}
