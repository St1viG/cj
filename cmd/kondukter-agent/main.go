// Command kondukter-agent runs on a partner node, dials out to Kondukter and
// reconciles desired state against the local hypervisor (SPEC §7).
package main

import (
	"fmt"

	"github.com/St1viG/cj/internal/version"
)

func main() {
	fmt.Println(version.String("kondukter-agent"))
}
