package ibmcloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/ppc64le-cloud/hypershift-agent-automation/log"
	"github.com/ppc64le-cloud/hypershift-agent-automation/util"
)

type Client struct {
	CISDomain   string
	CISDomainID string
}

var DNSRecordNotExist = func(name string) error { return fmt.Errorf("no dns record exists with name: %s", name) }

func LoadEnv(c *Client) error {
	var set bool
	var errs []error

	_, set = os.LookupEnv("IBMCLOUD_API_KEY")
	if !set {
		errs = append(errs, fmt.Errorf("IBMCLOUD_API_KEY env var not set"))
	}

	c.CISDomain, set = os.LookupEnv("BASE_DOMAIN")
	if !set {
		errs = append(errs, fmt.Errorf("BASE_DOMAIN env var not set"))
	}

	return errors.Join(errs...)
}

func NewClient() (*Client, error) {
	c := &Client{}
	var err error
	if err = LoadEnv(c); err != nil {
		return nil, fmt.Errorf("error reading env for ibmcloud client: %v", err)
	}

	var out, e string
	if out, e, err = util.ExecuteCommand("ibmcloud", []string{"login", "--no-region", "--quiet"}); err != nil || e != "" {
		return nil, fmt.Errorf("error login ibmcloud cli, out: %v, e: %v, err: %v", out, e, err)
	}
	log.Logger.Infof("out: %v, e: %v, err: %v", out, e, err)

	c.CISDomainID, err = util.GetCISDomainID(c.CISDomain)
	if err != nil {
		return nil, fmt.Errorf("error retrieve cis domain id: %v", err)
	}

	return c, nil
}

func (c Client) CreateDNSRecord(rType, name, content string) error {
	args := []string{"cis", "dns-record-create", c.CISDomainID, "--type", rType, "--name", name, "--content", content}
	_, e, err := util.ExecuteCommand("ibmcloud", args)
	if err != nil || e != "" {
		return fmt.Errorf("error creating dns record, e: %v, err: %v", e, err)
	}

	return nil
}

func (c Client) UpdateDNSRecord(dnsRecordID, content string) error {
	args := []string{"cis", "dns-record-update", c.CISDomainID, dnsRecordID, "--content", content}
	_, e, err := util.ExecuteCommand("ibmcloud", args)
	if err != nil || e != "" {
		return fmt.Errorf("error updating dns record, e: %v, err: %v", e, err)
	}

	return nil
}

func (c Client) DeleteDNSRecord(dnsRecordID string) error {
	args := []string{"cis", "dns-record-delete", c.CISDomainID, dnsRecordID}
	_, e, err := util.ExecuteCommand("ibmcloud", args)
	if err != nil || e != "" {
		return fmt.Errorf("error deleting dns record, e: %v, err: %v", e, err)
	}

	return nil
}

func (c Client) GetDNSRecordID(name string) (string, error) {
	args := []string{"cis", "dns-records", c.CISDomainID, "--name", fmt.Sprintf("%s.%s", name, c.CISDomain), "--output", "JSON"}
	out, e, err := util.ExecuteCommand("ibmcloud", args)
	if err != nil || e != "" {
		return "", fmt.Errorf("error retrieving dns record, e: %v, err: %v", e, err)
	}
	dnsRecords := make([]map[string]interface{}, 0)
	if err = json.Unmarshal([]byte(out), &dnsRecords); err != nil {
		return "", err
	}
	var dnsRecordID string
	if len(dnsRecords) > 0 {
		dnsRecordID = dnsRecords[0]["id"].(string)
	} else {
		return "", DNSRecordNotExist(name)
	}

	return dnsRecordID, nil
}
