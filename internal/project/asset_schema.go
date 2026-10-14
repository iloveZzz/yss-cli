package project

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/assetmigration"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type AssetSchemaMigration struct {
	Input            string `json:"input"`
	ExecutableDigest string `json:"executableDigest"`
}
type assetSchemaInput struct {
	Family string         `json:"family"`
	Source string         `json:"source"`
	Output string         `json:"output"`
	Values map[string]any `json:"values"`
}

func BuildAssetSchema(root, profile, input string) (*Plan, error) {
	id, err := Detect(root, profile, false)
	if err != nil {
		return nil, err
	}
	executable, err := domain.ExecutableDigest()
	if err != nil {
		return nil, err
	}
	p := &Plan{SchemaVersion: 2, ProtocolVersion: domain.ProtocolVersion, Command: "migrate", MigrationKind: "asset-schema", Root: id.Root, Profile: id.Profile.Name, Inputs: map[string]domain.Descriptor{}, Variables: map[string]string{}, Changes: []Change{}, Conflicts: []string{}, Preserved: []string{}, Blockers: []Blocker{}, AssetSchema: &AssetSchemaMigration{Input: input, ExecutableDigest: executable}}
	read := func(ref string) ([]byte, error) {
		file, err := safefs.Path(id.Root, ref)
		if err != nil {
			return nil, err
		}
		before, err := safefs.Describe(id.Root, ref)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		if before.Type != "file" || before.Digest != safefs.Digest(raw) {
			return nil, domain.Fail("INPUT_DRIFT", "迁移输入变化")
		}
		p.Inputs[ref] = before
		return raw, nil
	}
	for _, ref := range []string{"yss-project.yaml", domain.MetadataFile} {
		if _, err = read(ref); err != nil {
			return nil, err
		}
	}
	raw, err := read(input)
	if err != nil {
		return nil, err
	}
	parsed, err := schema.Parse(raw)
	if err != nil {
		return nil, err
	}
	raw, err = json.Marshal(parsed)
	if err != nil {
		return nil, err
	}
	var request assetSchemaInput
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&request); err != nil {
		return nil, err
	}
	if request.Source == "" || request.Output == "" || request.Source == request.Output {
		return nil, domain.Fail("ARGUMENT", "迁移必须显式指定源资产和不同的新草稿路径")
	}
	source, err := read(request.Source)
	if err != nil {
		return nil, err
	}
	before, err := safefs.Describe(id.Root, request.Output)
	if err != nil {
		return nil, err
	}
	p.Inputs[request.Output] = before
	if before.Type != "missing" {
		p.Blockers = append(p.Blockers, Blocker{Path: request.Output, Code: "CONFLICT", Reason: "新草稿路径已存在，禁止覆盖"})
	}
	data, issues, err := assetmigration.Upgrade(request.Family, source, request.Values)
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		p.Blockers = append(p.Blockers, Blocker{Path: issue.Path, Code: "MIGRATION_FACT_REQUIRED", Reason: issue.Message})
	}
	p.Changes = append(p.Changes, Change{Path: request.Output, Before: before, After: domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: 0644}, Data: base64.StdEncoding.EncodeToString(data), Ownership: "user-asset-schema"})
	p.ReadyToApply = len(p.Blockers) == 0
	p.Digest = planDigest(p)
	return p, nil
}

func applyAssetSchema(ctx context.Context, p *Plan) (transaction.Result, error) {
	if p.AssetSchema == nil || p.Command != "migrate" || p.SchemaVersion != 2 || p.Digest != planDigest(p) {
		return transaction.Result{}, domain.Fail("PLAN", "资产迁移计划无效")
	}
	if !p.ReadyToApply || len(p.Blockers) != 0 || len(p.Conflicts) != 0 {
		return transaction.Result{}, domain.Fail("CONFLICT", "资产迁移存在未关闭阻断")
	}
	fresh, err := BuildAssetSchema(p.Root, p.Profile, p.AssetSchema.Input)
	if err != nil {
		return transaction.Result{}, err
	}
	if fresh.Digest != p.Digest {
		return transaction.Result{}, domain.Fail("INPUT_DRIFT", "资产、迁移事实或执行器变化；重新规划")
	}
	ops := []transaction.Operation{}
	for _, c := range fresh.Changes {
		data, err := base64.StdEncoding.DecodeString(c.Data)
		if err != nil {
			return transaction.Result{}, err
		}
		before := c.Before
		ops = append(ops, transaction.Operation{Path: c.Path, Before: &before, Data: data, Mode: c.After.Mode})
	}
	return transaction.ApplyContextWithValidation(ctx, p.Root, "migrate", ops, p.Inputs, nil, func() error {
		for _, c := range p.Changes {
			observed, err := safefs.Describe(p.Root, c.Path)
			if err != nil {
				return err
			}
			if observed != c.After {
				return domain.Fail("INPUT_DRIFT", "迁移草稿内容变化")
			}
		}
		return nil
	})
}
