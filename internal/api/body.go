package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// body is a parsed write or preview request (api.md section 1).
type body struct {
	version   int64
	fromCueID int64
	deltaMin  int
	excluded  []int64
	roleID    int64
	checkedIn bool
	// The live rundown edits (api.md section 3, M2b).
	cueID       int64
	afterCueID  int64
	start       time.Time
	durationMin int
	title       string
	notePublic  *string
	roles       []store.Assignment
	// The preparation (api.md section 3, M2c).
	isFixed     bool
	name        string
	view        string
	checksIn    bool
	origin      string
	originGiven bool // origin is in the body, even as ""
	text        string
	newRoles    []store.NewRole
}

type fieldKind int

const (
	positiveInt fieldKind = iota // an id or a version: 1 or more
	signedInt                    // a shift in minutes
	idList                       // an array of ids, at most maxExcluded
	boolean                      // true or false
	count                        // a length in minutes: 0 or more
	text                         // a string
	optText                      // a string or null
	timestamp                    // an RFC 3339 time
	roleList                     // [{"roleId":n,"roleNote":"…"|null}…], at most maxRoles
	nullableID                   // an id or null (null puts a cue first, M2c)
	optString                    // a string that may be left out
	newRoleList                  // [{"name":"…","view":"…","checksIn":bool}…], at most maxRoles, no name twice
)

// maxRoles bounds the roles of one cue (api.md section 3).
const maxRoles = 50

// fieldSet is the fields a request takes; all are required but the
// idList and optString fields, which may be left out.
type fieldSet map[string]fieldKind

// maxExcluded bounds the unticked cues of one shift; a rundown has a few
// dozen cues.
const maxExcluded = 200

var versionOnly = fieldSet{"version": positiveInt}

// The check-ins carry no version (ADR-0023); a role's own check-in takes
// its role from the token alone, so a roleId there is an unknown field.
var (
	ownCheckInFields   = fieldSet{"checkedIn": boolean}
	proxyCheckInFields = fieldSet{"roleId": positiveInt, "checkedIn": boolean}
	// A link change carries no version either: it is not a cue (ADR-0023).
	linkFields   = fieldSet{"roleId": positiveInt}
	skipFields   = fieldSet{"version": positiveInt, "cueId": positiveInt}
	insertFields = fieldSet{"version": positiveInt, "afterCueId": nullableID, "start": timestamp,
		"durationMin": count, "title": text, "notePublic": optText, "roles": roleList}
	assignFields = fieldSet{"version": positiveInt, "cueId": positiveInt, "roles": roleList}
	editFields   = fieldSet{"version": positiveInt, "cueId": positiveInt, "title": text, "notePublic": optText}
	// The preparation (M2c). A reissue may ask for a QR code too.
	reissueFields = fieldSet{"roleId": positiveInt, "origin": optString}
	updateFields  = fieldSet{"version": positiveInt, "cueId": positiveInt, "start": timestamp, "durationMin": count,
		"isFixed": boolean, "title": text, "notePublic": optText}
	moveFields          = fieldSet{"version": positiveInt, "cueId": positiveInt, "afterCueId": nullableID}
	deleteCueFields     = fieldSet{"version": positiveInt, "cueId": positiveInt}
	importPreviewFields = fieldSet{"text": text}
	importFields        = fieldSet{"version": positiveInt, "text": text, "newRoles": newRoleList}
	addRoleFields       = fieldSet{"name": text, "view": text, "checksIn": boolean, "origin": text}
	renameFields        = fieldSet{"roleId": positiveInt, "name": text}
	deleteRoleFields    = fieldSet{"version": positiveInt, "roleId": positiveInt}
)

// LargeBodyBytes is the body limit of the pasted import only: its text of
// at most 100 KB may double when escaped as JSON (api.md section 1).
const LargeBodyBytes = 256 << 10

// bodyLimit is the limit of a request with these fields: only the import
// and its preview carry a pasted text.
func bodyLimit(fields fieldSet) int64 {
	if _, ok := fields["text"]; ok {
		return LargeBodyBytes
	}
	return MaxBodyBytes
}

