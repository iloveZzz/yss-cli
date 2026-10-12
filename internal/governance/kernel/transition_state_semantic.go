package kernel

// transitionState keeps the checkpoint's current authority separate from the
// observed outcome of its formal task. It never derives approval expectations
// or user-decision scope from the outcome being verified.
func (s *SemanticSession) TransitionState(checkpointRef, taskRef string) (map[string]any, error) {
	cp, err := s.Doc(checkpointRef)
	if err != nil {
		return nil, err
	}
	state := ApCopy(cp)
	protected := map[string]bool{"repository_mode": true, "gates": true, "checks": true, "artifacts": true, "human_review": true, "stage_tracking": true, "stage": true, "next_work_unit": true, "stage_trace": true, "profile_id": true, "checkpoint_ref": true, "user_decisions": true, "user_decision_not_applicable": true}
	review := SemMap(cp["human_review"])
	state["checkpoint_ref"] = checkpointRef
	state["user_decisions"] = review["user_decisions"]
	state["user_decision_not_applicable"] = review["not_applicable"]
	merge := func(extra map[string]any) error {
		for key, value := range extra {
			if key == "decision_state" {
				continue
			}
			if key == "context_reconciliation" {
				// Published task results carry status/ref/reason, while checkpoints
				// also carry evidence_refs. Compare the shared claims and retain the
				// independently persisted checkpoint's complete evidence binding.
				current, currentOK := Object(state[key])
				claims, claimsOK := Object(value)
				if !currentOK || !claimsOK || claims["status"] == nil {
					return s.Reject("TRANSITION_STATE_DRIFT", "任务 Context 对账缺少当前 checkpoint 绑定")
				}
				for field, claim := range claims {
					if !SemHas([]string{"status", "ref", "reason"}, field) || !ApEqual(current[field], claim) {
						return s.Reject("TRANSITION_STATE_DRIFT", "任务 Context 对账与当前 checkpoint 矛盾: "+field)
					}
				}
				continue
			}
			if protected[key] {
				if !ApEqual(state[key], value) {
					return s.Reject("TRANSITION_STATE_DRIFT", "流转结果与当前 checkpoint 权威矛盾: "+key)
				}
				continue
			}
			if current, present := state[key]; present && !ApEqual(current, value) {
				return s.Reject("TRANSITION_STATE_DRIFT", "流转结果与当前持久状态矛盾: "+key)
			}
			state[key] = value
		}
		return nil
	}
	if raw, present := cp["decision_state"]; present {
		extra, ok := Object(raw)
		if !ok {
			return nil, s.Unavailable("INPUT", "checkpoint.decision_state 必须为对象")
		}
		if err = merge(extra); err != nil {
			return nil, err
		}
	}
	if taskRef != "" {
		task, e := s.Doc(taskRef)
		if e != nil {
			return nil, e
		}
		if task["checkpoint_ref"] != checkpointRef {
			return nil, s.Reject("TRANSITION_STATE_DRIFT", "正式任务没有绑定当前 checkpoint")
		}
		result, ok := Object(task["result"])
		if !ok || task["workflow_status"] != "resolved" || result["result"] != "completed" || result["work_unit"] != task["work_unit_id"] {
			return nil, s.Reject("TRANSITION_STATE_REQUIRED", "流转须消费该正式任务的实际完成结果")
		}
		if err = merge(result); err != nil {
			return nil, err
		}
		if raw, present := task["execution_result"]; present {
			extra, ok := Object(raw)
			if !ok {
				return nil, s.Unavailable("INPUT", "task.execution_result 必须为对象")
			}
			if err = merge(extra); err != nil {
				return nil, err
			}
		}
	}
	return state, nil
}
