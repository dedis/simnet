#!/bin/sh

mkdir -p /dev/net
mknod /dev/net/tun c 10 200
chmod 600 /dev/net/tun

iptables -t nat -I POSTROUTING -o eth0 -s 10.0.0.0/8 -j MASQUERADE

openvpn --config server.conf
