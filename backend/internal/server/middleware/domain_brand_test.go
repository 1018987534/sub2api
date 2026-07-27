package middleware

import "testing"

func TestNeedsDomainBrandProfile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/", want: true},
		{path: "/dashboard/api-keys", want: true},
		{path: "/api/v1/settings/public", want: true},
		{path: "/api/v1/keys", want: true},
		{path: "/api/v1/keys/123", want: true},
		{path: "/api/v1/auth/register", want: true},
		{path: "/api/v1/auth/oauth/callback", want: true},
		{path: "/api/v1/user/totp/send-code", want: true},
		{path: "/api/v1/api-keys", want: false},
		{path: "/api/v1/groups/available", want: true},
		{path: "/api/v1/channel-monitors", want: true},
		{path: "/api/v1/channel-monitors/12/status", want: true},
		{path: "/api/v1/payment/plans", want: true},
		{path: "/api/v1/admin/settings", want: false},
		{path: "/v1/responses", want: false},
		{path: "/responses", want: false},
		{path: "/images/generations", want: false},
		{path: "/assets/app.js", want: false},
		{path: "/logo.svg", want: false},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			if got := needsDomainBrandProfile(test.path); got != test.want {
				t.Fatalf("needsDomainBrandProfile(%q) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}
