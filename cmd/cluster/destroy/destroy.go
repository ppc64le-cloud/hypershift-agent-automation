package destroy

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/options"
	cmdUtil "github.com/ppc64le-cloud/hypershift-agent-automation/cmd/util"
	"github.com/ppc64le-cloud/hypershift-agent-automation/log"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/client/powervc"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/cluster"
)

func DestroyPreReq(clusterName string, powervcClient *powervc.Client) error {
	var errs []error
	if err := powervcClient.CleanUpBootImage(clusterName); err != nil {
		errs = append(errs, err)
	} else {
		log.Logger.Info("boot image cleaned")
	}
	if err := powervcClient.CleanUpBootVolume(clusterName); err != nil {
		errs = append(errs, err)
	} else {
		log.Logger.Info("boot volume cleaned")
	}
	if err := powervcClient.CleanUpFlavor(clusterName); err != nil {
		errs = append(errs, err)
	} else {
		log.Logger.Info("flavor cleaned")
	}

	if len(errs) > 1 {
		return errors.Join(errs...)
	}

	return nil
}

func DestroyCluster(c *cluster.Cluster) error {
	var errs []error
	if err := c.DescaleNodePool(); err != nil {
		errs = append(errs, err)
	} else {
		log.Logger.Info("node pool descaled")
	}

	if err := c.DestroyHC(); err != nil {
		errs = append(errs, err)
	} else {
		log.Logger.Info("hosted cluster destroyed")
	}

	if err := DestroyPreReq(c.Name, c.PowerVC); err != nil {
		errs = append(errs, err)
	} else {
		log.Logger.Info("prereq resources destroyed")
	}

	if err := c.PowerVC.DestroyAgents(c.GetWorkerName()); err != nil {
		errs = append(errs, fmt.Errorf("error destroying agents: %v", err))
	} else {
		log.Logger.Info("agents destroyed")
	}

	if err := c.CleanupISOsInVIOS(); err != nil {
		errs = append(errs, fmt.Errorf("error cleaning up iso in hmc: %v", err))
	} else {
		log.Logger.Info("iso clean up done")
	}

	if err := c.RemoveCISDNSRecords(); err != nil {
		errs = append(errs, fmt.Errorf("error removing cis dns records: %v", err))
	} else {
		log.Logger.Info("cis records deleted")
	}

	if err := c.CleanupHCManifestDir(); err != nil {
		errs = append(errs, fmt.Errorf("error removing hc manifest dir: %v", err))
	} else {
		log.Logger.Infof("cleaned local manifest dir")
	}

	if len(errs) > 1 {
		return errors.Join(errs...)
	}

	log.Logger.Info("destroying cluster completed")

	return nil
}

func Command(opts *options.ClusterOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "destroy",
		Short:        "Command to destroy the hypershift agent cluster",
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

		if err = DestroyCluster(c); err != nil {
			return fmt.Errorf("error destroy cluster: %v", err)
		}
		log.Logger.Info("destroy cluster done")

		return nil
	}

	return cmd
}
