FROM alpine

EXPOSE 1194/udp

VOLUME [ "/etc/openvpn" ]

RUN apk add --no-cache openvpn

WORKDIR /app
COPY ./router/openvpn.conf .
COPY ./router/startup.sh .

# Requires NET_ADMIN capabilities
ENTRYPOINT [ "./startup.sh" ]
