package powervc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/3th1nk/cidr"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/blockstorage/v2/volumes"
	"github.com/gophercloud/gophercloud/openstack/blockstorage/v3/volumetypes"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/hypervisors"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/imageservice/v2/imagedata"
	"github.com/gophercloud/gophercloud/openstack/imageservice/v2/images"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/subnets"
	"github.com/gophercloud/utils/openstack/clientconfig"

	"github.com/ppc64le-cloud/hypershift-agent-automation/log"
	"github.com/ppc64le-cloud/hypershift-agent-automation/util"
)

type Client struct {
	computeClient   *gophercloud.ServiceClient
	imageClient     *gophercloud.ServiceClient
	networkClient   *gophercloud.ServiceClient
	volumeClient    *gophercloud.ServiceClient
	StorageTemplate string
	NetworkName     string
	Host            string
}

type Agent struct {
	ID            string
	Name          string
	PartitionName string
	IP            string
	MAC           string
}

const (
	DiskSize = 120
)

var tags = []string{"purpose:hypershift-bm-agent-ci"}

var VolumeNotFound = func(name string) error { return fmt.Errorf("volume not found by the name %s", name) }
var ImageNotFound = func(name string) error { return fmt.Errorf("image not found by the name %s", name) }

func LoadEnv(c *Client) error {
	var set bool
	var errs []error

	c.StorageTemplate, set = os.LookupEnv("POWERVC_STORAGE_TEMPLATE")
	if !set {
		errs = append(errs, fmt.Errorf("POWERVC_STORAGE_TEMPLATE env var not set"))
	}
	c.NetworkName, set = os.LookupEnv("POWERVC_NETWORK_NAME")
	if !set {
		errs = append(errs, fmt.Errorf("POWERVC_NETWORK_NAME env var not set"))
	}
	c.Host, set = os.LookupEnv("POWERVC_HOST")
	if !set {
		errs = append(errs, fmt.Errorf("POWERVC_HOST env var not set"))
	}

	return errors.Join(errs...)
}

func NewClient() (*Client, error) {
	options := &clientconfig.ClientOpts{}
	imageClient, err := clientconfig.NewServiceClient("image", options)
	if err != nil {
		return nil, err
	}

	computeClient, err := clientconfig.NewServiceClient("compute", options)
	if err != nil {
		return nil, err
	}

	networkClient, err := clientconfig.NewServiceClient("network", options)
	if err != nil {
		return nil, err
	}

	volumeClient, err := clientconfig.NewServiceClient("volume", options)
	if err != nil {
		return nil, err
	}

	c := &Client{
		imageClient:   imageClient,
		computeClient: computeClient,
		networkClient: networkClient,
		volumeClient:  volumeClient,
	}

	if err = LoadEnv(c); err != nil {
		return nil, fmt.Errorf("error loading env var of powervc: %v", err)
	}

	return c, nil
}

func (c Client) GetVolumeIDByName(name string) (string, error) {
	volumeListPage, err := volumes.List(c.volumeClient, volumes.ListOpts{Name: name}).AllPages()
	if err != nil {
		return "", err
	}

	volumeList := volumeListPage.GetBody().(map[string][]interface{})["volumes"]
	for _, volume := range volumeList {
		if volume.(map[string]interface{})["name"].(string) == name {
			return volume.(map[string]interface{})["id"].(string), nil
		}
	}

	return "", VolumeNotFound(name)
}

func (c Client) SetupEmptyBootVol(clusterName string) (string, error) {
	var volumeTypeID string

	volumeTypeListPage, err := volumetypes.List(c.volumeClient, volumetypes.ListOpts{}).AllPages()
	if err != nil {
		return "", err
	}

	volumeTypeList := volumeTypeListPage.GetBody().(map[string][]interface{})["volume_types"]
	for _, volumeType := range volumeTypeList {
		if volumeType.(map[string]interface{})["name"].(string) == c.StorageTemplate {
			volumeTypeID = volumeType.(map[string]interface{})["id"].(string)
		}
	}
	volumeID, err := c.GetVolumeIDByName(clusterName)
	if err != nil && err.Error() != VolumeNotFound(clusterName).Error() {
		return "", fmt.Errorf("error checking volume before creation: %v", err)
	}
	if volumeID != "" {
		return volumeID, nil
	}

	options := volumes.CreateOpts{
		Name:       clusterName,
		Size:       DiskSize,
		VolumeType: volumeTypeID,
		Metadata:   map[string]string{"is_image_volume": "True", "is_boot_volume": "True"},
	}

	volume, err := volumes.Create(c.volumeClient, options).Extract()
	if err != nil {
		return "", err
	}

	return volume.ID, nil
}

