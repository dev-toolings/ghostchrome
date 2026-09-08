package engine

import (
	"context"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type lifecycleHit struct {
	Name   proto.PageLifecycleEventName
	Frame  proto.PageFrameID
	Loader proto.NetworkLoaderID
}

// LifecycleArm buffers Page.lifecycleEvent until Stop. Arm it before
// Page.navigate so a fast load cannot outrun the waiter.
type LifecycleArm struct {
	mu     sync.Mutex
	hits   []lifecycleHit
	waitCh chan struct{}
	cancel context.CancelFunc
}

// ArmLifecycle enables lifecycle events and starts a persistent listener.
func ArmLifecycle(page *rod.Page) *LifecycleArm {
	if page == nil {
		return nil
	}
	_ = proto.PageSetLifecycleEventsEnabled{Enabled: true}.Call(page)
	ctx, cancel := context.WithCancel(page.GetContext())
	arm := &LifecycleArm{waitCh: make(chan struct{}, 8), cancel: cancel}
	scoped := page.Context(ctx)
	wait := scoped.EachEvent(func(e *proto.PageLifecycleEvent) bool {
		if e == nil {
			return false
		}
		arm.mu.Lock()
		arm.hits = append(arm.hits, lifecycleHit{Name: e.Name, Frame: e.FrameID, Loader: e.LoaderID})
		if len(arm.hits) > 64 {
			arm.hits = arm.hits[len(arm.hits)-64:]
		}
		arm.mu.Unlock()
		select {
		case arm.waitCh <- struct{}{}:
		default:
		}
		return false
	})
	go wait()
	return arm
}

func (a *LifecycleArm) Stop() {
	if a == nil {
		return
	}
	if a.cancel != nil {
		a.cancel()
	}
}

// Wait blocks until a matching lifecycle event is observed for frame+loader.
func (a *LifecycleArm) Wait(name proto.PageLifecycleEventName, frame proto.PageFrameID, loader proto.NetworkLoaderID, timeout time.Duration) error {
	if a == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = NavWaitTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		if a.has(name, frame, loader) {
			return nil
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return context.DeadlineExceeded
		}
		timer := time.NewTimer(remain)
		select {
		case <-a.waitCh:
			timer.Stop()
		case <-timer.C:
			if a.has(name, frame, loader) {
				return nil
			}
			return context.DeadlineExceeded
		}
	}
}

func (a *LifecycleArm) has(name proto.PageLifecycleEventName, frame proto.PageFrameID, loader proto.NetworkLoaderID) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, h := range a.hits {
		if h.Name != name {
			continue
		}
		if frame != "" && h.Frame != frame {
			continue
		}
		if loader != "" && h.Loader != loader {
			continue
		}
		return true
	}
	return false
}
