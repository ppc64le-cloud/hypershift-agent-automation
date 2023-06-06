package main

import (
	"encoding/json"
	"fmt"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/hypervisors"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"k8s.io/apimachinery/pkg/util/wait"
	"strings"
	"time"

	"github.com/3th1nk/cidr"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/blockstorage/v2/volumes"
	"github.com/gophercloud/gophercloud/openstack/blockstorage/v3/volumetypes"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/openstack/imageservice/v2/imagedata"
	"github.com/gophercloud/gophercloud/openstack/imageservice/v2/images"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/subnets"
	"github.com/gophercloud/utils/openstack/clientconfig"
)

type openStackClient struct {
	computeClient *gophercloud.ServiceClient
	imageClient   *gophercloud.ServiceClient
	networkClient *gophercloud.ServiceClient
	volumeClient  *gophercloud.ServiceClient
}

var tags = []string{"purpose:hypershift-bm-agent-ci"}

func createOpenStackServiceClient() (*openStackClient, error) {
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

	return &openStackClient{
		imageClient:   imageClient,
		computeClient: computeClient,
		networkClient: networkClient,
		volumeClient:  volumeClient,
	}, nil
}

func (osc openStackClient) setupEmptyBootVol(storageTemplate string) (string, error) {
	var volumeTypeID string

	volumeTypeListPage, err := volumetypes.List(osc.volumeClient, volumetypes.ListOpts{}).AllPages()
	if err != nil {
		return "", err
	}

	volumeTypeList := volumeTypeListPage.GetBody().(map[string][]interface{})["volume_types"]
	for _, volumeType := range volumeTypeList {
		if volumeType.(map[string]interface{})["name"].(string) == storageTemplate {
			volumeTypeID = volumeType.(map[string]interface{})["id"].(string)
		}
	}

	volumeListPage, err := volumes.List(osc.volumeClient, volumes.ListOpts{Name: resourceName}).AllPages()
	if err != nil {
		return "", err
	}

	volumeList := volumeListPage.GetBody().(map[string][]interface{})["volumes"]
	for _, volume := range volumeList {
		if volume.(map[string]interface{})["name"].(string) == resourceName {
			return volume.(map[string]interface{})["id"].(string), nil
		}
	}

	options := volumes.CreateOpts{
		Name:       resourceName,
		Size:       diskSize,
		VolumeType: volumeTypeID,
		Metadata:   map[string]string{"is_image_volume": "True", "is_boot_volume": "True"},
	}

	volume, err := volumes.Create(osc.volumeClient, options).Extract()
	if err != nil {
		return "", err
	}

	return volume.ID, nil
}

