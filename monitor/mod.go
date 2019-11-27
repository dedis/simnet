package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	dockerapi "github.com/docker/docker/client"
)

const (
	// MeasureInterval is the tick interval of the resource measures.
	MeasureInterval = 3000 * time.Millisecond
	// MeasureFileName is the name of the file created to write the measures.
	MeasureFileName = "data"
	// DockerSocketPath is the path of the socket used by Docker on Unix machines.
	DockerSocketPath = "/var/run/docker.sock"
	// DockerNetworkInterface is the name of the network interface to measure.
	DockerNetworkInterface = "eth0"
)

func main() {
	fmt.Println("Start monitoring the host")

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT)

	tick := time.NewTicker(MeasureInterval)
	defer tick.Stop()

	client, err := dockerapi.NewClient("unix://"+DockerSocketPath, "", makeHTTPClient(), nil)
	if err != nil {
		panic(err)
	}

	list, err := client.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		panic(err)
	}

	f, err := os.Create(MeasureFileName)
	if err != nil {
		panic(err)
	}

	writer := bufio.NewWriter(f)

	for {
		select {
		case <-sigc:
			f.Close()
			fmt.Printf("Stop monitoring the host\n")
			return
		case <-tick.C:
			fmt.Printf("%d: update\n", time.Now().Unix())
			stats, err := readDockerUsage(client, list[0])
			if err != nil {
				fmt.Println(err)
			}

			netstat := stats.Networks[DockerNetworkInterface]

			writer.WriteString(fmt.Sprintf("%d,%d,%d\n", time.Now().UnixNano(), netstat.RxBytes, netstat.TxBytes))
			writer.Flush()
		}
	}
}

func makeHTTPClient() *http.Client {
	t := &http.Transport{
		Dial: func(proto, addr string) (net.Conn, error) {
			return net.DialTimeout("unix", DockerSocketPath, 10*time.Second)
		},
	}

	return &http.Client{Transport: t}
}

func readDockerUsage(client *dockerapi.Client, container types.Container) (*types.StatsJSON, error) {
	reply, err := client.ContainerStats(context.Background(), container.ID, false)
	if err != nil {
		return nil, err
	}

	data := &types.StatsJSON{}
	err = json.NewDecoder(reply.Body).Decode(data)
	reply.Body.Close()
	if err != nil {
		return nil, err
	}

	return data, nil
}
