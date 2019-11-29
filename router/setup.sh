#!/bin/bash

while [ "$1" != "" ]; do
    case $1 in
        -p | --proxy )
        IFS='=' read -ra VALUES <<< "$2"
        # Proxy the port-forward to the actual pod so the traffic is registered
        # in the statistics.
        echo "socat ${VALUES[0]} ${VALUES[1]}"
        socat tcp-listen:${VALUES[0]},fork,bind=0.0.0.0 tcp-connect:${VALUES[1]} &
        shift
        ;;
    esac
    shift
done

# prevent the container from shutting down
while :; do sleep 2073600; done
