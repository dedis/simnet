package metrics

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// Stats represents the JSON structure of the statistics written for each node.
type Stats struct {
	Timestamp int64
	Nodes     map[string]NodeStats
}

// NodeStats contains the array of data that represents a timeline of the
// resource usage of a node.
type NodeStats struct {
	Timestamps []int64
	RxBytes    []uint64
	TxBytes    []uint64
	CPU        []uint64
	Memory     []uint64
}

// NewNodeStats creates statistics for a node by reading the reader line by line.
func NewNodeStats(reader io.Reader) NodeStats {
	ns := NodeStats{}

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		numbers := parseLine(line)

		if len(numbers) >= 5 {
			ns.Timestamps = append(ns.Timestamps, int64(numbers[0]))
			ns.RxBytes = append(ns.RxBytes, numbers[1])
			ns.TxBytes = append(ns.TxBytes, numbers[2])
			ns.CPU = append(ns.CPU, numbers[3])
			ns.Memory = append(ns.Memory, numbers[4])
		}
	}

	return ns
}

func parseInteger(value string) (uint64, error) {
	return strconv.ParseUint(strings.Trim(value, " "), 10, 64)
}

// parseLine reads a string and tries to convert values delimited by a
// comma. The returned array is filled on success and left empty if none.
func parseLine(line string) []uint64 {
	values := strings.Split(line, ",")
	numbers := make([]uint64, 0, len(values))

	for _, value := range values {
		num, err := parseInteger(value)
		if err == nil {
			numbers = append(numbers, num)
		}
	}

	return numbers
}
