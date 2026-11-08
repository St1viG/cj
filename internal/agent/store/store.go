// Package store is the agent's crash-safe local state on bbolt (SPEC §7.9,
// ADR-0018): the node identity, the last received snapshot and applied
// generation, the outbox of AgentMessages awaiting an Ack, and host port
// allocations.
//
// bbolt is copy-on-write with an fsync per committed transaction, so after
// kill -9 the file holds exactly the last committed state.
package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
	"google.golang.org/protobuf/proto"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
)

// FileName is the store's file inside the agent data directory.
const FileName = "state.db"

var (
	bucketMeta     = []byte("meta")
	bucketOutbox   = []byte("outbox")
	bucketPorts    = []byte("ports")
	bucketIdentity = []byte("identity")
	// bucketMeter maps instance id → end of its last metered period.
	bucketMeter = []byte("meter")

	keyNodeID            = []byte("node_id")
	keyAppliedGeneration = []byte("applied_generation")
	keySnapshot          = []byte("snapshot")

	keyKey  = []byte("key_pem")
	keyCert = []byte("cert_pem")
	keyCA   = []byte("ca_pem")
)

// ErrLocked means another process holds the store open.
var ErrLocked = errors.New("agent store: locked by another process")

// Store is the open local state database. Safe for concurrent use.
type Store struct {
	db *bolt.DB
}

// Open opens or creates the store at path, creating parent directories
// with mode 0700. It fails with ErrLocked if another agent holds it.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("agent store: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if errors.Is(err, berrors.ErrTimeout) {
		return nil, ErrLocked
	}
	if err != nil {
		return nil, fmt.Errorf("agent store: open %s: %w", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketMeta, bucketOutbox, bucketPorts, bucketIdentity, bucketMeter} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("agent store: init buckets: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database file.
func (s *Store) Close() error { return s.db.Close() }

// Path is the database file path.
func (s *Store) Path() string { return s.db.Path() }

// ---- meta -----------------------------------------------------------------

// NodeID is the enrolled node id, or "" before enrollment.
func (s *Store) NodeID() (string, error) {
	var id string
	err := s.db.View(func(tx *bolt.Tx) error {
		id = string(tx.Bucket(bucketMeta).Get(keyNodeID))
		return nil
	})
	return id, err
}

// SetNodeID records the enrolled node id.
func (s *Store) SetNodeID(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put(keyNodeID, []byte(id))
	})
}

// AppliedGeneration is the generation of the last snapshot the agent fully
// applied; 0 if none.
func (s *Store) AppliedGeneration() (uint64, error) {
	var g uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		g = decodeUint(tx.Bucket(bucketMeta).Get(keyAppliedGeneration))
		return nil
	})
	return g, err
}

// SetAppliedGeneration records that every instance of generation g has been
// attempted. It never moves the generation backwards.
func (s *Store) SetAppliedGeneration(g uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMeta)
		if g < decodeUint(b.Get(keyAppliedGeneration)) {
			return nil
		}
		return b.Put(keyAppliedGeneration, encodeUint(g))
	})
}

// Snapshot is the last DesiredState received from the gateway, or nil if
// none was ever received (the agent is then fail-static).
func (s *Store) Snapshot() (*agentv1.DesiredState, error) {
	var ds *agentv1.DesiredState
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketMeta).Get(keySnapshot)
		if raw == nil {
			return nil
		}
		ds = &agentv1.DesiredState{}
		return proto.Unmarshal(raw, ds)
	})
	if err != nil {
		return nil, fmt.Errorf("agent store: snapshot: %w", err)
	}
	return ds, nil
}

// SetSnapshot stores a newly received snapshot.
func (s *Store) SetSnapshot(ds *agentv1.DesiredState) error {
	raw, err := proto.Marshal(ds)
	if err != nil {
		return fmt.Errorf("agent store: marshal snapshot: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put(keySnapshot, raw)
	})
}

// ---- outbox ---------------------------------------------------------------

// Metered returns, per instance, the end of the last period metered.
func (s *Store) Metered() (map[string]time.Time, error) {
	out := map[string]time.Time{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeter).ForEach(func(k, v []byte) error {
			var t time.Time
			if err := t.UnmarshalBinary(v); err != nil {
				return fmt.Errorf("meter mark %s: %w", k, err)
			}
			out[string(k)] = t
			return nil
		})
	})
	return out, err
}

// AppendUsage appends msgs to the outbox and, in the same transaction, sets
// the metering marks and forgets the given instances: after a crash the
// records and the marks agree, so no period is lost (re-sending one is
// harmless, ingest is idempotent).
func (s *Store) AppendUsage(msgs []*agentv1.AgentMessage, marks map[string]time.Time, forget []string) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketOutbox)
		for _, msg := range msgs {
			n, err := b.NextSequence()
			if err != nil {
				return err
			}
			m := proto.Clone(msg).(*agentv1.AgentMessage)
			m.Seq = n
			raw, err := proto.Marshal(m)
			if err != nil {
				return err
			}
			if err := b.Put(encodeUint(n), raw); err != nil {
				return err
			}
			msg.Seq = n
		}
		mb := tx.Bucket(bucketMeter)
		for id, t := range marks {
			v, err := t.MarshalBinary()
			if err != nil {
				return err
			}
			if err := mb.Put([]byte(id), v); err != nil {
				return err
			}
		}
		for _, id := range forget {
			if err := mb.Delete([]byte(id)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("agent store: append usage: %w", err)
	}
	return nil
}

