package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/util/wait"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/template"
	"time"
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

	infraEnvFile     = "infraenv.yaml"
	discoveryISOFile = "discovery.iso"
	viosHomeDir      = "/home/padmin"
)

type agent struct {
	powerVCID            string
	powerVCPartitionName string
	ip                   string
	mac                  string
}

func getAbsoluteTemplatePath(templateName string) (string, error) {
	cd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/templates/%s", cd, templateName), nil
}

func setupHC(hc hostedCluster) error {
	cmd := exec.Command("oc", "create", "namespace", fmt.Sprintf("%s-%s", hc.namespace, hc.name))
	var e strings.Builder
	cmd.Stderr = &e
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error creating contorl plane namespace stderr: %s, error: %v", e.String(), err)
	}

	cmd = exec.Command("hypershift", "create", "cluster", "agent",
		"--name", hc.name,
		"--agent-namespace", hc.namespace,
		"--pull-secret", hc.pullSecret,
		"--base-domain", hc.baseDomain,
		"--ssh-key", hc.sshPubKeyFile,
		"--release-image", hc.releaseImage,
		"--render",
	)
	var out strings.Builder
	e = strings.Builder{}
	cmd.Stdout = &out
	cmd.Stderr = &e

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error create cluster agent %v, stdout: %s, stderr: %s", err, out.String(), e.String())
	}

	ogManifestStr := out.String()
	ogManifestL := strings.Split(ogManifestStr, "---")

	var hcManifestS string
	var hcManifestIndex int
	for i, m := range ogManifestL {
		if strings.Contains(m, "kind: HostedCluster") {
			hcManifestS = m
			hcManifestIndex = i
		}
	}

	hcManifest := map[string]interface{}{}

	if err := yaml.Unmarshal([]byte(hcManifestS), hcManifest); err != nil {
		return err
	}

	var desiredService []map[string]interface{}
	if err := json.Unmarshal([]byte(hcDesiredServiceSpec), &desiredService); err != nil {
		return err
	}

	spec := hcManifest["spec"].(map[interface{}]interface{})
	spec["services"] = desiredService
	hcManifest["spec"] = spec

	desiredHCSpec, err := yaml.Marshal(hcManifest)
	if err != nil {
		return err
	}

	ogManifestL[hcManifestIndex] = "\n" + string(desiredHCSpec)
	f, err := os.Create("clusters.yaml")
	if err != nil {
		return err
	}

	if _, err = f.Write([]byte(strings.Join(ogManifestL, "---"))); err != nil {
		return err
	}

	cmd = exec.Command("oc", "apply", "-f", "clusters.yaml")
	e = strings.Builder{}
	cmd.Stderr = &e
	if err = exec.Command("oc", "apply", "-f", "clusters.yaml").Run(); err != nil {
		return fmt.Errorf("error applying clusters manifest, stderr: %s, error: %v", e.String(), err)
	}
	logger.Info("hosted cluster manifests applied")

	logger.Info("waiting for hosted cluster status to become available")
	if err := exec.Command("oc", "wait", "hc", hc.name, "-n", hc.namespace, "--for=condition=Available", "--timeout=10m").Run(); err != nil {
		return err
	}

	return nil
}

