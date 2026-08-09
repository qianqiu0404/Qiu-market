package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/the-web3/s78-market-services/trading/goldenpath"
)

func main() {
	bind := flag.String("bind", "127.0.0.1:9095", "IP loopback listen address")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	running, err := goldenpath.Start(ctx, *bind)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("golden trading harness listening at %s\n", running.URL)
	<-ctx.Done()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if err := running.Close(closeCtx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
