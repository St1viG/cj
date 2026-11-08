package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agentproto"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Notifier is told when a node's desired state changed (SPEC §6.5 commit
// step 5). In-process in the demo (ADR-0017); LISTEN/NOTIFY with replicas.
type Notifier interface {
	NodeChanged(nodeID uuid.UUID)
}

// Hooks let the rest of the control plane observe ingest. They are called
// after the enclosing transaction commits and must not block.
type Hooks struct {
	// InstanceChanged fires for every instance whose row changed.
	InstanceChanged func(id uuid.UUID)
}

// Server is the AgentGateway.Connect handler. Mount it with NewGRPCServer,
// whose interceptor authenticates the node.
type Server struct {
	agentv1.UnimplementedAgentGatewayServer

	store *store.Store
	log   *slog.Logger
	hooks Hooks

	mu       sync.Mutex
	sessions map[uuid.UUID]*session
}

var _ Notifier = (*Server)(nil)

type session struct {
	kick   chan struct{}
	cancel context.CancelCauseFunc
}

var errReplaced = errors.New("replaced by a newer stream from the same node")

// New returns a gateway handler.
func New(s *store.Store, log *slog.Logger, hooks Hooks) *Server {
	return &Server{store: s, log: log, hooks: hooks, sessions: map[uuid.UUID]*session{}}
}

// NodeChanged implements Notifier: a connected node gets a fresh snapshot.
func (s *Server) NodeChanged(nodeID uuid.UUID) {
	s.mu.Lock()
	sess := s.sessions[nodeID]
	s.mu.Unlock()
	if sess == nil {
		return
	}
	select {
	case sess.kick <- struct{}{}:
	default: // a push is already pending and will read the latest state
	}
}

// Connected reports whether nodeID has a live stream.
func (s *Server) Connected(nodeID uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[nodeID] != nil
}

// Connect implements AgentGateway.
func (s *Server) Connect(stream agentv1.AgentGateway_ConnectServer) error {
	node, ok := NodeFromContext(stream.Context())
	if !ok {
		return status.Error(codes.Unauthenticated, "stream is not authenticated")
	}
	log := s.log.With("node", node.ID.String())
	ctx, cancel := context.WithCancelCause(stream.Context())
	defer cancel(nil)

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "the first message must be Hello")
	}
	if !agentproto.Compatible(hello.GetProtocolVersion()) {
		return status.Errorf(codes.FailedPrecondition, "agent protocol %q is not compatible with gateway protocol %q",
			hello.GetProtocolVersion(), agentproto.ProtocolVersion)
	}
	node, err = s.hello(ctx, node, hello)
	if err != nil {
		log.Error("hello", "err", err)
		return status.Error(codes.Internal, "hello failed")
	}
	log.Info("agent connected", "agent_version", hello.GetAgentVersion(), "driver", hello.GetDriver(),
		"last_applied_generation", hello.GetLastAppliedGeneration(), "desired_generation", node.DesiredGeneration,
		"status", node.Status)

	sess := &session{kick: make(chan struct{}, 1), cancel: cancel}
	s.mu.Lock()
	if old := s.sessions[node.ID]; old != nil {
		old.cancel(errReplaced)
	}
	s.sessions[node.ID] = sess
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.sessions[node.ID] == sess {
			delete(s.sessions, node.ID)
		}
		s.mu.Unlock()
		log.Info("agent disconnected", "reason", context.Cause(ctx))
	}()

	if err := s.pushSnapshot(ctx, stream, node.ID); err != nil {
		return err
	}

	msgs := make(chan *agentv1.AgentMessage)
	recvErr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case msgs <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

	driver := hello.GetDriver()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(context.Cause(ctx), errReplaced) {
				return status.Error(codes.Aborted, errReplaced.Error())
			}
			return status.FromContextError(ctx.Err()).Err()
		case err := <-recvErr:
			if errors.Is(err, io.EOF) || status.Code(err) == codes.Canceled {
				return nil
			}
			return err
		case <-sess.kick:
			if err := s.pushSnapshot(ctx, stream, node.ID); err != nil {
				return err
			}
		case m := <-msgs:
			if err := s.handle(ctx, stream, node.ID, driver, m); err != nil {
				log.Error("ingest failed; closing the stream so the agent resends", "seq", m.GetSeq(), "err", err)
				return status.Error(codes.Internal, "ingest failed")
			}
		}
	}
}