func (c Client) CleanUpBootVolume(clusterName string) error {
	volumeID, err := c.GetVolumeIDByName(clusterName)
	if err != nil && err.Error() != VolumeNotFound(clusterName).Error() {
		return fmt.Errorf("error checking volume before creation: %v", err)
	}
	if volumeID == "" {
		log.Logger.Infof("volume %s is cleaned", clusterName)
		return nil
	}

	if res := volumes.Delete(c.volumeClient, volumeID, nil); res.Err != nil {
		return fmt.Errorf("error deleting volume %s: %v", clusterName, res.PrettyPrintJSON())
	}

	return nil
}

func (c Client) GetImageIDByName(name string) (string, error) {
	imageListResult, err := images.List(c.imageClient, &images.ListOpts{Name: name, Tags: tags}).AllPages()
	if err != nil {
		return "", err
	}

	isImageExist, err := imageListResult.IsEmpty()
	if err != nil {
		return "", err
	}
	if !isImageExist {
		imageList := imageListResult.GetBody().(map[string][]interface{})["images"]
		return imageList[0].(map[string]interface{})["id"].(string), nil
	}

	return "", ImageNotFound(name)
}

func (c Client) CleanUpBootImage(clusterName string) error {
	imageID, err := c.GetImageIDByName(clusterName)
	if err != nil && ImageNotFound(clusterName).Error() != err.Error() {
		return fmt.Errorf("error getting imageID : %v", err)
	}
	if imageID == "" {
		log.Logger.Infof("image %s cleaned up", clusterName)
		return nil
	}
	if res := images.Delete(c.imageClient, imageID); res.Err != nil {
		return fmt.Errorf("error deleting image %s: %v", clusterName, res.PrettyPrintJSON())
	}

	return nil
}

func (c Client) SetupPreReqImage(clusterName, volumeID string) (string, error) {
	imageID, err := c.GetImageIDByName(clusterName)
	if err != nil && ImageNotFound(clusterName).Error() != err.Error() {
		return "", fmt.Errorf("error checking image before creation: %v", err)
	}
	if imageID != "" {
		log.Logger.Infof("Image %s already exist", clusterName)
		return imageID, nil
	}

	visibility := images.ImageVisibilityPrivate
	blockDeviceMapping := fmt.Sprintf("[{\"guest_format\":null,\"boot_index\":0,\"no_device\":null,\"image_id\":null,\"volume_id\":\"%v\",\"disk_bus\":null,\"volume_size\":null,\"source_type\":\"volume\",\"device_type\":\"disk\",\"snapshot_id\":null,\"destination_type\":\"volume\",\"delete_on_termination\":true}]", volumeID)
	createOpts := images.CreateOpts{
		Name:            clusterName,
		ContainerFormat: "bare",
		Visibility:      &visibility,
		DiskFormat:      "raw",
		MinDisk:         1,
		Tags:            tags,
		Properties: map[string]string{
			"os_distro":            "coreos",
			"endianness":           "little-endian",
			"architecture":         "ppc64",
			"hypervisor_type":      "phyp",
			"root_device_name":     "/dev/sda",
			"block_device_mapping": blockDeviceMapping,
			"bdm_v2":               "true",
		},
	}

	image, err := images.Create(c.imageClient, createOpts).Extract()
	if err != nil {
		return "", err
	}

	err = imagedata.Upload(c.imageClient, image.ID, strings.NewReader("")).Err

	return image.ID, err
}

