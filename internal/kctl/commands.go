package kctl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
)

func phaseName(p apiv1.InstancePhase) string {
	return strings.TrimPrefix(p.String(), "INSTANCE_PHASE_")
}

func nodeStatusName(s apiv1.NodeStatus) string {
	return strings.TrimPrefix(s.String(), "NODE_STATUS_")
}

func terminal(p apiv1.InstancePhase) bool {
	switch p {
	case apiv1.InstancePhase_INSTANCE_PHASE_TERMINATED, apiv1.InstancePhase_INSTANCE_PHASE_FAILED, apiv1.InstancePhase_INSTANCE_PHASE_LOST:
		return true
	}
	return false
}

func money(m *apiv1.Money) string {
	if m == nil {
		return "-"
	}
	return fmt.Sprintf("%d.%02d %s", m.GetAmountMinor()/100, m.GetAmountMinor()%100, m.GetCurrency())
}

func when(t *timestamppb.Timestamp) string {
	if t == nil {
		return "-"
	}
	return t.AsTime().Local().Format("2006-01-02 15:04:05")
}

func accel(s *apiv1.Shape) string {
	if s.GetAcceleratorCount() == 0 {
		return "-"
	}
	return fmt.Sprintf("%d× %s", s.GetAcceleratorCount(), s.GetAcceleratorModel())
}

func (a *app) loginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store the server URL and an API key in the config file",
		Long:  "Checks the key with GetMe, then writes --server and the key to the config file (mode 0600). Without --api-key the key is read from stdin.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _ := loadConfig(a.configPath)
			if a.server != "" {
				c.Server = strings.TrimRight(a.server, "/")
			}
			if c.Server == "" {
				return errors.New("--server is required the first time")
			}
			c.APIKey = a.apiKey
			if c.APIKey == "" {
				fmt.Fprint(a.err, "API key: ")
				line, err := bufio.NewReader(a.in).ReadString('\n')
				if err != nil && line == "" {
					return errors.New("no API key on stdin")
				}
				c.APIKey = strings.TrimSpace(line)
			}
			me, err := a.connect(c).auth.GetMe(cmd.Context(), connect.NewRequest(&apiv1.GetMeRequest{}))
			if err != nil {
				return err
			}
			if err := saveConfig(a.configPath, c); err != nil {
				return err
			}
			p := me.Msg.GetPrincipal()
			return a.print(me.Msg, func() error {
				_, err := fmt.Fprintf(a.out, "logged in to %s as %s (org %s, %s); saved to %s\n", c.Server,
					p.GetUser().GetEmail(), p.GetOrg().GetName(), strings.ToLower(strings.TrimPrefix(p.GetRole().String(), "ROLE_")), a.configPath)
				return err
			})
		},
	}
	return cmd
}

func (a *app) whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the caller",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			me, err := c.auth.GetMe(cmd.Context(), connect.NewRequest(&apiv1.GetMeRequest{}))
			if err != nil {
				return err
			}
			p := me.Msg.GetPrincipal()
			return a.print(me.Msg, func() error {
				_, err := fmt.Fprintf(a.out, "%s (org %s, %s)\n", p.GetUser().GetEmail(), p.GetOrg().GetName(),
					strings.ToLower(strings.TrimPrefix(p.GetRole().String(), "ROLE_")))
				return err
			})
		},
	}
}

