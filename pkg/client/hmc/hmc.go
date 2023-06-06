package hmc

import (
	"errors"
	"fmt"
	"golang.org/x/crypto/ssh"
	"os"
	"strings"

	"github.com/ppc64le-cloud/hypershift-agent-automation/util"
)

type VIOS struct {
	SSHClient *ssh.Client
	IP        string
	UserName  string
	Password  string
	HomeDir   string
}

type Client struct {
	SSHClient *ssh.Client
	VIOS      *VIOS
	IP        string
	UserName  string
	Password  string
}

func LoadEnv(c *Client) error {
	var set bool
	var errs []error

	c.IP, set = os.LookupEnv("HMC_IP")
	if !set {
		errs = append(errs, fmt.Errorf("HMC_IP env var not set"))
	}
	c.UserName, set = os.LookupEnv("HMC_USERNAME")
	if !set {
		errs = append(errs, fmt.Errorf("HMC_USERNAME env var not set"))
	}
	c.Password, set = os.LookupEnv("HMC_PASSWORD")
	if !set {
		errs = append(errs, fmt.Errorf("HMC_PASSWORD env var not set"))
	}
	c.VIOS.IP, set = os.LookupEnv("VIOS_IP")
	if !set {
		errs = append(errs, fmt.Errorf("VIOS_IP env var not set"))
	}
	c.VIOS.UserName, set = os.LookupEnv("VIOS_USERNAME")
	if !set {
		errs = append(errs, fmt.Errorf("VIOS_USERNAME env var not set"))
	}
	c.VIOS.Password, set = os.LookupEnv("VIOS_PASSWORD")
	if !set {
		errs = append(errs, fmt.Errorf("VIOS_PASSWORD env var not set"))
	}
	c.VIOS.HomeDir, set = os.LookupEnv("VIOS_HOMEDIR")
	if !set {
		errs = append(errs, fmt.Errorf("VIOS_HOMEDIR env var not set"))
	}

	return errors.Join(errs...)
}

func NewClient() (*Client, error) {
	c := &Client{VIOS: &VIOS{}}
	if err := LoadEnv(c); err != nil {
		return nil, fmt.Errorf("error reading env for hmc client: %v", err)
	}

	var err error
	c.SSHClient, err = util.CreateSSHClient(c.IP, c.UserName, c.Password)
	if err != nil {
		return nil, fmt.Errorf("error create hmc ssh client: %v", err)
	}

	c.VIOS.SSHClient, err = util.CreateSSHClient(c.VIOS.IP, c.VIOS.UserName, c.VIOS.Password)
	if err != nil {
		return nil, fmt.Errorf("error create vios ssh client: %v", err)
	}

	return c, nil
}

func (hmc Client) GetLPARID(host, lparName string) (string, error) {
	lparIDCommand := fmt.Sprintf("lshwres -m %s -r virtualio --rsubtype scsi --filter \"lpar_names=%s\"", host, lparName)
	out, _, err := util.ExecuteRemoteCommand(hmc.SSHClient, lparIDCommand)
	if err != nil {
		return "", fmt.Errorf("error executing command to retrieve lpar_id %v", err)
	}
	for _, item := range strings.Split(out, ",") {
		if strings.Contains(item, "lpar_id") {
			return strings.Split(item, "=")[1], nil
		}
	}

	return "", fmt.Errorf("not able to retrieve lpar_id command, output: %s", out)
}

func (hmc Client) GetVHOST(lparID string) (string, error) {
	vhostCommand := fmt.Sprintf("ioscli lsmap -all -dec -cpid %s | awk 'NR==3{ print $1 }'", lparID)
	out, _, err := util.ExecuteRemoteCommand(hmc.VIOS.SSHClient, vhostCommand)
	if err != nil {
		return "", fmt.Errorf("error executing command to retrieve vhost: %v", err)
	}
	if out == "" {
		return "", fmt.Errorf("not able to retrieve vhost, command used: %s", vhostCommand)
	}

	return out, nil
}

func (hmc Client) CreateVOpt(voptName, isoPath string) error {
	mkvoptCommand := fmt.Sprintf("ioscli mkvopt -name %s -file %s/%s", voptName, hmc.VIOS.HomeDir, isoPath)
	_, e, err := util.ExecuteRemoteCommand(hmc.VIOS.SSHClient, mkvoptCommand)
	if err != nil || e != "" {
		return fmt.Errorf("error executing command to create vopt: %v, e: %s", err, e)
	}
	return nil
}

func (hmc Client) MapVOptToVTOpt(vhost string, vopt string) error {
	mkvdevCommand := fmt.Sprintf("ioscli mkvdev -fbo -vadapter %s", vhost)
	out, _, err := util.ExecuteRemoteCommand(hmc.VIOS.SSHClient, mkvdevCommand)
	if err != nil {
		return fmt.Errorf("error executing command to create vopt %v", err)
	}

	var vtopt string
	if strings.Contains(out, "Available") {
		vtopt = strings.Split(out, " ")[0]
	}
	if vtopt == "" {
		return fmt.Errorf("error retrieving available vtopt for vhost: %s, error: %v", vhost, err)
	}

	loadoptCommand := fmt.Sprintf("ioscli loadopt -vtd %s -disk %s", vtopt, vopt)
	if _, _, err = util.ExecuteRemoteCommand(hmc.VIOS.SSHClient, loadoptCommand); err != nil {
		return fmt.Errorf("error executing loadopt command: %v", err)
	}

	return nil
}

func (hmc Client) SetupBootString(host, partitionName string) error {
	chsyscfgcmd := fmt.Sprintf("chsyscfg -r lpar -m %s -i name=%s,boot_string=/vdevice/v-scsi@30000002/disk@8200000000000000", host, partitionName)
	_, e, err := util.ExecuteRemoteCommand(hmc.SSHClient, chsyscfgcmd)
	if err != nil || e != "" {
		return fmt.Errorf("error executing command to configuring boot_string: %v, e: %s", err, e)
	}
	return nil
}
