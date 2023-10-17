package util

import (
	"errors"
	"fmt"

	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/options"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/client/hmc"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/client/ibmcloud"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/client/powervc"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/cluster"
)

func CreateClusterClient(opts *options.ClusterOptions) (*cluster.Cluster, error) {
	hmcClient, err := hmc.NewClient()
	if err != nil {
		return nil, fmt.Errorf("error create powervc client: %v", err)
	}

	ibmCloudClient, err := ibmcloud.NewClient()
	if err != nil {
		return nil, fmt.Errorf("error create ibmcloud client: %v", err)
	}

	powerVCClient, err := powervc.NewClient()
	if err != nil {
		return nil, fmt.Errorf("error create powervc client: %v", err)
	}

	c, err := cluster.New(opts, hmcClient, ibmCloudClient, powerVCClient)
	if err != nil {
		return nil, fmt.Errorf("error get new cluster: %v", err)
	}

	return c, nil
}

func ValidateCliOptions(clusterOptions *options.ClusterOptions) error {
	var errs []error
	if clusterOptions.Name == "" {
		errs = append(errs, fmt.Errorf("--name flag is required"))
	}

	if clusterOptions.BaseDomain == "" {
		errs = append(errs, fmt.Errorf("--base-domain flag is required"))
	}

	if clusterOptions.ReleaseImage == "" {
		errs = append(errs, fmt.Errorf("--release-image flag is required"))
	}

	if clusterOptions.PullSecretFile == "" {
		errs = append(errs, fmt.Errorf("--pull-secret flag is required"))
	}

	if clusterOptions.SSHKeyFile == "" {
		errs = append(errs, fmt.Errorf("--ssh-key flag is required"))
	}

	if len(errs) > 1 {
		return errors.Join(errs...)
	}

	return nil
}
