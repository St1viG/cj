// Command kdk-sim runs a fleet of simulated kondukter-agents (mock driver)
// against a running Kondukter, for manual runs and demos without hardware
// (roadmap P3.10). Agents enroll through PartnerService with a partner or
// operator API key; their state lives in --dir, so a rerun reuses the
// same nodes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"connectrpc.com/connect"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/agent/stream"
	"github.com/St1viG/cj/internal/simulator"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "kdk-sim:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		api       = flag.String("api", envOr("KDK_API_URL", "http://localhost:8080"), "Kondukter API URL")
		key       = flag.String("api-key", os.Getenv("KDK_API_KEY"), "partner or operator API key (env KDK_API_KEY)")
		site      = flag.String("site", "demo", "site name to enroll into")
		dir       = flag.String("dir", ".kdk-sim", "data directory; a rerun reuses its nodes")
		cpuAMD    = flag.Int("cpu-amd64", 2, "CPU-only amd64 nodes")
		cpuARM    = flag.Int("cpu-arm64", 2, "CPU-only arm64 nodes")
		gpu       = flag.Int("gpu", 1, "GPU nodes (amd64)")
		gpus      = flag.Int("gpus-per-node", 1, "simulated GTX 1060s per GPU node")
		delay     = flag.Duration("provision-delay", 2*time.Second, "time a VM spends provisioning")
		advertise = flag.String("advertise", "127.0.0.1", "address in every port mapping")
		drop      = flag.Float64("drop", 0, "fault: probability a message is dropped")
		dup       = flag.Float64("duplicate", 0, "fault: probability a message is duplicated")
		reorder   = flag.Float64("reorder", 0, "fault: probability an outbound message is reordered")
		heartbeat = flag.Duration("heartbeat", 10*time.Second, "heartbeat interval")
		verbose   = flag.Bool("v", false, "log agent info as well as warnings")
	)
	flag.Parse()
	if *key == "" {
		return errors.New("--api-key (or KDK_API_KEY) is required: a partner or operator key from `kondukter seed accounts`")
	}
	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	partner := apiv1connect.NewPartnerServiceClient(http.DefaultClient, *api, connect.WithInterceptors(bearer(*key)))
	siteID, err := findSite(ctx, partner, *site)
	if err != nil {
		return err
	}
	var specs []simulator.NodeSpec
	add := func(n int, mk func(i int) simulator.NodeSpec) {
		for i := range n {
			s := mk(i)
			s.ProvisionDelay = *delay
			specs = append(specs, s)
		}
	}
	add(*cpuAMD, func(i int) simulator.NodeSpec { return simulator.CPUNode(fmt.Sprintf("sim-amd64-%02d", i), "amd64") })
	add(*cpuARM, func(i int) simulator.NodeSpec { return simulator.CPUNode(fmt.Sprintf("sim-arm64-%02d", i), "arm64") })
	add(*gpu, func(i int) simulator.NodeSpec { return simulator.GPUNode(fmt.Sprintf("sim-gpu-%02d", i), *gpus) })

	opts := simulator.Options{
		Dir: *dir, APIURL: *api, AdvertiseAddress: *advertise, Log: log,
		Stream: stream.Config{HeartbeatInterval: *heartbeat},
		Token: func(ctx context.Context) (string, error) {
			res, err := partner.CreateEnrollmentToken(ctx, connect.NewRequest(&apiv1.CreateEnrollmentTokenRequest{SiteId: siteID}))
			if err != nil {
				return "", err
			}
			return res.Msg.GetToken(), nil
		},
	}
	if *drop > 0 || *dup > 0 || *reorder > 0 {
		opts.NewFaults = func(i int) stream.Faults {
			return simulator.NewChaos(uint64(i)+1, *drop, *dup, *reorder, 0) //nolint:gosec // small index
		}
	}
	fleet, err := simulator.Enroll(ctx, opts, specs)
	if err != nil {
		return err
	}
	if err := fleet.StartAll(); err != nil {
		return err
	}
	fmt.Printf("kdk-sim: %d agents running against %s (site %s); data in %s; Ctrl-C to stop\n", len(fleet.Agents), *api, *site, *dir)
	for _, a := range fleet.Agents {
		fmt.Printf("  %-14s %s  %s  %d vCPU  %d MiB  %d GPU\n", a.Spec.Hostname, a.NodeID, a.Spec.Arch, a.Spec.VCPU, a.Spec.RAMMiB, a.Spec.GPUs)
	}

	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("kdk-sim: stopping agents (their VMs stay in", *dir+"; a rerun picks them up)")
			return fleet.StopAll()
		case <-t.C:
			vms := 0
			for _, a := range fleet.Agents {
				if list, err := a.VMs(ctx); err == nil {
					vms += len(list)
				}
			}
			fmt.Printf("kdk-sim: %s  %d agents, %d VMs\n", time.Now().Format("15:04:05"), len(fleet.Agents), vms)
		}
	}
}

func findSite(ctx context.Context, c apiv1connect.PartnerServiceClient, name string) (string, error) {
	res, err := c.ListSites(ctx, connect.NewRequest(&apiv1.ListSitesRequest{}))
	if err != nil {
		return "", fmt.Errorf("list sites: %w", err)
	}
	for _, s := range res.Msg.GetSites() {
		if s.GetName() == name {
			return s.GetId(), nil
		}
	}
	return "", fmt.Errorf("no site %q visible to this key", name)
}

type bearer string

func (b bearer) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		req.Header().Set("Authorization", "Bearer "+string(b))
		return next(ctx, req)
	}
}

func (b bearer) WrapStreamingClient(n connect.StreamingClientFunc) connect.StreamingClientFunc {
	return n
}

func (b bearer) WrapStreamingHandler(n connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return n
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
