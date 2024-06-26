#!/bin/bash

# This script tries to clean up things configured for pxe boot by setup-pxe-boot.sh
# Run this script inside the bastion configured for pxe boot.
# Usage: ./cleanup-pxe-boot.sh $CLUSTER_NAME $NODE_COUNT $NODE_1_NAME ...
#
# Sample usage: ./cleanup-pxe-boot.sh dummy 2 agent-1 agent-2
#

set -x
set +e

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
SERVER_NAME=()

for arg in "${@:3}"; do
    SERVER_NAME+=("$arg")
done

if [ ${#SERVER_NAME[@]} -ne ${NODE_COUNT} ]; then
  echo "node count does not match the server details provided"
  exit 1
fi

ISO_FILE="/tmp/${CLUSTER_NAME}.iso"

MOUNT_LOCATION="/mnt/${CLUSTER_NAME}"

umount ${MOUNT_LOCATION}

rm -rf ${MOUNT_LOCATION}

rm -rf /var/lib/tftpboot/images/${CLUSTER_NAME}

rm -rf /var/www/html/${CLUSTER_NAME}

rm -f /tmp/${CLUSTER_NAME}-iso-download-link
rm -f /tmp/${CLUSTER_NAME}-grub-menu.output
rm -f ${ISO_FILE}

LOCK_FILE="lockfile.lock"
(
flock -n 200 || exit 1;
echo "removing server host entry from dhcpd.conf"
for (( i = 0; i < ${NODE_COUNT}; i++ )); do
    HOST_ENTRY="host ${SERVER_NAME[i]}"
    sed -i "/$(printf '%s' "$HOST_ENTRY")/d" /etc/dhcp/dhcpd.conf
done
systemctl restart dhcpd;

echo "removing menuentry from grub.cfg"
sed -i "/# menuentry for $(printf '%s' "${CLUSTER_NAME}") start/,/# menuentry for $(printf '%s' "${CLUSTER_NAME}") end/d" /var/lib/tftpboot/boot/grub2/grub.cfg

echo "restarting tftp & dhcpd"
systemctl restart tftp;
) 200>"$LOCK_FILE"
