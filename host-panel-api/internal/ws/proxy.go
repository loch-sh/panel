package ws

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"host-panel-api/internal/docker"
)

const (
	maxMappings  = 10
	maxStreams   = 256
	relayBufSize = 32 * 1024
)

// PortMapping represents a remote-to-local port mapping.
type PortMapping struct {
	Remote uint16 `json:"remote"`
	Local  uint16 `json:"local"`
}

// ProxySession manages multiplexed TCP streams over a single WebSocket connection.
type ProxySession struct {
	conn        *Conn
	docker      *docker.Client
	containerID string
	mappings    []PortMapping
	streams     map[uint32]*proxyStream
	mu          sync.Mutex
	logger      *slog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
}

type proxyStream struct {
	id         uint32
	tcpConn    net.Conn
	cancel     context.CancelFunc
	releaseNet func()
	closeOnce  sync.Once
}

// close cancels the stream, closes the TCP connection, and releases the
// bridged network. Safe to call multiple times (idempotent via sync.Once).
func (ps *proxyStream) close() {
	ps.closeOnce.Do(func() {
		ps.cancel()
		ps.tcpConn.Close()
		if ps.releaseNet != nil {
			ps.releaseNet()
		}
	})
}

// NewProxySession creates a proxy session after validating the configuration.
func NewProxySession(conn *Conn, dockerClient *docker.Client, containerID string, mappings []PortMapping, logger *slog.Logger) (*ProxySession, error) {
	if len(mappings) == 0 {
		return nil, fmt.Errorf("no port mappings provided")
	}
	if len(mappings) > maxMappings {
		return nil, fmt.Errorf("too many port mappings (max %d)", maxMappings)
	}

	seen := make(map[uint16]bool)
	for _, m := range mappings {
		if m.Remote == 0 {
			return nil, fmt.Errorf("invalid remote port 0")
		}
		if seen[m.Remote] {
			return nil, fmt.Errorf("duplicate remote port %d", m.Remote)
		}
		seen[m.Remote] = true
	}

	ctx, cancel := context.WithCancel(conn.Ctx)
	return &ProxySession{
		conn:        conn,
		docker:      dockerClient,
		containerID: containerID,
		mappings:    mappings,
		streams:     make(map[uint32]*proxyStream),
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
	}, nil
}

// IsAllowedPort checks if a remote port is in the declared mappings.
func (s *ProxySession) IsAllowedPort(port uint16) bool {
	for _, m := range s.mappings {
		if m.Remote == port {
			return true
		}
	}
	return false
}

// HandleStreamOpen dials the container on the given port and starts relaying data.
func (s *ProxySession) HandleStreamOpen(streamID uint32, remotePort uint16) {
	if !s.IsAllowedPort(remotePort) {
		s.conn.WriteEvent("streamError", map[string]any{"streamID": streamID, "error": fmt.Sprintf("port %d not in declared mappings", remotePort)})
		return
	}

	s.mu.Lock()
	if len(s.streams) >= maxStreams {
		s.mu.Unlock()
		s.conn.WriteEvent("streamError", map[string]any{"streamID": streamID, "error": "too many concurrent streams"})
		return
	}
	if _, exists := s.streams[streamID]; exists {
		s.mu.Unlock()
		s.conn.WriteEvent("streamError", map[string]any{"streamID": streamID, "error": "stream ID already in use"})
		return
	}
	// Reserve the slot to prevent TOCTOU race during dial
	s.streams[streamID] = nil
	s.mu.Unlock()

	tcpConn, releaseNet, err := s.docker.DialContainer(s.ctx, s.containerID, remotePort)
	if err != nil {
		s.mu.Lock()
		delete(s.streams, streamID)
		s.mu.Unlock()
		s.logger.Warn("proxy dial failed", "streamID", streamID, "port", remotePort, "error", err)
		s.conn.WriteEvent("streamError", map[string]any{"streamID": streamID, "error": "port not reachable"})
		return
	}

	streamCtx, streamCancel := context.WithCancel(s.ctx)
	stream := &proxyStream{
		id:         streamID,
		tcpConn:    tcpConn,
		cancel:     streamCancel,
		releaseNet: releaseNet,
	}

	s.mu.Lock()
	s.streams[streamID] = stream
	s.mu.Unlock()

	s.conn.WriteEvent("streamOpened", map[string]any{"streamID": streamID})
	s.logger.Info("proxy stream opened", "streamID", streamID, "port", remotePort)

	// Close stream when context is cancelled to unblock Read
	context.AfterFunc(streamCtx, func() { stream.close() })

	go s.relayFromTCP(stream)
}

// HandleBinaryData writes payload to the matching TCP connection.
func (s *ProxySession) HandleBinaryData(streamID uint32, payload []byte) {
	s.mu.Lock()
	stream, ok := s.streams[streamID]
	s.mu.Unlock()

	if !ok || stream == nil {
		return
	}

	if _, err := stream.tcpConn.Write(payload); err != nil {
		s.logger.Warn("proxy tcp write failed", "streamID", streamID, "error", err)
		s.removeStream(streamID)
		s.conn.WriteEvent("streamClose", map[string]any{"streamID": streamID})
	}
}

// HandleStreamClose closes a specific stream.
func (s *ProxySession) HandleStreamClose(streamID uint32) {
	s.removeStream(streamID)
}

// Close terminates all streams and the session.
func (s *ProxySession) Close() {
	s.cancel()

	s.mu.Lock()
	defer s.mu.Unlock()

	for id, stream := range s.streams {
		if stream != nil {
			stream.close()
		}
		delete(s.streams, id)
	}
	s.logger.Info("proxy session closed")
}

func (s *ProxySession) removeStream(streamID uint32) {
	s.mu.Lock()
	stream, ok := s.streams[streamID]
	if ok {
		delete(s.streams, streamID)
	}
	s.mu.Unlock()

	if ok && stream != nil {
		stream.close()
		s.logger.Info("proxy stream closed", "streamID", streamID)
	}
}

// relayFromTCP reads from the TCP connection and sends binary frames over the WebSocket.
func (s *ProxySession) relayFromTCP(stream *proxyStream) {
	defer func() {
		s.removeStream(stream.id)
		s.conn.WriteEvent("streamClose", map[string]any{"streamID": stream.id})
	}()

	// Combined buffer: [streamID(4)][payload(up to relayBufSize)]
	// Avoids per-read allocation and copy
	combined := make([]byte, 4+relayBufSize)
	binary.BigEndian.PutUint32(combined[:4], stream.id)
	payload := combined[4:]

	for {
		n, err := stream.tcpConn.Read(payload)
		if n > 0 {
			if wErr := s.conn.WriteBinary(combined[:4+n]); wErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
