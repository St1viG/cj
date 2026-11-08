package api

import (
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

func tsp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func phaseProto(p db.InstancePhase) apiv1.InstancePhase {
	return apiv1.InstancePhase(apiv1.InstancePhase_value["INSTANCE_PHASE_"+strings.ToUpper(string(p))])
}

func capacityClassProto(c string) apiv1.CapacityClass {
	return apiv1.CapacityClass(apiv1.CapacityClass_value["CAPACITY_CLASS_"+strings.ToUpper(c)])
}

func capacityClassName(c apiv1.CapacityClass) string {
	return strings.ToLower(strings.TrimPrefix(c.String(), "CAPACITY_CLASS_"))
}

func protocolProto(p string) apiv1.Protocol {
	if p == "udp" {
		return apiv1.Protocol_PROTOCOL_UDP
	}
	return apiv1.Protocol_PROTOCOL_TCP
}

func shapeProto(s db.Shape) *apiv1.Shape {
	return &apiv1.Shape{
		Id: s.ID.String(), Name: s.Name,
		Vcpu: uint32(max(s.Vcpu, 0)), RamMib: uint64(max(s.RamMib, 0)), DiskGib: uint64(max(s.DiskGib, 0)),
		AcceleratorKind: s.AccelKind, AcceleratorModel: s.AccelModel, AcceleratorCount: uint32(max(s.AccelCount, 0)),
	}
}

func moneyProto(minor int64, currency string) *apiv1.Money {
	return &apiv1.Money{AmountMinor: minor, Currency: currency}
}

func imageProto(i db.Image) *apiv1.Image {
	return &apiv1.Image{
		Id: i.ID.String(), Name: i.Name, Version: i.Version, Arch: i.Arch, Os: i.Os,
		AccelStack: i.AccelStack, CompatibleAcceleratorModels: i.CompatibleModels,
		Digest: i.Digest, SizeBytes: uint64(max(i.SizeBytes, 0)), Status: i.Status,
	}
}

func eventProto(e db.InstanceEvent) *apiv1.InstanceEvent {
	ev := &apiv1.InstanceEvent{ToPhase: phaseProto(e.ToPhase), Reason: e.Reason, At: ts(e.At)}
	if e.FromPhase != nil {
		ev.FromPhase = phaseProto(*e.FromPhase)
	}
	return ev
}

// instanceRow is what GetOrgInstance and ListOrgInstances return.
type instanceRow struct {
	Instance     db.Instance
	Offer        db.Offer
	Shape        db.Shape
	ImageName    string
	ImageVersion string
	OrgName      string
}

func instanceProto(r instanceRow) (*apiv1.Instance, error) {
	i := r.Instance
	spec, err := store.ParseSpec(i.Spec)
	if err != nil {
		return nil, err
	}
	conn, err := store.ParseConnection(i.Connection)
	if err != nil {
		return nil, err
	}
	out := &apiv1.Instance{
		Id: i.ID.String(), OrgId: i.OrgID.String(), OrgName: r.OrgName, Name: i.Name,
		OfferId: i.OfferID.String(), Shape: shapeProto(r.Shape),
		PricePerHour: moneyProto(r.Offer.PricePerHourMinor, r.Offer.Currency), Region: r.Offer.Region,
		ImageId: i.ImageID.String(), ImageName: r.ImageName + " " + r.ImageVersion, Arch: spec.Image.Arch,
		Phase: phaseProto(i.Phase), PhaseReason: i.PhaseReason, PhaseChangedAt: ts(i.PhaseChangedAt),
		CreatedAt: ts(i.CreatedAt), RunningAt: tsp(i.RunningAt), TerminatedAt: tsp(i.TerminatedAt),
	}
	if i.NodeID != nil {
		out.NodeId = i.NodeID.String()
	}
	for _, p := range spec.Ports {
		out.Ports = append(out.Ports, &apiv1.PortSpec{GuestPort: uint32(max(p.GuestPort, 0)), Protocol: protocolProto(p.Protocol)}) //nolint:gosec // validated port numbers
	}
	for _, k := range spec.SSHPublicKeys {
		if pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k)); err == nil {
			out.SshKeyFingerprints = append(out.SshKeyFingerprints, ssh.FingerprintSHA256(pub))
		}
	}
	if i.Phase == db.InstancePhaseRunning && (conn.Host != "" || len(conn.PortMappings) > 0) {
		c := &apiv1.Connection{Host: conn.Host, SshPort: uint32(max(conn.SSHPort, 0)), SshUser: conn.SSHUser, SshCommand: conn.SSHCommand} //nolint:gosec // port numbers
		for _, m := range conn.PortMappings {
			c.PortMappings = append(c.PortMappings, &apiv1.PortMapping{
				GuestPort: uint32(max(m.GuestPort, 0)), Protocol: protocolProto(m.Protocol), //nolint:gosec // port numbers
				HostAddress: m.HostAddress, HostPort: uint32(max(m.HostPort, 0)), //nolint:gosec // port numbers
			})
		}
		out.Connection = c
	}
	return out, nil
}

func principalProto(p Principal) *apiv1.Principal {
	out := &apiv1.Principal{
		User: &apiv1.User{Id: p.User.ID.String(), Email: p.User.Email, DisplayName: p.User.DisplayName},
		Org:  &apiv1.Org{Id: p.Org.ID.String(), Name: p.Org.Name},
		Role: p.Role,
	}
	if p.Org.PartnerID != nil {
		out.PartnerId = p.Org.PartnerID.String()
	}
	return out
}

func apiKeyProto(k db.ApiKey) *apiv1.APIKey {
	return &apiv1.APIKey{
		Id: k.ID.String(), Name: k.Name, Prefix: k.Prefix,
		CreatedAt: ts(k.CreatedAt), LastUsedAt: tsp(k.LastUsedAt), RevokedAt: tsp(k.RevokedAt),
	}
}

func sshKeyProto(k db.SshKey) *apiv1.SSHKey {
	return &apiv1.SSHKey{
		Id: k.ID.String(), Name: k.Name, PublicKey: k.PublicKey, Fingerprint: k.Fingerprint, CreatedAt: ts(k.CreatedAt),
	}
}
