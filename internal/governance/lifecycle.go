package governance

import "context"
import "github.com/iloveZzz/yss-cli/internal/governance/lifecycle"

var load = lifecycle.Load
var registry = lifecycle.Registry
var validateRegistry = lifecycle.ValidateRegistry
var lifecycleState = lifecycle.State
var assetCheck = lifecycle.AssetCheck
var rejectUnportedArgs = lifecycle.RejectUnportedArgs

func lifecycleRun(ctx context.Context, group, action, root string, args map[string]string) (any, error) {
	return lifecycle.Query(ctx, group, action, root, args, lifecycle.QuerySources{ProfileGuidance: ProfileGuidance, ProgressionRead: progressionRead})
}
