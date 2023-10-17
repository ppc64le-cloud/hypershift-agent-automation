# Hypershift Agent Automation

Cli tool to create and destroy agent based hypershift hosted cluster on PowerVC platform.

## Setup:
This tool is designed only to create and destroy the agent cluster. 

### Pre-Req:
- **Management Cluster** - An OCP cluster which will be used to host the agent based hosted cluster. Only x86 type of management cluster is supported as of now. Need to set up below things in management cluster.
  - **MCE** - Refer this [link](https://github.com/hypershift-on-power/hack/wiki/Agent-based-Hosted-Cluster-on-PowerVM-using-MCE-with-Assisted-Service-and-Hypershift#install-the-mce-operator) to install the MCE operator.
  - **AgentServiceConfig** - Refer this [link](https://github.com/hypershift-on-power/hack/wiki/Agent-based-Hosted-Cluster-on-PowerVM-using-MCE-with-Assisted-Service-and-Hypershift#create-agentserviceconfig) to configure agentserviceconfig.
- A valid [pull secret](https://cloud.redhat.com/openshift/install/aws/installer-provisioned) file.
- The OpenShift CLI (oc) or Kubernetes CLI (kubectl).
- **Hypershift**
```shell
  git clone https://github.com/openshift/hypershift.git
  cd hypershift
  make build
  sudo install -m 0755 bin/hypershift /usr/local/bin/hypershift
```
- **Hypershift Agent Automation**
```shell
  git clone https://github.com/ppc64le-cloud/hypershift-agent-automation
  cd hypershift-agent-automation
  make build
  sudo install -m 0755 bin/hypershift-agent-automation /usr/local/bin/hypershift-agent-automation
```

### Infra:
Need to set below env vars to connect HMC, VIOS and PowerVC.

```shell
# To get authenticated with PowerVC(OpenStack Client)
export OS_USERNAME=''
export OS_PASSWORD=''
export OS_IDENTITY_API_VERSION=''
export OS_AUTH_URL=''
export OS_CACERT=''
export OS_REGION_NAME=''
export OS_PROJECT_DOMAIN_NAME=''
export OS_PROJECT_NAME=''
export OS_TENANT_NAME=''
export OS_USER_DOMAIN_NAME=''

# Required PowerVC resource names
export POWERVC_STORAGE_TEMPLATE=''
export POWERVC_HOST=''
export POWERVC_NETWORK_NAME=''

# HMC details
export HMC_IP=''
export HMC_USERNAME=''
export HMC_PASSWORD=''

# VIOS details
export VIOS_IP=''
export VIOS_USERNAME=''
export VIOS_PASSWORD=''
export VIOS_HOMEDIR=''
```



## Commands:

### Create Agent Cluster:
```shell
hypershift-agent-automation cluster create \
--name $CLUSTER_NAME \
--base-domain $BASE_DOMAIN \
--pull-secret $PULL_SECRET \
--release-image $RELEASE_IMAGE \
--ssh-key $SSH_KEY_FILE \
--node-count $NODE_COUNT
```

### Destroy Agent Cluster:
```shell
hypershift-agent-automation cluster destroy \
--name $CLUSTER_NAME \
--base-domain $BASE_DOMAIN \
--pull-secret $PULL_SECRET \
--release-image $RELEASE_IMAGE \
--ssh-key $SSH_KEY_FILE \
--node-count $NODE_COUNT
```

### Running e2e:
```shell
hypershift-agent-automation e2e \
--name $CLUSTER_NAME \
--base-domain $BASE_DOMAIN \
--pull-secret $PULL_SECRET \
--release-image $RELEASE_IMAGE \
--ssh-key $SSH_KEY_FILE \
--node-count $NODE_COUNT
```