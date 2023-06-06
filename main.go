package main

import (
	"fmt"
	"go.uber.org/zap"
	"os"
	"time"
)

const (
	resourceName = "hypershift-bm-agent"
	flavorID     = "hypershift-bm-agent-1"
	diskSize     = 120
)

var (
	zapLogger, _ = zap.NewProduction()
	logger       = zapLogger.Sugar()
)

type powerVC struct {
	storageTemplate string
	networkName     string
	host            string
}

type powerInfra struct {
	powerVC      powerVC
	hmcIP        string
	hmcUserName  string
	hmcPassword  string
	viosIP       string
	viosUserName string
	viosPassword string
}

type hostedCluster struct {
	name          string
	namespace     string
	pullSecret    string
	baseDomain    string
	sshPubKeyFile string
	releaseImage  string
}

func (infra *powerInfra) readEnv() []error {
	var set bool
	var errs []error

	infra.powerVC.host, set = os.LookupEnv("HOST_NAME")
	if !set {
		errs = append(errs, fmt.Errorf("HOST_NAME env var not set"))
	}
	infra.powerVC.storageTemplate, set = os.LookupEnv("STORAGE_TEMPLATE")
	if !set {
		errs = append(errs, fmt.Errorf("STORAGE_TEMPLATE env var not set"))
	}
	infra.powerVC.networkName, set = os.LookupEnv("NETWORK_NAME")
	if !set {
		errs = append(errs, fmt.Errorf("NETWORK_NAME env var not set"))
	}

	infra.hmcIP, set = os.LookupEnv("HMC_IP")
	if !set {
		errs = append(errs, fmt.Errorf("HMC_IP env var not set"))
	}
	infra.hmcUserName, set = os.LookupEnv("HMC_USERNAME")
	if !set {
		errs = append(errs, fmt.Errorf("HMC_USERNAME env var not set"))
	}
	infra.hmcPassword, set = os.LookupEnv("HMC_PASSWORD")
	if !set {
		errs = append(errs, fmt.Errorf("HMC_PASSWORD env var not set"))
	}
	infra.viosIP, set = os.LookupEnv("VIOS_IP")
	if !set {
		errs = append(errs, fmt.Errorf("VIOS_IP env var not set"))
	}
	infra.viosUserName, set = os.LookupEnv("VIOS_USERNAME")
	if !set {
		errs = append(errs, fmt.Errorf("VIOS_USERNAME env var not set"))
	}
	infra.viosPassword, set = os.LookupEnv("VIOS_PASSWORD")
	if !set {
		errs = append(errs, fmt.Errorf("VIOS_PASSWORD env var not set"))
	}
	return errs
}

func (hc *hostedCluster) readEnv() []error {
	var set bool
	var errs []error

	hc.name, set = os.LookupEnv("HC_NAME")
	if !set {
		hc.name = fmt.Sprintf("%s-%s", resourceName, time.Now().UTC().Format(time.DateOnly))
	}
	hc.namespace, set = os.LookupEnv("HC_NAMESPACE")
	if !set {
		hc.namespace = "clusters"
	}
	hc.pullSecret, set = os.LookupEnv("PULL_SECRET")
	if !set {
		errs = append(errs, fmt.Errorf("PULL_SECRET env var not set"))
	}
	hc.baseDomain, set = os.LookupEnv("BASE_DOMAIN")
	if !set {
		errs = append(errs, fmt.Errorf("BASE_DOMAIN env var not set"))
	}
	hc.releaseImage, set = os.LookupEnv("RELEASE_IMAGE")
	if !set {
		errs = append(errs, fmt.Errorf("RELEASE_IMAGE env var not set"))
	}
	hc.sshPubKeyFile, set = os.LookupEnv("SSH_PUB_KEY")
	if !set {
		errs = append(errs, fmt.Errorf("SSH_PUB_KEY env var not set"))
	}

	return errs
}

func main() {
	infra := powerInfra{}
	infra.powerVC = powerVC{}
	if err := infra.readEnv(); len(err) > 0 {
		logger.Errorf("error read powerInfra env: %v", err)
	}
	hc := hostedCluster{}
	if err := hc.readEnv(); len(err) > 0 {
		logger.Errorf("error read hosted cluster env: %v", err)
	}

	client, err := createOpenStackServiceClient()
	if err != nil {
		logger.Errorf("error create openstack client: %v", err)
		return
	}

	if err = e2e(client, infra, hc); err != nil {
		logger.Errorf("error executing e2e: %v", err)
		return
	}
}
