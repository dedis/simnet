#!/bin/sh

NETDEV=${NETDEV:=eth0}

set -o xtrace

mkdir -p /dev/net
mknod /dev/net/tun c 10 200
chmod 600 /dev/net/tun

# Allow the traffic of the tunnel to be forwarded to the LAN.
iptables -t nat -I POSTROUTING -o eth0 -s 10.0.0.0/24 -j MASQUERADE

# The server must push the route to the cluster network to the client so
# that traffic is correctly redirected through the tunnel.
# To find what is the cluster network, as it can change from a provider
# to another, the routes to eth0 are looked up.

CIDR=$(ip route show dev $DEVICE | grep -v default | grep -m 1 via | awk '{print $1}')
NETWORK=$(ipcalc -n $CIDR | cut -d "=" -f 2)
MASK=$(ipcalc -m $CIDR | cut -d "=" -f 2)

openvpn --config server.conf --push "route $NETWORK $MASK"
