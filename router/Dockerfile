FROM alpine

RUN apk add --no-cache bash
RUN apk add --no-cache socat

WORKDIR /root/

COPY ./router/setup.sh .

ENTRYPOINT [ "./setup.sh" ]
