package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// IdempotencyTTL is how long a key is remembered (ADR-0006).
const IdempotencyTTL = 24 * time.Hour

const maxIdempotencyKey = 255

var errKeyReused = errors.New("idempotency key was already used with a different request")

// idempotent runs do at most once per (org, method, key) within tx. The
// first call claims the key, runs do, and stores what do returns (the
// response, possibly with secrets removed). A retry with the same key and
// the same request unmarshals the stored response into resp instead; a
// different request with the same key is rejected.
// An empty key runs do every time. Because the claim lives in tx, a failed
// do leaves no record, and a concurrent duplicate waits for the first one
// to commit and then replays it.
func idempotent(ctx context.Context, tx store.Tx, org uuid.UUID, method, key string, req, resp proto.Message, do func() (proto.Message, error)) error {
	if key == "" {
		_, err := do()
		return err
	}
	if len(key) > maxIdempotencyKey {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("idempotency_key is longer than 255 bytes"))
	}
	hash, err := requestHash(req)
	if err != nil {
		return err
	}
	params := db.ClaimIdempotencyKeyParams{OrgID: org, Method: method, Key: key, RequestHash: hash}
	for attempt := 0; ; attempt++ {
		_, err := tx.ClaimIdempotencyKey(ctx, params)
		if err == nil {
			break // claimed: first use
		}
		if !errors.Is(store.NotFound(err), store.ErrNotFound) {
			return err
		}
		prev, err := tx.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{OrgID: org, Method: method, Key: key})
		if err != nil {
			return err
		}
		if time.Since(prev.CreatedAt) > IdempotencyTTL && attempt == 0 {
			if err := tx.DeleteIdempotencyKey(ctx, db.DeleteIdempotencyKeyParams{OrgID: org, Method: method, Key: key}); err != nil {
				return err
			}
			continue // expired: reuse the key
		}
		if !bytes.Equal(prev.RequestHash, hash) {
			return connect.NewError(connect.CodeInvalidArgument, errKeyReused)
		}
		if prev.Response == nil {
			return connect.NewError(connect.CodeAborted, errors.New("the first request with this idempotency key is still running"))
		}
		return proto.Unmarshal(prev.Response, resp)
	}

	stored, err := do()
	if err != nil {
		return err
	}
	b, err := proto.Marshal(stored)
	if err != nil {
		return err
	}
	return tx.SetIdempotencyResponse(ctx, db.SetIdempotencyResponseParams{OrgID: org, Method: method, Key: key, Response: b})
}

// requestHash hashes req without its idempotency_key field.
func requestHash(req proto.Message) ([]byte, error) {
	m := proto.Clone(req)
	r := m.ProtoReflect()
	if f := r.Descriptor().Fields().ByName("idempotency_key"); f != nil {
		r.Clear(f)
	}
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return sum[:], nil
}
