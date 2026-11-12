package card

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

// goldenCases covers every state that produces a card, plus the two layout
// branches that are easy to break: stale header and missing document link.
type goldenCase struct {
	name    string
	subject Subject
	project *Project
	item    *WorkItem
	opts    Options
}

func goldenCases() []goldenCase {
	return []goldenCase{
		{
			name: "project_requirement_review",
			subject: Subject{
				Kind: KindProject, ID: "APP-12", State: StateRequirementReview,
				Title: "供应链看板改版", Age: 6 * time.Hour,
			},
			project: &Project{ProductID: "APP", Version: 3, MaterialID: "m001abcdef", MaterialVersion: 3, MaterialKind: "requirement"},
			opts:    Options{DocURL: "https://example.feishu.cn/docx/abc"},
		},
		{
			name: "project_spec_review_stale",
			subject: Subject{
				Kind: KindProject, ID: "APP-13", State: StateSpecReview,
				Title: "库存预警", Age: 80 * time.Hour, Stale: true,
				Reason: "缺少回滚方案",
			},
			project: &Project{ProductID: "APP", Version: 5, MaterialID: "m002deadbe", MaterialVersion: 2, MaterialKind: "spec"},
			opts:    Options{DocURL: "https://example.feishu.cn/docx/def"},
		},
		{
			name: "project_tasks_review_no_doc",
			subject: Subject{
				Kind: KindProject, ID: "APP-14", State: StateTasksReview,
				Title: "对账任务拆分", Age: 25 * time.Minute,
			},
			project: &Project{ProductID: "APP", Version: 1},
			opts:    Options{},
		},
		{
			name: "work_item_submitted",
			subject: Subject{
				Kind: KindWorkItem, ID: "APP-12-T0007", State: StateSubmitted,
				Title: "补齐库存接口", Age: 2 * time.Hour,
			},
			item: &WorkItem{ProjectID: "APP-12", Worker: "dev-1", Attempt: 2, MaxAttempts: 3, ReportID: "r0099aabb"},
			opts: Options{DocURL: "https://example.feishu.cn/docx/ghi"},
		},
		{
			name: "work_item_failed",
			subject: Subject{
				Kind: KindWorkItem, ID: "APP-12-T0008", State: StateFailed,
				Title: "对接旧 ERP", Age: 30 * time.Hour, Reason: "依赖接口 502",
			},
			item: &WorkItem{ProjectID: "APP-12", Worker: "dev-2", Attempt: 3, MaxAttempts: 3},
		},
	}
}

func TestRenderGolden(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := Render(tc.subject, tc.project, tc.item, tc.opts)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			path := filepath.Join("testdata", tc.name+".json")
			if *update {
				if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update): %v", err)
			}
			if string(raw) != strings.TrimRight(string(want), "\n") {
				t.Errorf("card mismatch for %s\ngot:  %s\nwant: %s", tc.name, raw, want)
			}
		})
	}
}

// TestGoldenCardsAreValidJSON guards against a golden file that was written but
// never parsed by Feishu.
func TestGoldenCardsAreValidJSON(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tc.name+".json"))
			if err != nil {
				t.Skip("golden missing")
			}
			var v map[string]any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatalf("golden is not valid JSON: %v", err)
			}
			if v["schema"] != "2.0" {
				t.Errorf("schema = %v, want 2.0", v["schema"])
			}
		})
	}
}

func TestRenderClosed(t *testing.T) {
	raw, err := RenderClosed(Subject{Kind: KindProject, ID: "APP-12", State: StateRequirementReview, Title: "供应链看板改版"}, "已通过", Options{DocURL: "https://example.feishu.cn/docx/abc"})
	if err != nil {
		t.Fatalf("RenderClosed: %v", err)
	}
	if strings.Contains(string(raw), `"gate_form"`) {
		t.Error("closed card must not carry the form")
	}
	if !strings.Contains(string(raw), "已通过") {
		t.Error("closed card must state the outcome")
	}
}

func TestValidateNames(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantSub string
	}{
		{"valid", `{"elements":[{"name":"a"},{"name":"b"}]}`, ""},
		{"duplicate", `{"elements":[{"name":"a"},{"name":"a"}]}`, "duplicate component names"},
		{"empty", `{"elements":[{"name":""}]}`, "empty name"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNames([]byte(tc.raw))
			if tc.wantSub == "" {
				if err != nil {
					t.Errorf("validateNames() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("validateNames() = %v, want %q", err, tc.wantSub)
			}
		})
	}
}

func TestHumanAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "不到 1 分钟"},
		{90 * time.Minute, "1 小时"},
		{80 * time.Hour, "3 天"},
	}
	for _, tc := range tests {
		if got := humanAge(tc.d); got != tc.want {
			t.Errorf("humanAge(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestRenderUnsupportedState(t *testing.T) {
	_, err := Render(Subject{Kind: KindProject, ID: "APP-1", State: "completed"}, nil, nil, Options{})
	if err == nil {
		t.Fatal("Render(completed) = nil error, want unsupported state")
	}
}
