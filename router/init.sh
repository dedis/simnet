#!/bin/sh

export EASYRSA_BATCH=1

cd /etc/openvpn/
/usr/share/easy-rsa/easyrsa init-pki
# /usr/share/easy-rsa/easyrsa gen-dh
cp /app/dh.pem ./pki/.
/usr/share/easy-rsa/easyrsa build-ca nopass
/usr/share/easy-rsa/easyrsa build-server-full server nopass
/usr/share/easy-rsa/easyrsa build-client-full client1 nopass
