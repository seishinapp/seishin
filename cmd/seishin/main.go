// Command seishin is the single entrypoint for every Seishin control-plane role.
//
// `seishin serve` runs every service in one process (the self-hosting default).
// `seishin serve --role=<name>` runs a single service, talking to the others over
// gRPC, for distributed deployments. Composition is a wiring decision, not a
// separate code path — see docs/architecture.md section 5.1.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "seishin:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return fmt.Errorf("usage: seishin serve [--role=<name>]")
	}
	// TODO: wire the role registry once internal/core services and
	// gateway/native exist. Placeholder only.
	return fmt.Errorf("not yet implemented")
}
