package api

// The differential harness of plan RB1 (docs/process/readbody-split-plan.md,
// 驗收 1): every input is read by readBody and by readBodyOld, a renamed
// copy of the call graph of origin/main:internal/api/body.go at the end of
// this file, under every field set the package passes to readBody. The
// two must give the same body and ok, refused inputs included, and neither
// may panic.
//
// This file is removed again before the PR is merged.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

type readBodyFunc func(http.ResponseWriter, *http.Request, fieldSet) (body, bool)

// readOutcome is one call of a readBodyFunc: its results, or the value it
// panicked with.
type readOutcome struct {
	b        body
	ok       bool
	panicked any
}

// readLikeCallers reads in the way the handlers do: a POST request with in
// as its body and a response writer, which readBody wraps in its
// MaxBytesReader itself.
func readLikeCallers(read readBodyFunc, fields fieldSet, in string) (out readOutcome) {
	defer func() { out.panicked = recover() }()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(in))
	out.b, out.ok = read(httptest.NewRecorder(), r, fields)
	return out
}

func shortInput(in string) string {
	if len(in) > 200 {
		return fmt.Sprintf("%q… (%d bytes)", in[:200], len(in))
	}
	return fmt.Sprintf("%q", in)
}

// compareReadBody fails t when readBody and readBodyOld differ on in.
func compareReadBody(t *testing.T, fs namedFieldSet, in string) {
	t.Helper()
	got := readLikeCallers(readBody, fs.fields, in)
	want := readLikeCallers(readBodyOld, fs.fields, in)
	if got.panicked != nil || want.panicked != nil {
		t.Fatalf("%s: panic on %s: new %v, old %v", fs.name, shortInput(in), got.panicked, want.panicked)
	}
	if got.ok != want.ok || !reflect.DeepEqual(got.b, want.b) {
		t.Fatalf("%s: %s\nnew %v %+v\nold %v %+v", fs.name, shortInput(in), got.ok, got.b, want.ok, want.b)
	}
}

func FuzzReadBodyOracle(f *testing.F) {
	for _, s := range oracleSeeds(f) {
		f.Add(s)
	}
	sets := allFieldSets()
	f.Fuzz(func(t *testing.T, in string) {
		for _, fs := range sets {
			compareReadBody(t, fs, in)
		}
	})
}

// withPrefix puts fields in front of the fields of a body made by
// rolesBody, newRolesBody or excludedBody.
func withPrefix(prefix, b string) string {
	return "{" + prefix + "," + strings.TrimPrefix(b, "{")
}

// padTo pads the JSON object obj with spaces before its closing brace to
// exactly n bytes.
func padTo(obj string, n int) string {
	return obj[:len(obj)-1] + strings.Repeat(" ", n-len(obj)) + "}"
}

// textOf is a body with one text field whose value fills it to exactly n
// bytes.
func textOf(prefix string, n int) string {
	head := `{` + prefix + `"text":"`
	return head + strings.Repeat("x", n-len(head)-2) + `"}`
}

