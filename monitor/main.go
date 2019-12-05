package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
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
	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	name := flagset.String("container", "", "container name prefix")
	output := flagset.String("output", MeasureFileName, "output file")
	flagset.Parse(os.Args)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT)

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
			cpu := stats.CPUStats.CPUUsage.TotalUsage
			mem := stats.MemoryStats.Usage

			// Each line has the following values:
			// TIMESTAMP || RX BYTES || TX BYTES || CPU || MEMORY
			_, err = writer.WriteString(fmt.Sprintf("%d,%d,%d,%d,%d\n", ts, rx, tx, cpu, mem))
			checkErr(err)
			err = writer.Flush()
			checkErr(err)

			fmt.Printf("[%d]: %s\n", ts, stats.Name)
		}
	}
}

func checkErr(err error) {
	if err != nil {
		panic(err.Error())
	}
}