func (a *app) offersCmd() *cobra.Command {
	var f apiv1.ListOffersRequest
	cmd := &cobra.Command{
		Use:   "offers",
		Short: "List what can be bought",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			res, err := c.catalog.ListOffers(cmd.Context(), connect.NewRequest(&f))
			if err != nil {
				return err
			}
			return a.print(res.Msg, func() error {
				var rows [][]string
				for _, o := range res.Msg.GetOffers() {
					s := o.GetShape()
					rows = append(rows, []string{o.GetId(), s.GetName(), strconv.Itoa(int(s.GetVcpu())),
						fmt.Sprintf("%d MiB", s.GetRamMib()), fmt.Sprintf("%d GiB", s.GetDiskGib()), accel(s),
						o.GetRegion(), money(o.GetPricePerHour()) + "/h", strings.Join(o.GetArchs(), ",")})
				}
				return a.table("ID\tSHAPE\tVCPU\tRAM\tDISK\tACCELERATORS\tREGION\tPRICE\tARCHS", rows)
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.AcceleratorKind, "accel", "", `accelerator kind, e.g. nvidia; "none" for CPU-only`)
	fl.StringVar(&f.AcceleratorModel, "model", "", "accelerator model")
	fl.StringVar(&f.Arch, "arch", "", "only offers with an image for this architecture (amd64, arm64)")
	fl.StringVar(&f.Region, "region", "", "region")
	fl.Uint32Var(&f.MinVcpu, "min-vcpu", 0, "at least this many vCPUs")
	fl.Uint64Var(&f.MinRamMib, "min-ram", 0, "at least this much RAM in MiB")
	fl.BoolVar(&f.IncludeInactive, "all", false, "include inactive offers")
	return cmd
}

func (a *app) imagesCmd() *cobra.Command {
	var f apiv1.ListImagesRequest
	cmd := &cobra.Command{
		Use:   "images",
		Short: "List guest images",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			if f.OfferId != "" {
				o, err := resolveOffer(cmd.Context(), c, f.OfferId)
				if err != nil {
					return err
				}
				f.OfferId = o.GetId()
			}
			res, err := c.catalog.ListImages(cmd.Context(), connect.NewRequest(&f))
			if err != nil {
				return err
			}
			return a.print(res.Msg, func() error {
				var rows [][]string
				for _, i := range res.Msg.GetImages() {
					stack := i.GetAccelStack()
					if stack == "" {
						stack = "-"
					}
					rows = append(rows, []string{i.GetId(), i.GetName(), i.GetVersion(), i.GetArch(), i.GetOs(), stack, i.GetStatus()})
				}
				return a.table("ID\tNAME\tVERSION\tARCH\tOS\tACCEL STACK\tSTATUS", rows)
			})
		},
	}
	cmd.Flags().StringVar(&f.Arch, "arch", "", "architecture (amd64, arm64)")
	cmd.Flags().StringVar(&f.OfferId, "offer", "", "only images for this offer (id or shape name)")
	return cmd
}

// resolveOffer accepts an offer id or a shape name.
func resolveOffer(ctx context.Context, c clients, ref string) (*apiv1.Offer, error) {
	res, err := c.catalog.ListOffers(ctx, connect.NewRequest(&apiv1.ListOffersRequest{}))
	if err != nil {
		return nil, err
	}
	var found []*apiv1.Offer
	for _, o := range res.Msg.GetOffers() {
		if o.GetId() == ref || o.GetShape().GetName() == ref {
			found = append(found, o)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no offer %q (see kctl offers)", ref)
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("%q matches %d offers; use the offer id", ref, len(found))
}

// resolveImage accepts an image id or name for offer, narrowed by arch.
func resolveImage(ctx context.Context, c clients, offer *apiv1.Offer, ref, arch string) (*apiv1.Image, error) {
	res, err := c.catalog.ListImages(ctx, connect.NewRequest(&apiv1.ListImagesRequest{OfferId: offer.GetId(), Arch: arch}))
	if err != nil {
		return nil, err
	}
	var found []*apiv1.Image
	for _, i := range res.Msg.GetImages() {
		if i.GetId() == ref || i.GetName() == ref || i.GetName()+":"+i.GetVersion() == ref {
			found = append(found, i)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no image %q for offer %s (see kctl images --offer %s)", ref, offer.GetShape().GetName(), offer.GetShape().GetName())
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("%q matches %d images; pass --arch or the image id", ref, len(found))
}

// sshKeys turns --ssh-key values into account key ids and inline keys: a
// UUID is an account key, an existing file is read, anything else must be
// an OpenSSH public key. Without any, ~/.ssh/id_ed25519.pub or id_rsa.pub.
func sshKeys(values []string) (ids, inline []string, err error) {
	if len(values) == 0 {
		home, _ := os.UserHomeDir()
		for _, name := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
			if _, err := os.Stat(filepath.Join(home, ".ssh", name)); err == nil {
				values = []string{filepath.Join(home, ".ssh", name)}
				break
			}
		}
		if len(values) == 0 {
			return nil, nil, errors.New("no --ssh-key and no ~/.ssh/id_*.pub")
		}
	}
	for _, v := range values {
		if _, err := uuid.Parse(v); err == nil {
			ids = append(ids, v)
			continue
		}
		key := v
		if b, err := os.ReadFile(v); err == nil { //nolint:gosec // the user's chosen key file
			key = strings.TrimSpace(string(b))
		}
		if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key)); err != nil {
			return nil, nil, fmt.Errorf("--ssh-key %q: not an account key id, a public key file, or a public key", v)
		}
		inline = append(inline, key)
	}
	return ids, inline, nil
}

// parsePort parses "8080" or "53/udp".
func parsePort(s string) (*apiv1.PortSpec, error) {
	num, proto, _ := strings.Cut(s, "/")
	n, err := strconv.ParseUint(num, 10, 16)
	if err != nil || n == 0 {
		return nil, fmt.Errorf("--port %q: want PORT or PORT/udp", s)
	}
	p := &apiv1.PortSpec{GuestPort: uint32(n), Protocol: apiv1.Protocol_PROTOCOL_TCP}
	switch proto {
	case "", "tcp":
	case "udp":
		p.Protocol = apiv1.Protocol_PROTOCOL_UDP
	default:
		return nil, fmt.Errorf("--port %q: protocol must be tcp or udp", s)
	}
	return p, nil
}

func (a *app) createCmd() *cobra.Command {
	var (
		offerRef, imageRef, arch, name, userData, idem string
		keys, ports                                    []string
		wait                                           bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an instance",
		Example: "  kctl create --offer cpu-2x2 --image ubuntu-cpu --arch arm64 --port 8080\n" +
			"  kctl create --offer gpu-gtx1060-1x --image ubuntu-gpu-pascal --ssh-key ~/.ssh/id_ed25519.pub --wait",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := a.clients()
			if err != nil {
				return err
			}
			offer, err := resolveOffer(ctx, c, offerRef)
			if err != nil {
				return err
			}
			img, err := resolveImage(ctx, c, offer, imageRef, arch)
			if err != nil {
				return err
			}
			req := &apiv1.CreateInstanceRequest{OfferId: offer.GetId(), ImageId: img.GetId(), Name: name, IdempotencyKey: idem}
			if req.SshKeyIds, req.SshPublicKeys, err = sshKeys(keys); err != nil {
				return err
			}
			for _, p := range ports {
				ps, err := parsePort(p)
				if err != nil {
					return err
				}
				req.Ports = append(req.Ports, ps)
			}
			if userData != "" {
				b, err := os.ReadFile(userData) //nolint:gosec // the user's chosen file
				if err != nil {
					return err
				}
				req.UserData = string(b)
			}
			if req.IdempotencyKey == "" {
				req.IdempotencyKey = "kctl-" + uuid.NewString()
			}
			var res *connect.Response[apiv1.CreateInstanceResponse]
			for attempt := 1; ; attempt++ { // the idempotency key makes retries safe
				res, err = c.instances.CreateInstance(ctx, connect.NewRequest(req))
				if err == nil || attempt == 3 || connect.CodeOf(err) != connect.CodeUnavailable {
					break
				}
				time.Sleep(time.Duration(attempt) * time.Second)
			}
			if err != nil {
				return err
			}
			inst := res.Msg.GetInstance()
			if err := a.print(res.Msg, func() error {
				_, err := fmt.Fprintf(a.out, "%s %s (%s, %s %s)\n", inst.GetId(), phaseName(inst.GetPhase()),
					inst.GetShape().GetName(), inst.GetImageName(), inst.GetArch())
				return err
			}); err != nil {
				return err
			}
			if wait {
				return a.watch(ctx, c, inst.GetId(), false)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&offerRef, "offer", "", "offer id or shape name (required)")
	fl.StringVar(&imageRef, "image", "", "image id or name (required)")
	fl.StringVar(&arch, "arch", "", "image architecture when the name has several")
	fl.StringArrayVar(&keys, "ssh-key", nil, "account key id, public key file, or public key (repeatable; default ~/.ssh/id_*.pub)")
	fl.StringArrayVar(&ports, "port", nil, "guest port to expose besides 22, e.g. 8080 or 53/udp (repeatable)")
	fl.StringVar(&userData, "user-data", "", "cloud-init user-data file (at most 16 KiB)")
	fl.StringVar(&name, "name", "", "instance name (also the hostname when it is a DNS label)")
	fl.StringVar(&idem, "idempotency-key", "", "reuse to retry a create safely (default: random)")
	fl.BoolVar(&wait, "wait", false, "watch until RUNNING")
	_ = cmd.MarkFlagRequired("offer")
	_ = cmd.MarkFlagRequired("image")
	return cmd
}

func (a *app) getCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get ID",
		Short: "Show an instance: phase, node, connection, usage, events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			res, err := c.instances.GetInstance(cmd.Context(), connect.NewRequest(&apiv1.GetInstanceRequest{Id: args[0]}))
			if err != nil {
				return err
			}
			return a.print(res.Msg, func() error {
				i := res.Msg.GetInstance()
				w := a.out
				line := func(k, v string) { fmt.Fprintf(w, "%-15s %s\n", k+":", v) }
				line("id", i.GetId())
				if i.GetName() != "" {
					line("name", i.GetName())
				}
				phase := phaseName(i.GetPhase())
				if r := i.GetPhaseReason(); r != "" {
					phase += " (" + r + ")"
				}
				line("phase", phase)
				line("shape", fmt.Sprintf("%s: %d vCPU, %d MiB, %d GiB, accelerators %s", i.GetShape().GetName(),
					i.GetShape().GetVcpu(), i.GetShape().GetRamMib(), i.GetShape().GetDiskGib(), accel(i.GetShape())))
				line("image", i.GetImageName()+" "+i.GetArch())
				line("region", i.GetRegion())
				line("price", money(i.GetPricePerHour())+"/h")
				if n := i.GetNodeId(); n != "" {
					line("node", n)
				}
				if conn := i.GetConnection(); conn.GetSshCommand() != "" {
					line("ssh", conn.GetSshCommand())
					for _, m := range conn.GetPortMappings() {
						if m.GetGuestPort() != 22 {
							line("port", fmt.Sprintf("%d/%s → %s:%d", m.GetGuestPort(),
								strings.ToLower(strings.TrimPrefix(m.GetProtocol().String(), "PROTOCOL_")), m.GetHostAddress(), m.GetHostPort()))
						}
					}
				}
				if u := i.GetUsage(); u != nil {
					line("running", fmt.Sprintf("%d min", u.GetRunningMinutes()))
					line("estimated cost", money(u.GetEstimatedCost()))
				}
				line("created", when(i.GetCreatedAt()))
				fmt.Fprintln(w, "events:")
				for _, e := range res.Msg.GetEvents() {
					fmt.Fprintf(w, "  %s  %-12s %s\n", when(e.GetAt()), phaseName(e.GetToPhase()), e.GetReason())
				}
				return nil
			})
		},
	}
}

