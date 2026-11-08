// Command kdk-stubgw is a development agent gateway: it serves desired-state
// YAML files from a directory to kondukter-agent and logs what the agent
// reports (roadmap P2.2). Run the agent with --insecure-dev against it.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/St1viG/cj/internal/logging"
	"github.com/St1viG/cj/internal/simulator/stubgw"
	"github.com/St1viG/cj/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "kdk-stubgw:", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", "127.0.0.1:8443", "address to listen on")
	dir := flag.String("dir", ".", "directory with <node>.yaml / default.yaml desired-state files")
	hosts := flag.String("hosts", "localhost,127.0.0.1", "comma-separated names for the self-signed certificate")
	level := flag.String("log-level", "info", "debug, info, warn or error")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String("kdk-stubgw"))
		return nil
	}

	log, err := logging.New(os.Stderr, *level)
	if err != nil {
		return err
	}
	tlsCfg, err := stubgw.SelfSignedTLS(strings.Split(*hosts, ",")...)
	if err != nil {
		return err
	}
	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", *listen)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := stubgw.New(*dir, log)
	g := srv.NewGRPCServer(tlsCfg)
	log.Info("stub gateway listening", "addr", lis.Addr().String(), "dir", *dir)

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error { return g.Serve(lis) })
	eg.Go(func() error { return srv.Watch(ctx) })
	eg.Go(func() error {
		<-ctx.Done()
		g.GracefulStop()
		return nil
	})
	return eg.Wait()
}
