package api

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BensonPigo/bethere/internal/store"
)

// namedFieldSet is one field set a handler passes to readBody, with the
// name of the variable or function it comes from.
type namedFieldSet struct {
	source string
	name   string
	fields fieldSet
}

// allFieldSets is every field set the package passes to readBody: the
// variables of body.go, invite.go and recover.go, both shapes of
// shiftFields and the empty set of the admin invite.
func allFieldSets() []namedFieldSet {
	return []namedFieldSet{
		{"versionOnly", "versionOnly", versionOnly},
		{"ownCheckInFields", "ownCheckInFields", ownCheckInFields},
		{"proxyCheckInFields", "proxyCheckInFields", proxyCheckInFields},
		{"linkFields", "linkFields", linkFields},
		{"skipFields", "skipFields", skipFields},
		{"insertFields", "insertFields", insertFields},
		{"assignFields", "assignFields", assignFields},
		{"editFields", "editFields", editFields},
		{"reissueFields", "reissueFields", reissueFields},
		{"updateFields", "updateFields", updateFields},
		{"moveFields", "moveFields", moveFields},
		{"deleteCueFields", "deleteCueFields", deleteCueFields},
		{"importPreviewFields", "importPreviewFields", importPreviewFields},
		{"importFields", "importFields", importFields},
		{"addRoleFields", "addRoleFields", addRoleFields},
		{"renameFields", "renameFields", renameFields},
		{"deleteRoleFields", "deleteRoleFields", deleteRoleFields},
		{"ackFields", "ackFields", ackFields},
		{"addTaskFields", "addTaskFields", addTaskFields},
		{"setTaskFields", "setTaskFields", setTaskFields},
		{"deleteTaskFields", "deleteTaskFields", deleteTaskFields},
		{"guestImportFields", "guestImportFields", guestImportFields},
		{"arriveFields", "arriveFields", arriveFields},
		{"checklistFields", "checklistFields", checklistFields},
		{"checkItemFields", "checkItemFields", checkItemFields},
		{"shiftFields", "shiftFields(true)", shiftFields(true)},
		{"shiftFields", "shiftFields(false)", shiftFields(false)},
		{"createEventFields", "createEventFields", createEventFields},
		{"recoverFields", "recoverFields", recoverFields},
		{"adminInvite", "fieldSet{}", fieldSet{}},
		{"emailFields", "emailFields", emailFields},
		{"logoutFields", "logoutFields", logoutFields},
		{"removeMemberFields", "removeMemberFields", removeMemberFields},
		{"companyEventFields", "companyEventFields", companyEventFields},
		{"openConsoleFields", "openConsoleFields", openConsoleFields},
		{"organizationFields", "organizationFields", organizationFields},
	}
}

func strPtr(s string) *string { return &s }

