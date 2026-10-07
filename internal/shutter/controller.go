package shutter

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Controller struct {
	mu       sync.Mutex
	driver   *Driver
	feedback *Feedback
	commands []string
}

func NewController(driver *Driver, feedback *Feedback) *Controller {
	return &Controller{driver: driver, feedback: feedback, commands: make([]string, 0)}
}

func (c *Controller) Open(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := c.driver.Open(); err != nil {
		return fmt.Errorf("open shutter: %w", err)
	}
	c.feedback.Update("open")
	c.record("open")
	return nil
}

func (c *Controller) Close(ctx context.Context) error {
	if err := CloseSafely(ctx, c.driver); err != nil {
		return fmt.Errorf("close shutter: %w", err)
	}
	c.feedback.Update("closed")
	c.record("closed")
	return nil
}

func (c *Controller) record(value string) {
	c.mu.Lock()
	c.commands = append(c.commands, fmt.Sprintf("%s:%s", time.Now().UTC().Format(time.RFC3339Nano), value))
	c.mu.Unlock()
}

func (c *Controller) Commands() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.commands...)
}

func (c *Controller) Feedback() *Feedback { return c.feedback }
