package docker

import (
	"context"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
)

// ContainerCache caches the container list with a TTL.
// Uses a double-checked locking pattern to avoid the thundering herd problem.
type ContainerCache struct {
	mu        sync.RWMutex
	data      []container.Summary
	fetchedAt time.Time
	ttl       time.Duration
	client    *Client
}

// NewContainerCache creates a cache with the given TTL.
func NewContainerCache(client *Client, ttl time.Duration) *ContainerCache {
	return &ContainerCache{
		client: client,
		ttl:    ttl,
	}
}

// List returns the container list, from the cache if still valid.
func (c *ContainerCache) List(ctx context.Context) ([]container.Summary, error) {
	c.mu.RLock()
	if c.data != nil && time.Since(c.fetchedAt) < c.ttl {
		defer c.mu.RUnlock()
		return c.data, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring the write lock
	if c.data != nil && time.Since(c.fetchedAt) < c.ttl {
		return c.data, nil
	}

	containers, err := c.client.ListContainers(ctx)
	if err != nil {
		return nil, err
	}

	c.data = containers
	c.fetchedAt = time.Now()
	return containers, nil
}

// Invalidate forces the next call to List to query Docker.
func (c *ContainerCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = nil
	c.fetchedAt = time.Time{}
}
