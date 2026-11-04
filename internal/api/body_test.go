package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func read(fields fieldSet, s string) (body, bool) {
	return readBody(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(s)), fields)
}

func TestIntegersAtTheirLimits(t *testing.T) {
	ok := map[string]fieldSet{
		`{"version":123456789012345678}`: versionOnly, // 18 digits
		`{"deltaMin":-2147483648}`:       {"deltaMin": signedInt},
		`{"deltaMin":2147483647}`:        {"deltaMin": signedInt},
		`{"deltaMin":5}`:                 {"deltaMin": signedInt}, // one digit, no sign
		`{"durationMin":0}`:              {"durationMin": count},
		`{"durationMin":2147483647}`:     {"durationMin": count},
	}
	for s, fields := range ok {
		if _, accepted := read(fields, s); !accepted {
			t.Errorf("%s refused", s)
		}
	}
	if b, _ := read(fieldSet{"deltaMin": signedInt}, `{"deltaMin":-2147483648}`); b.deltaMin != -1<<31 {
		t.Errorf("deltaMin = %d", b.deltaMin)
	}
	if b, _ := read(fieldSet{"deltaMin": signedInt}, `{"deltaMin":5}`); b.deltaMin != 5 {
		t.Errorf("deltaMin = %d", b.deltaMin)
	}
	bad := map[string]fieldSet{
		`{"version":1234567890123456789}`: versionOnly, // 19 digits
		`{"deltaMin":-2147483649}`:        {"deltaMin": signedInt},
		`{"deltaMin":2147483648}`:         {"deltaMin": signedInt},
		`{"durationMin":2147483648}`:      {"durationMin": count},
		`{"durationMin":-1}`:              {"durationMin": count},
	}
	for s, fields := range bad {
		if _, accepted := read(fields, s); accepted {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestRoleListItemsAreExact(t *testing.T) {
	roles := fieldSet{"roles": roleList}
	if b, ok := read(roles, `{"roles":[{"roleId":3,"roleNote":null}]}`); !ok || len(b.roles) != 1 || b.roles[0].RoleID != 3 {
		t.Errorf("valid roles: %+v %v", b.roles, ok)
	}
	for _, s := range []string{
		`{"roles":[{"roleNote":null}]}`,                          // no roleId
		`{"roles":[{"roleId":3}]}`,                               // no roleNote
		`{"roles":[{"roleId":3,"roleId":4,"roleNote":null}]}`,    // a key twice
		`{"roles":[{"roleId":3,"roleNote":null,"roleNote":""}]}`, // a key twice
	} {
		if _, ok := read(roles, s); ok {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestNewRoleListItemsAreExact(t *testing.T) {
	newRoles := fieldSet{"newRoles": newRoleList}
	if b, ok := read(newRoles, `{"newRoles":[{"name":"新秘","view":"x","checksIn":true}]}`); !ok || len(b.newRoles) != 1 || !b.newRoles[0].ChecksIn {
		t.Errorf("valid newRoles: %+v %v", b.newRoles, ok)
	}
	for _, s := range []string{
		`{"newRoles":[{"name":"新秘","view":"x"}]}`,                             // no checksIn
		`{"newRoles":[{"name":"新秘","name":"攝影","view":"x","checksIn":true}]}`, // a key twice
		`{"newRoles":[{"name":"新秘","view":"x","checksIn":"yes"}]}`,            // not a boolean
		`{"newRoles":[{"name":"新秘","view":"x","checksIn":null}]}`,
	} {
		if _, ok := read(newRoles, s); ok {
			t.Errorf("%s accepted", s)
		}
	}
}
