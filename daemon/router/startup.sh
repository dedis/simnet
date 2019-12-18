#!/bin/sh

set -o xtrace

mkdir -p /dev/net
mknod /dev/net/tun c 10 200
chmod 600 /dev/net/tun

IP=$(ifconfig eth0 | grep "inet\b" | awk '{print $2}' | cut -d ":" -f 2)
MASK=$(ifconfig eth0 | grep "inet\b" | awk '{print $4}' | cut -d ":" -f 2)
NETWORK=$(ipcalc -n $IP $MASK | cut -d "=" -f 2)

# Allow the traffic to be forwarded to the LAN.
iptables -t nat -I POSTROUTING -o eth0 -s 10.0.0.0/24 -j MASQUERADE

openvpn --config server.conf --route $NETWORK $MASK
