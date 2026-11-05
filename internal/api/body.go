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
	date        string // an invited event's YYYY-MM-DD (US-M-17)
	newRoles    []store.NewRole
	// The four jobs (api.md section 9).
	job     string
	taskID  int64
	guestID int64
	itemID  int64
	state   string
	on      bool // acked, arrived or done
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
	addRoleFields       = fieldSet{"name": text, "view": text, "checksIn": boolean, "origin": text, "job": optString}
	renameFields        = fieldSet{"roleId": positiveInt, "name": text}
	deleteRoleFields    = fieldSet{"version": positiveInt, "roleId": positiveInt}
	// The four jobs carry no version (api.md section 9, ADR-0023).
	ackFields         = fieldSet{"cueId": positiveInt, "acked": boolean}
	addTaskFields     = fieldSet{"title": text}
	setTaskFields     = fieldSet{"taskId": positiveInt, "state": text}
	deleteTaskFields  = fieldSet{"taskId": positiveInt}
	guestImportFields = fieldSet{"text": text}
	arriveFields      = fieldSet{"guestId": positiveInt, "arrived": boolean}
	checklistFields   = fieldSet{"text": text}
	checkItemFields   = fieldSet{"itemId": positiveInt, "done": boolean}
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
	if !expectDelim(dec, '{') {
		return body{}, false
	}
	var b body
	seen := map[string]bool{}
	for dec.More() {
		key, ok := readKey(dec, seen)
		if !ok {
			return body{}, false
		}
		kind, known := fields[key]
		if !known {
			return body{}, false
		}
		if !readField(dec, kind, key, &b) {
			return body{}, false
		}
	}
	if !expectDelim(dec, '}') {
		return body{}, false
	}
	// Nothing but white space may follow the object.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return body{}, false
	}
	if missingRequired(fields, seen) {
		return body{}, false
	}
	return b, true
}

// expectDelim reads one token, which must be the delimiter d.
func expectDelim(dec *json.Decoder, d json.Delim) bool {
	tok, err := dec.Token()
	return err == nil && tok == d
}

// readKey reads the name of one field of an object; a name already in
// seen is refused, and a new one is added to it.
func readKey(dec *json.Decoder, seen map[string]bool) (string, bool) {
	key, ok := readString(dec)
	if !ok {
		return "", false
	}
	if seen[key] {
		return "", false
	}
	seen[key] = true
	return key, true
}

// missingRequired reports whether a field other than an idList or an
// optString one was left out.
func missingRequired(fields fieldSet, seen map[string]bool) bool {
	for key, kind := range fields {
		if !seen[key] && kind != idList && kind != optString {
			return true
		}
	}
	return false
}

// fieldReader reads the value of the field key into b.
type fieldReader func(dec *json.Decoder, key string, b *body) bool

var fieldReaders = map[fieldKind]fieldReader{
	positiveInt: readPositiveField,
	signedInt:   readSignedField,
	idList:      readIDsField,
	boolean:     readBoolField,
	count:       readCountField,
	text:        readTextField,
	optString:   readTextField,
	nullableID:  readNullableID,
	newRoleList: readNewRolesField,
	optText:     readOptTextField,
	timestamp:   readTimestampField,
	roleList:    readRolesField,
}

// readField reads one value of the given kind; an unknown kind is refused.
func readField(dec *json.Decoder, kind fieldKind, key string, b *body) bool {
	read, known := fieldReaders[kind]
	if !known {
		return false
	}
	return read(dec, key, b)
}

// intTarget is where the positiveInt field key goes in b, or nil.
func intTarget(b *body, key string) *int64 {
	switch key {
	case "version":
		return &b.version
	case "fromCueId":
		return &b.fromCueID
	case "roleId":
		return &b.roleID
	case "cueId":
		return &b.cueID
	case "afterCueId":
		return &b.afterCueID
	case "taskId":
		return &b.taskID
	case "guestId":
		return &b.guestID
	case "itemId":
		return &b.itemID
	}
	return nil
}