func (c Client) CleanUpFlavor(clusterName string) error {
	flavorID := util.GenerateFlavourID(clusterName)
	if err := flavors.Delete(c.computeClient, flavorID).Err; err != nil {
		return fmt.Errorf("error deleting flavorID %s: %v", flavorID, err)
	}

	return nil
}

func (c Client) SetupFlavor(clusterName string) error {
	flavorID := util.GenerateFlavourID(clusterName)
	res := flavors.Get(c.computeClient, flavorID)
	if res.Err == nil {
		return nil
	}

	disk := 0
	flavorCreateOpts := flavors.CreateOpts{
		Name:  clusterName,
		RAM:   16384,
		VCPUs: 1,
		Disk:  &disk,
		ID:    flavorID,
	}
	if err := flavors.Create(c.computeClient, flavorCreateOpts).Err; err != nil {
		return err
	}

	extraSpecs := flavors.ExtraSpecsOpts{
		"powervm:processor_compatibility": "default",
		"powervm:srr_capability":          "false",
		"powervm:min_vcpu":                "1",
		"powervm:max_vcpu":                "1",
		"powervm:min_mem":                 "4096",
		"powervm:max_mem":                 "16384",
		"powervm:availability_priority":   "127",
		"powervm:enable_lpar_metric":      "false",
		"powervm:enforce_affinity_check":  "false",
		"powervm:secure_boot":             "0",
		"powervm:proc_units":              "0.5",
		"powervm:min_proc_units":          "0.5",
		"powervm:max_proc_units":          "1",
		"powervm:dedicated_proc":          "false",
		"powervm:shared_proc_pool_name":   "DefaultPool",
		"powervm:uncapped":                "true",
		"powervm:shared_weight":           "128",
		"powervm:ame_expansion_factor":    "0",
	}
	err := flavors.CreateExtraSpecs(c.computeClient, flavorID, extraSpecs).Err

	return err
}

func (c Client) GetNetworkID() (string, string, int, error) {
	networkPages, err := networks.List(c.networkClient, networks.ListOpts{Name: c.NetworkName}).AllPages()
	if err != nil {
		return "", "", 0, err
	}
	networkList := networkPages.GetBody().(map[string][]interface{})["networks"]
	if len(networkList) < 1 {
		return "", "", 0, fmt.Errorf("network %s not exist", c.NetworkName)
	}
	network := networkList[0].(map[string]interface{})

	subnetPages, err := subnets.List(c.networkClient, subnets.ListOpts{NetworkID: network["id"].(string)}).AllPages()
	if err != nil {
		return "", "", 0, err
	}

	subnetL, err := subnets.ExtractSubnets(subnetPages)
	if err != nil {
		return "", "", 0, err
	}
	if len(subnetL) < 0 {
		return "", "", 0, fmt.Errorf("network does not contain any subnets")
	}

	cidrParsed, err := cidr.Parse(subnetL[0].CIDR)
	if err != nil {
		return "", "", 0, err
	}
	ones, _ := cidrParsed.MaskSize()

	return network["id"].(string), subnetL[0].GatewayIP, ones, nil
}

func (c Client) GetHypervisorHostMTMS(hostDisplayName string) (string, error) {
	hypervisorsListPages, err := hypervisors.List(c.computeClient, hypervisors.ListOpts{}).AllPages()
	if err != nil {
		return "", err
	}

	hypervisorsList := hypervisorsListPages.GetBody().(map[string]interface{})["hypervisors"].([]interface{})
	var hypHostName string
	for _, hv := range hypervisorsList {
		hypervisor := hv.(map[string]interface{})
		if hypervisor["service"].(map[string]interface{})["host_display_name"].(string) == hostDisplayName {
			hypHostName = hypervisor["hypervisor_hostname"].(string)
		}
	}

	if hypHostName == "" {
		return "", fmt.Errorf("no host found with name %s", hostDisplayName)
	}
	return hypHostName, nil
}