// oracleSeeds is the seed corpus of plan RB1, 驗收 1「種子」.
func oracleSeeds(f *testing.F) []string {
	var seeds []string

	// A valid body per field set (the cases of TestEveryFieldMapsToBody),
	// and the optional fields left out.
	cases := fieldMappingCases()
	names := make([]string, 0, len(cases))
	for name := range cases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		seeds = append(seeds, cases[name].in)
	}
	seeds = append(seeds,
		`{"roleId":13}`,
		`{"roleId":13,"origin":""}`,
		`{"name":"攝影","view":"crew","checksIn":false,"origin":""}`,
		`{"name":"攝影","view":"crew","checksIn":false,"origin":"","job":""}`,
		`{"version":11,"fromCueId":12,"deltaMin":0}`,
		`{"fromCueId":12,"deltaMin":5,"excluded":[]}`,
		" \n{ \"version\" : 7 }\n ",
		"\t{\"version\":7}\r\n",
	)

	// TestWriteBodyIsStrict (internal/server/api_test.go), with v = 7.
	v := "7"
	seeds = append(seeds,
		"", " ", "null", "{}", "[]", "1", `"x"`,
		`{"version":null}`, `{"version":"`+v+`"}`, `{"version":`+v+`.0}`, `{"version":1e0}`,
		`{"version":0}`, `{"version":-1}`, `{"version":true}`, `{"version":[1]}`, `{"version":{}}`,
		`{"version":99999999999999999999}`,
		`{"version":`+v+`,"x":1}`, `{"Version":`+v+`}`, `{"VERSION":`+v+`}`,
		`{"version":`+v+`,"version":`+v+`}`,
		`{"version":`+v+`}{"version":`+v+`}`, `{"version":`+v+`} x`, `{"version":`+v,
		`{"version":`+v+`,"eventId":1}`, `{"version":`+v+`,"roleId":1}`,
		`{"version":`+v+`,"pad":"`+strings.Repeat("x", MaxBodyBytes)+`"}`,
		strings.Repeat(" ", MaxBodyBytes+1)+`{"version":1}`,
	)

	// TestShiftBodyIsStrict (internal/server/shift_and_notice_test.go),
	// with v = 7 and from = 3.
	from := "3"
	many := make([]string, 201)
	for i := range many {
		many[i] = "1"
	}
	seeds = append(seeds,
		`{}`, `{"version":`+v+`}`,
		`{"version":`+v+`,"fromCueId":`+from+`}`,
		`{"version":`+v+`,"deltaMin":5}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":-0}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":05}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":5.0}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":"5"}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":null}`,
		`{"version":`+v+`,"fromCueId":-1,"deltaMin":5}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":5,"excluded":null}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":5,"excluded":"1"}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":5,"excluded":["1"]}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":5,"excluded":[0]}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":5,"excluded":[`+strings.Join(many, ",")+`]}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":5,"DeltaMin":5}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":5,"deltaMin":5}`,
		`{"version":`+v+`,"fromCueId":`+from+`,"deltaMin":5,"eventId":1}`,
		`{"fromCueId":`+from+`,"deltaMin":5}`,
	)

	// body_test.go.
	seeds = append(seeds,
		`{"version":123456789012345678}`,
		`{"deltaMin":-2147483648}`,
		`{"deltaMin":2147483647}`,
		`{"deltaMin":5}`,
		`{"durationMin":0}`,
		`{"durationMin":2147483647}`,
		`{"version":1234567890123456789}`,
		`{"deltaMin":-2147483649}`,
		`{"deltaMin":2147483648}`,
		`{"durationMin":2147483648}`,
		`{"durationMin":-1}`,
		`{"roles":[{"roleId":3,"roleNote":null}]}`,
		`{"roles":[{"roleNote":null}]}`,
		`{"roles":[{"roleId":3}]}`,
		`{"roles":[{"roleId":3,"roleId":4,"roleNote":null}]}`,
		`{"roles":[{"roleId":3,"roleNote":null,"roleNote":""}]}`,
		`{"newRoles":[{"name":"新秘","view":"x","checksIn":true}]}`,
		`{"newRoles":[{"name":"新秘","view":"x"}]}`,
		`{"newRoles":[{"name":"新秘","name":"攝影","view":"x","checksIn":true}]}`,
		`{"newRoles":[{"name":"新秘","view":"x","checksIn":"yes"}]}`,
		`{"newRoles":[{"name":"新秘","view":"x","checksIn":null}]}`,
	)
	// The same items inside the field sets that take them.
	for _, item := range []string{
		`{"roleId":3,"roleNote":null}`, `{"roleNote":null}`, `{"roleId":3}`,
		`{"roleId":3,"roleId":4,"roleNote":null}`, `{"roleId":3,"roleNote":null,"roleNote":""}`,
		`{"roleNote":"x","roleId":3}`, `{"roleId":3,"roleNote":null,"x":1}`, `{"roleId":0,"roleNote":null}`,
		`{"roleId":3,"roleNote":1}`, `{}`, `["roleId",1,"roleNote",null]`, `null`,
	} {
		seeds = append(seeds, `{"version":1,"cueId":2,"roles":[`+item+`]}`)
	}
	seeds = append(seeds,
		`{"version":1,"cueId":2,"roles":[{"roleId":3,"roleNote":null},{"roleId":3,"roleNote":"x"}]}`,
		`{"version":1,"cueId":2,"roles":[]}`,
		`{"version":1,"cueId":2,"roles":{}}`,
		`{"version":1,"cueId":2,"roles":null}`,
	)
	for _, item := range []string{
		`{"name":"新秘","view":"x","checksIn":true}`, `{"name":"新秘","view":"x"}`,
		`{"name":"新秘","name":"攝影","view":"x","checksIn":true}`, `{"name":"新秘","view":"x","checksIn":"yes"}`,
		`{"name":"新秘","view":"x","checksIn":null}`, `{"checksIn":false,"view":"x","name":"新秘"}`,
		`{"name":"新秘","view":"x","checksIn":true,"x":1}`, `{"name":"新秘","view":1,"checksIn":true}`,
		`{"name":null,"view":"x","checksIn":true}`, `{}`, `[]`,
	} {
		seeds = append(seeds, `{"version":1,"text":"t","newRoles":[`+item+`]}`)
	}

	// TestBracketsAreChecked (body_spec_test.go).
	seeds = append(seeds,
		`["version",1]`,
		`[]`,
		`{"roles":[["roleId",1,"roleNote",null]]}`,
		`{"version":1,"fromCueId":2,"deltaMin":3,"excluded":{}}`,
		`{"version":1,"fromCueId":2,"deltaMin":3,"excluded":[1}`,
		`{"version":1]`,
	)

	// roles and newRoles at 50 and 51 items, excluded at 200 and 201 ids.
	for _, n := range []int{50, 51} {
		seeds = append(seeds,
			rolesBody(n),
			withPrefix(`"version":1,"cueId":2`, rolesBody(n)),
			newRolesBody(n),
			withPrefix(`"version":1,"text":"t"`, newRolesBody(n)),
		)
	}
	for _, n := range []int{200, 201} {
		seeds = append(seeds, excludedBody(n), withPrefix(`"version":1`, excludedBody(n)))
	}

	// Names that are the same after strings.TrimSpace, Unicode white space
	// included, written both as JSON escapes and as the raw characters.
	for _, name := range []string{
		`\u3000攝影`, "\u3000攝影", `攝影\u3000`, "攝影\u3000", `\u00a0攝影`, "\u00a0攝影",
		`\u2003攝影`, `\u0085攝影`, "\u0085攝影", ` 攝影 `, `\t攝影\n`,
		`\u200b攝影`, "\u200b攝影", `\ufeff攝影`, `攝影`, `攝 影`,
	} {
		seeds = append(seeds, `{"version":1,"text":"t","newRoles":[{"name":"攝影","view":"crew","checksIn":true},`+
			`{"name":"`+name+`","view":"crew","checksIn":false}]}`)
	}
	seeds = append(seeds,
		`{"version":1,"text":"t","newRoles":[{"name":"","view":"crew","checksIn":true},{"name":"\u3000","view":"crew","checksIn":true}]}`,
	)

	// Escaped field names: the decoded name is what is matched.
	escaped := []string{
		`{"\u0076ersion":1}`,
		`{"version":1,"\u0076ersion":1}`,
		`{"\u0056ersion":1}`,
		`{"version\u0000":1}`,
		`{"\u0076ersion":1,"\u0063ueId":2}`,
		`{"version":1,"cueId":2,"roles":[{"\u0072oleId":3,"roleNote":null}]}`,
		`{"version":1,"cueId":2,"roles":[{"roleId":3,"\u0072oleNote":null,"roleNote":null}]}`,
		`{"version":1,"text":"t","newRoles":[{"n\u0061me":"a","view":"crew","checksIn":true}]}`,
		`{"recoveryCode":"r","\u0072ecoveryCode":"r"}`,
	}
	// Each seed must carry a real backslash escape, not the character
	// it stands for.
	for _, s := range escaped {
		if !strings.Contains(s, "\\u00") {
			f.Fatalf("%s: the seed has no JSON escape", s)
		}
	}
	seeds = append(seeds, escaped...)

	// null and strings for notePublic, roleNote and afterCueId.
	const ts = `"2027-06-06T10:30:00Z"`
	for _, note := range []string{`null`, `""`, `"公開"`, `1`, `true`, `{}`} {
		seeds = append(seeds,
			`{"version":1,"cueId":2,"title":"t","notePublic":`+note+`}`,
			`{"version":1,"cueId":2,"roles":[{"roleId":3,"roleNote":`+note+`}]}`,
		)
	}
	for _, after := range []string{`null`, `5`, `"5"`, `0`, `-1`, `05`, `5.0`, `true`, `[]`, `-0`, `1e1`} {
		seeds = append(seeds,
			`{"version":1,"cueId":2,"afterCueId":`+after+`}`,
			`{"version":1,"afterCueId":`+after+`,"start":`+ts+`,"durationMin":5,"title":"t","notePublic":null,"roles":[]}`,
		)
	}

	// RFC 3339: zones, fractional seconds, lower case t and z.
	for _, start := range []string{
		"2027-06-06T10:30:00Z", "2027-06-06T10:30:00+08:00", "2027-06-06T10:30:00-05:30",
		"2027-06-06T10:30:00+00:00", "2027-06-06T10:30:00-00:00", "2027-06-06T10:30:00+14:00",
		"2027-06-06T10:30:00.5Z", "2027-06-06T10:30:00.123456789Z", "2027-06-06T10:30:00.1234567891Z",
		"2027-06-06T10:30:00,5Z", "2027-06-06T10:30:00.Z", "2027-06-06T10:30:00.000+08:00",
		"2027-06-06t10:30:00z", "2027-06-06t10:30:00Z", "2027-06-06T10:30:00z", "2027-06-06 10:30:00Z",
		"2027-06-06T10:30:00", "2027-06-06", "2027-06-06T10:30Z", "2027-06-06T24:00:00Z",
		"2027-06-06T23:59:60Z", "2027-02-30T10:30:00Z", "2027-06-06T10:30:00+24:00", "2027-06-06T10:30:00+08:60",
		"2027-06-06T10:30:00+0800", "0000-01-01T00:00:00Z", "9999-12-31T23:59:59.999999999Z", "",
	} {
		seeds = append(seeds,
			`{"version":1,"cueId":2,"start":"`+start+`","durationMin":0,"isFixed":false,"title":"t","notePublic":null}`)
	}
	seeds = append(seeds,
		`{"version":1,"cueId":2,"start":null,"durationMin":0,"isFixed":false,"title":"t","notePublic":null}`,
		`{"version":1,"cueId":2,"start":1,"durationMin":0,"isFixed":false,"title":"t","notePublic":null}`,
	)

	// Numbers, booleans and strings at their edges.
	for _, n := range []string{
		"-0", "0", "00", "01", "1", "-1", "+1", "1.0", "1e0", "1E0", "1.", ".1", "18446744073709551616",
		"123456789012345678", "-123456789012345678", "1234567890123456789", "-1234567890123456789",
		"999999999999999999", "-999999999999999999", "2147483647", "2147483648", "-2147483648", "-2147483649",
		"9223372036854775807", "-9223372036854775808", "--1", "-", `"1"`, "null", "true", "[]", "{}",
	} {
		seeds = append(seeds,
			`{"version":`+n+`}`,
			`{"fromCueId":1,"deltaMin":`+n+`}`,
			`{"version":1,"cueId":2,"start":`+ts+`,"durationMin":`+n+`,"isFixed":true,"title":"t","notePublic":null}`,
			`{"fromCueId":1,"deltaMin":1,"excluded":[`+n+`]}`,
		)
	}
	for _, flag := range []string{"true", "false", "1", "0", `"true"`, "null", "True", "[]"} {
		seeds = append(seeds,
			`{"checkedIn":`+flag+`}`,
			`{"cueId":1,"acked":`+flag+`}`,
			`{"version":1,"text":"t","newRoles":[{"name":"a","view":"crew","checksIn":`+flag+`}]}`,
		)
	}
	seeds = append(seeds,
		`{"roleId":1,"origin":null}`,
		`{"roleId":1,"origin":1}`,
		`{"name":"a","view":"crew","checksIn":true,"origin":"o","job":null}`,
		`{"name":"a","view":"crew","checksIn":true,"job":"j"}`,
		`{"inviteCode":"","name":"","date":""}`,
		`{"inviteCode":"c","name":"n","date":"2027-06-06","text":"t"}`,
		`{"recoveryCode":"\"\\\/\b\f\n\r\t\u00e9\ud83d\ude00\ud800"}`,
		"{\"text\":\"\xff\xfe\"}",
		"\xef\xbb\xbf{\"version\":1}",
		`{"":1}`,
		`{"version":1}}`, `{"version":1}]`, `{"version":1},`, `{"version":1} 1`, `{"version":1}null`,
		`{"version":1,}`, `{,"version":1}`, `{"version" 1}`, `{"version":1 "cueId":2}`, `{version:1}`,
		`{"version":`+strings.Repeat("[", 100)+strings.Repeat("]", 100)+`}`,
		`{"version":1,"cueId":2,"roles":[`+strings.Repeat(`{"roleId":{"roleId":`, 50)+`}]}`,
	)

	// Bodies near the 16 KB limit and near the 256 KB limit of the text
	// field sets.
	for _, n := range []int{MaxBodyBytes - 1, MaxBodyBytes, MaxBodyBytes + 1} {
		seeds = append(seeds,
			padTo(`{"version":1}`, n),
			padTo(`{"fromCueId":1,"deltaMin":5}`, n),
			`{"version":1}`+strings.Repeat(" ", n-len(`{"version":1}`)),
			textOf("", n),
			textOf(`"version":1,`, n),
		)
	}
	for _, n := range []int{LargeBodyBytes - 1, LargeBodyBytes, LargeBodyBytes + 1} {
		seeds = append(seeds,
			textOf("", n),
			padTo(`{"text":"t"}`, n),
			`{"text":"t"}`+strings.Repeat(" ", n-len(`{"text":"t"}`)),
			padTo(`{"version":1,"text":"t","newRoles":[]}`, n),
		)
	}
	return seeds
}

