package lifecycle

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func standardSession(t *testing.T, policy map[string]any, record map[string]any) *dailySession {
	t.Helper()
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	d := &dailySession{s: s, report: &DailyReport{SemanticReport: s.Report}, basis: map[string]string{}, record: record}
	d.policy = map[string]any{"excluded_impacts": []any{"data-migration", "breaking-api"}, "standard": policy}
	d.readStandardPolicy()
	return d
}

func validStandardPolicy() map[string]any {
	return map[string]any{"version": json.Number("1"), "impacts": []any{"behavior-change"}, "decision": map[string]any{"id": standardDecisionID, "actor_kind": "human"}}
}

func TestStandardPolicyIsOptionalAndStrict(t *testing.T) {
	if d := standardSession(t, validStandardPolicy(), nil); d.standard == nil || !d.standardImpact("behavior-change") || d.standardImpact("new-capability") {
		t.Fatal("合法策略应启用，且只认可登记的影响")
	}
	for name, mutate := range map[string]func(map[string]any){
		"未知版本":       func(p map[string]any) { p["version"] = json.Number("2") },
		"影响为空":       func(p map[string]any) { p["impacts"] = []any{} },
		"CLI 不认识的影响": func(p map[string]any) { p["impacts"] = []any{"permission-change-lite"} },
		"与排除影响重叠":    func(p map[string]any) { p["impacts"] = []any{"data-migration"} },
		"决定可由数字人作出": func(p map[string]any) {
			p["decision"] = map[string]any{"id": standardDecisionID, "actor_kind": "digital-human"}
		},
		"决定 ID 不符": func(p map[string]any) { p["decision"] = map[string]any{"id": "x", "actor_kind": "human"} },
	} {
		policy := validStandardPolicy()
		mutate(policy)
		if d := standardSession(t, policy, nil); d.standard != nil {
			t.Errorf("%s: 不认识的 standard 策略必须视为未启用", name)
		}
	}
	if d := standardSession(t, nil, nil); d.standard != nil {
		t.Error("缺少 standard 时不启用")
	}
}

func TestStandardDeciderMustBeIndependent(t *testing.T) {
	record := func(decider string) map[string]any {
		return map[string]any{"standard_decision": map[string]any{"decided_by": decider}, "review": map[string]any{"reviewer_id": "reviewer"}}
	}
	for decider, ok := range map[string]bool{"owner": true, "impl": false, "reviewer": false} {
		d := standardSession(t, validStandardPolicy(), record(decider))
		if err := d.standardIndependence("impl"); (err == nil) != ok {
			t.Errorf("决定人 %s: err=%v", decider, err)
		}
	}
}

func TestStandardEscalationIsReportedNotPassed(t *testing.T) {
	d := standardSession(t, validStandardPolicy(), nil)
	if err := d.requireStandard("route", "behavior-change"); err != nil || d.report.DeliveryPath != "standard" || !d.report.StandardRequired {
		t.Fatalf("route 应报告需要 standard: %v %+v", err, d.report)
	}
	if err := standardSession(t, validStandardPolicy(), nil).requireStandard("verify-daily", "behavior-change"); err == nil {
		t.Fatal("verify-daily 必须拒绝未升级的记录")
	}
}
