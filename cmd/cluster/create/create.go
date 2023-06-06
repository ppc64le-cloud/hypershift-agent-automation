package create

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/options"
	cmdUtil "github.com/ppc64le-cloud/hypershift-agent-automation/cmd/util"
	"github.com/ppc64le-cloud/hypershift-agent-automation/log"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/cluster"
	"github.com/ppc64le-cloud/hypershift-agent-automation/util"
)

func SetupPreReq(c *cluster.Cluster) (string, string, string, int, error) {
	volumeID, err := c.PowerVC.SetupEmptyBootVol(c.Name)
	if err != nil {
		log.Logger.Errorf("error setup empty boot volume: %v", err)
		return "", "", "", -1, fmt.Errorf("error setup empty boot volume: %v", err)
	}
	log.Logger.Infof("%s volume id will be used", volumeID)

	imageID, err := c.PowerVC.SetupPreReqImage(c.Name, volumeID)
	if err != nil {
		log.Logger.Errorf("error setup image %v", err)
		return "", "", "", -1, fmt.Errorf("error setup image: %v", err)
	}
	log.Logger.Infof("%s image id will be used", imageID)

	if err = c.PowerVC.SetupFlavor(c.Name); err != nil {
		log.Logger.Errorf("error setup flavor: %v", err)
		return "", "", "", -1, fmt.Errorf("error setup flavor: %v", err)
	}
	log.Logger.Infof("%s flavor id will be used", util.GenerateFlavourID(c.Name))

	networkID, gatewayIP, prefix, err := c.PowerVC.GetNetworkID()
	if err != nil {
		log.Logger.Errorf("unable to retrieve id for network, error: %v", err)
		return "", "", "", -1, fmt.Errorf("unable to retrieve id for network, error: %v", err)
	}
	log.Logger.Infof("%s network id will be used", networkID)

	return imageID, networkID, gatewayIP, prefix, nil
}

func SetupCluster(c *cluster.Cluster, imageID, networkID, gatewayIP string, prefix int) error {
	agents, err := c.PowerVC.SetupAgents(c.GetWorkerName(), imageID, networkID, util.GenerateFlavourID(c.Name), c.NodeCount)
	if err != nil {
		return fmt.Errorf("error setup agents: %v", err)
	}
	log.Logger.Infof("agent setup done. agent details: %+v", agents)

	nmStateLabel := fmt.Sprintf("label: nmstate-config-%s", c.Name)
	if err = os.Mkdir(util.GetManifestDir(c.Name), 0750); err != nil && !os.IsExist(err) {
		log.Logger.Error("error creating output dir for manifests", err)
	}
	if err = c.SetupHC(); err != nil {
		return fmt.Errorf("error setup hosted cluster: %v", err)
	}
	log.Logger.Info("hosted cluster setup done")
	if err = c.SetupNMStateConfig(agents, prefix, gatewayIP, nmStateLabel); err != nil {
		return fmt.Errorf("error setup nmstate config: %v", err)
	}
	log.Logger.Info("nmstate config setup done")

	if err = c.SetupInfraEnv(nmStateLabel); err != nil {
		return fmt.Errorf("error setup infraenv: %v", err)
	}
	log.Logger.Info("infraenv setup done")

	if err = c.DownloadISO(); err != nil {
		return fmt.Errorf("error download iso: %v", err)
	}
	log.Logger.Info("download discovery iso done")

	if err = c.CopyAndMountISO(agents); err != nil {
		return fmt.Errorf("error copy iso: %v", err)
	}
	log.Logger.Info("mount iso on agents done")

	if err = c.SetupCISDNSRecords(agents[0].IP); err != nil {
		return fmt.Errorf("error update cis dns records: %v", err)
	}
	log.Logger.Info("update cis dns records done")

	if err = c.PowerVC.RestartAgents(agents); err != nil {
		return fmt.Errorf("error restarting vm: %v", err)
	}
	log.Logger.Info("agents restarted")

	if err = c.ApproveAgents(agents); err != nil {
		return err
	}
	log.Logger.Info("agents approved")

	if err = c.ScaleNodePool(); err != nil {
		return err
	}
	log.Logger.Info("node pool scaled")

	if err = c.DownloadKubeConfig(); err != nil {
		return fmt.Errorf("error downloading kubeconfig: %v", err)
	}
	log.Logger.Info("kubeconfig downloaded")

	if err = c.SetupIngressControllerNodeSelector(agents[0].Name); err != nil {
		return err
	}
	log.Logger.Info("ingress controller patched to run on first agent")

	log.Logger.Info("waiting for hosted cluster to reach completed state")
	if err = c.MonitorHC(); err != nil {
		return fmt.Errorf("error monitor hosted cluster to reach completed state: %v", err)
	}
	log.Logger.Info("hosted cluster reached completed state")

	return nil
}

func Command(opts *options.ClusterOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Command to create a hypershift agent cluster",
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
		imageID, networkID, gatewayIP, prefix, err := SetupPreReq(c)
		if err != nil {
			return fmt.Errorf("error setup pre req: %v", err)
		}
		log.Logger.Infof("retrieved prereq resource info imageID: %s, networkID: %s, gatewayIP: %s, prefix: %d", imageID, networkID, gatewayIP, prefix)

		if err = SetupCluster(c, imageID, networkID, gatewayIP, prefix); err != nil {
			return fmt.Errorf("error setup cluster: %v", err)
		}
		log.Logger.Info("setup cluster done")

		return nil
	}

	return cmd
}
