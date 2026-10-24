// Package rundown reads the rundown CSV file of requirements/stories/m1.md (US-M-0,
// rundown file format): UTF-8 with an optional byte order mark, a header row of column
// names, then one cue per row.
//
//	seq          required  positive integer, unique
//	title        required  text
//	start        required  HH:MM, 24-hour, on the event date in the event's time zone
//	duration_min required  integer, 0 or more
//	fixed        optional  "Y" or blank
//	roles        optional  existing role names separated by ";"
//	role_note    optional  one note shared by every assigned role
//	note         optional  public note
//
// Columns are found by their header name; optional columns may be left
// out, unknown or repeated column names refuse the file. Every field is
// trimmed of surrounding white space. Reading never stops at the first bad
// row: each row carries its own problems, and the store refuses the whole
// file if any row has one (store.ImportRundown).
package rundown

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/BensonPigo/bethere/internal/store"
)

// MaxFileBytes bounds the file; a wedding rundown is a few kilobytes.
const MaxFileBytes = 1 << 20

var (
	columns  = []string{"seq", "title", "start", "duration_min", "fixed", "roles", "role_note", "note"}
	required = map[string]bool{"seq": true, "title": true, "start": true, "duration_min": true}
	bom      = []byte{0xEF, 0xBB, 0xBF}
)

// Parse reads a rundown file. It returns an error only for problems with
// the file as a whole (encoding of the header, missing or unknown columns,
// broken CSV quoting, size); a bad row is reported in that row's Problems.
func Parse(r io.Reader) ([]store.ImportRow, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("檔案超過 %d 位元組", MaxFileBytes)
	}
	return parse(bytes.TrimPrefix(data, bom), ',')
}

// Pasted text (M2c, US-M-12): the same columns, pasted from a spreadsheet.
const (
	MaxTextBytes = 100 << 10 // the decoded UTF-8 text (api.md section 3)
	MaxTextRows  = 200
)

// ParseText reads a rundown pasted into the editor: separated by tabs
// when its first line has one (copied from a spreadsheet), by commas
// otherwise (api.md section 3「貼上匯入」).
func ParseText(text string) ([]store.ImportRow, error) {
	if len(text) > MaxTextBytes {
		return nil, fmt.Errorf("貼上的內容超過 %d KB", MaxTextBytes>>10)
	}
	data := bytes.TrimPrefix([]byte(text), bom)
	comma := ','
	first, _, _ := bytes.Cut(data, []byte("\n"))
	if bytes.ContainsRune(first, '\t') {
		comma = '\t'
	}
	rows, err := parse(data, comma)
	if err != nil {
		return nil, err
	}
	// A header alone would replace the rundown with nothing (M2c verification P1).
	if len(rows) == 0 {
		return nil, errors.New("沒有任何環節列；第一列是欄位名稱，之後每列一個環節")
	}
	if len(rows) > MaxTextRows {
		return nil, fmt.Errorf("最多 %d 列環節", MaxTextRows)
	}
	return rows, nil
}

func parse(data []byte, comma rune) ([]store.ImportRow, error) {
	cr := csv.NewReader(bytes.NewReader(data))
	cr.Comma = comma
	cr.FieldsPerRecord = -1 // row length is checked per row below
	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("內容是空的；第一列必須是欄位名稱")
	}
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, name := range header {
		name = strings.TrimSpace(name)
		if !utf8.ValidString(name) {
			return nil, errors.New("標題列不是 UTF-8；請存成 UTF-8")
		}
		if !known(name) {
			return nil, fmt.Errorf("標題列：不認得的欄位 %q（欄位為 %s）", name, strings.Join(columns, ","))
		}
		if _, dup := col[name]; dup {
			return nil, fmt.Errorf("標題列：欄位 %q 出現兩次", name)
		}
		col[name] = i
	}
	for _, name := range columns {
		if _, ok := col[name]; required[name] && !ok {
			return nil, fmt.Errorf("標題列：缺少必要欄位 %q", name)
		}
	}

	var rows []store.ImportRow
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		line, _ := cr.FieldPos(0)
		rows = append(rows, parseRow(line, rec, col, len(header)))
	}
	return rows, nil
}

func known(name string) bool {
	for _, c := range columns {
		if c == name {
			return true
		}
	}
	return false
}

func parseRow(line int, rec []string, col map[string]int, width int) store.ImportRow {
	row := store.ImportRow{Line: line}
	bad := func(format string, args ...any) { row.Problems = append(row.Problems, fmt.Sprintf(format, args...)) }
	if len(rec) != width {
		bad("有 %d 欄，標題列有 %d 欄", len(rec), width)
		return row
	}
	for _, f := range rec {
		if !utf8.ValidString(f) {
			bad("不是 UTF-8；請存成 UTF-8")
			return row
		}
	}
	field := func(name string) string {
		if i, ok := col[name]; ok {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}

	if n, ok := integer(field("seq")); !ok || n < 1 {
		bad("seq %q：必須是正整數", field("seq"))
	} else {
		row.Seq = n
	}
	if row.Title = field("title"); row.Title == "" {
		bad("title：必填")
	}
	if h, m, ok := clock(field("start")); !ok {
		bad("start %q：必須是 24 小時制的 HH:MM（例如 09:05）", field("start"))
	} else {
		row.StartHour, row.StartMinute = h, m
	}
	if n, ok := integer(field("duration_min")); !ok || n > store.MaxCueDurationMin {
		bad("duration_min %q：必須是 0 到 %d 的整數", field("duration_min"), store.MaxCueDurationMin)
	} else {
		row.DurationMin = n
	}
	switch f := field("fixed"); f {
	case "":
	case "Y":
		row.Fixed = true
	default:
		bad("fixed %q：只能是 Y 或空白", f)
	}
	if roles := field("roles"); roles != "" {
		for _, name := range strings.Split(roles, ";") {
			row.Roles = append(row.Roles, strings.TrimSpace(name))
		}
	}
	row.RoleNote = field("role_note")
	row.Note = field("note")
	return row
}

// integer accepts only ASCII digits, so "+3", "3.0" and "1e2" are refused.
func integer(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// clock parses exactly HH:MM with two digits each, 00:00 to 23:59.
func clock(s string) (int, int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	h, okH := integer(s[:2])
	m, okM := integer(s[3:])
	if !okH || !okM || h > 23 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}
