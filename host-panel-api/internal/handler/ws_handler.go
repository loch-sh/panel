package handler

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"host-panel-api/internal/auth"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/store"
	"host-panel-api/internal/ws"
)

// generateConnID creates a unique connection ID using crypto/rand.
func generateConnID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// WSHandler handles the WebSocket upgrade and message dispatch.
type WSHandler struct {
	Hub      *ws.Hub
	Docker   *docker.Client
	Cookies  *auth.CookieManager
	Verifier *auth.JWKSVerifier
	Store    store.UserStore
	Logger   *slog.Logger
	Audit    *slog.Logger
	FrontURL string
}

func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Normalized like the CSRF check: FRONT_URL may carry a path or a
		// trailing slash, browsers send "scheme://host[:port]".
		OriginPatterns: []string{middleware.NormalizeOrigin(h.FrontURL)},
	})
	if err != nil {
		h.Logger.Error("websocket upgrade failed", "error", err)
		return
	}

	// Limit incoming message size to 64 KB to prevent memory exhaustion
	wsConn.SetReadLimit(64 * 1024)

	connID, err := generateConnID()
	if err != nil {
		h.Logger.Error("failed to generate connection ID", "error", err)
		wsConn.Close(websocket.StatusInternalError, "internal error")
		return
	}
	conn := ws.NewConn(wsConn, user, connID)
	apiKey, _ := middleware.APIKeyFromCtx(r.Context())
	conn.APIKey = apiKey
	h.Hub.Register(conn)

	// Session state: manages cancellations for active streams
	var (
		mu             sync.Mutex
		cancelAttach   func()
		cancelLogs     func()
		cancelStats    func()
		lastStatsStart time.Time
		hijackedResp   *client.HijackedResponse
		activeExecID   string
		proxySession   *ws.ProxySession
	)

	// closeProxySession closes the proxy and restores the default read limit
	closeProxySession := func() {
		if proxySession != nil {
			proxySession.Close()
			proxySession = nil
			wsConn.SetReadLimit(64 * 1024)
		}
	}

	defer func() {
		closeProxySession()
		if cancelStats != nil {
			cancelStats()
		}
		h.Hub.Unregister(conn)
		conn.Close()
	}()

	for {
		msgType, raw, err := conn.ReadRaw()
		if err != nil {
			return
		}

		// Binary frames are proxy data: [streamID(4 bytes)][payload]
		if msgType == websocket.MessageBinary {
			mu.Lock()
			ps := proxySession
			mu.Unlock()
			if ps != nil && len(raw) >= 4 {
				streamID := binary.BigEndian.Uint32(raw[:4])
				ps.HandleBinaryData(streamID, raw[4:])
			}
			continue
		}

		// Text frames are JSON control messages
		var msg ws.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Event {
		case "attach":
			if conn.APIKey != nil && !conn.APIKey.HasScope("containers:shell") {
				conn.WriteEvent("error", "Insufficient API key scope")
				continue
			}

			var data struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(msg.Data, &data); err != nil || data.ID == "" {
				conn.WriteEvent("error", "Invalid attach request")
				continue
			}

			// Cancel any previous attach session (lock only for shared state)
			mu.Lock()
			if cancelAttach != nil {
				cancelAttach()
				cancelAttach = nil
			}
			mu.Unlock()

			// Perform Docker API calls outside the lock
			if !UserCanAccessContainerByID(conn.Ctx, h.Docker, user, data.ID) {
				conn.WriteEvent("error", "Access denied")
				continue
			}
			if h.Docker.IsSelf(conn.Ctx, data.ID) {
				h.Audit.Warn("access denied", "user", user.Email, "reason", "shell on the API container", "container", data.ID)
				conn.WriteEvent("error", "The API container is protected (audit trail)")
				continue
			}
			if h.Docker.IsFunction(conn.Ctx, data.ID) {
				h.Audit.Warn("access denied", "user", user.Email, "reason", "shell on a function container", "container", data.ID)
				conn.WriteEvent("error", "Function containers are protected (secrets)")
				continue
			}

			info, err := h.Docker.InspectContainer(conn.Ctx, data.ID)
			if err != nil {
				conn.WriteEvent("error", "Container not found")
				continue
			}

			if !info.State.Running {
				conn.WriteEvent("error", "Container is not running")
				continue
			}

			if !info.Config.Tty {
				conn.WriteEvent("error", "Container has no TTY allocated")
				continue
			}

			hasTTY := info.Config.Tty

			execID, err := h.Docker.ContainerExecCreate(conn.Ctx, data.ID, []string{"/bin/sh"}, hasTTY)
			if err != nil {
				conn.WriteEvent("error", "Failed to create exec")
				continue
			}

			hijacked, err := h.Docker.ContainerExecAttach(conn.Ctx, execID, hasTTY)
			if err != nil {
				conn.WriteEvent("error", "Failed to attach exec")
				continue
			}

			cancel := startExecStream(conn, &hijacked, hasTTY, h.Logger)

			// Lock only to swap shared state
			mu.Lock()
			// Cancel again in case a concurrent attach raced during Docker calls
			if cancelAttach != nil {
				cancelAttach()
			}
			hijackedResp = &hijacked
			activeExecID = execID
			cancelAttach = func() {
				cancel()
				hijacked.Close()
				hijackedResp = nil
				activeExecID = ""
			}
			mu.Unlock()

			conn.WriteEvent("attach", "connected")
			h.Audit.Info("shell attached", "user", user.Email, "container", info.Name)

		case "streamInput":
			mu.Lock()
			if hijackedResp != nil {
				var data string
				if err := json.Unmarshal(msg.Data, &data); err == nil {
					if _, err := hijackedResp.Conn.Write([]byte(data)); err != nil {
						h.Logger.Warn("exec stdin write failed", "error", err)
					}
				}
			}
			mu.Unlock()

		case "streamClose":
			var data struct {
				StreamID uint32 `json:"streamID"`
			}
			// If the message has a streamID, it's a proxy stream close
			if err := json.Unmarshal(msg.Data, &data); err == nil && data.StreamID != 0 {
				mu.Lock()
				ps := proxySession
				mu.Unlock()
				if ps != nil {
					ps.HandleStreamClose(data.StreamID)
				}
			} else {
				// Legacy exec/attach stream close
				mu.Lock()
				if cancelAttach != nil {
					cancelAttach()
					cancelAttach = nil
				}
				mu.Unlock()
			}

		case "resize":
			var data struct {
				Cols uint `json:"cols"`
				Rows uint `json:"rows"`
			}
			if err := json.Unmarshal(msg.Data, &data); err != nil || data.Cols == 0 || data.Rows == 0 {
				continue
			}
			// Cap dimensions to reasonable limits
			if data.Cols > 500 {
				data.Cols = 500
			}
			if data.Rows > 200 {
				data.Rows = 200
			}
			mu.Lock()
			if activeExecID != "" {
				if err := h.Docker.ContainerExecResize(conn.Ctx, activeExecID, data.Cols, data.Rows); err != nil {
					h.Logger.Warn("exec resize failed", "error", err)
				}
			}
			mu.Unlock()

		case "logsStart":
			if conn.APIKey != nil && !conn.APIKey.HasScope("containers:logs") {
				conn.WriteEvent("logsError", "Insufficient API key scope")
				continue
			}

			var data struct {
				ID   string `json:"id"`
				Tail int    `json:"tail"`
				File string `json:"file"`
			}
			if err := json.Unmarshal(msg.Data, &data); err != nil || data.ID == "" {
				conn.WriteEvent("logsError", "Invalid logs request")
				continue
			}

			// Validate and cap the tail parameter
			if data.Tail < 0 {
				data.Tail = 0
			}
			const maxTail = 10000
			if data.Tail > maxTail {
				data.Tail = maxTail
			}

			// Cancel previous log stream under lock, then release before Docker calls
			mu.Lock()
			if cancelLogs != nil {
				cancelLogs()
				cancelLogs = nil
			}
			mu.Unlock()

			var cancel context.CancelFunc
			var logsErr error
			if data.File != "" {
				// Validate file path
				if err := validateContainerPath(data.File); err != nil {
					conn.WriteEvent("logsError", "Invalid file path: "+err.Error())
					continue
				}
				// Files of the API container hold the audit log.
				if h.Docker.IsSelf(conn.Ctx, data.ID) {
					h.Audit.Warn("access denied", "user", user.Email, "reason", "file tail on the API container", "container", data.ID)
					conn.WriteEvent("logsError", "The API container is protected (audit trail)")
					continue
				}
				if h.Docker.IsFunction(conn.Ctx, data.ID) {
					h.Audit.Warn("access denied", "user", user.Email, "reason", "file tail on a function container", "container", data.ID)
					conn.WriteEvent("logsError", "Function containers are protected (secrets)")
					continue
				}
				cancel, logsErr = ws.HandleFileLogsStart(conn.Ctx, conn, h.Docker, user, data.ID, data.File, data.Tail, h.Logger)
			} else {
				cancel, logsErr = ws.HandleLogsStart(conn.Ctx, conn, h.Docker, user, data.ID, data.Tail, h.Logger)
			}
			if logsErr != nil {
				h.Logger.Error("failed to start logs stream", "error", logsErr)
			} else {
				logTarget := "stdout/stderr"
				if data.File != "" {
					logTarget = data.File
				}
				h.Audit.Info("logs stream started", "user", user.Email, "container", data.ID, "target", logTarget)
				mu.Lock()
				cancelLogs = cancel
				mu.Unlock()
			}

		case "logsStop":
			mu.Lock()
			if cancelLogs != nil {
				cancelLogs()
				cancelLogs = nil
			}
			mu.Unlock()

		case "proxyOpen":
			if conn.APIKey != nil && !conn.APIKey.HasScope("containers:proxy") {
				conn.WriteEvent("proxyError", "Insufficient API key scope")
				continue
			}

			var data struct {
				ID       string           `json:"id"`
				Mappings []ws.PortMapping `json:"mappings"`
			}
			if err := json.Unmarshal(msg.Data, &data); err != nil || data.ID == "" {
				conn.WriteEvent("proxyError", "Invalid proxy request")
				continue
			}

			if !UserCanAccessContainerByID(conn.Ctx, h.Docker, user, data.ID) {
				conn.WriteEvent("proxyError", "Access denied")
				continue
			}
			// The panel never bridges into a function network: callers are
			// attached to it and authenticate (decision log §20).
			if h.Docker.IsFunction(conn.Ctx, data.ID) {
				h.Audit.Warn("access denied", "user", user.Email, "reason", "proxy to a function container", "container", data.ID)
				conn.WriteEvent("proxyError", "Function containers are protected (secrets)")
				continue
			}

			info, err := h.Docker.InspectContainer(conn.Ctx, data.ID)
			if err != nil {
				conn.WriteEvent("proxyError", "Container not found")
				continue
			}
			if !info.State.Running {
				conn.WriteEvent("proxyError", "Container is not running")
				continue
			}

			// Cancel any previous sessions
			mu.Lock()
			if cancelAttach != nil {
				cancelAttach()
				cancelAttach = nil
			}
			if cancelLogs != nil {
				cancelLogs()
				cancelLogs = nil
			}
			closeProxySession()

			ps, err := ws.NewProxySession(conn, h.Docker, data.ID, data.Mappings, h.Logger)
			if err != nil {
				mu.Unlock()
				conn.WriteEvent("proxyError", err.Error())
				continue
			}
			proxySession = ps
			mu.Unlock()

			// Raise read limit for binary data
			wsConn.SetReadLimit(128 * 1024)

			conn.WriteEvent("proxyReady", map[string]any{"mappings": data.Mappings})
			h.Logger.Info("proxy session started", "container", data.ID, "mappings", len(data.Mappings))
			h.Audit.Info("proxy session started", "user", user.Email, "container", data.ID, "mappings", len(data.Mappings))

		case "statsStart":
			if conn.APIKey != nil && !conn.APIKey.HasScope("containers:stats") {
				conn.WriteEvent("statsError", "Insufficient API key scope")
				continue
			}

			var data struct {
				IDs []string `json:"ids"`
			}
			if err := json.Unmarshal(msg.Data, &data); err != nil || len(data.IDs) == 0 {
				conn.WriteEvent("statsError", "Invalid stats request")
				continue
			}

			// Cap IDs to prevent DoS via O(n) Docker inspect calls
			const maxStatsIDs = 50
			if len(data.IDs) > maxStatsIDs {
				data.IDs = data.IDs[:maxStatsIDs]
			}

			// Cooldown: prevent rapid-fire statsStart from amplifying Docker API calls
			mu.Lock()
			if !lastStatsStart.IsZero() && time.Since(lastStatsStart) < 3*time.Second {
				mu.Unlock()
				conn.WriteEvent("statsError", "Too many stats requests, try again later")
				continue
			}

			// Cancel previous stats stream
			if cancelStats != nil {
				cancelStats()
				cancelStats = nil
			}
			mu.Unlock()

			cancel, err := ws.HandleStatsStart(conn.Ctx, conn, h.Docker, user, data.IDs, h.Logger)
			if err != nil {
				h.Logger.Error("failed to start stats stream", "error", err)
			} else if cancel != nil {
				h.Audit.Info("stats stream started", "user", user.Email, "containers", len(data.IDs))
				mu.Lock()
				cancelStats = cancel
				lastStatsStart = time.Now()
				mu.Unlock()
			}

		case "statsStop":
			mu.Lock()
			if cancelStats != nil {
				cancelStats()
				cancelStats = nil
			}
			mu.Unlock()

		case "streamOpen":
			// Defense-in-depth: proxyOpen already checks this scope, but verify again
			if conn.APIKey != nil && !conn.APIKey.HasScope("containers:proxy") {
				conn.WriteEvent("streamError", map[string]any{"streamID": uint32(0), "error": "Insufficient API key scope"})
				continue
			}

			var data struct {
				StreamID   uint32 `json:"streamID"`
				RemotePort uint16 `json:"remotePort"`
			}
			if err := json.Unmarshal(msg.Data, &data); err != nil || data.StreamID == 0 || data.RemotePort == 0 {
				conn.WriteEvent("streamError", map[string]any{"streamID": data.StreamID, "error": "invalid stream open request"})
				continue
			}

			mu.Lock()
			ps := proxySession
			mu.Unlock()
			if ps == nil {
				conn.WriteEvent("streamError", map[string]any{"streamID": data.StreamID, "error": "no active proxy session"})
				continue
			}

			ps.HandleStreamOpen(data.StreamID, data.RemotePort)
		}
	}
}