func updateCISDNSRecords(hcName, cpNamespace, baseDomain, workerIP string) error {
	cmd := exec.Command("oc", "get", "service", "kube-apiserver", "-n", cpNamespace, "-o", "json")
	out := strings.Builder{}
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return err
	}

	apiServerSvc := map[string]interface{}{}
	if err := json.Unmarshal([]byte(out.String()), &apiServerSvc); err != nil {
		return err
	}

	var apiServerHostname string
	ingress := apiServerSvc["status"].(map[string]interface{})["loadBalancer"].(map[string]interface{})["ingress"].([]interface{})
	if len(ingress) < 0 {
		return fmt.Errorf("hostname not generated for kube-apiserver")
	}
	apiServerHostname = ingress[0].(map[string]interface{})["hostname"].(string)

	cmd = exec.Command("ibmcloud", "cis", "domains", "--output", "json")
	out = strings.Builder{}
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return err
	}

	domainList := make([]map[string]interface{}, 0)
	if err := json.Unmarshal([]byte(out.String()), &domainList); err != nil {
		return err
	}
	if len(domainList) < 0 {
		return fmt.Errorf("%s domain not exist", baseDomain)
	}

	var domainID string
	for _, domain := range domainList {
		domainName := domain["name"].(string)
		if domainName == baseDomain {
			domainID = domain["id"].(string)
		}
	}

	if err := exec.Command("ibmcloud", "cis", "dns-record-create", fmt.Sprintf("%s", domainID), "--type", "CNAME", "--name", fmt.Sprintf("api.%s", hcName), "--content", apiServerHostname).Run(); err != nil {
		return err
	}

	if err := exec.Command("ibmcloud", "cis", "dns-record-create", fmt.Sprintf("%s", domainID), "--type", "CNAME", "--name", fmt.Sprintf("api-int.%s", hcName), "--content", apiServerHostname).Run(); err != nil {
		return err
	}

	if err := exec.Command("ibmcloud", "cis", "dns-record-create", fmt.Sprintf("%s", domainID), "--type", "A", "--name", fmt.Sprintf("*.apps.%s", hcName), "--content", workerIP).Run(); err != nil {
		return err
	}

	return nil
}

func setupNMStateConfig(agents []agent, prefix int, gateway, hcName, hcCPNamespace, label string) error {
	for _, agent := range agents {
		templateConfig := map[string]string{
			"Name":      fmt.Sprintf("%s-%s", hcName, agent.powerVCPartitionName),
			"Namespace": hcCPNamespace,
			"Label":     label,
			"Prefix":    strconv.Itoa(prefix),
			"Gateway":   gateway,
			"MAC":       agent.mac,
			"IP":        agent.ip,
		}

		nmStateConfigTemplateFileAbsPath, err := getAbsoluteTemplatePath(nmStateConfigTemplateFile)
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

		file := fmt.Sprintf("nmstate-config-%s.yaml", agent.powerVCPartitionName)
		f, err := os.Create(file)
		if err != nil {
			return err
		}

		if _, err = f.Write(nmStateConfigData.Bytes()); err != nil {
			return err
		}

		if err = exec.Command("oc", "apply", "-f", file).Run(); err != nil {
			return err
		}
		logger.Infof("%s applied", file)
	}

	return nil
}

func setupInfraEnv(name, namespace, label, sshPubKeyFile string) error {
	sshPubKeyContent, err := os.ReadFile(sshPubKeyFile)
	if err != nil {
		return err
	}

	templateConfig := map[string]string{
		"Name":        name,
		"Namespace":   namespace,
		"Label":       label,
		"SSH_Pub_Key": string(sshPubKeyContent),
	}

	infraEnvTemplateFileAbsPath, err := getAbsoluteTemplatePath(infraEnvTemplateFile)
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

	f, err := os.Create(infraEnvFile)
	if err != nil {
		return err
	}

	if _, err = f.Write(infraEnvConfigData.Bytes()); err != nil {
		return err
	}

	if err = exec.Command("oc", "apply", "-f", infraEnvFile).Run(); err != nil {
		return err
	}

	logger.Infof("%s applied", infraEnvFile)
	return nil
}

func downloadISO(hcName, hcNamespace, downloadToFile string) error {
	imageAvailableCmd := exec.Command("oc", "wait", "--timeout=5m", "--for=condition=ImageCreated", "-n", hcNamespace, fmt.Sprintf("infraenv/%s", hcName))
	if err := imageAvailableCmd.Run(); err != nil {
		return err
	}

	cmd := exec.Command("oc", "get", "infraenv", hcName, "-n", hcNamespace, "-o", "json")
	out := strings.Builder{}
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return err
	}

	infraEnv := map[string]interface{}{}
	if err := json.Unmarshal([]byte(out.String()), &infraEnv); err != nil {
		return err
	}
	isoDownloadURL := infraEnv["status"].(map[string]interface{})["isoDownloadURL"].(string)

	if err := exec.Command("curl", isoDownloadURL, "--output", downloadToFile).Run(); err != nil {
		return err
	}

	return nil
}

