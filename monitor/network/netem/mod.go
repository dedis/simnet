package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"go.dedis.ch/simnet/monitor/network"
)

func checkErr(err error, msg string) {
	if err != nil {
		panic(fmt.Errorf("%s: %v", msg, err))
	}
}

func main() {
	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	dev := flagset.String("dev", "eth0", "network device to target")
	cmd := flagset.String("cmd", "tc", "tc command location")
	input := flagset.String("input", "", "input to decode to rules")
	verbose := flagset.Bool("verbose", false, "display logs")

	flagset.Parse(os.Args[1:])

	out := ioutil.Discard
	if *verbose {
		out = os.Stdout
	}

	var err error
	reader := os.Stdin
	if *input != "" {
		reader, err = os.Open(*input)
		checkErr(err, "couldn't open the file")
	}

	dec := json.NewDecoder(reader)

	var data []network.RuleJSON
	err = dec.Decode(&data)
	checkErr(err, "couldn't decode")

	fmt.Fprintln(out, "Applying the rules to the network manager...")
	exec := executor{
		dev: *dev,
		cmd: *cmd,
		out: out,
	}

	err = exec.Execute(buildRules(data))
	checkErr(err, "couldn't execute the rules")

	fmt.Fprintln(out, "Applying the rules to the network manager... Done")
}

func buildRules(data []network.RuleJSON) []network.Rule {
	rules := make([]network.Rule, 0, len(data))

	for _, rule := range data {
		if rule.Delay != nil {
			rules = append(rules, rule.Delay)
		} else if rule.Loss != nil {
			rules = append(rules, rule.Loss)
		}
	}

	return rules
}
