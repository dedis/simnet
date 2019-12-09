package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Println(scanner.Text())

		segments := strings.Split(scanner.Text(), " ")
		cmd := exec.Command(segments[0], segments[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Println(string(out))
		}
	}
}