func (a *app) listCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your instances, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			out := &apiv1.ListInstancesResponse{}
			token := ""
			for {
				res, err := c.instances.ListInstances(cmd.Context(), connect.NewRequest(&apiv1.ListInstancesRequest{IncludeTerminal: all, PageToken: token, PageSize: 200}))
				if err != nil {
					return err
				}
				out.Instances = append(out.Instances, res.Msg.GetInstances()...)
				if token = res.Msg.GetNextPageToken(); token == "" {
					break
				}
			}
			return a.print(out, func() error {
				var rows [][]string
				for _, i := range out.GetInstances() {
					name := i.GetName()
					if name == "" {
						name = "-"
					}
					rows = append(rows, []string{i.GetId(), name, phaseName(i.GetPhase()), i.GetShape().GetName(), i.GetArch(), when(i.GetCreatedAt())})
				}
				return a.table("ID\tNAME\tPHASE\tSHAPE\tARCH\tCREATED", rows)
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include TERMINATED, FAILED and LOST instances")
	return cmd
}

func (a *app) watchCmd() *cobra.Command {
	var untilTerminal bool
	cmd := &cobra.Command{
		Use:   "watch ID",
		Short: "Stream phase changes until RUNNING or a terminal phase",
		Long:  "Prints every phase change. Stops at RUNNING (or, with --until-terminal, only at TERMINATED, FAILED or LOST). Exits 2 if the instance ends FAILED or LOST.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			return a.watch(cmd.Context(), c, args[0], untilTerminal)
		},
	}
	cmd.Flags().BoolVar(&untilTerminal, "until-terminal", false, "keep watching past RUNNING")
	return cmd
}

