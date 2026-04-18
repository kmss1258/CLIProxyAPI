package quota

import (
	"context"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func init() {
	coreusage.RegisterPlugin(&usagePlugin{})
}

type usagePlugin struct{}

func (p *usagePlugin) HandleUsage(_ context.Context, record coreusage.Record) {
	if record.APIKey == "" {
		return
	}
	LogApplyUsageError(record.APIKey, DefaultManager().ApplyUsage(record.APIKey, record.Detail.OutputTokens))
}