// fieldMappingCases is one valid body per field set and the body it must
// parse to. Every value differs from the others, so a field written to
// the wrong place of body shows.
func fieldMappingCases() map[string]struct {
	in   string
	want body
} {
	start := time.Date(2027, 6, 6, 10, 30, 0, 0, time.UTC)
	return map[string]struct {
		in   string
		want body
	}{
		"versionOnly":        {`{"version":11}`, body{version: 11}},
		"ownCheckInFields":   {`{"checkedIn":true}`, body{checkedIn: true}},
		"proxyCheckInFields": {`{"roleId":13,"checkedIn":true}`, body{roleID: 13, checkedIn: true}},
		"linkFields":         {`{"roleId":13}`, body{roleID: 13}},
		"skipFields":         {`{"version":11,"cueId":14}`, body{version: 11, cueID: 14}},
		"insertFields": {
			`{"version":11,"afterCueId":15,"start":"2027-06-06T10:30:00Z","durationMin":20,"title":"進場","notePublic":"公開","roles":[{"roleId":13,"roleNote":"備註"},{"roleId":23,"roleNote":null}]}`,
			body{version: 11, afterCueID: 15, start: start, durationMin: 20, title: "進場", notePublic: strPtr("公開"),
				roles: []store.Assignment{{RoleID: 13, RoleNote: strPtr("備註")}, {RoleID: 23}}},
		},
		"assignFields": {
			`{"version":11,"cueId":14,"roles":[{"roleId":13,"roleNote":null}]}`,
			body{version: 11, cueID: 14, roles: []store.Assignment{{RoleID: 13}}},
		},
		"editFields": {
			`{"version":11,"cueId":14,"title":"進場","notePublic":null}`,
			body{version: 11, cueID: 14, title: "進場"},
		},
		"reissueFields": {
			`{"roleId":13,"origin":"https://a.example"}`,
			body{roleID: 13, origin: "https://a.example", originGiven: true},
		},
		"updateFields": {
			`{"version":11,"cueId":14,"start":"2027-06-06T10:30:00Z","durationMin":20,"isFixed":true,"title":"進場","notePublic":""}`,
			body{version: 11, cueID: 14, start: start, durationMin: 20, isFixed: true, title: "進場", notePublic: strPtr("")},
		},
		"moveFields":          {`{"version":11,"cueId":14,"afterCueId":15}`, body{version: 11, cueID: 14, afterCueID: 15}},
		"deleteCueFields":     {`{"version":11,"cueId":14}`, body{version: 11, cueID: 14}},
		"importPreviewFields": {`{"text":"貼上的文字"}`, body{text: "貼上的文字"}},
		"importFields": {
			`{"version":11,"text":"貼上的文字","newRoles":[{"name":"攝影","view":"crew","checksIn":true}]}`,
			body{version: 11, text: "貼上的文字", newRoles: []store.NewRole{{Name: "攝影", View: "crew", ChecksIn: true}}},
		},
		"addRoleFields": {
			`{"name":"攝影","view":"crew","checksIn":true,"origin":"https://a.example","job":"tasks"}`,
			body{name: "攝影", view: "crew", checksIn: true, origin: "https://a.example", originGiven: true, job: "tasks"},
		},
		"renameFields":      {`{"roleId":13,"name":"攝影"}`, body{roleID: 13, name: "攝影"}},
		"deleteRoleFields":  {`{"version":11,"roleId":13}`, body{version: 11, roleID: 13}},
		"ackFields":         {`{"cueId":14,"acked":true}`, body{cueID: 14, on: true}},
		"addTaskFields":     {`{"title":"買花"}`, body{title: "買花"}},
		"setTaskFields":     {`{"taskId":16,"state":"done"}`, body{taskID: 16, state: "done"}},
		"deleteTaskFields":  {`{"taskId":16}`, body{taskID: 16}},
		"guestImportFields": {`{"text":"王小明"}`, body{text: "王小明"}},
		"arriveFields":      {`{"guestId":17,"arrived":true}`, body{guestID: 17, on: true}},
		"checklistFields":   {`{"text":"捧花"}`, body{text: "捧花"}},
		"checkItemFields":   {`{"itemId":18,"done":true}`, body{itemID: 18, on: true}},
		"shiftFields(true)": {
			`{"version":11,"fromCueId":12,"deltaMin":-19,"excluded":[21,22]}`,
			body{version: 11, fromCueID: 12, deltaMin: -19, excluded: []int64{21, 22}},
		},
		"shiftFields(false)": {
			`{"fromCueId":12,"deltaMin":-19,"excluded":[21,22]}`,
			body{fromCueID: 12, deltaMin: -19, excluded: []int64{21, 22}},
		},
		"createEventFields": {
			`{"inviteCode":"abc-123","name":"小明與小華","date":"2027-06-06"}`,
			body{text: "abc-123", name: "小明與小華", date: "2027-06-06"},
		},
		"recoverFields": {`{"recoveryCode":"r-456"}`, body{text: "r-456"}},
		"fieldSet{}":    {`{}`, body{}},
		// The company page (api.md section 10).
		"emailFields":        {`{"email":"a@x.tw"}`, body{email: "a@x.tw"}},
		"logoutFields":       {`{}`, body{}},
		"removeMemberFields": {`{"memberId":19}`, body{memberID: 19}},
		"companyEventFields": {`{"name":"小明與小華","date":"2027-06-07"}`, body{name: "小明與小華", date: "2027-06-07"}},
		"openConsoleFields":  {`{"eventId":24}`, body{eventID: 24}},
		"organizationFields": {`{"name":"甲婚顧","email":"b@x.tw"}`, body{name: "甲婚顧", email: "b@x.tw"}},
	}
}

// TestEveryFieldMapsToBody reads one valid body per field set and checks
// that every field lands in its own place of body.
func TestEveryFieldMapsToBody(t *testing.T) {
	cases := fieldMappingCases()
	for _, fs := range allFieldSets() {
		c, ok := cases[fs.name]
		if !ok {
			t.Errorf("%s: no case", fs.name)
			continue
		}
		// The case must name every field of the set, the optional ones too.
		if got, want := jsonKeys(t, c.in), fieldNames(fs.fields); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: case has keys %v, the set has %v", fs.name, got, want)
		}
		b, ok := read(fs.fields, c.in)
		if !ok {
			t.Errorf("%s: %s refused", fs.name, c.in)
			continue
		}
		if !b.start.Equal(c.want.start) {
			t.Errorf("%s: start = %v, want %v", fs.name, b.start, c.want.start)
		}
		b.start = c.want.start
		if !reflect.DeepEqual(b, c.want) {
			t.Errorf("%s: got %+v\nwant %+v", fs.name, b, c.want)
		}
	}
	if len(cases) != len(allFieldSets()) {
		t.Errorf("%d cases for %d field sets", len(cases), len(allFieldSets()))
	}
}