func getLPARID(hmcSSHClient *ssh.Client, host, lparName string) (string, error) {
	lparIDCommand := fmt.Sprintf("lshwres -m %s -r virtualio --rsubtype scsi --filter \"lpar_names=%s\"", host, lparName)
	out, err := executeCommand(hmcSSHClient, lparIDCommand)
	if err != nil {
		return "", fmt.Errorf("error executing command to retrieve lpar_id %v", err)
	}
	for _, item := range strings.Split(out, ",") {
		if strings.Contains(item, "lpar_id") {
			fmt.Printf("\nlparIDCommand: %s\nout: %s\n", lparIDCommand, out)
			return strings.Split(item, "=")[1], nil
		}
	}

	return "", fmt.Errorf("not able to retrieve lpar_id command, output: %s", out)
}

func getVHOST(viosSSHClient *ssh.Client, lparID string) (string, error) {
	vhostCommand := fmt.Sprintf("ioscli lsmap -all -dec -cpid %s | awk 'NR==3{ print $1 }'", lparID)
	out, err := executeCommand(viosSSHClient, vhostCommand)
	if err != nil {
		return "", fmt.Errorf("error executing command to retrieve vhost: %v", err)
	}
	if out == "" {
		return "", fmt.Errorf("not able to retrieve vhost, command used: %s", vhostCommand)
	}

	fmt.Printf("vhostCommand: %s \nout: %s\n", vhostCommand, out)
	return out, nil
}

func createVOpt(viosSSHClient *ssh.Client, voptName, isoPath string) error {
	mkvoptCommand := fmt.Sprintf("ioscli mkvopt -name %s -file %s/%s", voptName, viosHomeDir, isoPath)
	out, err := executeCommand(viosSSHClient, mkvoptCommand)
	if err != nil {
		return fmt.Errorf("error executing command to create vopt: %v", err)
	}
	fmt.Printf("mkvoptCommand: %s\nout: %s\n", mkvoptCommand, out)
	return nil
}

func mapVOptToVTOpt(viosSSHClient *ssh.Client, vhost string, vopt string) error {
	mkvdevCommand := fmt.Sprintf("ioscli mkvdev -fbo -vadapter %s", vhost)
	out, err := executeCommand(viosSSHClient, mkvdevCommand)
	if err != nil {
		return fmt.Errorf("error executing command to create vopt %v", err)
	}
	fmt.Printf("mkvdevCommand: %s\nout: %s\n", mkvdevCommand, out)

	var vtopt string
	if strings.Contains(out, "Available") {
		vtopt = strings.Split(out, " ")[0]
	}
	if vtopt == "" {
		return fmt.Errorf("error retrieving available vtopt for vhost: %s, error: %v", vhost, err)
	}

	loadoptCommand := fmt.Sprintf("ioscli loadopt -vtd %s -disk %s", vtopt, vopt)
	if _, err = executeCommand(viosSSHClient, loadoptCommand); err != nil {
		return fmt.Errorf("error executing loadopt command: %v", err)
	}

	fmt.Printf("loadoptCommand: %s\n", loadoptCommand)

	return nil
}

func copyAndMountISO(agents []agent, infra powerInfra, isoPath string) error {
	hmcSSHClient, err := createSSHClient(infra.hmcIP, infra.hmcUserName, infra.hmcPassword)
	if err != nil {
		return fmt.Errorf("error create ssh client: %v", err)
	}

	viosSSHClient, err := createSSHClient(infra.viosIP, infra.viosUserName, infra.viosPassword)
	if err != nil {
		return fmt.Errorf("error create ssh client: %v", err)
	}

	if err = scpFile(viosSSHClient, isoPath, viosHomeDir); err != nil {
		return fmt.Errorf("error scp file: %v", err)
	}
	logger.Infof("scp file %s to vios done", isoPath)

	for _, agent := range agents {
		lparID, err := getLPARID(hmcSSHClient, infra.powerVC.host, agent.powerVCPartitionName)
		if err != nil {
			return fmt.Errorf("error retrieving lpar id for host: %s, error: %v", agent.powerVCPartitionName, err)
		}

		vhost, err := getVHOST(viosSSHClient, lparID)
		if err != nil {
			return fmt.Errorf("error retrieving vhost: %v", err)
		}
		voptName := fmt.Sprintf("%s-agent", agent.powerVCPartitionName)
		if err = createVOpt(viosSSHClient, voptName, isoPath); err != nil {
			return err
		}

		if err = mapVOptToVTOpt(viosSSHClient, vhost, voptName); err != nil {
			return fmt.Errorf("error map vtopt to vopt: %v", err)
		}
		logger.Infof("mounted iso on agent %s", agent.powerVCPartitionName)
	}

	return nil
}