// boolTarget is where the boolean field key goes in b, or nil.
func boolTarget(b *body, key string) *bool {
	switch key {
	case "checkedIn":
		return &b.checkedIn
	case "isFixed":
		return &b.isFixed
	case "checksIn":
		return &b.checksIn
	case "acked", "arrived", "done":
		return &b.on
	}
	return nil
}

// stringTarget is where the text or optString field key goes in b, or nil.
func stringTarget(b *body, key string) *string {
	switch key {
	case "title":
		return &b.title
	case "name":
		return &b.name
	case "view":
		return &b.view
	case "origin":
		return &b.origin
	case "text", "recoveryCode", "inviteCode":
		return &b.text
	case "date":
		return &b.date
	case "job":
		return &b.job
	case "state":
		return &b.state
	}
	return nil
}

func readPositiveField(dec *json.Decoder, key string, b *body) bool {
	p := intTarget(b, key)
	if p == nil {
		return false
	}
	v, ok := readInt(dec, false)
	if !ok {
		return false
	}
	*p = v
	return true
}

// readSignedField reads a shift in minutes within the int32 range.
func readSignedField(dec *json.Decoder, _ string, b *body) bool {
	v, ok := readInt(dec, true)
	if !ok || v < -1<<31 || v > 1<<31-1 {
		return false
	}
	b.deltaMin = int(v)
	return true
}

func readIDsField(dec *json.Decoder, _ string, b *body) bool {
	ids, err := readIDs(dec)
	if err != nil {
		return false
	}
	b.excluded = ids
	return true
}

func readBoolField(dec *json.Decoder, key string, b *body) bool {
	p := boolTarget(b, key)
	if p == nil {
		return false
	}
	v, ok := readBool(dec)
	if !ok {
		return false
	}
	*p = v
	return true
}

// readCountField reads a length in minutes, 0..2^31-1. It is parsed as
// signed, so 0 passes parseInt and -0 is still refused.
func readCountField(dec *json.Decoder, _ string, b *body) bool {
	v, ok := readInt(dec, true)
	if !ok || v < 0 || v > 1<<31-1 {
		return false
	}
	b.durationMin = int(v)
	return true
}

// readTextField reads a text or optString field; origin is marked as
// given even when it is "".
func readTextField(dec *json.Decoder, key string, b *body) bool {
	p := stringTarget(b, key)
	if p == nil {
		return false
	}
	s, ok := readString(dec)
	if !ok {
		return false
	}
	*p = s
	if key == "origin" {
		b.originGiven = true
	}
	return true
}

// readNullableID reads an id or null; null leaves afterCueID at 0.
func readNullableID(dec *json.Decoder, _ string, b *body) bool {
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	if tok == nil {
		return true
	}
	n, isNumber := tok.(json.Number)
	v, ok := parseInt(string(n), false)
	if !isNumber || !ok {
		return false
	}
	b.afterCueID = v
	return true
}

func readNewRolesField(dec *json.Decoder, _ string, b *body) bool {
	roles, ok := readNewRoles(dec)
	if !ok {
		return false
	}
	b.newRoles = roles
	return true
}

func readOptTextField(dec *json.Decoder, _ string, b *body) bool {
	s, ok := readOptString(dec)
	if !ok {
		return false
	}
	b.notePublic = s
	return true
}

func readTimestampField(dec *json.Decoder, _ string, b *body) bool {
	s, ok := readString(dec)
	if !ok {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return false
	}
	b.start = t
	return true
}

