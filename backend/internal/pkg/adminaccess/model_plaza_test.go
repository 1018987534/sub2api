package adminaccess

import "testing"

func TestModelPlazaPermissionsAreSeparate(t *testing.T) {
	const plaza = "/api/v1/admin/custom-model-plaza"
	read := Policy{GlobalPermissions: []string{"model_plaza.read"}}
	write := Policy{GlobalPermissions: []string{"model_plaza.write"}}
	remove := Policy{GlobalPermissions: []string{"model_plaza.delete"}}
	for _, tc := range []struct {
		policy       Policy
		method, path string
		allow        bool
	}{
		{read, "GET", plaza, true}, {read, "GET", plaza + "/defaults", true}, {read, "POST", plaza, false},
		{write, "POST", plaza, true}, {write, "PATCH", plaza + "/prices", true}, {write, "PUT", plaza, true},
		{write, "DELETE", plaza, false}, {write, "PUT", "/api/v1/admin/channels/:id", false},
		{remove, "DELETE", plaza, true}, {remove, "GET", plaza, false},
	} {
		if got := RouteAllowed(tc.policy, tc.method, tc.path); got != tc.allow {
			t.Errorf("%v %s %s: allowed=%v, want %v", tc.policy.GlobalPermissions, tc.method, tc.path, got, tc.allow)
		}
	}
}