func (a *app) watch(ctx context.Context, c clients, id string, untilTerminal bool) error {
	j, err := a.json()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.instances.WatchInstance(ctx, connect.NewRequest(&apiv1.WatchInstanceRequest{Id: id}))
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	last := apiv1.InstancePhase_INSTANCE_PHASE_UNSPECIFIED
	for stream.Receive() {
		msg := stream.Msg()
		i := msg.GetInstance()
		if i.GetPhase() != last {
			last = i.GetPhase()
			if j {
				if err := a.emit(msg); err != nil {
					return err
				}
			} else {
				line := fmt.Sprintf("%s  %s", when(i.GetPhaseChangedAt()), phaseName(last))
				if r := i.GetPhaseReason(); r != "" {
					line += "  " + r
				}
				if s := i.GetConnection().GetSshCommand(); s != "" && last == apiv1.InstancePhase_INSTANCE_PHASE_RUNNING {
					line += "  " + s
				}
				fmt.Fprintln(a.out, line)
			}
		}
		switch {
		case last == apiv1.InstancePhase_INSTANCE_PHASE_FAILED || last == apiv1.InstancePhase_INSTANCE_PHASE_LOST:
			return exitError{code: 2, msg: fmt.Sprintf("instance %s ended %s: %s", id, phaseName(last), i.GetPhaseReason())}
		case terminal(last), last == apiv1.InstancePhase_INSTANCE_PHASE_RUNNING && !untilTerminal:
			return nil
		}
	}
	if err := stream.Err(); err != nil {
		return err
	}
	return fmt.Errorf("watch ended at %s", phaseName(last))
}

