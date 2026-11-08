// Command kondukter is the control plane: public Connect API, agent gateway,
// scheduler and controllers in one binary (SPEC §6).
package main

import (
	"fmt"

	"github.com/St1viG/cj/internal/version"
)

func main() {
	fmt.Println(version.String("kondukter"))
}
