FROM golang:latest as builder

LABEL maintainer="DEDIS <dedis@epfl.ch>"

WORKDIR /simnet

COPY go.mod go.sum ./

RUN go mod download

COPY ./network ./network
COPY ./daemon ./daemon

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o monitor ./daemon/monitor
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o netem ./daemon/monitor/netem

FROM alpine:latest

WORKDIR /root/

RUN apk add iproute2

COPY --from=builder ./simnet/monitor ./monitor
COPY --from=builder ./simnet/netem ./netem

ENTRYPOINT [ "./monitor" ]
