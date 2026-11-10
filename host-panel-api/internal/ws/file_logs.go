package ws

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/moby/moby/api/pkg/stdcopy"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/model"
)

// HandleFileLogsStart starts streaming a file inside a container to the WebSocket
// using docker exec tail. Returns a cancel function to stop the streaming.
func HandleFileLogsStart(
	ctx context.Context,
	conn *Conn,
	client *docker.Client,
	user *model.User,
	containerID string,
	filePath string,
	tail int,
	logger *slog.Logger,
) (context.CancelFunc, error) {
	// Inspect the container
	info, err := client.InspectContainer(ctx, containerID)
	if err != nil {
		conn.WriteEvent("logsError", "Container not found")
		return nil, err
	}

	// Shared check: restricted users only reach granted containers, never
	// a function container.
	if !docker.UserCanAccess(user, info) {
		conn.WriteEvent("logsError", "Access denied")
		return nil, nil
	}

	// Container must be running for exec
	if !info.State.Running {
		conn.WriteEvent("logsError", "Container is not running")
		return nil, nil
	}

	// Build tail command — passed as array, no shell interpolation
	tailN := "100"
	if tail > 0 {
		tailN = fmt.Sprintf("%d", tail)
	}
	cmd := []string{"tail", "-f", "-n", tailN, "--", filePath}

	execID, err := client.ContainerExecCreateReadOnly(ctx, containerID, cmd)
	if err != nil {
		conn.WriteEvent("logsError", "Failed to create exec")
		return nil, err
	}

	hijacked, err := client.ContainerExecAttach(ctx, execID, false)
	if err != nil {
		conn.WriteEvent("logsError", "Failed to attach to exec")
		return nil, err
	}

	logsCtx, cancel := context.WithCancel(ctx)

	// Close the hijacked connection when context is cancelled to unblock
	// the stdcopy and scanner goroutines (tail -f runs indefinitely).
	context.AfterFunc(logsCtx, func() { hijacked.Close() })

	go func() {
		defer func() {
			hijacked.Close()
			conn.WriteEvent("logsEnd", "stream closed")
		}()

		// Demultiplex stdout/stderr (tty=false)
		stdoutPR, stdoutPW := io.Pipe()
		stderrPR, stderrPW := io.Pipe()

		go func() {
			_, err := stdcopy.StdCopy(stdoutPW, stderrPW, hijacked.Reader)
			stdoutPW.CloseWithError(err)
			stderrPW.CloseWithError(err)
		}()

		// Read stderr in background to detect errors (file not found, etc.)
		go func() {
			scanner := bufio.NewScanner(stderrPR)
			for scanner.Scan() {
				select {
				case <-logsCtx.Done():
					return
				default:
				}
				logger.Warn("file tail stderr", "container", containerID, "path", filePath, "stderr", scanner.Text())
				conn.WriteEvent("logsError", "File not found or not readable")
			}
		}()

		// Stream stdout lines raw (no Docker timestamp prefix — file content as-is)
		scanner := bufio.NewScanner(stdoutPR)
		for scanner.Scan() {
			select {
			case <-logsCtx.Done():
				return
			default:
			}

			line := scanner.Text()
			conn.WriteEvent("logsData", line+"\r\n")
		}

		if err := scanner.Err(); err != nil && logsCtx.Err() == nil {
			logger.Error("file logs read error", "error", err)
		}
	}()

	return cancel, nil
}
