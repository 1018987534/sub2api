package qqactivity

import "testing"

func TestQQBindingEmailNormalization(t *testing.T) {
	for _, input := range []string{"12345678", "12345678@qq.com", " 12345678@QQ.COM "} {
		qq, email, err := normalizeQQEmail(input)
		if err != nil || qq != "12345678" || email != "12345678@qq.com" {
			t.Fatalf("normalization failed for %q", input)
		}
	}
	for _, input := range []string{"", "012345@qq.com", "alias@qq.com", "12345+tag@qq.com", "12345@example.com", "12345@qq.com.evil.test", "12****78@qq.com", "12345@qq.com\r\nBcc: attacker@example.test"} {
		if _, _, err := normalizeQQEmail(input); err == nil {
			t.Errorf("invalid QQ email accepted: %q", input)
		}
	}
}
