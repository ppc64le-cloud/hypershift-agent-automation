#!/bin/bash


# This script tries to setup pxe boot for agents created via upstream ci.
# Run this script inside the bastion configured for pxe boot.
# Usage: ./setup-pxe-boot.sh $CLUSTER_NAME $NODE_COUNT $NODE_1_DETAIL ...
# $NODE_1_DETAIL - Pass name, mac and ip details separated by a comma
#
# Sample usage: ./setup-pxe-boot.sh dummy 2 agent-1,fa:c5:e7:72:da:20,192.168.140.10 agent-2,fa:fd:c9:9d:9f:20,192.168.140.11
#

set -x
set -e

export CLUSTER_NAME=$1
if [ -z $CLUSTER_NAME ]; then
  echo "CLUSTER_NAME is not passed"
  exit 1
fi

NODE_COUNT=$2
if [ -z $NODE_COUNT ]; then
  echo "NODE_COUNT is not passed"
  exit 1
fi

GRUB_MENU_START="# menuentry for ${CLUSTER_NAME} start"
GRUB_MENU_END="# menuentry for ${CLUSTER_NAME} end"

# Parse server details sent with the delimiter as ","
SERVER_NAME=()
MAC=()
IP=()

# Using comma as delimiter to extract the server details provided
IFS=','

for arg in "${@:3}"; do
    read -ra serverDet <<< "$arg"
    indexArg=0
    for det in "${serverDet[@]}"; do
        case "$indexArg" in
            0)
              SERVER_NAME+=("$det")
              ;;
            1)
              MAC+=("$det")
              ;;
            2)
              IP+=("$det")
              ;;
        esac
        indexArg=$(($indexArg+1))
    done
done

if [ ${#SERVER_NAME[@]} -ne ${NODE_COUNT} ] || [ ${#MAC[@]} -ne ${NODE_COUNT} ] || [ ${#IP[@]} -ne ${NODE_COUNT} ]; then
  echo "node count does not match the server details provided"
  exit 1
fi

ISO_FILE="/tmp/${CLUSTER_NAME}.iso"
DISCOVERY_ISO_DOWNLOAD_LINK_FILE="/tmp/${CLUSTER_NAME}-iso-download-link"

# Download discovery ISO
curl -k "$(cat ${DISCOVERY_ISO_DOWNLOAD_LINK_FILE})" -o "${ISO_FILE}"

# Mount ISO
MOUNT_LOCATION="/mnt/${CLUSTER_NAME}"
mkdir -p ${MOUNT_LOCATION}
mount -o loop ${ISO_FILE} ${MOUNT_LOCATION}

# Copy images from mount
mkdir -p /var/lib/tftpboot/images/${CLUSTER_NAME}
cp -rf ${MOUNT_LOCATION}/images/* /var/lib/tftpboot/images/${CLUSTER_NAME}

# Preparing menu entry content by changin image path and
MENU_ENTRY_CONTENT=$(sed -n "/menuentry /,/}/p" /mnt/${CLUSTER_NAME}/boot/grub/grub.cfg | sed '1d;$d' | sed 's/\/images/images\/${CLUSTER_NAME}/g')
MENU_ENTRY_CONTENT=$(echo $MENU_ENTRY_CONTENT | envsubst)
export MENU_ENTRY_CONTENT

# Preparing GRUB_MENU_OUTPUT
# Use envsubst to create menu entry content for each host and append it with GRUB_MENU_OUTPUT
export GRUB_MAC_CONFIG="\${net_default_mac}"
GRUB_MENU_OUTPUT+=${GRUB_MENU_START}
GRUB_MENU_OUTPUT+="\n"
for (( i = 0; i < ${NODE_COUNT}; i++ )); do
    export SERVER_MAC=${MAC[i]}
    CONFIG=$(cat grub-menu.template | envsubst)
    GRUB_MENU_OUTPUT+=${CONFIG}
    GRUB_MENU_OUTPUT+="\n"
done
GRUB_MENU_OUTPUT+="\n"
GRUB_MENU_OUTPUT+=${GRUB_MENU_END}

GRUB_MENU_OUTPUT_FILE="/tmp/${CLUSTER_NAME}-grub-menu.output"
echo -e ${GRUB_MENU_OUTPUT} > ${GRUB_MENU_OUTPUT_FILE}

# Adding new line before initrd which is expected by tftp for parsing purpose
sed -i 's/initrd/\
        initrd/' ${GRUB_MENU_OUTPUT_FILE}

# Using lock to do below operations
# Writing dhcpd.conf and grub.cfg files
# Restart dhcpd and tftp servers
LOCK_FILE="lockfile.lock"
(
flock 200 || exit 1
echo "writing menuentry to grub.cfg "
sed -i -e "/menuentry 'RHEL CoreOS (Live)' --class fedora --class gnu-linux --class gnu --class os {/r $(printf '%s' "$GRUB_MENU_OUTPUT_FILE")" /var/lib/tftpboot/boot/grub2/grub.cfg;
systemctl restart tftp;

echo "writing host entries to dhcpd.conf"
for (( i = 0; i < ${NODE_COUNT}; i++ )); do
    HOST_ENTRY="host ${SERVER_NAME[i]} { hardware ethernet ${MAC[i]}; fixed-address ${IP[i]}; }"
    sed -i "/# Static entries/a\    $(printf '%s' "$HOST_ENTRY")" /etc/dhcp/dhcpd.conf;
done

echo "restarting services tftp & dhcpd"
systemctl restart dhcpd;
)200>"$LOCK_FILE"
