package cli

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/tracker"
	"github.com/spf13/cobra"
)

func addTrackerCommands(root *cobra.Command, inv *invocation) {
	parent := addCommand(root, inv, "tracker", trackerCommand)
	addFlags(parent, inv, "root")
	addFlags(addCommand(parent, inv, "status", trackerCommand), inv, "root fetch")
	addFlags(addCommand(parent, inv, "link", trackerCommand), inv, "root ticket issue apply")
	addFlags(addCommand(parent, inv, "sync", trackerCommand), inv, "root ticket plan out apply plan-file confirm-remote")
}

func trackerCommand(ctx context.Context, o options) (any, string, error) {

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
	result, err := tracker.Run(ctx, action, root, o.values, tracker.Env{})
	return result, profile, err

}