// TestParseIntOracleExhaustive compares parseInt with parseIntOld on every
// string of length 0 to 5 over the alphabet 0-9, '-', '+', '.', 'e' and
// 'E', both signed and unsigned.
func TestParseIntOracleExhaustive(t *testing.T) {
	const alphabet = "0123456789-+.eE"
	checked := 0
	var walk func(prefix []byte)
	walk = func(prefix []byte) {
		raw := string(prefix)
		for _, signed := range []bool{true, false} {
			v, ok := parseInt(raw, signed)
			oldV, oldOK := parseIntOld(raw, signed)
			if v != oldV || ok != oldOK {
				t.Fatalf("parseInt(%q, %v) = %d, %v; parseIntOld = %d, %v", raw, signed, v, ok, oldV, oldOK)
			}
		}
		checked++
		if len(prefix) == 5 {
			return
		}
		for i := 0; i < len(alphabet); i++ {
			walk(append(prefix, alphabet[i]))
		}
	}
	walk(make([]byte, 0, 5))
	// 1 + 15 + 15² + 15³ + 15⁴ + 15⁵ strings.
	if checked != 813616 {
		t.Errorf("checked %d strings, want 813616", checked)
	}
}

// ---- BEGIN OLD COPY: origin/main:internal/api/body.go, renamed with the suffix Old ----

