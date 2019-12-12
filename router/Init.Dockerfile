FROM alpine

LABEL maintainer="DEDIS <dedis@epfl.ch>"

EXPOSE 1194/udp

VOLUME ["/etc/openvpn"]

RUN apk add --no-cache easy-rsa

WORKDIR /app
COPY ./router/init.sh .
# TODO: generate it instead
COPY ./router/dh.pem .

ENTRYPOINT [ "./init.sh" ]
