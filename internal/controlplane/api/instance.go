package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/internal/controlplane/controller"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Create-time limits.
const (
	MaxUserDataBytes = 16 << 10
	MaxPorts         = 16
	MaxSSHKeys       = 16
	MaxNameLen       = 63
	defaultPageSize  = 50
	maxPageSize      = 200
	defaultRefresh   = 30 * time.Second
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type instanceHandler struct{ *API }

func (h instanceHandler) CreateInstance(ctx context.Context, req *connect.Request[apiv1.CreateInstanceRequest]) (*connect.Response[apiv1.CreateInstanceResponse], error) {
	p := mustPrincipal(ctx)
	resp := &apiv1.CreateInstanceResponse{}
	var created *uuid.UUID
	err := h.Store.WithTx(ctx, func(tx store.Tx) error {
		created = nil
		err := idempotent(ctx, tx, p.Org.ID, "CreateInstance", req.Msg.GetIdempotencyKey(), req.Msg, resp, func() (proto.Message, error) {
			id, err := h.create(ctx, tx, p, req.Msg)
			if err != nil {
				return nil, err
			}
			row, err := tx.GetOrgInstance(ctx, db.GetOrgInstanceParams{ID: id, OrgID: p.Org.ID})
			if err != nil {
				return nil, err
			}
			if resp.Instance, err = instanceProto(instanceRow(row)); err != nil {
				return nil, err
			}
			created = &id
			return resp, nil
		})
		return err
	})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	if created != nil {
		h.Log.InfoContext(ctx, "instance created", "instance", *created, "org", p.Org.Name, "offer", req.Msg.GetOfferId())
		if h.Scheduler != nil {
			h.Scheduler.Trigger()
		}
		h.published(*created)
	}
	return connect.NewResponse(resp), nil
}

// create validates m and inserts a PENDING instance with its created event.
func (h instanceHandler) create(ctx context.Context, tx store.Tx, p Principal, m *apiv1.CreateInstanceRequest) (uuid.UUID, error) {
	offerID, err := uuid.Parse(m.GetOfferId())
	if err != nil {
		return uuid.Nil, invalid("offer_id: unknown offer")
	}
	offer, err := tx.GetOffer(ctx, offerID)
	if err != nil {
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			return uuid.Nil, invalid("offer_id: unknown offer")
		}
		return uuid.Nil, err
	}
	if !offer.Offer.Active {
		return uuid.Nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("offer_id: the offer is not for sale"))
	}
	imageID, err := uuid.Parse(m.GetImageId())
	if err != nil {
		return uuid.Nil, invalid("image_id: unknown image")
	}
	img, err := tx.GetImage(ctx, imageID)
	if err != nil {
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			return uuid.Nil, invalid("image_id: unknown image")
		}
		return uuid.Nil, err
	}
	if !Compatible(offer.Shape, img) {
		if offer.Shape.AccelCount > 0 {
			return uuid.Nil, invalid(fmt.Sprintf("image_id: %s %s does not support the offer's %s", img.Name, img.Version, offer.Shape.AccelModel))
		}
		return uuid.Nil, invalid(fmt.Sprintf("image_id: %s %s is not a CPU image for this offer", img.Name, img.Version))
	}

	keys, err := h.sshKeys(ctx, tx, p, m)
	if err != nil {
		return uuid.Nil, err
	}
	ports, err := validatePorts(m.GetPorts())
	if err != nil {
		return uuid.Nil, err
	}
	if len(m.GetUserData()) > MaxUserDataBytes {
		return uuid.Nil, invalid(fmt.Sprintf("user_data: %d bytes, at most %d", len(m.GetUserData()), MaxUserDataBytes))
	}
	name := strings.TrimSpace(m.GetName())
	if len(name) > MaxNameLen {
		return uuid.Nil, invalid(fmt.Sprintf("name: at most %d characters", MaxNameLen))
	}
	hostname := ""
	if dnsLabel.MatchString(name) {
		hostname = name
	}

	spec := store.InstanceSpec{
		VCPU: int(offer.Shape.Vcpu), RAMMiB: offer.Shape.RamMib, DiskGiB: offer.Shape.DiskGib,
		Image: store.ImageRef{
			Name: img.Name, Version: img.Version, Arch: img.Arch, Digest: img.Digest,
			URL: img.Url, SizeBytes: img.SizeBytes, Format: img.Format,
		},
		SSHPublicKeys: keys, UserData: m.GetUserData(), Ports: ports, Hostname: hostname,
	}
	inst, err := tx.CreateInstance(ctx, db.CreateInstanceParams{
		OrgID: p.Org.ID, OfferID: offer.Offer.ID, ImageID: img.ID, Name: name,
		Spec: store.MustJSON(spec), CreatedBy: &p.User.ID,
	})
	if err != nil {
		return uuid.Nil, err
	}
	if err := tx.InsertInstanceEvent(ctx, db.InsertInstanceEventParams{
		InstanceID: inst.ID, ToPhase: db.InstancePhasePending, Reason: "created",
	}); err != nil {
		return uuid.Nil, err
	}
	return inst.ID, nil
}