func approveAgents(agents []agent, hcCPNamespace string) error {
	f := func() (bool, error) {
		var primaryAgentApproved, secondaryAgentApproved bool

		cmd := exec.Command("oc", "get", "agents", "-n", hcCPNamespace, "-o", "json")
		var out strings.Builder
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return false, err
		}
		var resp map[string]interface{}
		if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
			return false, fmt.Errorf("error unmarshal agent list resp: %v", err)
		}

		for _, a := range resp["items"].([]interface{}) {
			agent := a.(map[string]interface{})
			name := agent["metadata"].(map[string]interface{})["name"].(string)
			approved := agent["spec"].(map[string]interface{})["approved"].(bool)
			nwInterfaces := agent["status"].(map[string]interface{})["inventory"].(map[string]interface{})["interfaces"].([]interface{})

			for _, i := range nwInterfaces {
				nwIn := i.(map[string]interface{})
				if nwIn["macAddress"].(string) == agents[0].mac {
					if !approved {
						patchCont := fmt.Sprintf("{\"spec\":{\"approved\":true, \"hostname\": \"%s\"}}", agents[0].powerVCPartitionName)
						cmd = exec.Command("oc", "patch", "agent", name, "-n", hcCPNamespace, "-p", patchCont, "--type", "merge")
						if err := cmd.Run(); err != nil {
							return false, fmt.Errorf("error approving primary agent: %v", err)
						}
						logger.Infof("Approved primary agent %s", agents[0].powerVCPartitionName)
					}
					primaryAgentApproved = true
				}

				if nwIn["macAddress"].(string) == agents[1].mac {
					if !approved {
						patchCont := fmt.Sprintf("'{\"spec\":{\"approved\": true, \"hostname\": \"%s\"}}'", agents[1].powerVCPartitionName)
						cmd = exec.Command("oc", "patch", "agent", name, "-n", hcCPNamespace, "-p", patchCont, "--type", "merge")
						if err := cmd.Run(); err != nil {
							return false, fmt.Errorf("error approving secondary agent: %v", err)
						}
						logger.Infof("Approved secondary agent %s", agents[0].powerVCPartitionName)
					}
					secondaryAgentApproved = true
				}
			}
		}
		if !primaryAgentApproved || !secondaryAgentApproved {
			logger.Infof("still agents are not approved, primaryAgentApprovalStatus: %v, secondaryAgentApprovalStatus: %v", primaryAgentApproved, secondaryAgentApproved)
			return false, nil
		}

		return true, nil
	}
	if err := wait.PollImmediate(time.Minute*1, time.Minute*30, f); err != nil {
		return fmt.Errorf("error approving agents %v", err)
	}

	return nil
}

func scaleNodePool(hc hostedCluster) error {
	return exec.Command("oc", "scale", "np", hc.name, "-n", hc.namespace, "--replicas", "2").Run()
}

func monitorHC(hc hostedCluster) error {
	return exec.Command("oc", "wait", "hc", hc.name, "-n", hc.namespace, "--for=condition=Completed", "--timeout=30m").Run()
}

func setupPreReq(openStackClient *openStackClient, storageTemplate, networkName string) (string, string, string, int, error) {
	volumeID, err := openStackClient.setupEmptyBootVol(storageTemplate)
	if err != nil {
		logger.Errorf("error setup empty boot volume: %v", err)
		return "", "", "", -1, fmt.Errorf("error setup empty boot volume: %v", err)
	}
	logger.Infof("%s volume id will be used", volumeID)

	imageID, err := openStackClient.setupPreReqImage(volumeID)
	if err != nil {
		logger.Errorf("error setup image %v", err)
		return "", "", "", -1, fmt.Errorf("error setup image: %v", err)
	}
	logger.Infof("%s image id will be used", imageID)

	if err = openStackClient.SetupFlavor(); err != nil {
		logger.Errorf("error setup flavor: %v", err)
		return "", "", "", -1, fmt.Errorf("error setup flavor: %v", err)
	}
	logger.Infof("%s flavor id will be used", flavorID)

	networkID, gatewayIP, prefix, err := openStackClient.GetNetworkID(networkName)
	if err != nil {
		logger.Errorf("unable to retrieve id for network, error: %v", err)
		return "", "", "", -1, fmt.Errorf("unable to retrieve id for network, error: %v", err)
	}
	logger.Infof("%s network id will be used", networkID)

	return imageID, networkID, gatewayIP, prefix, nil
}

