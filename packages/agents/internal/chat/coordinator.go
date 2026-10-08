package chat

import (
	"errors"
	"sort"
	"time"

	"go.temporal.io/sdk/workflow"
)

// DefaultCap is the session cap when the worker sets none.
const DefaultCap = 10

// pruneInterval is how often the coordinator drops expired slots.
const pruneInterval = 10 * time.Minute

// Holder is a ChatSession holding a slot.
type Holder struct {
	ID           string
	LastActivity time.Time
	Expires      time.Time
}

// CoordinatorState is SessionCoordinator's input, carried across
// continue-as-new.
type CoordinatorState struct {
	Holders map[string]Holder
	Cap     int
}

// SessionCoordinator keeps the number of running session sandboxes under
// the cap. A ChatSession acquires a slot (AcquireUpdate) before it ensures a
// sandbox, touches it after every turn and releases it when it stops. A slot
// is granted at once; when that puts the holders over the cap, the
// longest-idle other holder loses its slot and is signalled to stop early.
// A holder that vanishes without releasing (terminated, say) keeps its slot
// only until it expires.
func SessionCoordinator(ctx workflow.Context, st CoordinatorState) error {
	if st.Holders == nil {
		st.Holders = map[string]Holder{}
	}
	if st.Cap <= 0 {
		st.Cap = DefaultCap
	}
	c := &coordinator{CoordinatorState: st}

	err := workflow.SetUpdateHandlerWithOptions(ctx, AcquireUpdate,
		func(ctx workflow.Context, req SlotRequest) error {
			c.acquire(ctx, req)
			return nil
		},
		workflow.UpdateHandlerOptions{Validator: func(ctx workflow.Context, req SlotRequest) error {
			if req.Holder == "" {
				return errors.New("no holder")
			}
			return nil
		}})
	if err != nil {
		return err
	}
	err = workflow.SetQueryHandler(ctx, HoldersQuery, func() ([]Holder, error) {
		holders := make([]Holder, 0, len(c.Holders))
		for _, id := range c.ids() {
			holders = append(holders, c.Holders[id])
		}
		return holders, nil
	})
	if err != nil {
		return err
	}
	touch := workflow.GetSignalChannel(ctx, TouchSignal)
	release := workflow.GetSignalChannel(ctx, ReleaseSignal)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var req SlotRequest
			touch.Receive(ctx, &req)
			c.touch(ctx, req)
		}
	})
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var id string
			release.Receive(ctx, &id)
			delete(c.Holders, id)
		}
	})

	for {
		if err := workflow.Sleep(ctx, pruneInterval); err != nil {
			return err
		}
		c.prune(workflow.Now(ctx))
		if workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
				return err
			}
			for {
				var req SlotRequest
				if !touch.ReceiveAsync(&req) {
					break
				}
				c.touch(ctx, req)
			}
			for {
				var id string
				if !release.ReceiveAsync(&id) {
					break
				}
				delete(c.Holders, id)
			}
			return workflow.NewContinueAsNewError(ctx, SessionCoordinator, c.CoordinatorState)
		}
	}
}

type coordinator struct {
	CoordinatorState
}

func (c *coordinator) acquire(ctx workflow.Context, req SlotRequest) {
	now := workflow.Now(ctx)
	if req.Cap > 0 {
		c.Cap = req.Cap
	}
	c.prune(now)
	c.Holders[req.Holder] = Holder{ID: req.Holder, LastActivity: now, Expires: req.Expires}
	for len(c.Holders) > c.Cap {
		victim := c.longestIdle(req.Holder)
		if victim == "" {
			return
		}
		delete(c.Holders, victim)
		workflow.GetLogger(ctx).Info("Session cap reached, evicting the longest idle", "cap", c.Cap, "evicted", victim)
		// A holder that has gone already has nothing to stop.
		_ = workflow.SignalExternalWorkflow(ctx, victim, "", EvictSignal, nil).Get(ctx, nil)
	}
}

// touch records activity on a slot. A holder that lost its slot gets it
// back only by acquiring again.
func (c *coordinator) touch(ctx workflow.Context, req SlotRequest) {
	h, ok := c.Holders[req.Holder]
	if !ok {
		return
	}
	h.LastActivity, h.Expires = workflow.Now(ctx), req.Expires
	c.Holders[req.Holder] = h
}

func (c *coordinator) prune(now time.Time) {
	for _, id := range c.ids() {
		if now.After(c.Holders[id].Expires) {
			delete(c.Holders, id)
		}
	}
}

// longestIdle is the holder other than except with the oldest activity,
// by ID among equals.
func (c *coordinator) longestIdle(except string) string {
	var victim string
	for _, id := range c.ids() {
		if id == except {
			continue
		}
		if victim == "" || c.Holders[id].LastActivity.Before(c.Holders[victim].LastActivity) {
			victim = id
		}
	}
	return victim
}

func (c *coordinator) ids() []string {
	ids := make([]string, 0, len(c.Holders))
	for id := range c.Holders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