// sshKeys resolves account keys by id and parses inline keys; at least one
// is required, duplicates are dropped.
func (h instanceHandler) sshKeys(ctx context.Context, tx store.Tx, p Principal, m *apiv1.CreateInstanceRequest) ([]string, error) {
	var (
		out  []string
		seen = map[string]bool{}
	)
	add := func(line string) error {
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return err
		}
		if fp := ssh.FingerprintSHA256(pub); !seen[fp] {
			seen[fp] = true
			out = append(out, strings.TrimSpace(line))
		}
		return nil
	}
	if len(m.GetSshKeyIds()) > 0 {
		ids := make([]uuid.UUID, 0, len(m.GetSshKeyIds()))
		for _, s := range m.GetSshKeyIds() {
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, invalid("ssh_key_ids: unknown key " + s)
			}
			ids = append(ids, id)
		}
		rows, err := tx.GetSSHKeysByIDs(ctx, db.GetSSHKeysByIDsParams{OrgID: p.Org.ID, Ids: ids})
		if err != nil {
			return nil, err
		}
		byID := map[uuid.UUID]db.SshKey{}
		for _, r := range rows {
			byID[r.ID] = r
		}
		for _, id := range ids {
			k, ok := byID[id]
			if !ok {
				return nil, invalid("ssh_key_ids: unknown key " + id.String())
			}
			if err := add(k.PublicKey); err != nil {
				return nil, err
			}
		}
	}
	for i, line := range m.GetSshPublicKeys() {
		if err := add(line); err != nil {
			return nil, invalid(fmt.Sprintf("ssh_public_keys[%d]: not an OpenSSH public key", i))
		}
	}
	switch {
	case len(out) == 0:
		return nil, invalid("at least one SSH key is required (ssh_key_ids or ssh_public_keys)")
	case len(out) > MaxSSHKeys:
		return nil, invalid(fmt.Sprintf("at most %d SSH keys", MaxSSHKeys))
	}
	return out, nil
}

func validatePorts(in []*apiv1.PortSpec) ([]store.PortSpec, error) {
	if len(in) > MaxPorts {
		return nil, invalid(fmt.Sprintf("ports: at most %d", MaxPorts))
	}
	var (
		out  []store.PortSpec
		seen = map[store.PortSpec]bool{}
	)
	for i, p := range in {
		proto := "tcp"
		switch p.GetProtocol() {
		case apiv1.Protocol_PROTOCOL_TCP, apiv1.Protocol_PROTOCOL_UNSPECIFIED:
		case apiv1.Protocol_PROTOCOL_UDP:
			proto = "udp"
		default:
			return nil, invalid(fmt.Sprintf("ports[%d]: unknown protocol", i))
		}
		port := p.GetGuestPort()
		switch {
		case port == 0 || port > 65535:
			return nil, invalid(fmt.Sprintf("ports[%d]: guest_port must be 1-65535", i))
		case port == 22 && proto == "tcp":
			return nil, invalid(fmt.Sprintf("ports[%d]: 22/tcp is always exposed", i))
		}
		ps := store.PortSpec{GuestPort: int(port), Protocol: proto}
		if seen[ps] {
			return nil, invalid(fmt.Sprintf("ports[%d]: %d/%s is listed twice", i, port, proto))
		}
		seen[ps] = true
		out = append(out, ps)
	}
	return out, nil
}

func (h instanceHandler) GetInstance(ctx context.Context, req *connect.Request[apiv1.GetInstanceRequest]) (*connect.Response[apiv1.GetInstanceResponse], error) {
	p := mustPrincipal(ctx)
	id, err := parseID(req.Msg.GetId(), "instance")
	if err != nil {
		return nil, err
	}
	inst, err := h.get(ctx, h.Store.Queries, p, id)
	if err != nil {
		return nil, err
	}
	evs, err := h.Store.ListInstanceEvents(ctx, id)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.GetInstanceResponse{Instance: inst}
	for _, e := range evs {
		out.Events = append(out.Events, eventProto(e))
	}
	return connect.NewResponse(out), nil
}

// get reads an instance of the caller's org; another org's is NotFound.
func (h instanceHandler) get(ctx context.Context, q *db.Queries, p Principal, id uuid.UUID) (*apiv1.Instance, error) {
	row, err := q.GetOrgInstance(ctx, db.GetOrgInstanceParams{ID: id, OrgID: p.Org.ID})
	if err != nil {
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			return nil, notFound("instance")
		}
		return nil, h.internal(ctx, err)
	}
	inst, err := instanceProto(instanceRow(row))
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	return inst, nil
}