func setupCluster(client *openStackClient, infra powerInfra, hc hostedCluster, imageID, networkID, gatewayIP string, prefix int) error {
	agents, err := client.SetupAgents(infra.powerVC.host, imageID, infra.powerVC.networkName, networkID)
	if err != nil {
		logger.Errorf("error setup agents: %v", err)
		return fmt.Errorf("error setup agents: %v", err)
	}
	logger.Infof("agent setup done. agent details: %+v", agents)

	hcCPNamespace := fmt.Sprintf("%s-%s", hc.namespace, hc.name)

	nmStateLabel := fmt.Sprintf("label: nmstate-config-%s", hc.name)
	if err = setupHC(hc); err != nil {
		logger.Errorf("error setup hosted cluster: %v", err)
		return fmt.Errorf("error setup hosted cluster: %v", err)
	}
	logger.Info("hosted cluster setup done")
	if err = setupNMStateConfig(agents, prefix, gatewayIP, hc.name, hcCPNamespace, nmStateLabel); err != nil {
		logger.Errorf("error setup nmstate config: %v", err)
		return fmt.Errorf("error setup nmstate config: %v", err)
	}
	logger.Info("nmstate config setup done")

	if err = updateCISDNSRecords(hc.name, hcCPNamespace, hc.baseDomain, agents[0].ip); err != nil {
		logger.Errorf("error update cis dns records: %v", err)
		return fmt.Errorf("error update cis dns records: %v", err)
	}
	logger.Info("update cis dns records done")

	if err = setupInfraEnv(hc.name, hcCPNamespace, nmStateLabel, hc.sshPubKeyFile); err != nil {
		logger.Errorf("error setup infraenv: %v", err)
		return fmt.Errorf("error setup infraenv: %v", err)
	}
	logger.Info("infraenv setup done")

	isoPath := fmt.Sprintf("%s-%s", hc.name, discoveryISOFile)

	if err = downloadISO(hc.name, hcCPNamespace, isoPath); err != nil {
		logger.Errorf("error download iso: %v", err)
		return fmt.Errorf("error download iso: %v", err)
	}
	logger.Info("download discovery iso done")

	if err = copyAndMountISO(agents, infra, isoPath); err != nil {
		logger.Errorf("error copy iso: %v", err)
		return fmt.Errorf("error copy iso: %v", err)
	}
	logger.Info("mount iso on agents done")

	if err = client.restartAgents(agents); err != nil {
		return fmt.Errorf("error restarting vm: %v", err)
	}
	logger.Info("agents restarted")

	if err = approveAgents(agents, hcCPNamespace); err != nil {
		return fmt.Errorf("error approving agents: %v", err)
	}

	if err = scaleNodePool(hc); err != nil {
		return fmt.Errorf("error scaling nodepool: %v", err)
	}

	if err = monitorHC(hc); err != nil {
		return fmt.Errorf("error monitor hosted cluster to reach completed state: %v", err)
	}

	return nil
}

func e2e(client *openStackClient, infra powerInfra, hc hostedCluster) error {
	imageID, networkID, gatewayIP, prefix, err := setupPreReq(client, infra.powerVC.storageTemplate, infra.powerVC.networkName)
	if err != nil {
		logger.Errorf("error setup pre req: %v", err)
		return fmt.Errorf("error setup pre req: %v", err)
	}
	logger.Infof("retrieved prereq resource info imageID: %s, networkID: %s, gatewayIP: %s, prefix: %d", imageID, networkID, gatewayIP, prefix)

	if err = setupCluster(client, infra, hc, imageID, networkID, gatewayIP, prefix); err != nil {
		logger.Errorf("error setup cluster: %v", err)
		return fmt.Errorf("error setup cluster: %v", err)
	}
	logger.Info("setup cluster done")

	return nil
}