// shiftFields is the shift's body, with the version for the real write
// and without it for the preview.
func shiftFields(withVersion bool) fieldSet {
	f := fieldSet{"fromCueId": positiveInt, "deltaMin": signedInt, "excluded": idList}
	if withVersion {
		f["version"] = positiveInt
	}
	return f
}

// readBody reads one JSON object of at most bodyLimit(fields) with exactly
// the given fields.
//
// The body is read token by token rather than decoded into a struct,
// because encoding/json matches field names case-insensitively and keeps
// the last of repeated names: {"VERSION":1} or {"version":"x","version":1}
// must be refused as unknown or malformed, not accepted. Numbers must be
// plain integers: 1.0, 1e0, -0 and leading zeros are refused.
func readBody(w http.ResponseWriter, r *http.Request, fields fieldSet) (body, bool) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, bodyLimit(fields)))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return body{}, false
	}
	var b body
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		key, isString := tok.(string)
		if err != nil || !isString || seen[key] {
			return body{}, false
		}
		kind, known := fields[key]
		if !known {
			return body{}, false
		}
		seen[key] = true
		switch kind {
		case positiveInt:
			v, ok := readInt(dec, false)
			if !ok {
				return body{}, false
			}
			switch key {
			case "version":
				b.version = v
			case "fromCueId":
				b.fromCueID = v
			case "roleId":
				b.roleID = v
			case "cueId":
				b.cueID = v
			case "afterCueId":
				b.afterCueID = v
			}
		case signedInt:
			v, ok := readInt(dec, true)
			if !ok || v < -1<<31 || v > 1<<31-1 {
				return body{}, false
			}
			b.deltaMin = int(v)
		case idList:
			if b.excluded, err = readIDs(dec); err != nil {
				return body{}, false
			}
		case boolean:
			tok, err := dec.Token()
			v, isBool := tok.(bool)
			if err != nil || !isBool {
				return body{}, false
			}
			switch key {
			case "checkedIn":
				b.checkedIn = v
			case "isFixed":
				b.isFixed = v
			case "checksIn":
				b.checksIn = v
			}
		case count:
			tok, err := dec.Token()
			n, isNumber := tok.(json.Number)
			if err != nil || !isNumber {
				return body{}, false
			}
			v, ok := parseInt(string(n), true)
			if !ok || v < 0 || v > 1<<31-1 {
				return body{}, false
			}
			b.durationMin = int(v)
		case text, optString:
			s, ok := readString(dec)
			if !ok {
				return body{}, false
			}
			switch key {
			case "title":
				b.title = s
			case "name":
				b.name = s
			case "view":
				b.view = s
			case "origin":
				b.origin, b.originGiven = s, true
			case "text":
				b.text = s
			}
		case nullableID:
			tok, err := dec.Token()
			if err != nil {
				return body{}, false
			}
			if tok != nil {
				n, isNumber := tok.(json.Number)
				v, ok := parseInt(string(n), false)
				if !isNumber || !ok {
					return body{}, false
				}
				b.afterCueID = v
			}
		case newRoleList:
			roles, ok := readNewRoles(dec)
			if !ok {
				return body{}, false
			}
			b.newRoles = roles
		case optText:
			s, ok := readOptString(dec)
			if !ok {
				return body{}, false
			}
			b.notePublic = s
		case timestamp:
			s, ok := readString(dec)
			if !ok {
				return body{}, false
			}
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return body{}, false
			}
			b.start = t
		case roleList:
			roles, ok := readRoles(dec)
			if !ok {
				return body{}, false
			}
			b.roles = roles
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return body{}, false
	}
	// Nothing but white space may follow the object.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return body{}, false
	}
	for key, kind := range fields {
		if !seen[key] && kind != idList && kind != optString {
			return body{}, false
		}
	}
	return b, true
}

// readInt reads one number token; null, strings, booleans, objects and
// arrays are refused.
func readInt(dec *json.Decoder, signed bool) (int64, bool) {
	tok, err := dec.Token()
	n, isNumber := tok.(json.Number)
	if err != nil || !isNumber {
		return 0, false
	}
	return parseInt(string(n), signed)
}

