package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/ppc64le-cloud/hypershift-agent-automation/cmd/options"
	"github.com/ppc64le-cloud/hypershift-agent-automation/log"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/client/hmc"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/client/ibmcloud"
	"github.com/ppc64le-cloud/hypershift-agent-automation/pkg/client/powervc"
	"github.com/ppc64le-cloud/hypershift-agent-automation/util"
)

const (
	hcDesiredServiceSpec = `[
		{
			"service": "APIServer",
			"servicePublishingStrategy": {
				"type": "LoadBalancer"
			}
		},
		{
			"service": "OAuthServer",
			"servicePublishingStrategy": {
				"type": "Route"
			}
		},
		{
			"service": "OIDC",
			"servicePublishingStrategy": {
				"type": "None"
			}
		},
		{
			"service": "Konnectivity",
			"servicePublishingStrategy": {
				"type": "Route"
			}
		},
		{
			"service": "Ignition",
			"servicePublishingStrategy": {
				"type": "Route"
			}
		},
		{
			"service": "OVNSbDb",
			"servicePublishingStrategy": {
				"type": "Route"
			}
		}
	]`

	nmStateConfigTemplateFile = "nmstate-config-template.yaml"
	infraEnvTemplateFile      = "infraenv-template.yaml"

	infraEnvFile = "infraenv.yaml"
)

var discoveryISOFile = func(hcName string) string { return fmt.Sprintf("%s-discovery.iso", hcName) }

type Cluster struct {
	Name           string
	Namespace      string
	PullSecretFile string
	BaseDomain     string
	SSHKeyFile     string
	ReleaseImage   string
	NodeCount      int
	HMC            *hmc.Client
	IBMCloud       *ibmcloud.Client
	PowerVC        *powervc.Client
}

func New(opts *options.ClusterOptions, hmc *hmc.Client, ibmcloud *ibmcloud.Client, powervc *powervc.Client) (*Cluster, error) {
	c := &Cluster{
		Name:           opts.Name,
		Namespace:      opts.NameSpace,
		BaseDomain:     opts.BaseDomain,
		ReleaseImage:   opts.ReleaseImage,
		PullSecretFile: opts.PullSecretFile,
		SSHKeyFile:     opts.SSHKeyFile,
		NodeCount:      opts.NodeCount,
		HMC:            hmc,
		IBMCloud:       ibmcloud,
		PowerVC:        powervc,
	}

	return c, nil
}

func (c Cluster) getCPNamespace() string { return fmt.Sprintf("%s-%s", c.Namespace, c.Name) }
func (c Cluster) GetWorkerName() string  { return fmt.Sprintf("%s-worker", c.Name) }

func (c Cluster) createHC() error {
	args := []string{"create", "cluster", "agent",
		"--name", c.Name,
		"--infra-id", c.Name,
		"--agent-namespace", c.getCPNamespace(),
		"--pull-secret", c.PullSecretFile,
		"--base-domain", c.BaseDomain,
		"--ssh-key", c.SSHKeyFile,
		"--release-image", c.ReleaseImage,
		"--render",
	}

	out, e, err := util.ExecuteCommand("hypershift", args)

	if err != nil {
		return fmt.Errorf("error create cluster agent %v, stdout: %s, stderr: %s", err, out, e)
	}
	log.Logger.Infof("out: %v", out)

	ogManifestStr := out
	ogManifestL := strings.Split(ogManifestStr, "---")

	log.Logger.Infof("ogL", ogManifestL)
	var hcManifestS string
	var hcManifestIndex int
	for i, m := range ogManifestL {
		if strings.Contains(m, "kind: HostedCluster") {
			hcManifestS = m
			hcManifestIndex = i
		}
	}

	log.Logger.Infof("hcmanifest: %v", hcManifestS)
	hcManifest := map[string]interface{}{}

	if err = yaml.Unmarshal([]byte(hcManifestS), hcManifest); err != nil {
		return err
	}

	var desiredService []map[string]interface{}
	if err = json.Unmarshal([]byte(hcDesiredServiceSpec), &desiredService); err != nil {
		return err
	}

	spec := hcManifest["spec"].(map[string]interface{})
	spec["services"] = desiredService
	hcManifest["spec"] = spec

	desiredHCSpec, err := yaml.Marshal(hcManifest)
	if err != nil {
		return err
	}

	ogManifestL[hcManifestIndex] = "\n" + string(desiredHCSpec)
	clusterManifestLoc := fmt.Sprintf("%s/clusters.yaml", util.GetManifestDir(c.Name))
	f, err := os.Create(clusterManifestLoc)
	if err != nil {
		return err
	}

	if _, err = f.Write([]byte(strings.Join(ogManifestL, "---"))); err != nil {
		return err
	}

	args = []string{"apply", "-f", clusterManifestLoc}
	out, e, err = util.ExecuteCommand("oc", args)
	if err != nil || e != "" {
		return fmt.Errorf("error applying clusters manifest, stderr: %s, error: %v", e, err)
	}

	log.Logger.Info("hosted cluster manifests applied")
	return nil
}

