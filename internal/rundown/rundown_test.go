package rundown

import (
	"strings"
	"testing"
)

func TestParseTheAcceptanceFormat(t *testing.T) {
	file := "\xEF\xBB\xBF" + // Excel's byte order mark
		"seq,title,start,duration_min,fixed,roles,role_note,note\r\n" +
		"1,新娘化妝,08:00,120,,新秘,新娘房集合,\r\n" +
		"3,一進預備,11:15,15,Y,新秘;攝影A,\"側門, 二樓\",公開\r\n" +
		"2, 迎賓 , 10:30 ,0,,,,\r\n"
	rows, err := Parse(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	r := rows[1]
	if r.Line != 3 || r.Seq != 3 || r.Title != "一進預備" || r.StartHour != 11 || r.StartMinute != 15 ||
		r.DurationMin != 15 || !r.Fixed || len(r.Roles) != 2 || r.Roles[1] != "攝影A" ||
		r.RoleNote != "側門, 二樓" || r.Note != "公開" || len(r.Problems) != 0 {
		t.Errorf("row 3 = %+v", r)
	}
	if r := rows[2]; r.Title != "迎賓" || r.StartHour != 10 || r.Fixed || r.Roles != nil || len(r.Problems) != 0 {
		t.Errorf("row 4 = %+v", r)
	}
}

func TestOptionalColumnsMayBeLeftOutAndOrderIsFree(t *testing.T) {
	rows, err := Parse(strings.NewReader("title,seq,duration_min,start\n開場,1,5,18:00\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Seq != 1 || rows[0].Title != "開場" || rows[0].StartHour != 18 || len(rows[0].Problems) != 0 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestBadRowsCarryTheirProblems(t *testing.T) {
	file := "seq,title,start,duration_min,fixed,roles,role_note,note\n" +
		"0,a,08:00,1,,,,\n" + // line 2: seq
		"x,a,08:00,1,,,,\n" + // line 3: seq
		"3,,08:00,1,,,,\n" + // line 4: title
		"4,a,8:00,1,,,,\n" + // line 5: start
		"5,a,24:00,1,,,,\n" + // line 6: start
		"6,a,08:60,1,,,,\n" + // line 7: start
		"7,a,08:00,-1,,,,\n" + // line 8: duration
		"8,a,08:00,1.5,,,,\n" + // line 9: duration
		"9,a,08:00,1,y,,,\n" + // line 10: fixed
		"10,a,08:00,1\n" + // line 11: field count
		"11,a,08:00,1,N,,,\n" + // line 12: fixed
		"+12,a,08:00,1,,,,\n" + // line 13: seq
		"13,a,08:00,1441,,,,\n" + // line 14: duration over a day
		"14,\xff,08:00,1,,,,\n" // line 15: not UTF-8
	rows, err := Parse(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 14 {
		t.Fatalf("%d rows", len(rows))
	}
	for i, r := range rows {
		if r.Line != i+2 {
			t.Errorf("row %d has line %d", i, r.Line)
		}
		if len(r.Problems) != 1 {
			t.Errorf("line %d: problems %v, want exactly one", r.Line, r.Problems)
		}
	}
}

func TestFileLevelErrors(t *testing.T) {
	for name, file := range map[string]string{
		"empty":           "",
		"bom only":        "\xEF\xBB\xBF",
		"unknown column":  "seq,title,start,duration_min,colour\n",
		"missing start":   "seq,title,duration_min\n",
		"repeated":        "seq,title,start,duration_min,seq\n",
		"broken quotes":   "seq,title,start,duration_min\n1,\"a,08:00,1\n",
		"header not utf8": "seq,title,start,duration_min,\xff\n",
	} {
		if _, err := Parse(strings.NewReader(file)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	big := "seq,title,start,duration_min\n" + strings.Repeat("1,a,08:00,1\n", MaxFileBytes/12+1)
	if _, err := Parse(strings.NewReader(big)); err == nil {
		t.Error("oversized file accepted")
	}
}

func TestValuesAtTheLimitsAreAccepted(t *testing.T) {
	file := "seq,title,start,duration_min\n" +
		"123456789,a,23:00,1440\n" + // nine digits, last hour, a full day
		"2,a,12:59,000000005\n" // last minute, nine digits with leading zeros
	rows, err := Parse(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	if r := rows[0]; r.Seq != 123456789 || r.StartHour != 23 || r.StartMinute != 0 || r.DurationMin != 1440 || len(r.Problems) != 0 {
		t.Errorf("line 2 = %+v", r)
	}
	if r := rows[1]; r.StartHour != 12 || r.StartMinute != 59 || r.DurationMin != 5 || len(r.Problems) != 0 {
		t.Errorf("line 3 = %+v", r)
	}
}

func TestMalformedNumbersAndClocks(t *testing.T) {
	file := "seq,title,start,duration_min\n" +
		"1234567890,a,08:00,1\n" + // line 2: seq has ten digits
		"2,a,12345,1\n" + // line 3: start without a colon
		"3,a,ab:30,1\n" + // line 4: start hour not a number
		"4,a,,1\n" // line 5: start blank
	rows, err := Parse(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("%d rows", len(rows))
	}
	for _, r := range rows {
		if len(r.Problems) != 1 {
			t.Errorf("line %d: problems %v, want exactly one", r.Line, r.Problems)
		}
	}
}

// A start time in a shape a spreadsheet copies is still refused, and the
// problem says what to write instead (US-M-12.C1).
func TestStartProblemNamesTheFix(t *testing.T) {
	for start, want := range map[string]string{
		"9:05":     `start "9:05"：必須是 24 小時制的 HH:MM，請寫成 09:05`,
		"09:05:00": `start "09:05:00"：必須是 24 小時制的 HH:MM，請寫成 09:05`,
		"9:05:00":  `start "9:05:00"：必須是 24 小時制的 HH:MM，請寫成 09:05`,
		"23:59:59": `start "23:59:59"：必須是 24 小時制的 HH:MM，請寫成 23:59`,
		"24:00":    `start "24:00"：必須是 24 小時制的 HH:MM（例如 09:05）`,
		"9:60":     `start "9:60"：必須是 24 小時制的 HH:MM（例如 09:05）`,
		"10:5":     `start "10:5"：必須是 24 小時制的 HH:MM（例如 09:05）`,
		"0905":     `start "0905"：必須是 24 小時制的 HH:MM（例如 09:05）`,
		"":         `start ""：必須是 24 小時制的 HH:MM（例如 09:05）`,
	} {
		rows, err := Parse(strings.NewReader("seq,title,start,duration_min\n1,a," + start + ",1\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got := rows[0].Problems; len(got) != 1 || got[0] != want {
			t.Errorf("start %q: problems %q, want [%q]", start, got, want)
		}
	}
}

// padded returns a rundown of exactly size bytes: one row whose title fills the rest.
func padded(size int) string {
	header := "seq,title,start,duration_min\n"
	return header + "1," + strings.Repeat("x", size-len(header)-len("1,,08:00,1\n")) + ",08:00,1\n"
}

func TestSizeLimitsAreInclusive(t *testing.T) {
	if file := padded(MaxFileBytes); len(file) != MaxFileBytes {
		t.Fatalf("file is %d bytes", len(file))
	} else if _, err := Parse(strings.NewReader(file)); err != nil {
		t.Errorf("file of exactly MaxFileBytes: %v", err)
	}
	if text := padded(MaxTextBytes); len(text) != MaxTextBytes {
		t.Fatalf("text is %d bytes", len(text))
	} else if _, err := ParseText(text); err != nil {
		t.Errorf("text of exactly MaxTextBytes: %v", err)
	}
	text := "seq,title,start,duration_min\n" + strings.Repeat("1,a,08:00,1\n", MaxTextRows)
	if rows, err := ParseText(text); err != nil || len(rows) != MaxTextRows {
		t.Errorf("exactly MaxTextRows rows: %d rows, %v", len(rows), err)
	}
}