func readString(dec *json.Decoder) (string, bool) {
	tok, err := dec.Token()
	s, isString := tok.(string)
	return s, err == nil && isString
}

// readOptString reads a string or null; null is nil.
func readOptString(dec *json.Decoder) (*string, bool) {
	tok, err := dec.Token()
	if err != nil {
		return nil, false
	}
	if tok == nil {
		return nil, true
	}
	s, isString := tok.(string)
	return &s, isString
}

// readRoles reads the assignment list of a cue: each item exactly
// {"roleId":n,"roleNote":"…"|null}, with the same strictness as the body
// itself, and no role twice.
func readRoles(dec *json.Decoder) ([]store.Assignment, bool) {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, false
	}
	roles := []store.Assignment{}
	seenRole := map[int64]bool{}
	for dec.More() {
		if len(roles) == maxRoles {
			return nil, false
		}
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			return nil, false
		}
		var (
			a    store.Assignment
			seen = map[string]bool{}
		)
		for dec.More() {
			tok, err := dec.Token()
			key, isString := tok.(string)
			if err != nil || !isString || seen[key] {
				return nil, false
			}
			seen[key] = true
			var ok bool
			switch key {
			case "roleId":
				a.RoleID, ok = readInt(dec, false)
			case "roleNote":
				a.RoleNote, ok = readOptString(dec)
			}
			if !ok {
				return nil, false
			}
		}
		if tok, err := dec.Token(); err != nil || tok != json.Delim('}') || !seen["roleId"] || !seen["roleNote"] ||
			seenRole[a.RoleID] {
			return nil, false
		}
		seenRole[a.RoleID] = true
		roles = append(roles, a)
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
		return nil, false
	}
	return roles, true
}

// readNewRoles reads the roles an import creates: each item exactly
// {"name":"…","view":"…","checksIn":bool}, no name twice after trimming
// (api.md section 3). The names and screens are checked by the store.
func readNewRoles(dec *json.Decoder) ([]store.NewRole, bool) {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, false
	}
	roles := []store.NewRole{}
	names := map[string]bool{}
	for dec.More() {
		if len(roles) == maxRoles {
			return nil, false
		}
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			return nil, false
		}
		var (
			nr   store.NewRole
			seen = map[string]bool{}
		)
		for dec.More() {
			tok, err := dec.Token()
			key, isString := tok.(string)
			if err != nil || !isString || seen[key] {
				return nil, false
			}
			seen[key] = true
			var ok bool
			switch key {
			case "name":
				nr.Name, ok = readString(dec)
			case "view":
				var v string
				v, ok = readString(dec)
				nr.View = domain.View(v)
			case "checksIn":
				tok, err := dec.Token()
				nr.ChecksIn, ok = tok.(bool)
				ok = ok && err == nil
			}
			if !ok {
				return nil, false
			}
		}
		name := strings.TrimSpace(nr.Name)
		if tok, err := dec.Token(); err != nil || tok != json.Delim('}') || len(seen) != 3 || names[name] {
			return nil, false
		}
		names[name] = true
		roles = append(roles, nr)
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
		return nil, false
	}
	return roles, true
}

var errBadIDs = errors.New("excluded must be an array of ids")

func readIDs(dec *json.Decoder) ([]int64, error) {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, errBadIDs
	}
	ids := []int64{}
	for dec.More() {
		if len(ids) == maxExcluded {
			return nil, errBadIDs
		}
		id, ok := readInt(dec, false)
		if !ok {
			return nil, errBadIDs
		}
		ids = append(ids, id)
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
		return nil, errBadIDs
	}
	return ids, nil
}

// parseInt accepts only a JSON integer of ASCII digits, at most 18 of
// them and without leading zeros. Unsigned values must be 1 or more;
// signed ones may be negative but never -0.
func parseInt(raw string, signed bool) (int64, bool) {
	digits := raw
	if signed && len(raw) > 0 && raw[0] == '-' {
		digits = raw[1:]
	}
	if len(digits) == 0 || len(digits) > 18 || (len(digits) > 1 && digits[0] == '0') {
		return 0, false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || raw == "-0" {
		return 0, false
	}
	if !signed && v < 1 {
		return 0, false
	}
	return v, true
}