func (c Cluster) SetupHC() error {
	args := []string{"create", "namespace", c.getCPNamespace()}
	_, e, err := util.ExecuteCommand("oc", args)
	if err != nil && !strings.Contains(e, "AlreadyExists") {
		return fmt.Errorf("error creating contorl plane namespace stderr: %s, error: %v", e, err)
	}

	args = []string{"get", "hc", c.Name, "-n", c.Namespace}
	_, e, err = util.ExecuteCommand("oc", args)
	if err != nil || e != "" {
		if strings.Contains(e, "NotFound") {
			if err = c.createHC(); err != nil {
				return fmt.Errorf("error create hc: %v", err)
			}
		} else {
			return fmt.Errorf("error get hc: %v, e: %v", err, e)
		}
	}

	log.Logger.Info("waiting for hosted cluster status to become available")
	if _, _, err = util.ExecuteCommand("oc", []string{"wait", "hc", c.Name, "-n", c.Namespace, "--for=condition=Available", "--timeout=10m"}); err != nil {
		return err
	}

	return nil
}

func (c Cluster) SetupCISDNSRecords(workerIP string) error {
	args := []string{"get", "service", "kube-apiserver", "-n", c.getCPNamespace(), "-o", "json"}
	out, e, err := util.ExecuteCommand("oc", args)

	if err != nil || e != "" {
		return fmt.Errorf("error retrieving kube apiserver details: %v, e: %v", err, e)
	}

	apiServerSvc := map[string]interface{}{}
	if err = json.Unmarshal([]byte(out), &apiServerSvc); err != nil {
		return fmt.Errorf("error unmarshal kube apiserver response: %v", err)
	}

	var apiServerHostname string
	ingress := apiServerSvc["status"].(map[string]interface{})["loadBalancer"].(map[string]interface{})["ingress"].([]interface{})
	if len(ingress) < 0 {
		return fmt.Errorf("hostname not generated for kube-apiserver")
	}
	apiServerHostname = ingress[0].(map[string]interface{})["hostname"].(string)

	createOrUpdateDNSRecord := func(rType, name, content string) error {
		var dnsRecordID string
		dnsRecordID, err = c.IBMCloud.GetDNSRecordID(name)
		if err != nil && err.Error() != ibmcloud.DNSRecordNotExist(name).Error() {
			return err
		}

		if dnsRecordID != "" {
			log.Logger.Infof("updating dns record name %s content %s", name, content)
			if err = c.IBMCloud.UpdateDNSRecord(dnsRecordID, content); err != nil {
				return err
			}
		} else {
			log.Logger.Infof("creating dns record name %s content %s", name, content)
			if err = c.IBMCloud.CreateDNSRecord(rType, name, content); err != nil {
				return err
			}
		}

		return nil
	}

	if err = createOrUpdateDNSRecord("CNAME", fmt.Sprintf("api.%s", c.Name), apiServerHostname); err != nil {
		return fmt.Errorf("error setting up api cis record: %v", err)
	}

	if err = createOrUpdateDNSRecord("CNAME", fmt.Sprintf("api-int.%s", c.Name), apiServerHostname); err != nil {
		return fmt.Errorf("error setting up api-int cis record: %v", err)
	}

	if err = createOrUpdateDNSRecord("A", fmt.Sprintf("*.apps.%s", c.Name), workerIP); err != nil {
		return fmt.Errorf("error setting up *.apps cis record: %v", err)
	}

	return nil
}

