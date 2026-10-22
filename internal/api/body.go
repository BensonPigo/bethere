package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

// body is a parsed write or preview request (api.md section 1).
type body struct {
	version   int64
	fromCueID int64
	deltaMin  int
	excluded  []int64
}

type fieldKind int

const (
	positiveInt fieldKind = iota // an id or a version: 1 or more
	signedInt                    // a shift in minutes
	idList                       // an array of ids, at most maxExcluded
)

// fieldSet is the fields a request takes; all are required but
// "excluded", which may be left out.
type fieldSet map[string]fieldKind

// maxExcluded bounds the unticked cues of one shift; a rundown has a few
// dozen cues.
const maxExcluded = 200

var versionOnly = fieldSet{"version": positiveInt}

// shiftFields is the shift's body, with the version for the real write
// and without it for the preview.
func shiftFields(withVersion bool) fieldSet {
	f := fieldSet{"fromCueId": positiveInt, "deltaMin": signedInt, "excluded": idList}
	if withVersion {
		f["version"] = positiveInt
	}
	return f
}

// readBody reads one JSON object of at most MaxBodyBytes with exactly the
// given fields.
//
// The body is read token by token rather than decoded into a struct,
// because encoding/json matches field names case-insensitively and keeps
// the last of repeated names: {"VERSION":1} or {"version":"x","version":1}
// must be refused as unknown or malformed, not accepted. Numbers must be
// plain integers: 1.0, 1e0, -0 and leading zeros are refused.
func readBody(w http.ResponseWriter, r *http.Request, fields fieldSet) (body, bool) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
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
		if !seen[key] && kind != idList {
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
