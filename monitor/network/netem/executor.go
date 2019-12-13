package main

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"go.dedis.ch/simnet/monitor/network"
)

// Executor is responsible for executing the commands necessary to apply the
// rules to the network device.
type executor struct {
	dev string
	cmd string
	out io.Writer
}

type command []string

// Execute takes a bunch of rules and apply them to the network device by
// using the tc command.
func (e executor) Execute(rules []network.Rule) error {
	commands := make([]command, 0)

	// Reset the tree by replacing the root by a HTB queueing discpline and
	// also create a single class that will allow the flows to be shared
	// correctly.
	commands = append(commands, e.makeRoot(), e.makeParent())

	// Then create a path for each target (single IP or a range).
	classes := make(map[string]int)
	for _, rule := range rules {
		m := rule.MatchAddr()
		if _, ok := classes[m]; !ok {
			minor := (len(classes) + 1) * 10
			classes[m] = minor + 1

			commands = append(
				commands,
				// Define the new flow attached to the parent class created
				// previously.
				e.makeClass(minor),
				// Define which IPs can go through this flow.
				e.makeFilter(m, minor),
				// Attach the first queuing discpline (delay, loss, etc...).
				e.makeQDisc(minor, classes[m], rule),
			)
		}
	}

	// Run the commands in order.
	for _, args := range commands {
		err := e.execTc(args)
		if err != nil {
			return fmt.Errorf("%s command failed: %v", e.cmd, err)
		}
	}

	return nil
}

func (e executor) execTc(args []string) error {
	cmd := exec.Command(e.cmd, args...)
	cmd.Stderr = e.out
	cmd.Stdout = e.out

	fmt.Fprintln(e.out, cmd.String())

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("cmd failed: %s", err)
	}

	return nil
}

func (e executor) makeRoot() []string {
	cmd := fmt.Sprintf("qdisc add dev %s root handle 1: htb", e.dev)
	return strings.Split(cmd, " ")
}

func (e executor) makeParent() []string {
	cmd := fmt.Sprintf("class add dev %s parent 1: classid 1:1 htb rate 1000Mbps", e.dev)
	return strings.Split(cmd, " ")
}

func (e executor) makeClass(minor int) []string {
	cmd := fmt.Sprintf("class add dev %s parent 1:1 classid 1:%d htb rate 1000Mbps", e.dev, minor)
	return strings.Split(cmd, " ")
}

func (e executor) makeQDisc(minor, major int, rule network.Rule) []string {
	cmd := fmt.Sprintf("qdisc add dev %s parent 1:%d handle %d: %s", e.dev, minor, major, makeNetEm(rule))
	return strings.Split(cmd, " ")
}

func makeNetEm(rule network.Rule) string {
	return strings.Trim(fmt.Sprintf("netem %s %s", rule.Delay, rule.Loss), " ")
}

func (e executor) makeFilter(ip string, minor int) []string {
	cmd := fmt.Sprintf("filter add dev %s protocol ip parent 1:0 prio 1 u32 match ip dst %s flowid 1:%d", e.dev, ip, minor)
	return strings.Split(cmd, " ")
}