func (c Cluster) SetupNMStateConfig(agents []powervc.Agent, prefix int, gateway, label string) error {
	for _, agent := range agents {
		templateConfig := map[string]string{
			"Name":      fmt.Sprintf("%s-%s", c.Name, agent.PartitionName),
			"Namespace": c.getCPNamespace(),
			"Label":     label,
			"Prefix":    strconv.Itoa(prefix),
			"Gateway":   gateway,
			"MAC":       agent.MAC,
			"IP":        agent.IP,
		}

		nmStateConfigTemplateFileAbsPath, err := util.GetAbsoluteTemplatePath(nmStateConfigTemplateFile)
		if err != nil {
			return err
		}

		templateCont, err := os.ReadFile(nmStateConfigTemplateFileAbsPath)
		if err != nil {
			return err
		}

		t := template.Must(template.New("nmstate-config").Parse(string(templateCont)))

		nmStateConfigData := &bytes.Buffer{}
		if err = t.Execute(nmStateConfigData, templateConfig); err != nil {
			return err
		}

		nmStateConfigFileLoc := fmt.Sprintf("%s/nmstate-config-%s.yaml", util.GetManifestDir(c.Name), agent.PartitionName)
		f, err := os.Create(nmStateConfigFileLoc)
		if err != nil {
			return err
		}

		if _, err = f.Write(nmStateConfigData.Bytes()); err != nil {
			return err
		}

		_, e, err := util.ExecuteCommand("oc", []string{"apply", "-f", nmStateConfigFileLoc})
		if err != nil || e != "" {
			return fmt.Errorf("error applying nmstate config for agent: %s, e: %v, err: %v", agent, e, err)
		}

		log.Logger.Infof("%s applied", nmStateConfigFileLoc)
	}

	return nil
}

func (c Cluster) SetupInfraEnv(label string) error {
	sshPubKeyContent, err := os.ReadFile(c.SSHKeyFile)
	if err != nil {
		return err
	}

	templateConfig := map[string]string{
		"Name":        c.Name,
		"Namespace":   c.getCPNamespace(),
		"Label":       label,
		"SSH_Pub_Key": string(sshPubKeyContent),
	}

	infraEnvTemplateFileAbsPath, err := util.GetAbsoluteTemplatePath(infraEnvTemplateFile)
	if err != nil {
		return err
	}

	templateCont, err := os.ReadFile(infraEnvTemplateFileAbsPath)
	if err != nil {
		return err
	}

	t := template.Must(template.New("infraenv").Parse(string(templateCont)))

	infraEnvConfigData := &bytes.Buffer{}
	if err = t.Execute(infraEnvConfigData, templateConfig); err != nil {
		return err
	}

	infraEnvFileLoc := fmt.Sprintf("%s/%s", util.GetManifestDir(c.Name), infraEnvFile)
	f, err := os.Create(infraEnvFileLoc)
	if err != nil {
		return err
	}

	if _, err = f.Write(infraEnvConfigData.Bytes()); err != nil {
		return err
	}

	_, e, err := util.ExecuteCommand("oc", []string{"apply", "-f", infraEnvFileLoc})
	if err != nil || e != "" {
		return fmt.Errorf("error applying infraenv, e: %v, err: %v", e, err)
	}

	log.Logger.Infof("%s applied", infraEnvFileLoc)
	return nil
}

func (c Cluster) DownloadISO() error {
	cpNamespce := c.getCPNamespace()
	_, e, err := util.ExecuteCommand("oc", []string{"wait", "--timeout=5m", "--for=condition=ImageCreated", "-n", cpNamespce, fmt.Sprintf("infraenv/%s", c.Name)})
	if err != nil || e != "" {
		return fmt.Errorf("error waiting for discovery iso creation, e: %v, err: %v", e, err)
	}

	out, e, err := util.ExecuteCommand("oc", []string{"get", "infraenv", c.Name, "-n", cpNamespce, "-o", "json"})
	if err != nil || e != "" {
		return fmt.Errorf("error get infraenv, e: %v, err: %v", e, err)
	}

	infraEnv := map[string]interface{}{}
	if err = json.Unmarshal([]byte(out), &infraEnv); err != nil {
		return err
	}
	isoDownloadURL := infraEnv["status"].(map[string]interface{})["isoDownloadURL"].(string)

	out, e, err = util.ExecuteCommand("curl", []string{isoDownloadURL, "--output", fmt.Sprintf("%s/%s", util.GetManifestDir(c.Name), discoveryISOFile(c.Name))})
	if err != nil {
		return fmt.Errorf("error downloading iso, e: %v, err: %v", e, err)
	}

	return nil
}

