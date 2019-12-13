package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"go.dedis.ch/simnet/monitor/network"
)

const (
	// DefaultNetworkDevice is the name of the network device to apply the
	// topology emulation.
	DefaultNetworkDevice = "eth0"
	// DefaultCommand is the location of the command to use for emulation.
	DefaultCommand = "tc"
	// DefaultInput is an empty string to use the standard input.
	DefaultInput = ""
	// DefaultLogFile is the file where to write the logs.
	DefaultLogFile = "/dev/null"
)

func checkErr(err error, msg string) {
	if err != nil {
		panic(fmt.Errorf("%s: %v", msg, err))
	}
}

func main() {
	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	dev := flagset.String("dev", DefaultNetworkDevice, "network device to target")
	cmd := flagset.String("cmd", DefaultCommand, "tc command location")
	input := flagset.String("input", DefaultInput, "input to decode to rules")
	logpath := flagset.String("log", DefaultLogFile, "file to write the logs")

	flagset.Parse(os.Args[1:])

	log, err := os.Create(*logpath)
	checkErr(err, "couldn't create or open the logging file")

	defer log.Close()

	reader := os.Stdin
	if *input != "" {
		reader, err = os.Open(*input)
		checkErr(err, "couldn't open the file")
	}

	dec := json.NewDecoder(reader)

	var data []network.Rule
	err = dec.Decode(&data)
	checkErr(err, "couldn't decode")

	fmt.Fprintln(log, "Applying rules to the network manager...")
	exec := executor{
		dev: *dev,
		cmd: *cmd,
		out: log,
	}

	err = exec.Execute(data)
	checkErr(err, "couldn't execute the rules")

	fmt.Fprintln(log, "Applying the rules to the network manager... Done")
}
