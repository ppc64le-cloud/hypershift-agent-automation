package util

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/bramvdbogaerde/go-scp"
	"github.com/ppc64le-cloud/hypershift-agent-automation/log"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"os"
	"os/exec"
	"strings"
)

var GetManifestDir = func(hcName string) string { return fmt.Sprintf(".%s", hcName) }
var KubeConfigFile = func(hcName string) string { return fmt.Sprintf("%s/kubeconfig", GetManifestDir(hcName)) }
var GenerateFlavourID = func(hcName string) string { return fmt.Sprintf("%s-flavor", hcName) }

func CreateSSHClient(host, username, password string) (*ssh.Client, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	hostKeyCallback, err := knownhosts.New(fmt.Sprintf("%s/.ssh/known_hosts", homeDir))
	if err != nil {
		return nil, err
	}

	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: hostKeyCallback,
	}
	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:22", host), config)
	if err != nil {
		return nil, fmt.Errorf("error establishing ssh connection to %s %v", host, err)
	}

	return conn, nil
}

func ExecuteRemoteCommand(client *ssh.Client, command string) (string, string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("error creating ssh session %v", err)
	}
	var outBuff, errBuff bytes.Buffer
	session.Stdout = &outBuff
	session.Stderr = &errBuff

	if err = session.Run(command); err != nil {
		return "", "", fmt.Errorf("error running command %s, %v", command, err)
	}

	return outBuff.String(), errBuff.String(), nil
}

func SCPFile(sshClient *ssh.Client, file, srcDir, destDir string) error {
	client, err := scp.NewClientBySSH(sshClient)
	if err != nil {
		return fmt.Errorf("error creating new ssh session from existing connection: %v", err)
	}
	srcFilePath := fmt.Sprintf("%s/%s", srcDir, file)
	f, err := os.Open(srcFilePath)
	if err != nil {
		return fmt.Errorf("error opening source file: %v", err)
	}
	defer client.Close()
	log.Logger.Infof("scp file %s started", srcFilePath)
	return client.CopyFromFile(context.Background(), *f, fmt.Sprintf("%s/%s", destDir, file), "0644")
}

func ExecuteCommand(command string, args []string) (string, string, error) {
	cmd := exec.Command(command, args...)

	var out strings.Builder
	var e strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &e
	cmd.Env = append(os.Environ())

	err := cmd.Run()

	return out.String(), e.String(), err
}

func GetAbsoluteTemplatePath(templateName string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/templates/%s", cwd, templateName), nil
}

func GetCISDomainID(baseDomain string) (string, error) {
	args := []string{"cis", "domains", "--output", "json"}
	out, e, err := ExecuteCommand("ibmcloud", args)
	if err != nil || e != "" {
		return "", fmt.Errorf("error listing ibmcloud domains: %v, e: %v", err, e)
	}

	domainList := make([]map[string]interface{}, 0)
	if err = json.Unmarshal([]byte(out), &domainList); err != nil {
		return "", err
	}
	if len(domainList) < 0 {
		return "", fmt.Errorf("%s domain not exist", baseDomain)
	}

	var domainID string
	for _, domain := range domainList {
		domainName := domain["name"].(string)
		if domainName == baseDomain {
			domainID = domain["id"].(string)
		}
	}

	return domainID, nil
}
