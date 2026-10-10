package helpview

import (
	"encoding/json"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestStageReadingUsesPublicPresentationAndKeepsLegacyFallback(t *testing.T) {
	for _, tc := range []struct {
		name, presentation, wantName, wantGoal, wantExit string
	}{
		{"legacy", "", "Ticket 正式化", "冻结范围形成垂直切片", "验收可执行"},
		{"public", "public_name: 实现切片拆分与合同准入\npublic_goal: 消费冻结工程契约形成实现切片\npublic_exit_criteria: 当前合同批准并完成就绪核验\n", "实现切片拆分与合同准入", "消费冻结工程契约形成实现切片", "当前合同批准并完成就绪核验"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stage Stage
			raw := "id: stage.ticket-formalization\nname: Ticket 正式化\ngoal: 冻结范围形成垂直切片\nexit_criteria: 验收可执行\n" + tc.presentation
			if err := yaml.Unmarshal([]byte(raw), &stage); err != nil {
				t.Fatal(err)
			}
			want := Stage{ID: "stage.ticket-formalization", Name: tc.wantName, Goal: tc.wantGoal, Exit: tc.wantExit}
			if stage != want {
				t.Fatalf("阅读结果错误: got=%+v want=%+v", stage, want)
			}
		})
	}
}

func TestHelpViewsMatchFixedSourcesAndProfileExecutionOrder(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			want, err := Derive(profile)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(want)
			if string(a) != string(b) {
				t.Fatal("固定 Bundle 变化后必须重新生成帮助视图")
			}
		})
	}
	frontend, err := Load("frontend")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, stage := range frontend.Stages {
		ids = append(ids, stage.ID)
	}
	if !reflect.DeepEqual(ids, []string{"stage.plan", "stage.spec-architecture", "stage.harness-entry", "stage.frontend-engineering-design", "stage.slice-contract", "stage.slice-implementation", "stage.verification", "stage.product-design"}) {
		t.Fatalf("前端帮助需消费固定 Profile 的执行顺序: %v", ids)
	}
	design, err := Load("design")
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range design.Stages {
		if stage.ID == "stage.vertical-slice-implementation" {
			t.Fatal("Design 兼容登记不授予实现资格")
		}
	}
}
