package ws

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	dockerclient "github.com/moby/moby/client"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/model"
)

// HandleLogsStart starts streaming a container's logs to the WebSocket.
// Checks permissions for restricted users.
// Returns a cancel function to stop the streaming.
func HandleLogsStart(
	ctx context.Context,
	conn *Conn,
	client *docker.Client,
	user *model.User,
	containerID string,
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

	hasTTY := info.Config.Tty

	tailStr := "100"
	if tail > 0 {
		tailStr = fmt.Sprintf("%d", tail)
	}

	logsReader, err := client.ContainerLogs(ctx, containerID, dockerclient.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       tailStr,
		Timestamps: true,
	})
	if err != nil {
		conn.WriteEvent("logsError", "Failed to get logs")
		return nil, err
	}

	logsCtx, cancel := context.WithCancel(ctx)

	go func() {
		defer func() {
			logsReader.Close()
			conn.WriteEvent("logsEnd", "stream closed")
		}()

		var reader io.Reader

		if hasTTY {
			reader = logsReader
		} else {
			// Demultiplex stdout/stderr
			pr, pw := io.Pipe()
			go func() {
				_, err := stdcopy.StdCopy(pw, pw, logsReader)
				pw.CloseWithError(err)
			}()
			reader = pr
		}

		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			select {
			case <-logsCtx.Done():
				return
			default:
			}

			line := scanner.Text()
			formatted := formatLogLine(line)
			conn.WriteEvent("logsData", formatted)
		}

		if err := scanner.Err(); err != nil && logsCtx.Err() == nil {
			logger.Error("logs read error", "error", err)
		}
	}()

	return cancel, nil
}

// formatLogLine formats a log line with a colored (ANSI) timestamp.
func formatLogLine(line string) string {
	// Docker timestamp format is: "2024-01-01T12:00:00.123456789Z log message"
	if len(line) > 30 && line[4] == '-' && line[10] == 'T' {
		spaceIdx := strings.Index(line, " ")
		if spaceIdx > 0 {
			tsStr := line[:spaceIdx]
			message := line[spaceIdx+1:]

			ts, err := time.Parse(time.RFC3339Nano, tsStr)
			if err == nil {
				// ANSI cyan color for the timestamp
				return fmt.Sprintf("\033[36m%s\033[0m | %s\r\n",
					ts.Format("2006-01-02 15:04:05"),
					message,
				)
			}
		}
	}
	return line + "\r\n"
}
