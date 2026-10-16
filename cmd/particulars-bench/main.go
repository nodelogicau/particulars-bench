// Command particulars-bench measures DKF implementations as memory for an
// agent, on LongMemEval.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/nodelogicau/particulars-bench/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], cli.DefaultDeps())
	stop()
	os.Exit(code)
}
