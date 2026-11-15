package app

import "testing"

func TestGenerateDevBranch(t *testing.T) {
	cases := []struct {
		name      string
		projectID string
		title     string
		want      string
	}{
		{"ascii title", "APP-1", "Social Login", "feat/app-1-social-login"},
		{"punctuation collapses", "APP-2", "  Upload / Timeout!! ", "feat/app-2-upload-timeout"},
		{"chinese title falls back to id", "APP-3", "登录接口", "feat/app-3"},
		{"long title truncates", "PROD-12", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "feat/prod-12-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := generateDevBranch(c.projectID, c.title); got != c.want {
				t.Fatalf("generateDevBranch(%q, %q) = %q, want %q", c.projectID, c.title, got, c.want)
			}
		})
	}
}

func TestValidateUpdateProjectDevBranch(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"valid feat", "feat/app-1-social-login", "feat/app-1-social-login", false},
		{"valid fix with dots", "fix/app-1.v2-timeout", "fix/app-1.v2-timeout", false},
		{"trimmed", "  feat/app-1-x  ", "feat/app-1-x", false},
		{"empty", "   ", "", true},
		{"main rejected", "main", "", true},
		{"uppercase rejected", "feat/App-1", "", true},
		{"unknown type rejected", "wip/app-1", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			_, got, fields := validateUpdateProject(UpdateProjectInput{DevBranch: &in})
			if c.wantErr {
				if len(fields) == 0 || fields[0].Field != "dev_branch" {
					t.Fatalf("validateUpdateProject(%q) fields = %+v, want dev_branch error", c.in, fields)
				}
				return
			}
			if len(fields) != 0 {
				t.Fatalf("validateUpdateProject(%q) unexpected fields = %+v", c.in, fields)
			}
			if got != c.want {
				t.Fatalf("validateUpdateProject(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
