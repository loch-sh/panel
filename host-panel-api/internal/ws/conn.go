package ws

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/coder/websocket"

	"host-panel-api/internal/model"
)

// Message represents a message exchanged over the WebSocket.
type Message struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// Conn wraps a WebSocket connection with the associated user.
type Conn struct {
	WS     *websocket.Conn
	User   *model.User
	APIKey *model.APIKey // nil for JWT sessions
	ID     string
	Ctx    context.Context
	Cancel context.CancelFunc

	mu sync.Mutex // protects concurrent writes
}

// NewConn creates a WebSocket connection wrapper.
func NewConn(ws *websocket.Conn, user *model.User, id string) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{
		WS:     ws,
		User:   user,
		ID:     id,
		Ctx:    ctx,
		Cancel: cancel,
	}
}

// ReadMessage reads and decodes a JSON message from the WebSocket.
func (c *Conn) ReadMessage() (*Message, error) {
	_, data, err := c.WS.Read(c.Ctx)
	if err != nil {
		return nil, err
	}

	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// WriteEvent sends an event with data to the client. Delivery is best-effort:
// a write failure means the client is gone, so the connection context is
// cancelled, which stops the streams feeding it and ends the read loop. The
// data are the API's own types: a marshalling failure drops the event.
func (c *Conn) WriteEvent(event string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}

	payload, err := json.Marshal(Message{Event: event, Data: raw})
	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.WS.Write(c.Ctx, websocket.MessageText, payload); err != nil {
		c.Cancel()
	}
}

// WriteBinary sends a raw binary message over the WebSocket (mutex-protected).
func (c *Conn) WriteBinary(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.WS.Write(c.Ctx, websocket.MessageBinary, data)
}

// ReadRaw reads a message and returns the type and raw bytes without JSON parsing.
func (c *Conn) ReadRaw() (websocket.MessageType, []byte, error) {
	return c.WS.Read(c.Ctx)
}

// Close closes the WebSocket connection.
func (c *Conn) Close() {
	c.Cancel()
	c.WS.Close(websocket.StatusNormalClosure, "")
}
