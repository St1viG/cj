package api

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/St1viG/cj/gen/go/kondukter/api/v1"
	"github.com/St1viG/cj/internal/controlplane/accounts"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

const maxKeyName = 64

type accountHandler struct{ *API }

func keyName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxKeyName {
		return "", invalid("name: 1 to 64 characters")
	}
	return s, nil
}

// CreateAPIKey returns the secret once. A replay with the same idempotency
// key returns the key without its secret, which is not stored anywhere.
func (h accountHandler) CreateAPIKey(ctx context.Context, req *connect.Request[apiv1.CreateAPIKeyRequest]) (*connect.Response[apiv1.CreateAPIKeyResponse], error) {
	p := mustPrincipal(ctx)
	name, err := keyName(req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	resp := &apiv1.CreateAPIKeyResponse{}
	err = h.Store.WithTx(ctx, func(tx store.Tx) error {
		err := idempotent(ctx, tx, p.Org.ID, "CreateAPIKey/"+p.User.ID.String(), req.Msg.GetIdempotencyKey(), req.Msg, resp, func() (proto.Message, error) {
			secret, prefix, hash := accounts.NewAPIKey()
			k, err := tx.CreateAPIKey(ctx, db.CreateAPIKeyParams{OrgID: p.Org.ID, UserID: p.User.ID, Name: name, Prefix: prefix, SecretHash: hash})
			if err != nil {
				return nil, err
			}
			resp.ApiKey, resp.Secret = apiKeyProto(k), secret
			return &apiv1.CreateAPIKeyResponse{ApiKey: resp.ApiKey}, nil // never store the secret
		})
		return err
	})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	return connect.NewResponse(resp), nil
}

func (h accountHandler) ListAPIKeys(ctx context.Context, _ *connect.Request[apiv1.ListAPIKeysRequest]) (*connect.Response[apiv1.ListAPIKeysResponse], error) {
	p := mustPrincipal(ctx)
	keys, err := h.Store.ListAPIKeys(ctx, db.ListAPIKeysParams{OrgID: p.Org.ID, UserID: p.User.ID})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.ListAPIKeysResponse{}
	for _, k := range keys {
		out.ApiKeys = append(out.ApiKeys, apiKeyProto(k))
	}
	return connect.NewResponse(out), nil
}

func (h accountHandler) RevokeAPIKey(ctx context.Context, req *connect.Request[apiv1.RevokeAPIKeyRequest]) (*connect.Response[apiv1.RevokeAPIKeyResponse], error) {
	p := mustPrincipal(ctx)
	id, err := parseID(req.Msg.GetId(), "API key")
	if err != nil {
		return nil, err
	}
	resp := &apiv1.RevokeAPIKeyResponse{}
	err = h.Store.WithTx(ctx, func(tx store.Tx) error {
		err := idempotent(ctx, tx, p.Org.ID, "RevokeAPIKey/"+p.User.ID.String(), req.Msg.GetIdempotencyKey(), req.Msg, resp, func() (proto.Message, error) {
			k, err := tx.RevokeAPIKey(ctx, db.RevokeAPIKeyParams{ID: id, OrgID: p.Org.ID, UserID: p.User.ID})
			if err != nil {
				if errors.Is(store.NotFound(err), store.ErrNotFound) {
					return nil, notFound("API key")
				}
				return nil, err
			}
			resp.ApiKey = apiKeyProto(k)
			return resp, nil
		})
		return err
	})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	return connect.NewResponse(resp), nil
}

func (h accountHandler) AddSSHKey(ctx context.Context, req *connect.Request[apiv1.AddSSHKeyRequest]) (*connect.Response[apiv1.AddSSHKeyResponse], error) {
	p := mustPrincipal(ctx)
	name, err := keyName(req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(req.Msg.GetPublicKey())
	pub, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || len(strings.TrimSpace(string(rest))) > 0 {
		return nil, invalid("public_key: one OpenSSH public key, as in authorized_keys")
	}
	resp := &apiv1.AddSSHKeyResponse{}
	err = h.Store.WithTx(ctx, func(tx store.Tx) error {
		err := idempotent(ctx, tx, p.Org.ID, "AddSSHKey", req.Msg.GetIdempotencyKey(), req.Msg, resp, func() (proto.Message, error) {
			k, err := tx.AddSSHKey(ctx, db.AddSSHKeyParams{
				OrgID: p.Org.ID, UserID: p.User.ID, Name: name, PublicKey: line, Fingerprint: ssh.FingerprintSHA256(pub),
			})
			if err != nil {
				if store.IsUniqueViolation(err, "") {
					return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("this key is already in the account"))
				}
				return nil, err
			}
			resp.SshKey = sshKeyProto(k)
			return resp, nil
		})
		return err
	})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	return connect.NewResponse(resp), nil
}

func (h accountHandler) ListSSHKeys(ctx context.Context, _ *connect.Request[apiv1.ListSSHKeysRequest]) (*connect.Response[apiv1.ListSSHKeysResponse], error) {
	p := mustPrincipal(ctx)
	keys, err := h.Store.ListSSHKeys(ctx, p.Org.ID)
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	out := &apiv1.ListSSHKeysResponse{}
	for _, k := range keys {
		out.SshKeys = append(out.SshKeys, sshKeyProto(k))
	}
	return connect.NewResponse(out), nil
}

func (h accountHandler) DeleteSSHKey(ctx context.Context, req *connect.Request[apiv1.DeleteSSHKeyRequest]) (*connect.Response[apiv1.DeleteSSHKeyResponse], error) {
	p := mustPrincipal(ctx)
	id, err := parseID(req.Msg.GetId(), "SSH key")
	if err != nil {
		return nil, err
	}
	resp := &apiv1.DeleteSSHKeyResponse{}
	err = h.Store.WithTx(ctx, func(tx store.Tx) error {
		err := idempotent(ctx, tx, p.Org.ID, "DeleteSSHKey", req.Msg.GetIdempotencyKey(), req.Msg, resp, func() (proto.Message, error) {
			n, err := tx.DeleteSSHKey(ctx, db.DeleteSSHKeyParams{ID: id, OrgID: p.Org.ID})
			if err != nil {
				return nil, err
			}
			if n == 0 {
				return nil, notFound("SSH key")
			}
			return resp, nil
		})
		return err
	})
	if err != nil {
		return nil, h.internal(ctx, err)
	}
	return connect.NewResponse(resp), nil
}
