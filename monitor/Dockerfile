FROM golang:latest as builder

LABEL maintainer="DEDIS <dedis@epfl.ch>"

WORKDIR /simnet

COPY go.mod go.sum ./

RUN go mod download

COPY ./monitor ./monitor

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o app ./monitor
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o netem ./monitor/network/netem

FROM alpine:latest

WORKDIR /root/

RUN apk add iproute2

COPY --from=builder ./simnet/app ./app
COPY --from=builder ./simnet/netem ./netem

ENTRYPOINT [ "./app" ]