func (c Cluster) CopyAndMountISO(agents []powervc.Agent) error {

	if err := util.SCPFile(c.HMC.VIOS.SSHClient, discoveryISOFile(c.Name), util.GetManifestDir(c.Name), c.HMC.VIOS.HomeDir); err != nil {
		return fmt.Errorf("error scp file: %v", err)
	}
	log.Logger.Info("scp discovery file to vios done")

	for _, agent := range agents {
		lparID, err := c.HMC.GetLPARID(c.PowerVC.Host, agent.PartitionName)
		if err != nil {
			return fmt.Errorf("error retrieving lpar id for host: %s, error: %v", agent.PartitionName, err)
		}
		log.Logger.Infof("%s lparID retrieved for agent: %s", lparID, agent.Name)

		vhost, err := c.HMC.GetVHOST(lparID)
		if err != nil {
			return fmt.Errorf("error retrieving vhost: %v", err)
		}
		log.Logger.Infof("%s vhost retrieved for agent: %s", vhost, agent.Name)

		voptName := fmt.Sprintf("%s-agent", agent.PartitionName)
		if err = c.HMC.CreateVOpt(voptName, discoveryISOFile(c.Name)); err != nil {
			return err
		}
		log.Logger.Infof("%s vopt created for agent %s", voptName, agent.Name)

		if err = c.HMC.MapVOptToVTOpt(vhost, voptName); err != nil {
			return fmt.Errorf("error map vtopt to vopt: %v", err)
		}
		log.Logger.Infof("mounted iso on agent %s", agent.Name)

		// Using this hack to set the boot string to a static dev path(/vdevice/v-scsi@30000002/disk@8200000000000000) till iso based deployment is ready on PowerVC
		//
		if err = c.HMC.SetupBootString(c.PowerVC.Host, agent.PartitionName); err != nil {
			return err
		}
		log.Logger.Infof("boot_string configured for %s", agent.Name)
	}

	return nil
}

func (c Cluster) ApproveAgents(agents []powervc.Agent) error {
	var currentlyApproved int
	f := func() (bool, error) {

		out, e, err := util.ExecuteCommand("oc", []string{"get", "agents", "-n", c.getCPNamespace(), "-o", "json"})
		if err != nil || e != "" {
			return false, fmt.Errorf("error get agents, e: %v, err: %v", e, err)
		}
		var resp map[string]interface{}
		if err = json.Unmarshal([]byte(out), &resp); err != nil {
			return false, fmt.Errorf("error unmarshal agent list resp: %v", err)
		}

		approveAgent := func(name, hostName string) error {
			patchCont := fmt.Sprintf("{\"spec\":{\"approved\":true, \"hostname\": \"%s\"}}", hostName)
			_, e, err = util.ExecuteCommand("oc", []string{"patch", "agent", name, "-n", c.getCPNamespace(), "-p", patchCont, "--type", "merge"})
			if err != nil || e != "" {
				return fmt.Errorf("error approving primary agent, e: %v, err: %v", e, err)
			}
			return nil
		}

		for _, a := range resp["items"].([]interface{}) {
			agent := a.(map[string]interface{})
			approved := agent["spec"].(map[string]interface{})["approved"].(bool)

			if !approved {
				rName := agent["metadata"].(map[string]interface{})["name"].(string)
				log.Logger.Infof("agent: %v", agent)
				inventory := agent["status"].(map[string]interface{})["inventory"]
				if inventory == nil {
					// still inventory not collected for the agent
					continue
				}
				nwInterfaces := inventory.(map[string]interface{})["interfaces"].([]interface{})
				mac := nwInterfaces[0].(map[string]interface{})["macAddress"].(string)
				for _, ag := range agents {
					if ag.MAC == mac {
						if err = approveAgent(rName, ag.Name); err != nil {
							return false, fmt.Errorf("error approving agent %s: %v", rName, err)
						}
						log.Logger.Infof("Approved agent %s", rName)
						currentlyApproved += 1
						break
					}
				}
			}
		}
		if currentlyApproved < c.NodeCount {
			log.Logger.Infof("still agents are not approved, currently approved %v", currentlyApproved)
			return false, nil
		}

		return true, nil
	}
	if err := wait.PollImmediate(time.Minute*1, time.Minute*30, f); err != nil {
		return fmt.Errorf("error approving agents %v", err)
	}

	return nil
}

func (c Cluster) ScaleNodePool() error {
	_, e, err := util.ExecuteCommand("oc", []string{"scale", "np", c.Name, "-n", c.Namespace, "--replicas", "2"})
	if err != nil || e != "" {
		return fmt.Errorf("error scaling node pool, e: %v, err: %v", e, err)
	}
	return nil
}

