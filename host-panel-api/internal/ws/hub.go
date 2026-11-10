package ws

import (
	"log/slog"
	"sync"
)

// Hub manages the registry of active WebSocket connections.
type Hub struct {
	mu     sync.Mutex
	conns  map[string]*Conn
	logger *slog.Logger
}

// NewHub creates a WebSocket connection hub.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		conns:  make(map[string]*Conn),
		logger: logger,
	}
}

// Register adds a connection to the hub.
func (h *Hub) Register(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[c.ID] = c
	h.logger.Info("websocket connected", "id", c.ID, "email", c.User.Email)
}

// Unregister removes a connection from the hub.
func (h *Hub) Unregister(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, c.ID)
	h.logger.Info("websocket disconnected", "id", c.ID, "email", c.User.Email)
}
