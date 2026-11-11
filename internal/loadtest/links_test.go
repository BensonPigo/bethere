package loadtest

import (
	"strings"
	"testing"
)

const tok = "abcdefghijklmnopqrstuv" // 22 characters

func TestUS_O_13_3_ParseLinks(t *testing.T) {
	in := "\n1 master https://t.example/#t=" + tok + "\n  1 role https://t.example/#t=" + tok + "  \n2 master https://t.example/#t=" + tok + "\n"
	links, err := ParseLinks(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []Link{
		{Event: 1, Master: true, Base: "https://t.example", Token: tok},
		{Event: 1, Master: false, Base: "https://t.example", Token: tok},
		{Event: 2, Master: true, Base: "https://t.example", Token: tok},
	}
	if len(links) != len(want) {
		t.Fatalf("links = %+v", links)
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("link %d = %+v, want %+v", i, links[i], want[i])
		}
	}
}

func TestUS_O_13_3_ParseLinksRefuses(t *testing.T) {
	for name, in := range map[string]string{
		"fields":      "1 master",
		"event":       "x master https://t.example/#t=" + tok,
		"event zero":  "0 master https://t.example/#t=" + tok,
		"kind":        "1 boss https://t.example/#t=" + tok,
		"no fragment": "1 master https://t.example/" + tok,
		"short token": "1 master https://t.example/#t=" + tok[1:],
		"long token":  "1 master https://t.example/#t=" + tok + "x",
		"http":        "1 master http://t.example/#t=" + tok,
		"no master":   "1 role https://t.example/#t=" + tok,
		"two masters": "1 master https://t.example/#t=" + tok + "\n1 master https://t.example/#t=" + tok,
		"empty":       "\n",
	} {
		if _, err := ParseLinks(strings.NewReader(in)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestUS_O_13_3_ParseLinksSaysWhichLine(t *testing.T) {
	_, err := ParseLinks(strings.NewReader("1 master https://t.example/#t=" + tok + "\n\n1 boss x"))
	if err == nil || !strings.HasPrefix(err.Error(), "line 3:") {
		t.Errorf("err = %v", err)
	}
}
