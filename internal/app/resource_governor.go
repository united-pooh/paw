package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrResourceCapacity = errors.New("resource_capacity")

type ResourceGovernor struct {
	workerSlots chan struct{}
}

func NewResourceGovernor(maxWorkers int) *ResourceGovernor {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	return &ResourceGovernor{workerSlots: make(chan struct{}, maxWorkers)}
}

func (g *ResourceGovernor) AcquireWorker(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case g.workerSlots <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-g.workerSlots })
		}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrResourceCapacity, context.Cause(ctx))
	}
}
