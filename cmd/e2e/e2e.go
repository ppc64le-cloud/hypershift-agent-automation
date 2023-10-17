package e2e

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/cluster/create"
	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/cluster/destroy"
	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/options"
	cmdUtil "github.com/ppc64le-cloud/hypershift-agent-automation/cmd/util"
	"github.com/ppc64le-cloud/hypershift-agent-automation/log"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/cluster"
)

func Run(c *cluster.Cluster) error {
	imageID, networkID, gatewayIP, prefix, err := create.SetupPreReq(c)
	if err != nil {
		return fmt.Errorf("error setup pre req: %v", err)
	}
	log.Logger.Infof("retrieved prereq resource info imageID: %s, networkID: %s, gatewayIP: %s, prefix: %d", imageID, networkID, gatewayIP, prefix)

	if err = create.SetupCluster(c, imageID, networkID, gatewayIP, prefix); err != nil {
		return fmt.Errorf("error setup cluster: %v", err)
	}
	log.Logger.Info("setup cluster done")

	if err = destroy.DestroyCluster(c); err != nil {
		return fmt.Errorf("error destroying cluster: %v", err)
	}
	log.Logger.Info("destroying cluster completed")

	return nil
}

func Command(opts *options.ClusterOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "e2e",
		Short:        "Command to run e2e test on hypershift agent cluster",
		SilenceUsage: true,
	}
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		if err := cmdUtil.ValidateCliOptions(opts); err != nil {
			return err
		}
		c, err := cmdUtil.CreateClusterClient(opts)
		if err != nil {
			return fmt.Errorf("error create clients: %v", err)
		}
		return Run(c)
	}
	return cmd
}
