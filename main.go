package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/cluster"
	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/e2e"
	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/options"
)

func main() {
	opts := &options.ClusterOptions{
		NameSpace: "clusters",
		NodeCount: 2,
	}

	cmd := &cobra.Command{
		Use:              "hypershift-agent-automation",
		SilenceUsage:     true,
		TraverseChildren: true,

		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}

	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.PersistentFlags().StringVar(&opts.NameSpace, "namespace", opts.NameSpace, "A namespace for the cluster")
	cmd.PersistentFlags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the cluster")
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The OCP release image for the cluster")
	cmd.PersistentFlags().StringVar(&opts.PullSecretFile, "pull-secret", opts.PullSecretFile, "File path to a pull secret.")
	cmd.PersistentFlags().StringVar(&opts.SSHKeyFile, "ssh-key", opts.SSHKeyFile, "Path to an SSH key file")
	cmd.PersistentFlags().IntVar(&opts.NodeCount, "node-count", opts.NodeCount, "Number of nodes in the the cluster")

	cmd.AddCommand(e2e.Command(opts))
	cmd.AddCommand(cluster.Command(opts))

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
