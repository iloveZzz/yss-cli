package helpview

import (
	"encoding/json"
	"reflect"
	"testing"
)

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
	if !reflect.DeepEqual(ids, []string{"stage.harness-entry", "stage.frontend-engineering-design", "stage.slice-contract", "stage.slice-implementation", "stage.verification"}) {
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
