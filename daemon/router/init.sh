#!/bin/sh

export EASYRSA_BATCH=1

cd /etc/openvpn/
/usr/share/easy-rsa/easyrsa init-pki

# DH parameter can be generated but it takes time. As this is not critical in
# terms of security, a file is provided so the initialization is faster.
# /usr/share/easy-rsa/easyrsa gen-dh
cp /app/dh.pem ./pki/.

/usr/share/easy-rsa/easyrsa build-ca nopass
/usr/share/easy-rsa/easyrsa build-server-full server nopass
/usr/share/easy-rsa/easyrsa build-client-full client1 nopass
