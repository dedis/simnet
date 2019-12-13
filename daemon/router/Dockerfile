FROM alpine

LABEL maintainer="DEDIS <dedis@epfl.ch>"

EXPOSE 1194/udp

VOLUME [ "/etc/openvpn" ]

RUN apk add --no-cache openvpn

WORKDIR /app
COPY ./daemon/router/server.conf .
COPY ./daemon/router/startup.sh .

# Requires NET_ADMIN capabilities
ENTRYPOINT [ "./startup.sh" ]