func (a *app) terminateCmd() *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "terminate ID",
		Short: "Terminate an instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			res, err := c.instances.TerminateInstance(cmd.Context(), connect.NewRequest(&apiv1.TerminateInstanceRequest{
				Id: args[0], IdempotencyKey: "kctl-terminate-" + args[0],
			}))
			if err != nil {
				return err
			}
			if err := a.print(res.Msg, func() error {
				_, err := fmt.Fprintf(a.out, "%s %s\n", res.Msg.GetInstance().GetId(), phaseName(res.Msg.GetInstance().GetPhase()))
				return err
			}); err != nil {
				return err
			}
			if wait && !terminal(res.Msg.GetInstance().GetPhase()) {
				return a.watch(cmd.Context(), c, args[0], true)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "watch until TERMINATED")
	return cmd
}

func (a *app) sshCmd() *cobra.Command {
	var (
		run      bool
		identity string
	)
	cmd := &cobra.Command{
		Use:   "ssh ID [-- SSH ARGS…]",
		Short: "Print (or with --exec run) the ssh command for a running instance",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			res, err := c.instances.GetInstance(cmd.Context(), connect.NewRequest(&apiv1.GetInstanceRequest{Id: args[0]}))
			if err != nil {
				return err
			}
			i := res.Msg.GetInstance()
			conn := i.GetConnection()
			if i.GetPhase() != apiv1.InstancePhase_INSTANCE_PHASE_RUNNING || conn.GetHost() == "" || conn.GetSshPort() == 0 {
				return fmt.Errorf("instance %s is %s with no SSH endpoint yet", i.GetId(), phaseName(i.GetPhase()))
			}
			user := conn.GetSshUser()
			if user == "" {
				user = "ubuntu"
			}
			// The server supplies user and host; never let them read as options.
			if !sshUser.MatchString(user) || !sshHost.MatchString(conn.GetHost()) {
				return fmt.Errorf("instance %s has an unusable SSH endpoint %q@%q", i.GetId(), user, conn.GetHost())
			}
			sshArgs := []string{"-p", strconv.Itoa(int(conn.GetSshPort()))}
			if identity != "" {
				sshArgs = append(sshArgs, "-i", identity)
			}
			sshArgs = append(sshArgs, "--", user+"@"+conn.GetHost())
			sshArgs = append(sshArgs, args[1:]...)
			if !run {
				j, err := a.json()
				if err != nil {
					return err
				}
				if j {
					return a.emit(conn)
				}
				printed := slices.DeleteFunc(slices.Clone(sshArgs), func(s string) bool { return s == "--" })
				_, err = fmt.Fprintln(a.out, "ssh "+strings.Join(printed, " "))
				return err
			}
			ssh := exec.CommandContext(cmd.Context(), "ssh", sshArgs...) //nolint:gosec // G204: argv, not a shell; user and host validated, options ended by --
			ssh.Stdin, ssh.Stdout, ssh.Stderr = a.in, a.out, a.err
			return ssh.Run()
		},
	}
	cmd.Flags().BoolVar(&run, "exec", false, "run ssh instead of printing the command")
	cmd.Flags().StringVarP(&identity, "identity", "i", "", "ssh identity file")
	return cmd
}

