package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/spf13/cobra"
	"math/big"
	"os"
	"strings"
)

func addGovernanceCommands(root *cobra.Command, inv *invocation) {
	for group, actions := range map[string]string{
		"context": "check query verify", "lifecycle": "query route status target verify verify-daily",
		"stage": "apply check plan query register status update", "reading": "affected apply-enable check-views plan-enable render",
		"contract": "check verify view publish-review", "evidence": "check verify", "handoff": "check export import verify",
		"project-ci": "apply check install plan transition verify", "runtime": "begin commands complete event events inspect pin pins run unpin",
		"archive": "pack unpack verify", "xml": "inspect query",
	} {
		parent := addCommand(root, inv, group, governanceCommand)
		addFlags(parent, inv, strings.Join(governance.ArgumentKeys(group, "status"), " "))
		for _, action := range strings.Fields(actions) {
			handler := governanceCommand
			cmd := addCommand(parent, inv, action, handler)
			keys := governance.ArgumentKeys(group, action)
			if action == "verify" && containsName([]string{"lifecycle", "contract", "evidence", "handoff"}, group) {
				keys = verificationOptions(group, "")
			}
			addFlags(cmd, inv, strings.Join(keys, " "))
			if containsName(governance.ArgumentKeys(group, action), "arg0") {
				cmd.Args = maxPositionals(1)
			}
		}
	}
}

func governanceCommand(ctx context.Context, o options) (any, string, error) {
	command := o.args[0]

	profile := o.values["profile"]
	root, err := projectRoot(o)
	if err != nil {
		return nil, profile, err
	}
	if err = ctx.Err(); err != nil {
		return nil, profile, domain.Wrap("CANCELLED", err)
	}

	action := "status"
	if len(o.args) > 1 {
		action = o.args[1]
	}
	if len(o.args) > 2 {
		for i, a := range o.args[2:] {
			o.values[fmt.Sprintf("arg%d", i)] = a
		}
	}
	if isSemanticCommand(o) {
		r, err := governance.RunContext(ctx, command, action, root, o.values)
		if report, ok := r.(*governance.SemanticReport); ok && report.Profile != "" {
			profile = report.Profile
		}
		if report, ok := r.(*governance.DailyReport); ok && report.Profile != "" {
			profile = report.Profile
		}
		return r, profile, err
	}
	if command == "context" {
		source, err := contextTemplateSource(root, profile)
		if err != nil {
			return nil, profile, err
		}
		if source {
			r, err := governance.RunContext(ctx, command, action, root, o.values)
			if result, ok := r.(map[string]any); ok {
				result["repository_mode"] = "template-source"
				result["approval_created"] = false
			}
			return r, profile, err
		}
	}
	id, err := project.Detect(root, profile, false)
	if err != nil {
		return nil, profile, err
	}
	profile = id.Profile.Name
	if command != "context" && !isSemanticCommand(o) && o.values["view"] == "" && !(command == "contract" && action == "view") {
		if _, err := governance.Run("context", "verify", root, nil); err != nil {
			return nil, profile, err
		}
	}
	r, err := governance.RunContext(ctx, command, action, root, o.values)
	return r, profile, err

}

// Context alone accepts source identity for read-only vocabulary checks.
// Instance and all other governance command boundaries remain unchanged.
func contextTemplateSource(root, profile string) (bool, error) {
	file, err := safefs.Path(root, "yss-project.yaml")
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(file); os.IsNotExist(err) {
		return false, nil // Let normal instance identity detection explain missing identity.
	} else if err != nil {
		return false, err
	}
	v, err := schema.LoadFile(file)
	if err != nil {
		return false, err
	}
	m, ok := v.(map[string]any)
	if !ok || m["repository_mode"] != "template-source" {
		return false, nil
	}
	n, ok := m["schema_version"].(json.Number)
	rational, valid := new(big.Rat).SetString(string(n))
	if !ok || !valid || rational.Cmp(big.NewRat(1, 1)) != 0 {
		return false, domain.Fail("IDENTITY", "模板源身份 schema 不支持")
	}
	if profile != "" {
		if _, err := domain.GetProfile(profile); err != nil {
			return false, err
		}
	}
	refs := []string{domain.MetadataFile}
	for _, p := range domain.Profiles {
		refs = append(refs, p.Metadata)
	}
	for _, ref := range refs {
		file, err := safefs.Path(root, ref)
		if err != nil {
			return false, err
		}
		if _, err = os.Lstat(file); err == nil {
			return false, domain.Fail("IDENTITY", "模板源不能携带实例 metadata: "+ref)
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	return true, nil
}

// 供 Node 工作流判断能否把校验委托给本程序：每个令牌对应一组已发布的命令行行为，缺令牌就不委托。
var nodeDelegation = []string{
	"user-decision.from-record.v1",
	"user-decision.requirements-object.v1",
	"user-decision.validated-rows.v1",
	"approval.expected.v1",
	"approval.binding.v1",
	"approval.rule-rows.v1",
	"task.directory-write-scope.v1",
	"lifecycle.legacy-untracked-checkpoint.v1",
	"handoff.package-identity.v1",
	"handoff.consumption-report.v1",
	"handoff.spec-baseline-package.v1",
	"evidence.context-reconciliation.v1",
	"contract.openapi-validation.v1",
	"contract.backend-terminal.v1",
	"handoff.backend-delivery-package.v1",
	"contract.scaffold-decisions.v1",
	"contract.slice-preflight-report.v1",
	"contract.business-tickets-report.v1",
	"governance.validation-dependencies.v1",
	"handoff.source-view.v1",
	"handoff.delivery-view.v1",
	"handoff.source-approval.v1",
	"handoff.source-context-snapshot.v1",
	"handoff.spec-baseline-source-view.v1",
	"contract.spec-baseline-binding.v1",
	"contract.pending-review-publication.v1",
	"contract.plan-entry.v1",
	"contract.gate-checks.v1",
	"contract.checkpoint-approvals.v1",
	"contract.implementation-scope.v1",
	"contract.checkpoint-user-decisions.v1",
	"contract.scaffold-user-decision.v1",
	"contract.business-tickets-source-views.v1",
	"user-decision.result.v1",
	"reading.views.v1",
	"reading.render.v1",
	"reading.enable.v1",

	"template-source.verify-root.v1",
}