func (h instanceHandler) ListInstances(ctx context.Context, req *connect.Request[apiv1.ListInstancesRequest]) (*connect.Response[apiv1.ListInstancesResponse], error) {
	p := mustPrincipal(ctx)
	size := pageSize(req.Msg.GetPageSize())
	params := db.ListOrgInstancesParams{OrgID: p.Org.ID, IncludeTerminal: req.Msg.GetIncludeTerminal(), MaxRows: int32(size + 1)} //nolint:gosec // size ≤ maxPageSize
	if tok := req.Msg.GetPageToken(); tok != "" {
		at, id, err := decodePageToken(tok)
		if err != nil {
			return nil, invalid("page_token: malformed")
		}
		params.BeforeCreatedAt, params.BeforeID = &at, &id
	}
	rows, err := h.Store.ListOrgInstances(ctx, params)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.ListInstancesResponse{}
	for i, r := range rows {
		if i == size {
			last := rows[size-1].Instance
			out.NextPageToken = encodePageToken(last.CreatedAt, last.ID)
			break
		}
		inst, err := instanceProto(instanceRow(r))
		if err != nil {
			return nil, h.internal(ctx, err)
		}
		out.Instances = append(out.Instances, inst)
	}
	return connect.NewResponse(out), nil
}

func encodePageToken(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodePageToken(tok string) (time.Time, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	ts, ids, ok := strings.Cut(string(b), "|")
	if !ok {
		return time.Time{}, uuid.Nil, errors.New("no separator")
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(ids)
	return at, id, err
}

func (h instanceHandler) TerminateInstance(ctx context.Context, req *connect.Request[apiv1.TerminateInstanceRequest]) (*connect.Response[apiv1.TerminateInstanceResponse], error) {
	p := mustPrincipal(ctx)
	id, err := parseID(req.Msg.GetId(), "instance")
	if err != nil {
		return nil, err
	}
	resp := &apiv1.TerminateInstanceResponse{}
	var (
		eff     controller.Effects
		changed bool
	)
	err = h.Store.WithTx(ctx, func(tx store.Tx) error {
		changed = false
		err := idempotent(ctx, tx, p.Org.ID, "TerminateInstance", req.Msg.GetIdempotencyKey(), req.Msg, resp, func() (proto.Message, error) {
			// Org check before touching the row.
			if _, err := h.get(ctx, tx.Queries, p, id); err != nil {
				return nil, err
			}
			before, err := tx.GetInstance(ctx, id)
			if err != nil {
				return nil, err
			}
			after, e, err := controller.TerminateTx(ctx, tx, id, "terminated by "+p.User.Email)
			switch {
			case errors.Is(err, controller.ErrTerminal):
				// Already ended: terminating is a desired state, so succeed.
			case err != nil:
				return nil, err
			}
			eff, changed = e, after.Phase != before.Phase
			if err := audit(ctx, tx, p, ActionTerminateInstance, "instance/"+id.String(),
				map[string]any{"from": string(before.Phase), "to": string(after.Phase)}); err != nil {
				return nil, err
			}
			if resp.Instance, err = h.get(ctx, tx.Queries, p, id); err != nil {
				return nil, err
			}
			return resp, nil
		})
		return err
	})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	if changed {
		h.Log.InfoContext(ctx, "instance terminated", "instance", id, "by", p.User.Email)
		if h.Instances != nil {
			h.Instances.Notify(id, eff)
		}
		h.published(id)
	}
	return connect.NewResponse(resp), nil
}

// WatchInstance sends the instance now and after every change until it is
// terminal or the client goes away.
func (h instanceHandler) WatchInstance(ctx context.Context, req *connect.Request[apiv1.WatchInstanceRequest], stream *connect.ServerStream[apiv1.WatchInstanceResponse]) error {
	p := mustPrincipal(ctx)
	id, err := parseID(req.Msg.GetId(), "instance")
	if err != nil {
		return err
	}
	var changed <-chan struct{}
	if h.Bus != nil {
		ch, cancel := h.Bus.Subscribe(id) // before the first read: no change is missed
		defer cancel()
		changed = ch
	}
	refresh := h.WatchRefresh
	if refresh <= 0 {
		refresh = defaultRefresh
	}
	t := time.NewTicker(refresh)
	defer t.Stop()
	var last []byte
	for {
		inst, err := h.get(ctx, h.Store.Queries, p, id)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(inst)
		if err != nil {
			return h.internal(ctx, err)
		}
		if !bytes.Equal(b, last) {
			if err := stream.Send(&apiv1.WatchInstanceResponse{Instance: inst}); err != nil {
				return nil // client went away
			}
			last = b
		}
		switch inst.GetPhase() {
		case apiv1.InstancePhase_INSTANCE_PHASE_TERMINATED, apiv1.InstancePhase_INSTANCE_PHASE_FAILED, apiv1.InstancePhase_INSTANCE_PHASE_LOST:
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-h.stopped:
			return nil
		case <-changed:
		case <-t.C:
		}
	}
}