func readRolesField(dec *json.Decoder, _ string, b *body) bool {
	roles, ok := readRoles(dec)
	if !ok {
		return false
	}
	b.roles = roles
	return true
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

// readBool reads true or false; null and every other token are refused.
func readBool(dec *json.Decoder) (bool, bool) {
	tok, err := dec.Token()
	v, isBool := tok.(bool)
	return v, err == nil && isBool
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
	if !expectDelim(dec, '[') {
		return nil, false
	}
	roles := []store.Assignment{}
	seenRole := map[int64]bool{}
	for dec.More() {
		if len(roles) == maxRoles {
			return nil, false
		}
		a, ok := readRoleItem(dec, seenRole)
		if !ok {
			return nil, false
		}
		roles = append(roles, a)
	}
	if !expectDelim(dec, ']') {
		return nil, false
	}
	return roles, true
}

// readRoleItem reads one item of readRoles with its own seen keys; a role
// already in seenRole is refused, and a new one is added to it.
func readRoleItem(dec *json.Decoder, seenRole map[int64]bool) (store.Assignment, bool) {
	var a store.Assignment
	if !expectDelim(dec, '{') {
		return a, false
	}
	seen := map[string]bool{}
	for dec.More() {
		key, ok := readKey(dec, seen)
		if !ok {
			return a, false
		}
		if !readRoleField(dec, key, &a) {
			return a, false
		}
	}
	if !expectDelim(dec, '}') {
		return a, false
	}
	if !seen["roleId"] || !seen["roleNote"] || seenRole[a.RoleID] {
		return a, false
	}
	seenRole[a.RoleID] = true
	return a, true
}

// readRoleField reads one field of a readRoles item; any other key is
// refused.
func readRoleField(dec *json.Decoder, key string, a *store.Assignment) bool {
	var ok bool
	switch key {
	case "roleId":
		a.RoleID, ok = readInt(dec, false)
	case "roleNote":
		a.RoleNote, ok = readOptString(dec)
	}
	return ok
}

// readNewRoles reads the roles an import creates: each item exactly
// {"name":"…","view":"…","checksIn":bool}, no name twice after trimming
// (api.md section 3). The names and screens are checked by the store.
func readNewRoles(dec *json.Decoder) ([]store.NewRole, bool) {
	if !expectDelim(dec, '[') {
		return nil, false
	}
	roles := []store.NewRole{}
	names := map[string]bool{}
	for dec.More() {
		if len(roles) == maxRoles {
			return nil, false
		}
		nr, ok := readNewRoleItem(dec, names)
		if !ok {
			return nil, false
		}
		roles = append(roles, nr)
	}
	if !expectDelim(dec, ']') {
		return nil, false
	}
	return roles, true
}

// readNewRoleItem reads one item of readNewRoles with its own seen keys; a
// trimmed name already in names is refused, and a new one is added to it.
func readNewRoleItem(dec *json.Decoder, names map[string]bool) (store.NewRole, bool) {
	var nr store.NewRole
	if !expectDelim(dec, '{') {
		return nr, false
	}
	seen := map[string]bool{}
	for dec.More() {
		key, ok := readKey(dec, seen)
		if !ok {
			return nr, false
		}
		if !readNewRoleField(dec, key, &nr) {
			return nr, false
		}
	}
	name := strings.TrimSpace(nr.Name)
	if !expectDelim(dec, '}') {
		return nr, false
	}
	if len(seen) != 3 || names[name] {
		return nr, false
	}
	names[name] = true
	return nr, true
}

// readNewRoleField reads one field of a readNewRoles item; any other key
// is refused.
func readNewRoleField(dec *json.Decoder, key string, nr *store.NewRole) bool {
	var ok bool
	switch key {
	case "name":
		nr.Name, ok = readString(dec)
	case "view":
		var v string
		v, ok = readString(dec)
		nr.View = domain.View(v)
	case "checksIn":
		nr.ChecksIn, ok = readBool(dec)
	}
	return ok
}

var errBadIDs = errors.New("excluded must be an array of ids")

func readIDs(dec *json.Decoder) ([]int64, error) {
	if !expectDelim(dec, '[') {
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
	if !expectDelim(dec, ']') {
		return nil, errBadIDs
	}
	return ids, nil
}

// parseInt accepts only a JSON integer of ASCII digits, at most 18 of
// them and without leading zeros. Unsigned values must be 1 or more;
// signed ones may be negative but never -0.
func parseInt(raw string, signed bool) (int64, bool) {
	digits := raw
	if signed {
		digits = strings.TrimPrefix(raw, "-")
	}
	if !plainLength(digits) {
		return 0, false
	}
	if !allDigits(digits) {
		return 0, false
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

// plainLength reports whether digits is 1 to 18 long, without a leading
// zero unless it is the only digit.
func plainLength(digits string) bool {
	if len(digits) == 0 || len(digits) > 18 {
		return false
	}
	return len(digits) == 1 || digits[0] != '0'
}

// allDigits reports whether s is made of ASCII digits only.
func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
