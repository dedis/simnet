#!/bin/sh

NETDEV=${NETDEV:=eth0}
DEFAULT_MASK=${DEFAULT_MASK:=255.255.0.0}

# set -o xtrace

mkdir -p /dev/net
mknod /dev/net/tun c 10 200
chmod 600 /dev/net/tun

# Enable ip forward for IPv4 so that the VPN can redirect the traffic
# to the LAN.

sysctl -w net.ipv4.ip_forward=1
if [ "$(sysctl -n net.ipv4.ip_forward)" -ne "1" ]; then
    echo "[ERROR] Permission denied when enabling ip forwarding."
    echo "[ERROR] Please check that Kubernetes allows privileged containers."
    exit 1
fi

iptables -t nat -I POSTROUTING -o eth0 -s 10.0.0.0/24 -j MASQUERADE

# The server must push the route to the cluster network to the client so
# that traffic is correctly redirected through the tunnel.
# To find what is the cluster network, as it can change from a provider
# to another, the routes to eth0 are looked up.

CIDR=$(ip route show dev $NETDEV | grep -v default | grep -m 1 via | awk '{print $1}')
if [ -z "$CIDR" ]; then
    # There is no via route other than the default so the subnetwork is calculated
    # with the default mask and the current IP address.
    
    IP=$(ifconfig $NETDEV | grep -m 1 inet | awk '{print $2}' | cut -d ":" -f 2)

    MASK=$DEFAULT_MASK
    NETWORK=$(ipcalc -n $IP $MASK | cut -d "=" -f 2)
else
    NETWORK=$(ipcalc -n $CIDR | cut -d "=" -f 2)
    MASK=$(ipcalc -m $CIDR | cut -d "=" -f 2)
fi

echo "Using route $NETWORK $MASK"

terminate() {
    echo "Caught signal. Process will stop."
    kill -9 "$child" 2>/dev/null
}

# Docker sends SIGTERM to let the container stops by itself.
trap terminate TERM

openvpn --config server.conf --push "route $NETWORK $MASK" &

child=$!
wait "$child"
