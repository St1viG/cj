package api

import (
	"context"
	"encoding/json"
	"strconv"

	"google.golang.org/protobuf/types/known/structpb"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Audit actions (ADR-0021): every Partner and Admin mutation, and customer
// terminations.
const (
	ActionCreateEnrollmentToken = "partner.create_enrollment_token"
	ActionDrainNode             = "partner.drain_node"
	ActionUndrainNode           = "partner.undrain_node"
	ActionQuarantineNode        = "admin.quarantine_node"
	ActionUnquarantineNode      = "admin.unquarantine_node"
	ActionForceTerminate        = "admin.force_terminate_instance"
	ActionTerminateInstance     = "instance.terminate"
)

// audit appends an audit_log row for p's action on target ("<type>/<id>")
// inside tx, so the record commits exactly when the change does.
func audit(ctx context.Context, tx store.Tx, p Principal, action, target string, payload map[string]any) error {
	actorType := "user"
	if p.APIKeyID != nil {
		actorType = "api_key"
		if payload == nil {
			payload = map[string]any{}
		}
		payload["api_key_id"] = p.APIKeyID.String()
	}
	raw := []byte("{}")
	if len(payload) > 0 {
		var err error
		if raw, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	_, err := tx.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ActorType: actorType, ActorID: p.User.ID.String(), ActorDisplay: p.User.Email,
		Action: action, Target: target, Payload: raw,
	})
	return err
}

func auditProto(e db.AuditLog) *apiv1.AuditEntry {
	out := &apiv1.AuditEntry{
		Id: strconv.FormatInt(e.ID, 10), ActorType: e.ActorType, ActorId: e.ActorID, ActorDisplay: e.ActorDisplay,
		Action: e.Action, Target: e.Target, At: ts(e.At),
	}
	var m map[string]any
	if json.Unmarshal(e.Payload, &m) == nil && len(m) > 0 {
		if s, err := structpb.NewStruct(m); err == nil {
			out.Payload = s
		}
	}
	return out
}
