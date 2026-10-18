package lifecycle

import (
	"fmt"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

type DailyInputChange struct {
	Pointer string `json:"pointer"`
	Ref     string `json:"ref"`
	Before  string `json:"before"`
	After   string `json:"after"`
}

type DailyInputPreparation struct {
	SchemaVersion   int                `json:"schema_version"`
	Record          map[string]any     `json:"record"`
	Changes         []DailyInputChange `json:"changes"`
	ReadOnly        bool               `json:"read_only"`
	ApprovalCreated bool               `json:"approval_created"`
	EvidenceState   string             `json:"evidence_state"`
}

func (d *dailySession) prepareInputBindings() (*DailyInputPreparation, error) {
	api, _ := kernel.Object(d.record["api"])
	if kernel.Text(d.record["delivery_path"]) != "daily" || kernel.Text(api["mode"]) != "none" {
		return nil, d.s.Unavailable("PREPARE_INPUTS_UNSUPPORTED", "输入准备仅支持无 API 变化的 daily 记录，不转换 standard 或正式路线")
	}
	var business map[string]any
	if d.report.Profile == "backend" {
		business, _ = kernel.Object(d.record["business_input"])
		if kernel.Text(business["mode"]) != "standalone" {
			return nil, d.s.Unavailable("PREPARE_INPUTS_UNSUPPORTED", "Backend 输入准备仅支持 standalone，不更新正式上游绑定")
		}
	}
	p := &DailyInputPreparation{SchemaVersion: 1, Record: d.record, Changes: []DailyInputChange{}, ReadOnly: true, EvidenceState: "not-evaluated"}
	refresh := func(row map[string]any, pointer string) error {
		before := ""
		if value := row["digest"]; value != nil {
			var ok bool
			before, ok = value.(string)
			if !ok {
				return d.info("输入 digest 必须是字符串: " + pointer)
			}
		}
		ref := kernel.Text(row["ref"])
		if _, err := d.basisRef(ref, ""); err != nil {
			return err
		}
		after := d.basis[ref]
		if before != after {
			p.Changes = append(p.Changes, DailyInputChange{Pointer: pointer, Ref: ref, Before: before, After: after})
			row["digest"] = after
		}
		return nil
	}
	for _, key := range []string{"skills", "inputs"} {
		rows, ok := d.record[key].([]any)
		if !ok || key == "skills" && len(rows) == 0 {
			return nil, d.info("缺少明确的 " + key)
		}
		for i, value := range rows {
			row, ok := kernel.Object(value)
			if !ok {
				return nil, d.info(key + " 引用必须是对象")
			}
			if err := refresh(row, fmt.Sprintf("/%s/%d/digest", key, i)); err != nil {
				return nil, err
			}
		}
	}
	if business != nil {
		if err := refresh(business, "/business_input/digest"); err != nil {
			return nil, err
		}
	}
	return p, nil
}