func (c Client) CreateServer(clusterName, host, imageID, networkID, flavorRef string, nodeCount int) error {
	hostMTMS, err := c.GetHypervisorHostMTMS(host)
	if err != nil {
		return fmt.Errorf("error retrieving mtms for the host %s %v", host, err)
	}
	createOpts := servers.CreateOpts{
		Name:             clusterName,
		ImageRef:         imageID,
		AvailabilityZone: fmt.Sprintf(":%s", hostMTMS),
		FlavorRef:        flavorRef,
		Networks:         []servers.Network{{UUID: networkID}},
		Metadata:         map[string]string{"primary_network": networkID},
		Min:              nodeCount,
	}

	p, _ := json.Marshal(createOpts)
	log.Logger.Infof("create server with payload %s", string(p))

	return servers.Create(c.computeClient, createOpts).Err
}

func (c Client) SetupAgents(workerName, imageID, networkID, flavorRef string, nodeCount int) ([]Agent, error) {

	serverPages, err := servers.List(c.computeClient, servers.ListOpts{Name: workerName}).AllPages()
	if err != nil {
		return nil, err
	}

	serverList := serverPages.GetBody().(map[string][]interface{})["servers"]
	if len(serverList) < 1 {
		if err = c.CreateServer(workerName, c.Host, imageID, networkID, flavorRef, nodeCount); err != nil {
			return nil, err
		}

		serverPages, err = servers.List(c.computeClient, servers.ListOpts{Name: workerName}).AllPages()
		if err != nil {
			return nil, err
		}

		serverList = serverPages.GetBody().(map[string][]interface{})["servers"]
	}

	monitorServer := func(id string) (map[string]interface{}, error) {
		var server map[string]interface{}
		f := func() (bool, error) {
			server = servers.Get(c.computeClient, id).Body.(map[string]interface{})["server"].(map[string]interface{})
			if err != nil {
				return false, err
			}
			currentState := server["OS-EXT-STS:vm_state"].(string)
			log.Logger.Infof("waiting for agent to reach active state, current state: %s", currentState)
			if currentState == "active" {
				return true, nil
			}

			if currentState == "failed" || currentState == "error" {
				details, _ := json.Marshal(server)
				return false, fmt.Errorf("agent %s is in failed or error state, details %v", server["name"].(string), string(details))
			}

			return false, nil
		}

		err = wait.PollImmediate(time.Second*30, time.Minute*10, f)
		return server, err
	}

	var serverL []map[string]interface{}
	for _, s := range serverList {
		server, err := monitorServer(s.(map[string]interface{})["id"].(string))
		if err != nil {
			return nil, err
		}
		serverL = append(serverL, server)
	}

	var agentList []Agent
	for _, server := range serverL {
		name := server["name"].(string)
		partitionName := server["OS-EXT-SRV-ATTR:instance_name"].(string)
		id := server["id"].(string)
		addr := server["addresses"].(map[string]interface{})[c.NetworkName].([]interface{})[0].(map[string]interface{})
		mac := addr["OS-EXT-IPS-MAC:mac_addr"].(string)
		ip := addr["addr"].(string)

		agentList = append(agentList, Agent{ID: id, Name: name, PartitionName: partitionName, IP: ip, MAC: mac})
	}

	return agentList, nil
}

func (c Client) RestartAgents(agents []Agent) error {
	for _, agent := range agents {
		//time.Sleep(time.Minute * 2)
		if err := servers.Reboot(c.computeClient, agent.ID, servers.RebootOpts{Type: servers.SoftReboot}).Err; err != nil {
			return fmt.Errorf("error rebooting agent %s: %v ", agent.PartitionName, err)
		}
		log.Logger.Infof("rebooted %s", agent.PartitionName)
	}

	return nil
}

func (c Client) DestroyAgents(agentName string) error {
	serverPages, err := servers.List(c.computeClient, servers.ListOpts{Name: agentName}).AllPages()
	if err != nil {
		return err
	}
	serverList := serverPages.GetBody().(map[string][]interface{})["servers"]

	for _, s := range serverList {
		serverID := s.(map[string]interface{})["id"].(string)
		log.Logger.Infof("deleting %s", s.(map[string]interface{})["name"].(string))
		if err = servers.Delete(c.computeClient, serverID).Err; err != nil {
			return err
		}
	}

	return nil
}