var (
	sshUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	sshHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:_-]*$`)
)

func (a *app) nodesCmd() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "nodes",
		Short: "List your sites' nodes (partners and operators)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			siteID, err := resolveSite(cmd.Context(), c, site)
			if err != nil {
				return err
			}
			res, err := c.partner.ListNodes(cmd.Context(), connect.NewRequest(&apiv1.ListNodesRequest{SiteId: siteID}))
			if err != nil {
				return err
			}
			return a.print(res.Msg, func() error {
				var rows [][]string
				for _, n := range res.Msg.GetNodes() {
					cp := n.GetCapacity()
					gpus, used := 0, 0
					for _, acc := range n.GetAccelerators() {
						gpus++
						if acc.GetAllocated() {
							used++
						}
					}
					rows = append(rows, []string{n.GetHostname(), nodeStatusName(n.GetStatus()), n.GetSiteName(), n.GetArch(), n.GetDriver(),
						fmt.Sprintf("%d/%d", cp.GetFree().GetVcpu(), cp.GetTotal().GetVcpu()),
						fmt.Sprintf("%d/%d", cp.GetFree().GetRamMib(), cp.GetTotal().GetRamMib()),
						fmt.Sprintf("%d/%d", gpus-used, gpus), strconv.Itoa(int(n.GetActiveInstanceCount())), when(n.GetLastSeenAt())})
				}
				return a.table("HOSTNAME\tSTATUS\tSITE\tARCH\tDRIVER\tVCPU FREE\tRAM MIB FREE\tACCEL FREE\tINSTANCES\tLAST SEEN", rows)
			})
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site id or name")
	return cmd
}

// resolveSite accepts a site id or name; empty means all.
func resolveSite(ctx context.Context, c clients, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	if _, err := uuid.Parse(ref); err == nil {
		return ref, nil
	}
	res, err := c.partner.ListSites(ctx, connect.NewRequest(&apiv1.ListSitesRequest{}))
	if err != nil {
		return "", err
	}
	var found []string
	for _, s := range res.Msg.GetSites() {
		if s.GetName() == ref {
			found = append(found, s.GetId())
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no site %q", ref)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("%q matches %d sites; use the site id", ref, len(found))
}

func (a *app) enrollTokenCmd() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "enroll-token",
		Short: "Create a one-time node enrollment token (partners and operators)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			siteID, err := resolveSite(cmd.Context(), c, site)
			if err != nil {
				return err
			}
			if siteID == "" {
				return errors.New("--site is required")
			}
			res, err := c.partner.CreateEnrollmentToken(cmd.Context(), connect.NewRequest(&apiv1.CreateEnrollmentTokenRequest{SiteId: siteID}))
			if err != nil {
				return err
			}
			return a.print(res.Msg, func() error {
				_, err := fmt.Fprintf(a.out, "token:   %s\nexpires: %s\nrun on the node:\n  %s\n",
					res.Msg.GetToken(), when(res.Msg.GetExpiresAt()), res.Msg.GetEnrollCommand())
				return err
			})
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site id or name (required)")
	return cmd
}

func (a *app) usageCmd() *cobra.Command {
	var instance string
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show running time and estimated cost",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.clients()
			if err != nil {
				return err
			}
			res, err := c.usage.ListUsage(cmd.Context(), connect.NewRequest(&apiv1.ListUsageRequest{InstanceId: instance}))
			if err != nil {
				return err
			}
			return a.print(res.Msg, func() error {
				var rows [][]string
				for _, u := range res.Msg.GetInstances() {
					name := u.GetInstanceName()
					if name == "" {
						name = "-"
					}
					rows = append(rows, []string{u.GetInstanceId(), name, u.GetShapeName(), phaseName(u.GetPhase()),
						strconv.FormatInt(u.GetUsage().GetRunningMinutes(), 10), money(u.GetPricePerHour()) + "/h",
						money(u.GetUsage().GetEstimatedCost())})
				}
				if err := a.table("INSTANCE\tNAME\tSHAPE\tPHASE\tMINUTES\tPRICE\tESTIMATED COST", rows); err != nil {
					return err
				}
				var totals []string
				for _, m := range res.Msg.GetTotalEstimatedCost() {
					totals = append(totals, money(m))
				}
				_, err := fmt.Fprintf(a.out, "total: %d min, %s\n", res.Msg.GetTotalRunningMinutes(), strings.Join(totals, " + "))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&instance, "instance", "", "limit to one instance")
	return cmd
}
