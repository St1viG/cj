package store_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	agentv1 "github.com/St1viG/cj/gen/go/kondukter/agent/v1"
	"github.com/St1viG/cj/internal/agent/store"
)

func open(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func heartbeat(gen uint64) *agentv1.AgentMessage {
	return &agentv1.AgentMessage{Body: &agentv1.AgentMessage_Heartbeat{
		Heartbeat: &agentv1.Heartbeat{AppliedGeneration: gen},
	}}
}

func TestOutboxAppendPendingAck(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), store.FileName))
	defer func() { _ = s.Close() }()

	for i := uint64(1); i <= 5; i++ {
		msg := heartbeat(i)
		seq, err := s.Append(msg)
		if err != nil {
			t.Fatal(err)
		}
		if seq != i || msg.Seq != i {
			t.Fatalf("append #%d: seq=%d msg.Seq=%d", i, seq, msg.Seq)
		}
	}

	all, err := s.Pending(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("pending = %d, want 5", len(all))
	}
	for i, m := range all {
		if m.Seq != uint64(i+1) || m.GetHeartbeat().GetAppliedGeneration() != uint64(i+1) {
			t.Fatalf("pending[%d] = seq %d gen %d", i, m.Seq, m.GetHeartbeat().GetAppliedGeneration())
		}
	}

	page, _ := s.Pending(2, 2)
	if len(page) != 2 || page[0].Seq != 3 || page[1].Seq != 4 {
		t.Fatalf("Pending(2, 2) = %v", seqs(page))
	}

	// Acks are per message: acking 3 leaves 1 and 2 (a lost message is
	// never confirmed by a later one).
	n, err := s.Ack(3)
	if err != nil || n != 1 {
		t.Fatalf("Ack(3) = %d, %v; want 1", n, err)
	}
	if n, _ := s.Ack(3); n != 0 {
		t.Fatalf("repeated Ack(3) pruned %d, want 0", n)
	}
	if got, _ := s.Pending(0, 0); fmt.Sprint(seqs(got)) != "[1 2 4 5]" {
		t.Fatalf("after Ack(3) pending = %v, want [1 2 4 5]", seqs(got))
	}
	for _, seq := range []uint64{1, 2} {
		if n, _ := s.Ack(seq); n != 1 {
			t.Fatalf("Ack(%d) pruned %d", seq, n)
		}
	}
	rest, _ := s.Pending(0, 0)
	if got := seqs(rest); fmt.Sprint(got) != "[4 5]" {
		t.Fatalf("after Ack(1..3) pending = %v, want [4 5]", got)
	}

	st, _ := s.Outbox()
	if st.Len != 2 || st.First != 4 || st.Last != 5 || st.NextSeq != 6 {
		t.Fatalf("stats = %+v", st)
	}

	if n, _ := s.EvictOldest(1); n != 1 {
		t.Fatalf("EvictOldest(1) = %d", n)
	}
	if rest, _ := s.Pending(0, 0); fmt.Sprint(seqs(rest)) != "[5]" {
		t.Fatalf("after evict pending = %v", seqs(rest))
	}
}

func TestSeqMonotonicAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), store.FileName)
	s := open(t, path)
	for range 3 {
		if _, err := s.Append(heartbeat(1)); err != nil {
			t.Fatal(err)
		}
	}
	// Prune everything: the next seq must still continue, not restart.
	for seq := uint64(1); seq <= 3; seq++ {
		if _, err := s.Ack(seq); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()

	s = open(t, path)
	defer func() { _ = s.Close() }()
	seq, err := s.Append(heartbeat(1))
	if err != nil {
		t.Fatal(err)
	}
	if seq != 4 {
		t.Fatalf("seq after reopen = %d, want 4", seq)
	}
}

func TestSnapshotAndGenerationRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), store.FileName)
	s := open(t, path)

	if ds, err := s.Snapshot(); err != nil || ds != nil {
		t.Fatalf("fresh snapshot = %v, %v; want nil", ds, err)
	}
	want := &agentv1.DesiredState{Generation: 7, Instances: []*agentv1.InstanceSpec{{
		Id: "i-1", Vcpu: 2, RamMib: 2048, DiskGib: 20,
		Image:         &agentv1.ImageRef{Name: "ubuntu-cpu", Version: "24.04", Arch: "arm64", Digest: "sha256:ab"},
		SshPublicKeys: []string{"ssh-ed25519 AAAA test"},
		Ports:         []*agentv1.PortSpec{{GuestPort: 8080, Protocol: agentv1.Protocol_PROTOCOL_TCP}},
	}}}
	if err := s.SetSnapshot(want); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAppliedGeneration(7); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAppliedGeneration(5); err != nil { // never goes backwards
		t.Fatal(err)
	}
	if err := s.SetIdentity("node-1", store.Identity{KeyPEM: []byte("k"), CertPEM: []byte("c"), CAPEM: []byte("ca")}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePorts(map[int]store.PortOwner{40001: {InstanceID: "i-1", GuestPort: 22, Protocol: "tcp"}}, nil); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s = open(t, path)
	defer func() { _ = s.Close() }()
	got, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("snapshot round trip:\n got %v\nwant %v", got, want)
	}
	if g, _ := s.AppliedGeneration(); g != 7 {
		t.Fatalf("applied generation = %d, want 7", g)
	}
	if id, _ := s.NodeID(); id != "node-1" {
		t.Fatalf("node id = %q", id)
	}
	if ident, _ := s.Identity(); string(ident.CAPEM) != "ca" || ident.Empty() {
		t.Fatalf("identity = %+v", ident)
	}
	ports, _ := s.Ports()
	if ports[40001].InstanceID != "i-1" || len(ports) != 1 {
		t.Fatalf("ports = %v", ports)
	}
	if err := s.UpdatePorts(nil, []int{40001}); err != nil {
		t.Fatal(err)
	}
	if ports, _ := s.Ports(); len(ports) != 0 {
		t.Fatalf("ports after release = %v", ports)
	}
}

func TestOpenTwiceIsLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), store.FileName)
	s := open(t, path)
	defer func() { _ = s.Close() }()
	if _, err := store.Open(path); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("second Open err = %v, want ErrLocked", err)
	}
}

// TestCrashDuringWrites SIGKILLs a child process that appends, acks and
// writes snapshots in a tight loop, then checks the reopened store is
// consistent: every outbox key decodes to a message carrying that seq, keys
// are contiguous after the pruned prefix, the next seq is above them, and
// the snapshot generation never trails the applied generation by more than
// one write.
func TestCrashDuringWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns subprocesses")
	}
	path := filepath.Join(t.TempDir(), store.FileName)
	lastSeq := uint64(0)
	for iter := range 8 {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHelperCrashWriter$")
		cmd.Env = append(os.Environ(), "KDK_CRASH_WRITER_DB="+path)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(50+iter*25) * time.Millisecond)
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()

		s := open(t, path)
		st, err := s.Outbox()
		if err != nil {
			t.Fatal(err)
		}
		msgs, err := s.Pending(0, 0)
		if err != nil {
			t.Fatalf("iteration %d: outbox unreadable after kill -9: %v", iter, err)
		}
		for i, m := range msgs {
			if i > 0 && m.Seq != msgs[i-1].Seq+1 {
				t.Fatalf("iteration %d: gap in outbox after %d: %d", iter, msgs[i-1].Seq, m.Seq)
			}
			if m.GetHeartbeat() == nil {
				t.Fatalf("iteration %d: seq %d has no body", iter, m.Seq)
			}
		}
		if st.Last != 0 && st.NextSeq <= st.Last {
			t.Fatalf("iteration %d: next seq %d not above last %d", iter, st.NextSeq, st.Last)
		}
		if st.NextSeq-1 < lastSeq {
			t.Fatalf("iteration %d: sequence went backwards: %d < %d", iter, st.NextSeq-1, lastSeq)
		}
		lastSeq = st.NextSeq - 1
		applied, _ := s.AppliedGeneration()
		snap, err := s.Snapshot()
		if err != nil {
			t.Fatalf("iteration %d: snapshot unreadable: %v", iter, err)
		}
		if snap != nil && snap.Generation < applied {
			t.Fatalf("iteration %d: snapshot gen %d < applied %d", iter, snap.Generation, applied)
		}
		_ = s.Close()
	}
	if lastSeq == 0 {
		t.Fatal("child never committed a write; the test proved nothing")
	}
}

// TestHelperCrashWriter is the child half of TestCrashDuringWrites.
func TestHelperCrashWriter(t *testing.T) {
	path := os.Getenv("KDK_CRASH_WRITER_DB")
	if path == "" {
		t.Skip("helper process for TestCrashDuringWrites")
	}
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	gen, _ := s.AppliedGeneration()
	// Ack the prefix in order (acks are per message), so the outbox stays
	// contiguous after the pruned part.
	st, _ := s.Outbox()
	acked := st.NextSeq - 1
	if st.Len > 0 {
		acked = st.First - 1
	}
	for i := 0; ; i++ {
		seq, err := s.Append(heartbeat(gen))
		if err != nil {
			t.Fatal(err)
		}
		if i%5 == 4 {
			for acked < seq-2 {
				acked++
				if _, err := s.Ack(acked); err != nil {
					t.Fatal(err)
				}
			}
		}
		if i%7 == 6 {
			gen++
			if err := s.SetSnapshot(&agentv1.DesiredState{Generation: gen}); err != nil {
				t.Fatal(err)
			}
			if err := s.SetAppliedGeneration(gen); err != nil {
				t.Fatal(err)
			}
		}
		if i%11 == 10 {
			if err := s.UpdatePorts(map[int]store.PortOwner{40000 + i%100: {InstanceID: strconv.Itoa(i)}}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func seqs(ms []*agentv1.AgentMessage) []uint64 {
	out := make([]uint64, len(ms))
	for i, m := range ms {
		out[i] = m.Seq
	}
	return out
}
