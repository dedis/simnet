package network

import (
	"fmt"
	"os/exec"
	"strings"
)

// Command is a network emulator command that will apply a given rule to
// a network interface.
type Command interface {
	Run() error
}

// ParseCommand reads the string in parameter and instantiate the associated
// command.
func ParseCommand(cmd string) Command {
	segments := strings.Split(cmd, " ")
	switch segments[0] {
	case "delay":
		return NewDelayCommand(segments)
	}

	return nil
}

// DelayCommand is a command that will add delay to incoming traffic from a
// specific IP address.
type DelayCommand struct {
	ip    string
	delay string
}

// NewDelayCommand creates the command from a list of segments.
func NewDelayCommand(segments []string) DelayCommand {
	return DelayCommand{
		ip:    segments[1],
		delay: segments[2],
	}
}

// Run applies the command.
func (c DelayCommand) Run() error {
	cmds := [][]string{
		strings.Split("tc qdisc add dev eth0 root handle 1: htb", " "),
		strings.Split("tc class add dev eth0 parent 1: classid 1:1 htb rate 1000Mbps", " "),
		strings.Split("tc class add dev eth0 parent 1:1 classid 1:10 htb rate 1000Mbps", " "),
		strings.Split("tc qdisc add dev eth0 parent 1:10 handle 20: netem delay 50ms", " "),
		strings.Split("tc filter add dev eth0 protocol ip parent 1:0 prio 1 u32 match ip dst 172.17.0.6/32 flowid 1:10", " "),
		strings.Split("tc -s qdisc ls dev eth0", " "),
	}

	for _, values := range cmds {
		cmd := exec.Command(values[0], values[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Println(string(out))
			return err
		}
	}

	return nil
}
