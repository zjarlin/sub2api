package main

import "testing"

func TestDeepseekAccountRestriction(t *testing.T) {
	for _, tc := range []struct {
		name       string
		text       string
		want       string
		restricted bool
	}{
		{
			name:       "suspended with expiry",
			text:       "Due to violation of user policies, your account has been suspended until October 2, 2026 21:18. If you have any questions, please Contact us.",
			want:       "DeepSeek account is suspended until October 2, 2026 21:18; wait for the restriction to end, then try again",
			restricted: true,
		},
		{
			name:       "suspended without expiry",
			text:       "Your account is suspended.",
			want:       "DeepSeek account is suspended; wait for the restriction to end, then try again",
			restricted: true,
		},
		{
			name: "unrelated error",
			text: "Invalid email or password.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, restricted := deepseekAccountRestriction(tc.text)
			if got != tc.want || restricted != tc.restricted {
				t.Fatalf("deepseekAccountRestriction() = %q, %v; want %q, %v", got, restricted, tc.want, tc.restricted)
			}
		})
	}
}