// maxRolesOld bounds the roles of one cue (api.md section 3).
const maxRolesOld = 50

// maxExcludedOld bounds the unticked cues of one shift; a rundown has a few
// dozen cues.
const maxExcludedOld = 200

// LargeBodyBytesOld is the body limit of the pasted import only: its text of
// at most 100 KB may double when escaped as JSON (api.md section 1).
const LargeBodyBytesOld = 256 << 10

// bodyLimitOld is the limit of a request with these fields: only the import
// and its preview carry a pasted text.
func bodyLimitOld(fields fieldSet) int64 {
	if _, ok := fields["text"]; ok {
		return LargeBodyBytesOld
	}
	return MaxBodyBytes
}

// readBodyOld reads one JSON object of at most bodyLimitOld(fields) with exactly
// the given fields.
//
// The body is read token by token rather than decoded into a struct,
// because encoding/json matches field names case-insensitively and keeps
// the last of repeated names: {"VERSION":1} or {"version":"x","version":1}
// must be refused as unknown or malformed, not accepted. Numbers must be
// plain integers: 1.0, 1e0, -0 and leading zeros are refused.
func readBodyOld(w http.ResponseWriter, r *http.Request, fields fieldSet) (body, bool) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, bodyLimitOld(fields)))
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
			v, ok := readIntOld(dec, false)
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
			case "taskId":
				b.taskID = v
			case "guestId":
				b.guestID = v
			case "itemId":
				b.itemID = v
			}
		case signedInt:
			v, ok := readIntOld(dec, true)
			if !ok || v < -1<<31 || v > 1<<31-1 {
				return body{}, false
			}
			b.deltaMin = int(v)
		case idList:
			if b.excluded, err = readIDsOld(dec); err != nil {
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
			case "acked", "arrived", "done":
				b.on = v
			}
		case count:
			tok, err := dec.Token()
			n, isNumber := tok.(json.Number)
			if err != nil || !isNumber {
				return body{}, false
			}
			v, ok := parseIntOld(string(n), true)
			if !ok || v < 0 || v > 1<<31-1 {
				return body{}, false
			}
			b.durationMin = int(v)
		case text, optString:
			s, ok := readStringOld(dec)
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
			case "text", "recoveryCode", "inviteCode":
				b.text = s
			case "date":
				b.date = s
			case "job":
				b.job = s
			case "state":
				b.state = s
			}
		case nullableID:
			tok, err := dec.Token()
			if err != nil {
				return body{}, false
			}
			if tok != nil {
				n, isNumber := tok.(json.Number)
				v, ok := parseIntOld(string(n), false)
				if !isNumber || !ok {
					return body{}, false
				}
				b.afterCueID = v
			}
		case newRoleList:
			roles, ok := readNewRolesOld(dec)
			if !ok {
				return body{}, false
			}
			b.newRoles = roles
		case optText:
			s, ok := readOptStringOld(dec)
			if !ok {
				return body{}, false
			}
			b.notePublic = s
		case timestamp:
			s, ok := readStringOld(dec)
			if !ok {
				return body{}, false
			}
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return body{}, false
			}
			b.start = t
		case roleList:
			roles, ok := readRolesOld(dec)
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

