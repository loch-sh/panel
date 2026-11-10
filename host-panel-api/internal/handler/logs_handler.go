package handler

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/middleware"
)

// LogsHandler handles the log download endpoints.
type LogsHandler struct {
	Client *docker.Client
	Logger *slog.Logger
	Audit  *slog.Logger
}

// dockerLogEntry represents a Docker log entry.
type dockerLogEntry struct {
	Log    string `json:"log"`
	Stream string `json:"stream"`
	Time   string `json:"time"`
}

// readContainerLogs fetches logs via the Docker SDK and returns parsed entries.
func (h *LogsHandler) readContainerLogs(r *http.Request, id string, hasTTY bool) ([]dockerLogEntry, error) {
	logsReader, err := h.Client.ContainerLogs(r.Context(), id, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
	})
	if err != nil {
		return nil, err
	}
	defer logsReader.Close()

	var reader io.Reader
	if hasTTY {
		reader = logsReader
	} else {
		pr, pw := io.Pipe()
		go func() {
			stdcopy.StdCopy(pw, pw, logsReader)
			pw.Close()
		}()
		// Close pipe and logsReader on request cancellation to unblock
		// both the StdCopy goroutine and the Docker connection
		go func() {
			<-r.Context().Done()
			logsReader.Close()
			pw.CloseWithError(r.Context().Err())
		}()
		reader = pr
	}

	var entries []dockerLogEntry
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		entry := parseLogLine(line)
		entries = append(entries, entry)
	}

	if entries == nil {
		entries = []dockerLogEntry{}
	}
	return entries, nil
}

// parseLogLine parses a Docker log line with timestamp into a dockerLogEntry.
// Format: "2024-01-01T12:00:00.123456789Z log message"
func parseLogLine(line string) dockerLogEntry {
	if len(line) > 30 && line[4] == '-' && line[10] == 'T' {
		if spaceIdx := strings.Index(line, " "); spaceIdx > 0 {
			return dockerLogEntry{
				Log:    line[spaceIdx+1:] + "\n",
				Stream: "stdout",
				Time:   line[:spaceIdx],
			}
		}
	}
	return dockerLogEntry{
		Log:    line + "\n",
		Stream: "stdout",
	}
}

// Download returns the logs formatted as downloadable text.
func (h *LogsHandler) Download(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "containers:logs") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")

	info, err := h.Client.InspectContainer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrContainerNotFound, "Container not found")
		return
	}

	if !docker.UserCanAccess(user, info) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	entries, err := h.readContainerLogs(r, id, info.Config.Tty)
	if err != nil {
		h.Logger.Error("failed to read container logs", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, ErrLogsReadFailed, "Failed to read logs")
		return
	}

	h.Audit.Info("logs downloaded", "user", user.Email, "container", info.Name)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.log"`, id))

	for _, entry := range entries {
		ts, err := time.Parse(time.RFC3339Nano, entry.Time)
		if err != nil {
			ts = time.Time{}
		}
		line := fmt.Sprintf("%s | %s", ts.Format("2006-01-02 15:04:05"), strings.TrimRight(entry.Log, "\n"))
		fmt.Fprintln(w, line)
	}
}

// FileTail returns the tail of a file inside a container using docker exec.
func (h *LogsHandler) FileTail(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "containers:logs") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeError(w, http.StatusBadRequest, ErrMissingPath, "Missing 'path' query parameter")
		return
	}
	if err := validateContainerPath(filePath); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidFilePath, "Invalid file path: "+err.Error())
		return
	}

	info, err := h.Client.InspectContainer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrContainerNotFound, "Container not found")
		return
	}

	if !docker.UserCanAccess(user, info) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	if h.Client.IsSelf(r.Context(), info.ID) {
		writeError(w, http.StatusForbidden, ErrSelfProtected, "The API container is protected (audit trail)")
		return
	}
	if docker.IsFunctionContainer(info) {
		writeError(w, http.StatusForbidden, ErrFunctionProtected, "Function containers are protected (secrets)")
		return
	}

	if !info.State.Running {
		writeError(w, http.StatusUnprocessableEntity, ErrFileTailFailed, "Container is not running")
		return
	}

	// Parse tail parameter (default: 1000 lines to avoid dumping huge files)
	tailN := 1000
	const maxTail = 10000
	if t := r.URL.Query().Get("tail"); t != "" {
		if n, err := strconv.Atoi(t); err == nil && n > 0 {
			if n > maxTail {
				n = maxTail
			}
			tailN = n
		}
	}

	cmd := []string{"tail", "-n", fmt.Sprintf("%d", tailN), "--", filePath}

	execID, err := h.Client.ContainerExecCreateReadOnly(r.Context(), id, cmd)
	if err != nil {
		h.Logger.Error("file tail exec create failed", "id", id, "path", filePath, "error", err)
		writeError(w, http.StatusInternalServerError, ErrFileTailFailed, "Failed to execute command in container")
		return
	}

	hijacked, err := h.Client.ContainerExecAttach(r.Context(), execID, false)
	if err != nil {
		h.Logger.Error("file tail exec attach failed", "id", id, "path", filePath, "error", err)
		writeError(w, http.StatusInternalServerError, ErrFileTailFailed, "Failed to attach to exec")
		return
	}
	defer hijacked.Close()

	// Demultiplex stdout/stderr (tty=false)
	var stdoutBuf, stderrBuf bytes.Buffer
	stdcopy.StdCopy(&stdoutBuf, &stderrBuf, hijacked.Reader)

	if stderrBuf.Len() > 0 {
		h.Logger.Warn("file tail stderr", "container", id, "path", filePath, "stderr", strings.TrimSpace(stderrBuf.String()))
		writeError(w, http.StatusUnprocessableEntity, ErrFileTailFailed, "File not found or not readable")
		return
	}

	h.Audit.Info("file tail", "user", user.Email, "container", info.Name, "path", filePath)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, &stdoutBuf)
}

// JSON returns the logs as a JSON array.
func (h *LogsHandler) JSON(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "containers:logs") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")

	info, err := h.Client.InspectContainer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrContainerNotFound, "Container not found")
		return
	}

	if !docker.UserCanAccess(user, info) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	entries, err := h.readContainerLogs(r, id, info.Config.Tty)
	if err != nil {
		h.Logger.Error("failed to read container logs", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, ErrLogsReadFailed, "Failed to read logs")
		return
	}

	h.Audit.Info("logs viewed", "user", user.Email, "container", info.Name)

	writeJSON(w, http.StatusOK, entries)
}
