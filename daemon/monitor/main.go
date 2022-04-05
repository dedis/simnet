package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
)

const (
	// MeasureFileName is the name of the file created to write the measures.
	MeasureFileName = "data"
	// DockerSocketPath is the path of the socket used by Docker on Unix machines.
	DockerSocketPath = "/var/run/docker.sock"
	// DockerNetworkInterface is the name of the network interface to measure.
	DockerNetworkInterface = "eth0"
)

var monitorFactory = func(name string) monitor {
	return newMonitor(name)
}

func main() {
	return

	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	name := flagset.String("container", "", "container name prefix")
	output := flagset.String("output", MeasureFileName, "output file")

	flagset.Parse(os.Args[1:])

	sigc := make(chan os.Signal, 1)
	// Docker stop sends a SIGTERM signal to gracefully stop the containers.
	signal.Notify(sigc, syscall.SIGTERM)

	f, err := os.Create(*output)
	checkErr(err)

	writer := bufio.NewWriter(f)

	monitor := monitorFactory(*name)
	err = monitor.Start()
	checkErr(err)

	for {
		select {
		case <-sigc:
			// Close the stream with the docker API.
			err = monitor.Stop()
			checkErr(err)

			err = f.Close()
			checkErr(err)
			return
		case stats, ok := <-monitor.Stream():
			if !ok {
				return
			}

			netstat := stats.Networks[DockerNetworkInterface]
			ts := time.Now().Unix()
			rx := netstat.RxBytes
			tx := netstat.TxBytes
			previousCPU := stats.PreCPUStats.CPUUsage.TotalUsage
			previousSystem := stats.PreCPUStats.SystemUsage
			cpu := calculateCPUPercent(previousCPU, previousSystem, stats)
			mem := stats.MemoryStats.Usage

			// Each line has the following values:
			// TIMESTAMP || RX BYTES || TX BYTES || CPU || MEMORY
			_, err = writer.WriteString(fmt.Sprintf("%d,%d,%d,%d,%d\n", ts, rx, tx, cpu, mem))
			checkErr(err)
			err = writer.Flush()
			checkErr(err)
		}
	}
}

// https://github.com/moby/moby/blob/eb131c5383db8cac633919f82abad86c99bffbe5/cli/command/container/stats_helpers.go#L175-L188
func calculateCPUPercent(previousCPU, previousSystem uint64, v *types.StatsJSON) int {
	var (
		cpuPercent = 0.0
		// calculate the change for the cpu usage of the container in between readings
		cpuDelta = float64(v.CPUStats.CPUUsage.TotalUsage) - float64(previousCPU)
		// calculate the change for the entire system between readings
		systemDelta = float64(v.CPUStats.SystemUsage) - float64(previousSystem)
	)

	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(len(v.CPUStats.CPUUsage.PercpuUsage)) * 100.0
	}
	return int(math.Ceil(cpuPercent))
}

func checkErr(err error) {
	if err != nil {
		panic(err.Error())
	}
}