// startExecStream starts reading the exec stream and forwarding it to the WebSocket.
func startExecStream(conn *ws.Conn, hijacked *client.HijackedResponse, hasTTY bool, logger *slog.Logger) context.CancelFunc {
	ctx, cancel := context.WithCancel(conn.Ctx)

	go func() {
		defer conn.WriteEvent("end", "stream closed")

		// Close the hijacked connection on context cancellation to unblock
		// any blocking Read on hijacked.Reader (both TTY and non-TTY paths).
		context.AfterFunc(ctx, func() {
			hijacked.Close()
		})

		if hasTTY {
			buf := make([]byte, 4096)
			for {
				n, err := hijacked.Reader.Read(buf)
				if n > 0 {
					conn.WriteEvent("stdout", string(buf[:n]))
				}
				if err != nil {
					return
				}
			}
		} else {
			stdoutPR, stdoutPW := io.Pipe()
			stderrPR, stderrPW := io.Pipe()

			go func() {
				_, err := stdcopy.StdCopy(stdoutPW, stderrPW, hijacked.Reader)
				stdoutPW.CloseWithError(err)
				stderrPW.CloseWithError(err)
			}()

			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := stderrPR.Read(buf)
					if n > 0 {
						conn.WriteEvent("stderr", string(buf[:n]))
					}
					if err != nil {
						return
					}
				}
			}()

			buf := make([]byte, 4096)
			for {
				n, err := stdoutPR.Read(buf)
				if n > 0 {
					conn.WriteEvent("stdout", string(buf[:n]))
				}
				if err != nil {
					return
				}
			}
		}
	}()

	return cancel
}