func (osc openStackClient) setupPreReqImage(volumeID string) (string, error) {
	imageListResult, err := images.List(osc.imageClient, &images.ListOpts{Name: resourceName, Tags: tags}).AllPages()
	if err != nil {
		return "", err
	}

	isImageExist, err := imageListResult.IsEmpty()
	if err != nil {
		return "", err
	}
	if !isImageExist {
		logger.Infof("Image %s already exist\n", resourceName)
		imageList := imageListResult.GetBody().(map[string][]interface{})["images"]
		return imageList[0].(map[string]interface{})["id"].(string), nil
	}

	visibility := images.ImageVisibilityPrivate
	blockDeviceMapping := fmt.Sprintf("[{\"guest_format\":null,\"boot_index\":0,\"no_device\":null,\"image_id\":null,\"volume_id\":\"%v\",\"disk_bus\":null,\"volume_size\":null,\"source_type\":\"volume\",\"device_type\":\"disk\",\"snapshot_id\":null,\"destination_type\":\"volume\",\"delete_on_termination\":true}]", volumeID)
	createOpts := images.CreateOpts{
		Name:            resourceName,
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

	image, err := images.Create(osc.imageClient, createOpts).Extract()
	if err != nil {
		return "", err
	}

	err = imagedata.Upload(osc.imageClient, image.ID, strings.NewReader("")).Err

	return image.ID, err
}

func (osc openStackClient) SetupFlavor() error {
	if err := flavors.Get(osc.computeClient, flavorID).Err; err == nil {
		return nil
	}

	disk := 0
	flavorCreateOpts := flavors.CreateOpts{
		Name:  resourceName,
		RAM:   16384,
		VCPUs: 1,
		Disk:  &disk,
		ID:    flavorID,
	}
	if err := flavors.Create(osc.computeClient, flavorCreateOpts).Err; err != nil {
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
	err := flavors.CreateExtraSpecs(osc.computeClient, flavorID, extraSpecs).Err

	return err
}

func (osc openStackClient) GetNetworkID(networkName string) (string, string, int, error) {
	networkPages, err := networks.List(osc.networkClient, networks.ListOpts{Name: networkName}).AllPages()
	if err != nil {
		return "", "", 0, err
	}
	networkList := networkPages.GetBody().(map[string][]interface{})["networks"]
	if len(networkList) < 1 {
		return "", "", 0, fmt.Errorf("network %s not exist", networkName)
	}
	network := networkList[0].(map[string]interface{})

	subnetPages, err := subnets.List(osc.networkClient, subnets.ListOpts{NetworkID: network["id"].(string)}).AllPages()
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

	c, err := cidr.Parse(subnetL[0].CIDR)
	if err != nil {
		return "", "", 0, err
	}
	ones, _ := c.MaskSize()

	return network["id"].(string), subnetL[0].GatewayIP, ones, nil
}

func (osc openStackClient) GetHypervisorHostMTMS(hostDisplayName string) (string, error) {
	hypervisorsListPages, err := hypervisors.List(osc.computeClient, hypervisors.ListOpts{}).AllPages()
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

func (osc openStackClient) SetupAgents(host, imageID, networkName, networkID string) ([]agent, error) {

	hostMTMS, err := osc.GetHypervisorHostMTMS(host)
	if err != nil {
		return nil, fmt.Errorf("error retrieving mtms for the host %s %v", host, err)
	}
	createOpts := servers.CreateOpts{
		Name:             resourceName,
		ImageRef:         imageID,
		AvailabilityZone: fmt.Sprintf(":%s", hostMTMS),
		FlavorRef:        flavorID,
		Networks:         []servers.Network{{UUID: networkID}},
		Metadata:         map[string]string{"primary_network": networkID},
		Min:              2,
	}

	p, _ := json.Marshal(createOpts)
	logger.Infof("create server with payload %s", string(p))

	if err = servers.Create(osc.computeClient, createOpts).Err; err != nil {
		return nil, err
	}

	serverPages, err := servers.List(osc.computeClient, servers.ListOpts{Name: resourceName}).AllPages()
	if err != nil {
		return nil, err
	}

	serverList := serverPages.GetBody().(map[string][]interface{})["servers"]
	if len(serverList) != 2 {
		return nil, fmt.Errorf("list servers with name filter did not return 2 servers")
	}

	monitorServer := func(id string) (map[string]interface{}, error) {
		var server map[string]interface{}
		f := func() (bool, error) {
			server = servers.Get(osc.computeClient, id).Body.(map[string]interface{})["server"].(map[string]interface{})
			if err != nil {
				return false, err
			}
			currentState := server["OS-EXT-STS:vm_state"].(string)
			logger.Infof("waiting for agent to reach active state, current state: %s", currentState)
			if currentState == "active" {
				return true, nil
			}

			if currentState == "failed" {
				details, _ := json.Marshal(server)
				return false, fmt.Errorf("agent %s is in failed state, details %v", server["name"].(string), string(details))
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

	var agentList []agent
	for _, server := range serverL {
		partitionName := server["OS-EXT-SRV-ATTR:instance_name"].(string)

		id := server["id"].(string)
		addr := server["addresses"].(map[string]interface{})[networkName].([]interface{})[0].(map[string]interface{})
		mac := addr["OS-EXT-IPS-MAC:mac_addr"].(string)
		ip := addr["addr"].(string)

		agentList = append(agentList, agent{powerVCID: id, powerVCPartitionName: partitionName, ip: ip, mac: mac})
	}

	return agentList, nil
}

func (osc openStackClient) restartAgents(agents []agent) error {
	for _, agent := range agents {
		if err := servers.Reboot(osc.computeClient, agent.powerVCID, servers.RebootOpts{Type: servers.SoftReboot}).Err; err != nil {
			return fmt.Errorf("error rebooting agent %s: %v ", agent.powerVCPartitionName)
		}
		logger.Infof("rebooted %s", agent.powerVCPartitionName)
	}

	return nil
}