func (c Cluster) DownloadKubeConfig() error {
	f, err := os.Create(util.KubeConfigFile(c.Name))
	if err != nil {
		return fmt.Errorf("error creating kubeconfig file: %v", err)
	}

	out, e, err := util.ExecuteCommand("hypershift", []string{"create", "kubeconfig", "--name", c.Name})
	if e != "" || err != nil {
		return fmt.Errorf("error retrieving kubeconfig, e: %s, err: %v", e, err)
	}

	_, err = f.Write([]byte(out))
	if err != nil {
		return fmt.Errorf("error writing kubeconfig content to file: %v", err)
	}

	return nil
}

func (c Cluster) SetupIngressControllerNodeSelector(agentName string) error {
	args := []string{"patch", "ingresscontroller", "default", "-n", "openshift-ingress-operator", "-p", fmt.Sprintf(`{"spec": {"nodePlacement": {"nodeSelector": { "matchLabels": { "kubernetes.io/hostname": "%s"}}, "tolerations": [{ "effect": "NoSchedule", "key": "kubernetes.io/hostname", "operator": "Exists"}]}}}`, agentName), "--type=merge", fmt.Sprintf("--kubeconfig=%s", util.KubeConfigFile(c.Name))}
	_, e, err := util.ExecuteCommand("oc", args)
	if e != "" || err != nil {
		return fmt.Errorf("error configuring ingresscontroller node selector on agent cluster, e: %s, err: %v", e, err)
	}

	return nil
}

func (c Cluster) MonitorHC() error {
	_, e, err := util.ExecuteCommand("oc", []string{"wait", "hc", c.Name, "-n", c.Namespace, "--for=condition=ClusterVersionAvailable=True", "--timeout=30m"})
	if err != nil || e != "" {
		return fmt.Errorf("error wait for hosted cluster to reach completed state, e: %v, err: %v", e, err)
	}
	return nil
}

func (c Cluster) DescaleNodePool() error {
	_, e, err := util.ExecuteCommand("oc", []string{"scale", "np", c.Name, "-n", c.Namespace, "--replicas", "0"})
	if err != nil || e != "" {
		return fmt.Errorf("error descaling node pool, e: %v, err: %v", e, err)
	}
	return nil
}

func (c Cluster) CleanupISOsInVIOS() interface{} {
	viosSSHClient, err := util.CreateSSHClient(c.HMC.VIOS.IP, c.HMC.VIOS.UserName, c.HMC.VIOS.Password)
	if err != nil {
		return fmt.Errorf("error create ssh client: %v", err)
	}

	isoPath := fmt.Sprintf("%s/%s", c.HMC.VIOS.HomeDir, c.Name)
	rmISOCommand := fmt.Sprintf("rm -f %s", isoPath)
	if _, _, err = util.ExecuteRemoteCommand(viosSSHClient, rmISOCommand); err != nil {
		return fmt.Errorf("error executing command to remove iso: %v", err)
	}

	return nil
}

func (c Cluster) DestroyHC() error {
	_, e, err := util.ExecuteCommand("hypershift", []string{"destroy", "cluster", "agent", "--name", c.Name, "--infra-id", c.Name})
	if err != nil || e != "" {
		return fmt.Errorf("error destroying hosted cluster, e: %v, err: %v", e, err)
	}
	return nil
}

func (c Cluster) RemoveCISDNSRecords() error {
	deleteDNSRecord := func(name string) error {
		var dnsRecordID string
		var err error
		dnsRecordID, err = c.IBMCloud.GetDNSRecordID(name)
		if err != nil {
			return err
		}
		if err = c.IBMCloud.DeleteDNSRecord(dnsRecordID); err != nil {
			return err
		}
		return nil
	}

	if err := deleteDNSRecord(fmt.Sprintf("api.%s", c.Name)); err != nil {
		return fmt.Errorf("error deleteing dns record %s, err: %v", fmt.Sprintf("api.%s", c.Name), err)
	}

	if err := deleteDNSRecord(fmt.Sprintf("api-int.%s", c.Name)); err != nil {
		return fmt.Errorf("error deleteing dns record %s, err: %v", fmt.Sprintf("api.%s", c.Name), err)
	}

	if err := deleteDNSRecord(fmt.Sprintf("*.apps.%s", c.Name)); err != nil {
		return fmt.Errorf("error deleteing dns record %s, err: %v", fmt.Sprintf("api.%s", c.Name), err)
	}

	return nil
}

func (c Cluster) CleanupHCManifestDir() error {
	return os.RemoveAll(util.GetManifestDir(c.Name))
}