// readIntOld reads one number token; null, strings, booleans, objects and
// arrays are refused.
func readIntOld(dec *json.Decoder, signed bool) (int64, bool) {
	tok, err := dec.Token()
	n, isNumber := tok.(json.Number)
	if err != nil || !isNumber {
		return 0, false
	}
	return parseIntOld(string(n), signed)
}

func readStringOld(dec *json.Decoder) (string, bool) {
	tok, err := dec.Token()
	s, isString := tok.(string)
	return s, err == nil && isString
}

// readOptStringOld reads a string or null; null is nil.
func readOptStringOld(dec *json.Decoder) (*string, bool) {
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

// readRolesOld reads the assignment list of a cue: each item exactly
// {"roleId":n,"roleNote":"…"|null}, with the same strictness as the body
// itself, and no role twice.
func readRolesOld(dec *json.Decoder) ([]store.Assignment, bool) {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, false
	}
	roles := []store.Assignment{}
	seenRole := map[int64]bool{}
	for dec.More() {
		if len(roles) == maxRolesOld {
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
				a.RoleID, ok = readIntOld(dec, false)
			case "roleNote":
				a.RoleNote, ok = readOptStringOld(dec)
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

// readNewRolesOld reads the roles an import creates: each item exactly
// {"name":"…","view":"…","checksIn":bool}, no name twice after trimming
// (api.md section 3). The names and screens are checked by the store.
func readNewRolesOld(dec *json.Decoder) ([]store.NewRole, bool) {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, false
	}
	roles := []store.NewRole{}
	names := map[string]bool{}
	for dec.More() {
		if len(roles) == maxRolesOld {
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
				nr.Name, ok = readStringOld(dec)
			case "view":
				var v string
				v, ok = readStringOld(dec)
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

var errBadIDsOld = errors.New("excluded must be an array of ids")

func readIDsOld(dec *json.Decoder) ([]int64, error) {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, errBadIDsOld
	}
	ids := []int64{}
	for dec.More() {
		if len(ids) == maxExcludedOld {
			return nil, errBadIDsOld
		}
		id, ok := readIntOld(dec, false)
		if !ok {
			return nil, errBadIDsOld
		}
		ids = append(ids, id)
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
		return nil, errBadIDsOld
	}
	return ids, nil
}

// parseIntOld accepts only a JSON integer of ASCII digits, at most 18 of
// them and without leading zeros. Unsigned values must be 1 or more;
// signed ones may be negative but never -0.
func parseIntOld(raw string, signed bool) (int64, bool) {
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

// ---- END OLD COPY ----
