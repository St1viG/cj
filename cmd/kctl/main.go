// Command kctl is the customer, partner and operator CLI for the Kondukter API.
package main

import (
	"fmt"

	"github.com/St1viG/cj/internal/version"
)

func main() {
	fmt.Println(version.String("kctl"))
}
