package cluster

import (
	"github.com/spf13/cobra"

	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/cluster/create"
	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/cluster/destroy"
	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/options"
)

func Command(opts *options.ClusterOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Commands to interact with hypershift agent cluster",
		SilenceUsage: true,
	}

	cmd.AddCommand(create.Command(opts))
	cmd.AddCommand(destroy.Command(opts))

	return cmd
}