// hello marks the node connected and ingests the agent's observed instances.
func (s *Server) hello(ctx context.Context, node db.Node, h *agentv1.Hello) (db.Node, error) {
	var changed []uuid.UUID
	err := s.store.WithTx(ctx, func(tx store.Tx) error {
		var err error
		node, err = tx.NodeHello(ctx, db.NodeHelloParams{
			ID: node.ID, AgentVersion: h.GetAgentVersion(), Driver: h.GetDriver(),
			LastApplied: clampGen(h.GetLastAppliedGeneration()),
		})
		if err != nil {
			return err
		}
		if node.Status == db.NodeStatusQuarantined {
			return nil
		}
		for _, st := range h.GetObserved() {
			out, err := controller.AgentStatus(ctx, tx, node, st)
			if err != nil {
				return err
			}
			if out.InstanceID != nil {
				changed = append(changed, *out.InstanceID)
			}
		}
		return nil
	})
	if err == nil {
		s.instancesChanged(changed)
	}
	return node, err
}

// handle ingests one message. Sequenced messages are acked only after
// their transaction commits.
func (s *Server) handle(ctx context.Context, stream agentv1.AgentGateway_ConnectServer, nodeID uuid.UUID, driver string, m *agentv1.AgentMessage) error {
	var (
		changed     []uuid.UUID
		nodeChanged bool
	)
	switch b := m.Body.(type) {
	case *agentv1.AgentMessage_Heartbeat:
		if err := s.store.NodeHeartbeat(ctx, db.NodeHeartbeatParams{
			ID: nodeID, ObservedGeneration: clampGen(b.Heartbeat.GetAppliedGeneration()),
		}); err != nil {
			return err
		}
	case *agentv1.AgentMessage_Inventory:
		if err := s.store.WithTx(ctx, func(tx store.Tx) error {
			node, err := tx.GetNodeForUpdate(ctx, nodeID)
			if err != nil || node.Status == db.NodeStatusQuarantined {
				return err
			}
			return ingestInventory(ctx, tx, nodeID, driver, b.Inventory)
		}); err != nil {
			return err
		}
	case *agentv1.AgentMessage_InstanceStatus:
		if err := s.store.WithTx(ctx, func(tx store.Tx) error {
			node, err := tx.GetNode(ctx, nodeID)
			if err != nil || node.Status == db.NodeStatusQuarantined {
				return err
			}
			out, err := controller.AgentStatus(ctx, tx, node, b.InstanceStatus)
			if err != nil {
				return err
			}
			if out.Ignored != "" {
				s.log.Debug("status ignored", "node", nodeID.String(), "instance", b.InstanceStatus.GetId(), "why", out.Ignored)
			}
			if out.InstanceID != nil {
				changed = append(changed, *out.InstanceID)
			}
			nodeChanged = out.Effects.NodeChanged != nil
			return nil
		}); err != nil {
			return err
		}
	case *agentv1.AgentMessage_Usage:
		if err := s.store.WithTx(ctx, func(tx store.Tx) error {
			node, err := tx.GetNode(ctx, nodeID)
			if err != nil || node.Status == db.NodeStatusQuarantined {
				return err
			}
			for _, r := range b.Usage.GetRecords() {
				id, err := uuid.Parse(r.GetInstanceId())
				if err != nil || r.GetPeriodStart() == nil || r.GetPeriodEnd() == nil {
					continue
				}
				if _, err := tx.InsertOwnedUsageRecord(ctx, db.InsertOwnedUsageRecordParams{
					InstanceID: id, PeriodStart: r.GetPeriodStart().AsTime(), PeriodEnd: r.GetPeriodEnd().AsTime(),
					RunningSeconds: int32(min(r.GetRunningSeconds(), 3600)), NodeID: nodeID,
				}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	case *agentv1.AgentMessage_Hello:
		return errors.New("a second Hello on the same stream")
	}

	if seq := m.GetSeq(); seq > 0 {
		if err := stream.Send(&agentv1.ControlMessage{Body: &agentv1.ControlMessage_Ack{Ack: &agentv1.Ack{Seq: seq}}}); err != nil {
			return err
		}
	}
	s.instancesChanged(changed)
	if nodeChanged {
		if err := s.pushSnapshot(ctx, stream, nodeID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) instancesChanged(ids []uuid.UUID) {
	if s.hooks.InstanceChanged == nil {
		return
	}
	for _, id := range ids {
		s.hooks.InstanceChanged(id)
	}
}

// pushSnapshot sends the node's current full snapshot.
func (s *Server) pushSnapshot(ctx context.Context, stream agentv1.AgentGateway_ConnectServer, nodeID uuid.UUID) error {
	ds, err := s.Snapshot(ctx, nodeID)
	if err != nil {
		return status.Errorf(codes.Internal, "build snapshot: %v", err)
	}
	return stream.Send(&agentv1.ControlMessage{Body: &agentv1.ControlMessage_DesiredState{DesiredState: ds}})
}

// Snapshot builds the node's full DesiredState (SPEC §6.6). The generation
// and the instance list come from one repeatable-read transaction, so a
// snapshot never pairs a generation with older content.
func (s *Server) Snapshot(ctx context.Context, nodeID uuid.UUID) (*agentv1.DesiredState, error) {
	ds := &agentv1.DesiredState{}
	err := s.store.WithTxOptions(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx store.Tx) error {
		node, err := tx.GetNode(ctx, nodeID)
		if err != nil {
			return err
		}
		ds.Generation = uint64(max(node.DesiredGeneration, 0))
		rows, err := tx.ListSnapshotInstances(ctx, &nodeID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			spec, err := store.ParseSpec(r.Spec)
			if err != nil {
				return fmt.Errorf("instance %s: %w", r.ID, err)
			}
			ds.Instances = append(ds.Instances, specProto(r.ID, spec, r.AcceleratorIds))
		}
		return nil
	})
	return ds, err
}

func specProto(id uuid.UUID, s store.InstanceSpec, accelerators []string) *agentv1.InstanceSpec {
	hostname := s.Hostname
	if hostname == "" {
		hostname = "kdk-" + id.String()[:8]
	}
	p := &agentv1.InstanceSpec{
		Id:             id.String(),
		Vcpu:           uint32(max(min(s.VCPU, 1<<16), 0)),
		RamMib:         uint64(max(s.RAMMiB, 0)),
		DiskGib:        uint64(max(s.DiskGiB, 0)),
		AcceleratorIds: accelerators,
		Image: &agentv1.ImageRef{
			Name: s.Image.Name, Version: s.Image.Version, Arch: s.Image.Arch, Digest: s.Image.Digest,
			Url: s.Image.URL, SizeBytes: uint64(max(s.Image.SizeBytes, 0)), Format: s.Image.Format,
		},
		SshPublicKeys: s.SSHPublicKeys,
		UserData:      []byte(s.UserData),
		Hostname:      hostname,
	}
	for _, port := range s.Ports {
		proto := agentv1.Protocol_PROTOCOL_TCP
		if port.Protocol == "udp" {
			proto = agentv1.Protocol_PROTOCOL_UDP
		}
		p.Ports = append(p.Ports, &agentv1.PortSpec{GuestPort: uint32(max(min(port.GuestPort, 65535), 0)), Protocol: proto})
	}
	return p
}

// ingestInventory upserts the node's capacity and accelerators. An
// accelerator that disappeared is deleted, unless an active allocation holds
// it: then it is kept and marked missing.
func ingestInventory(ctx context.Context, tx store.Tx, nodeID uuid.UUID, driver string, inv *agentv1.Inventory) error {
	arch := inv.GetArch()
	if arch != "amd64" && arch != "arm64" {
		arch = ""
	}
	labels, err := json.Marshal(maps.Clone(inv.GetLabels()))
	if err != nil {
		return err
	}
	if labels == nil || string(labels) == "null" {
		labels = []byte("{}")
	}
	type cached struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Digest  string `json:"digest"`
	}
	images := []cached{}
	for _, c := range inv.GetCachedImages() {
		images = append(images, cached{Name: c.GetName(), Version: c.GetVersion(), Digest: c.GetDigest()})
	}
	if _, err := tx.UpdateNodeInventory(ctx, db.UpdateNodeInventoryParams{
		ID: nodeID, Hostname: inv.GetHostname(), Arch: arch, Os: inv.GetOs(), Kernel: inv.GetKernel(),
		Driver: driver, AdvertiseAddress: inv.GetAdvertiseAddress(), Labels: labels, CachedImages: store.MustJSON(images),
	}); err != nil {
		return err
	}
	tot, res := inv.GetTotal(), inv.GetReserved()
	if err := tx.UpsertNodeCapacity(ctx, db.UpsertNodeCapacityParams{
		NodeID:    nodeID,
		VcpuTotal: clampI32(tot.GetVcpu()), VcpuReserved: clampI32(res.GetVcpu()),
		RamMibTotal: clampGen(tot.GetRamMib()), RamMibReserved: clampGen(res.GetRamMib()),
		DiskGibTotal: clampGen(tot.GetDiskGib()), DiskGibReserved: clampGen(res.GetDiskGib()),
	}); err != nil {
		return err
	}

	reported := map[string]bool{}
	for _, a := range inv.GetAccelerators() {
		if a.GetId() == "" {
			continue
		}
		reported[a.GetId()] = true
		health := "unavailable"
		if a.GetHealth() == agentv1.AcceleratorHealth_ACCELERATOR_HEALTH_OK {
			health = "ok"
		}
		if _, err := tx.UpsertAccelerator(ctx, db.UpsertAcceleratorParams{
			NodeID: nodeID, LocalID: a.GetId(), Kind: a.GetKind(), Model: a.GetModel(), MemMib: clampGen(a.GetMemMib()),
			PciAddress: a.GetPciAddress(), NumaNode: a.GetNumaNode(), IommuGroup: a.GetIommuGroup(),
			HostDriver: a.GetHostDriver(), Health: health, HealthReason: a.GetHealthReason(),
		}); err != nil {
			return err
		}
	}
	existing, err := tx.ListAcceleratorsByNode(ctx, nodeID)
	if err != nil {
		return err
	}
	inUseList, err := tx.ListAcceleratorLocalIDsInUse(ctx, nodeID)
	if err != nil {
		return err
	}
	inUse := map[string]bool{}
	for _, id := range inUseList {
		inUse[id] = true
	}
	for _, a := range existing {
		if reported[a.LocalID] {
			continue
		}
		p := db.MarkAcceleratorMissingParams{NodeID: nodeID, LocalID: a.LocalID}
		if inUse[a.LocalID] {
			err = tx.MarkAcceleratorMissing(ctx, p)
		} else {
			err = tx.DeleteAccelerator(ctx, db.DeleteAcceleratorParams(p))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func clampGen(v uint64) int64 { return int64(min(v, 1<<62)) }

func clampI32(v uint32) int32 { return int32(min(v, 1<<30)) }
