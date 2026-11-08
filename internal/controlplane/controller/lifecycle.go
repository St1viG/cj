package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// Instance timers (SPEC §6.3). The scheduling timer, PENDING for 60 s →
// FAILED(no_capacity), belongs to the scheduler: it fails an instance only
// after a pass that found no node.
const (
	ProvisionTimeout     = 10 * time.Minute
	LostTimeout          = 15 * time.Minute
	DefaultTimerInterval = 5 * time.Second

	ReasonProvisionTimeout = "provision_timeout"
	ReasonLostTimeout      = "unreachable_timeout"
	ReasonTerminate        = "terminated by request"
)

// ErrTerminal is returned by Terminate for an instance that already ended.
var ErrTerminal = errors.New("controller: instance already ended")

// InstanceController applies the instance timers and customer actions.
type InstanceController struct {
	Store *store.Store
	Log   *slog.Logger
	Hooks Hooks
	// Now is the clock for the timers; time.Now if nil.
	Now func() time.Time
	// Interval between sweeps; DefaultTimerInterval if zero.
	Interval time.Duration
	// ProvisionTimeout and LostTimeout override the defaults when set.
	ProvisionTimeout time.Duration
	LostTimeout      time.Duration
}

// Terminate stops an instance. A placed instance goes to TERMINATING: it
// is dropped from its node's snapshot, the agent deletes it, and its
// deletion report ends it TERMINATED. A PENDING one ends TERMINATED at
// once. Terminating a TERMINATING instance is a no-op; an instance that
// already ended returns ErrTerminal.
func (c *InstanceController) Terminate(ctx context.Context, id uuid.UUID, reason string) (db.Instance, error) {
	if reason == "" {
		reason = ReasonTerminate
	}
	var (
		inst db.Instance
		eff  Effects
	)
	err := c.Store.WithTx(ctx, func(tx store.Tx) error {
		cur, err := tx.GetInstanceForUpdate(ctx, id)
		if err != nil {
			return store.NotFound(err)
		}
		switch {
		case Terminal(cur.Phase):
			inst = cur
			return ErrTerminal
		case cur.Phase == db.InstancePhaseTerminating:
			inst = cur
			return nil
		}
		inst, eff, err = Transition(ctx, tx, cur, EvTerminate, reason)
		return err
	})
	if err != nil {
		return inst, err
	}
	c.notify([]uuid.UUID{inst.ID}, eff)
	return inst, nil
}

// TimerReport counts what a sweep changed.
type TimerReport struct {
	ProvisionTimedOut int
	Lost              int
}

// Run sweeps every Interval until ctx is canceled. Sweep errors are logged;
// the next sweep retries.
func (c *InstanceController) Run(ctx context.Context) error {
	every := c.Interval
	if every <= 0 {
		every = DefaultTimerInterval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		if _, err := c.Sweep(ctx); err != nil && ctx.Err() == nil {
			c.Log.Error("instance timer sweep failed", "err", err)
		}
	}
}

// Sweep applies the timers once: SCHEDULED or PROVISIONING for longer than
// ProvisionTimeout since placement → FAILED(provision_timeout); UNREACHABLE
// for longer than LostTimeout → LOST. Both release the allocations and bump
// the node generation, so the agent deletes the VM if it still has it.
func (c *InstanceController) Sweep(ctx context.Context) (TimerReport, error) {
	var rep TimerReport
	now := c.now()
	n, err := c.expire(ctx, func(tx store.Tx) ([]db.Instance, error) {
		return tx.ListProvisionTimedOutForUpdate(ctx, now.Add(-c.provisionTimeout()))
	}, EvProvisionTimeout, ReasonProvisionTimeout)
	rep.ProvisionTimedOut = n
	if err != nil {
		return rep, fmt.Errorf("provision timer: %w", err)
	}
	n, err = c.expire(ctx, func(tx store.Tx) ([]db.Instance, error) {
		return tx.ListUnreachableTimedOutForUpdate(ctx, now.Add(-c.lostTimeout()))
	}, EvLostTimeout, ReasonLostTimeout)
	rep.Lost = n
	if err != nil {
		return rep, fmt.Errorf("lost timer: %w", err)
	}
	return rep, nil
}

// expire applies ev to every instance list returns, in one transaction.
func (c *InstanceController) expire(ctx context.Context, list func(store.Tx) ([]db.Instance, error), ev Event, reason string) (int, error) {
	var (
		changed []uuid.UUID
		nodes   = map[uuid.UUID]bool{}
	)
	err := c.Store.WithTx(ctx, func(tx store.Tx) error {
		changed, nodes = nil, map[uuid.UUID]bool{}
		insts, err := list(tx)
		if err != nil {
			return err
		}
		for _, inst := range insts {
			_, eff, err := Transition(ctx, tx, inst, ev, reason)
			if err != nil {
				return err
			}
			changed = append(changed, inst.ID)
			if eff.NodeChanged != nil {
				nodes[*eff.NodeChanged] = true
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, id := range changed {
		c.Log.Info("instance timer expired", "instance", id, "event", ev)
	}
	c.notify(changed, Effects{})
	for id := range nodes {
		if c.Hooks.NodeChanged != nil {
			c.Hooks.NodeChanged(id)
		}
	}
	return len(changed), nil
}

func (c *InstanceController) notify(instances []uuid.UUID, eff Effects) {
	if c.Hooks.InstanceChanged != nil {
		for _, id := range instances {
			c.Hooks.InstanceChanged(id)
		}
	}
	if eff.NodeChanged != nil && c.Hooks.NodeChanged != nil {
		c.Hooks.NodeChanged(*eff.NodeChanged)
	}
}

func (c *InstanceController) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *InstanceController) provisionTimeout() time.Duration {
	if c.ProvisionTimeout > 0 {
		return c.ProvisionTimeout
	}
	return ProvisionTimeout
}

func (c *InstanceController) lostTimeout() time.Duration {
	if c.LostTimeout > 0 {
		return c.LostTimeout
	}
	return LostTimeout
}