// Append assigns msg the next sequence number, persists it and returns the
// number. Sequence numbers start at 1, are monotonic and survive restarts
// (they come from the bucket's persisted sequence counter).
func (s *Store) Append(msg *agentv1.AgentMessage) (uint64, error) {
	var seq uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketOutbox)
		n, err := b.NextSequence()
		if err != nil {
			return err
		}
		seq = n
		m := proto.Clone(msg).(*agentv1.AgentMessage)
		m.Seq = seq
		raw, err := proto.Marshal(m)
		if err != nil {
			return err
		}
		return b.Put(encodeUint(seq), raw)
	})
	if err != nil {
		return 0, fmt.Errorf("agent store: append: %w", err)
	}
	msg.Seq = seq
	return seq, nil
}

// Pending returns up to limit outbox messages with seq > after, in seq
// order. limit <= 0 means no limit.
func (s *Store) Pending(after uint64, limit int) ([]*agentv1.AgentMessage, error) {
	var out []*agentv1.AgentMessage
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketOutbox).Cursor()
		for k, v := c.Seek(encodeUint(after + 1)); k != nil; k, v = c.Next() {
			m := &agentv1.AgentMessage{}
			if err := proto.Unmarshal(v, m); err != nil {
				return fmt.Errorf("seq %d: %w", decodeUint(k), err)
			}
			out = append(out, m)
			if limit > 0 && len(out) == limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("agent store: pending: %w", err)
	}
	return out, nil
}

// Ack removes the outbox message with seq, if present, and reports how
// many it removed (0 or 1). Acks are per message (agent.proto Ack).
func (s *Store) Ack(seq uint64) (int, error) {
	n := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketOutbox)
		k := encodeUint(seq)
		if b.Get(k) == nil {
			return nil
		}
		n = 1
		return b.Delete(k)
	})
	if err != nil {
		return 0, fmt.Errorf("agent store: ack: %w", err)
	}
	return n, nil
}

// OutboxStats describes the outbox.
type OutboxStats struct {
	// Len is the number of un-acked messages.
	Len int
	// First and Last are the lowest and highest pending seq (0 if empty).
	First, Last uint64
	// NextSeq is the seq the next Append will get.
	NextSeq uint64
}

// Outbox returns outbox statistics.
func (s *Store) Outbox() (OutboxStats, error) {
	var st OutboxStats
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketOutbox)
		st.Len = b.Stats().KeyN
		st.NextSeq = b.Sequence() + 1
		c := b.Cursor()
		if k, _ := c.First(); k != nil {
			st.First = decodeUint(k)
		}
		if k, _ := c.Last(); k != nil {
			st.Last = decodeUint(k)
		}
		return nil
	})
	return st, err
}

// EvictOldest removes the n oldest outbox messages (used only when the
// outbox exceeds its cap) and returns how many were removed.
func (s *Store) EvictOldest(n int) (int, error) {
	removed := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketOutbox).Cursor()
		for k, _ := c.First(); k != nil && removed < n; k, _ = c.Next() {
			if err := c.Delete(); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	return removed, err
}

// ---- ports ----------------------------------------------------------------

// PortOwner is what a host port is allocated to.
type PortOwner struct {
	InstanceID string `json:"instance_id"`
	GuestPort  int    `json:"guest_port"`
	Protocol   string `json:"protocol"`
}

// Ports returns every allocated host port and its owner.
func (s *Store) Ports() (map[int]PortOwner, error) {
	out := map[int]PortOwner{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketPorts).ForEach(func(k, v []byte) error {
			if len(k) != 2 {
				return fmt.Errorf("port key %x: want 2 bytes", k)
			}
			port := int(binary.BigEndian.Uint16(k))
			var o PortOwner
			if err := json.Unmarshal(v, &o); err != nil {
				return fmt.Errorf("port %d: %w", port, err)
			}
			out[port] = o
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("agent store: ports: %w", err)
	}
	return out, nil
}

// UpdatePorts applies changes to the port table in one transaction: every
// port in release is removed, then every entry in assign is written.
func (s *Store) UpdatePorts(assign map[int]PortOwner, release []int) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketPorts)
		for _, p := range release {
			k, err := portKey(p)
			if err != nil {
				return err
			}
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		for p, o := range assign {
			k, err := portKey(p)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(o)
			if err != nil {
				return err
			}
			if err := b.Put(k, raw); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- identity -------------------------------------------------------------

// Identity is the node's mTLS credentials from enrollment (ADR-0024).
type Identity struct {
	KeyPEM  []byte
	CertPEM []byte
	CAPEM   []byte
}

// Empty reports whether the node has not enrolled yet.
func (id Identity) Empty() bool { return len(id.CertPEM) == 0 }

// Identity returns the stored credentials; Empty() if not enrolled.
func (s *Store) Identity() (Identity, error) {
	var id Identity
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketIdentity)
		id.KeyPEM = clone(b.Get(keyKey))
		id.CertPEM = clone(b.Get(keyCert))
		id.CAPEM = clone(b.Get(keyCA))
		return nil
	})
	return id, err
}

// SetIdentity stores the credentials and the node id in one transaction.
func (s *Store) SetIdentity(nodeID string, id Identity) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketIdentity)
		for k, v := range map[string][]byte{string(keyKey): id.KeyPEM, string(keyCert): id.CertPEM, string(keyCA): id.CAPEM} {
			if err := b.Put([]byte(k), v); err != nil {
				return err
			}
		}
		return tx.Bucket(bucketMeta).Put(keyNodeID, []byte(nodeID))
	})
}

// ---- encoding ---------------------------------------------------------------

func encodeUint(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func decodeUint(b []byte) uint64 {
	if len(b) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

// portKey encodes a TCP/UDP port as a 2-byte big-endian key.
func portKey(p int) ([]byte, error) {
	if p < 1 || p > 65535 {
		return nil, fmt.Errorf("agent store: port %d out of range", p)
	}
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(p))
	return b, nil
}

// clone copies a bbolt value, which is only valid inside its transaction.
func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}