// jsonKeys lists the top-level keys of a JSON object in the test input.
func jsonKeys(t *testing.T, s string) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func fieldNames(f fieldSet) []string {
	keys := []string{}
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestAllFieldSetsIsComplete parses the package's source and checks that
// every fieldSet literal is in allFieldSets: each package variable by its
// name, each literal inside a function by that function's name.
func TestAllFieldSetsIsComplete(t *testing.T) {
	listed := map[string]bool{}
	for _, fs := range allFieldSets() {
		listed[fs.source] = true
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			for _, source := range fieldSetSources(decl) {
				found++
				if !listed[source] {
					t.Errorf("%s: fieldSet in %s is not in allFieldSets", name, source)
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("no fieldSet literal found")
	}
}

// fieldSetSources names where each fieldSet literal of one declaration is.
func fieldSetSources(decl ast.Decl) []string {
	var sources []string
	switch d := decl.(type) {
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, v := range vs.Values {
				if isFieldSetLit(v) {
					sources = append(sources, vs.Names[i].Name)
				}
			}
		}
	case *ast.FuncDecl:
		ast.Inspect(d, func(n ast.Node) bool {
			if e, ok := n.(ast.Expr); ok && isFieldSetLit(e) {
				sources = append(sources, d.Name.Name)
			}
			return true
		})
	}
	return sources
}

func isFieldSetLit(e ast.Expr) bool {
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return false
	}
	id, ok := lit.Type.(*ast.Ident)
	return ok && id.Name == "fieldSet"
}

func rolesBody(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"roleId":%d,"roleNote":null}`, i+1)
	}
	return `{"roles":[` + strings.Join(items, ",") + `]}`
}

func newRolesBody(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"name":"角色%d","view":"crew","checksIn":false}`, i+1)
	}
	return `{"newRoles":[` + strings.Join(items, ",") + `]}`
}

func excludedBody(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprint(i + 1)
	}
	return `{"fromCueId":1,"deltaMin":5,"excluded":[` + strings.Join(items, ",") + `]}`
}

func TestRolesAtMostFifty(t *testing.T) {
	roles := fieldSet{"roles": roleList}
	b, ok := read(roles, rolesBody(50))
	if !ok || len(b.roles) != 50 || b.roles[0].RoleID != 1 || b.roles[49].RoleID != 50 {
		t.Errorf("50 roles: %d %v", len(b.roles), ok)
	}
	if b, ok := read(roles, rolesBody(51)); ok || !reflect.DeepEqual(b, body{}) {
		t.Errorf("51 roles accepted or leaked: %v", ok)
	}
}

func TestNewRolesAtMostFifty(t *testing.T) {
	newRoles := fieldSet{"newRoles": newRoleList}
	b, ok := read(newRoles, newRolesBody(50))
	if !ok || len(b.newRoles) != 50 || b.newRoles[0].Name != "角色1" || b.newRoles[49].Name != "角色50" {
		t.Errorf("50 newRoles: %d %v", len(b.newRoles), ok)
	}
	if b, ok := read(newRoles, newRolesBody(51)); ok || !reflect.DeepEqual(b, body{}) {
		t.Errorf("51 newRoles accepted or leaked: %v", ok)
	}
}

func TestExcludedTwoHundred(t *testing.T) {
	b, ok := read(shiftFields(false), excludedBody(200))
	if !ok || len(b.excluded) != 200 || b.excluded[0] != 1 || b.excluded[199] != 200 {
		t.Errorf("200 excluded: %d %v", len(b.excluded), ok)
	}
}

// TestBracketsAreChecked sends the right tokens inside the wrong brackets.
func TestBracketsAreChecked(t *testing.T) {
	for _, c := range []struct {
		fields fieldSet
		in     string
	}{
		{versionOnly, `["version",1]`},
		{fieldSet{}, `[]`},
		{fieldSet{"roles": roleList}, `{"roles":[["roleId",1,"roleNote",null]]}`},
	} {
		if b, ok := read(c.fields, c.in); ok || !reflect.DeepEqual(b, body{}) {
			t.Errorf("%s accepted", c.in)
		}
	}
}
