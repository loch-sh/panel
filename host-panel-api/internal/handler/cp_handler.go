package handler

import (
	"archive/tar"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	pathpkg "path"
	"strings"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/middleware"
)

// CpHandler handles file copy operations on containers.
type CpHandler struct {
	Client *docker.Client
	Logger *slog.Logger
	Audit  *slog.Logger
}

// validateContainerPath checks that p is a safe absolute container path.
func validateContainerPath(p string) error {
	if len(p) == 0 || p[0] != '/' {
		return fmt.Errorf("path must be absolute")
	}
	if len(p) > 4096 {
		return fmt.Errorf("path too long")
	}
	for _, b := range []byte(p) {
		if b == 0x00 || b == '\n' || b == '\r' {
			return fmt.Errorf("path contains invalid character")
		}
	}
	return nil
}

// Download streams a tar archive of the given path from the container.
func (h *CpHandler) Download(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "containers:cp") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, ErrMissingPath, "Missing 'path' query parameter")
		return
	}
	if err := validateContainerPath(path); err != nil {
		writeError(w, http.StatusBadRequest, ErrMissingPath, "Invalid path")
		return
	}

	if !UserCanAccessContainerByID(r.Context(), h.Client, user, id) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	if h.Client.IsSelf(r.Context(), id) {
		writeError(w, http.StatusForbidden, ErrSelfProtected, "The API container is protected (audit trail)")
		return
	}
	if h.Client.IsFunction(r.Context(), id) {
		writeError(w, http.StatusForbidden, ErrFunctionProtected, "Function containers are protected (secrets)")
		return
	}

	reader, err := h.Client.CopyFromContainer(r.Context(), id, path)
	if err != nil {
		h.Logger.Error("copy from container failed", "id", id, "path", path, "error", err)
		status, code, msg := classifySourceError(err, path)
		writeError(w, status, code, msg)
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "application/x-tar")
	w.WriteHeader(http.StatusOK)
	// Flush the header immediately so the client starts receiving without delay.
	_ = http.NewResponseController(w).Flush()

	// 1 MB copy buffer: fewer write syscalls on the ResponseWriter than the
	// default 32 KB used by io.Copy, which matters for large tar streams.
	// io.CopyBuffer ignores this buffer if the source implements io.WriterTo
	// or the destination io.ReaderFrom; neither applies here (Docker SDK HTTP
	// body / middleware statusWriter), so the buffer is used as intended.
	buf := make([]byte, 1<<20)
	if _, err := io.CopyBuffer(w, reader, buf); err != nil {
		h.Logger.Error("cp download stream error", "id", id, "path", path, "error", err)
		return
	}

	h.Audit.Info("cp download", "user", user.Email, "container", id, "path", path)
}

// Upload receives a tar archive and extracts it into the container at the given path.
func (h *CpHandler) Upload(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "containers:cp") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, ErrMissingPath, "Missing 'path' query parameter")
		return
	}
	if err := validateContainerPath(path); err != nil {
		writeError(w, http.StatusBadRequest, ErrMissingPath, "Invalid path")
		return
	}

	if !UserCanAccessContainerByID(r.Context(), h.Client, user, id) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	if h.Client.IsSelf(r.Context(), id) {
		writeError(w, http.StatusForbidden, ErrSelfProtected, "The API container is protected (audit trail)")
		return
	}
	if h.Client.IsFunction(r.Context(), id) {
		writeError(w, http.StatusForbidden, ErrFunctionProtected, "Function containers are protected (secrets)")
		return
	}

	defer r.Body.Close()

	// Resolve the destination the way `docker cp` does: CopyToContainer treats
	// the path as the directory to extract into, so a path that is not an
	// existing directory (e.g. /tmp/new.img) must be split into a parent dir
	// plus a rename applied to the tar entries. Without this, the daemon refuses
	// to extract into a non-directory and stops draining the body, stalling the
	// upload.
	dstDir := path
	var body io.Reader = r.Body
	if !strings.HasSuffix(path, "/") {
		stat, err := h.Client.StatPath(r.Context(), id, path)
		if err != nil || !stat.Mode.IsDir() {
			dstDir = pathpkg.Dir(path)
			rebased := rebaseTarStream(r.Body, pathpkg.Base(path))
			defer rebased.Close()
			body = rebased
		}
	}

	// Fail fast with an explicit reason when the destination directory does not
	// exist inside the container: without this the daemon would only reject the
	// stream mid-transfer, which reads to the client as a stalled upload.
	statDir := strings.TrimSuffix(dstDir, "/")
	if statDir == "" {
		statDir = "/"
	}
	if stat, err := h.Client.StatPath(r.Context(), id, statDir); err != nil {
		h.Logger.Warn("cp upload destination unreachable", "id", id, "path", path, "dir", dstDir, "error", err)
		h.abortUpload(w, r, http.StatusNotFound, ErrCopyDestNotFound,
			fmt.Sprintf("Destination directory %q does not exist in the container", dstDir))
		return
	} else if !stat.Mode.IsDir() {
		h.abortUpload(w, r, http.StatusBadRequest, ErrCopyDestNotDir,
			fmt.Sprintf("Destination %q is not a directory in the container", dstDir))
		return
	}

	if err := h.Client.CopyToContainer(r.Context(), id, dstDir, body); err != nil {
		h.Logger.Error("copy to container failed", "id", id, "path", path, "error", err)
		status, code, msg := classifyCopyError(err, dstDir)
		h.abortUpload(w, r, status, code, msg)
		return
	}

	h.Audit.Info("cp upload", "user", user.Email, "container", id, "path", path)
	w.WriteHeader(http.StatusOK)
}

// maxUploadDrain caps how much of an aborted upload body we read after
// answering, just enough to keep the connection usable without letting a
// rejected multi-gigabyte transfer keep the handler busy.
const maxUploadDrain = 8 << 20

// abortUpload answers an upload with an error and flushes it immediately, so
// the client sees the reason while it is still streaming its body, then drains
// a bounded amount of that body to avoid tearing the connection down mid-write.
func (h *CpHandler) abortUpload(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeError(w, status, code, message)
	_ = http.NewResponseController(w).Flush()
	_, _ = io.CopyN(io.Discard, r.Body, maxUploadDrain)
}

// classifyCopyError turns a Docker copy failure into an HTTP status and a
// message the user can act on (missing directory, insufficient permissions),
// falling back to a generic error otherwise.
func classifyCopyError(err error, dstDir string) (int, string, string) {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "permission denied"), strings.Contains(msg, "read-only file system"):
		return http.StatusForbidden, ErrCopyPermissionDenied,
			fmt.Sprintf("Permission denied writing to %q in the container", dstDir)
	case strings.Contains(msg, "no such file or directory"), strings.Contains(msg, "not found"):
		return http.StatusNotFound, ErrCopyDestNotFound,
			fmt.Sprintf("Destination directory %q does not exist in the container", dstDir)
	case strings.Contains(msg, "not a directory"):
		return http.StatusBadRequest, ErrCopyDestNotDir,
			fmt.Sprintf("Destination %q is not a directory in the container", dstDir)
	case strings.Contains(msg, "no space left"):
		return http.StatusInsufficientStorage, ErrCopyFailed,
			"No space left on the container filesystem"
	}
	return http.StatusInternalServerError, ErrCopyFailed, "Failed to copy to container"
}

// classifySourceError does the same for a download, where the failing path is
// the source inside the container.
func classifySourceError(err error, path string) (int, string, string) {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "permission denied"):
		return http.StatusForbidden, ErrCopyPermissionDenied,
			fmt.Sprintf("Permission denied reading %q in the container", path)
	case strings.Contains(msg, "no such file or directory"), strings.Contains(msg, "could not find the file"):
		return http.StatusNotFound, ErrCopyDestNotFound,
			fmt.Sprintf("Path %q does not exist in the container", path)
	}
	return http.StatusInternalServerError, ErrCopyFailed, "Failed to copy from container"
}

// rebaseTarStream rewrites a tar stream on the fly so its single root entry is
// renamed to newBase, mirroring how `docker cp` renames a source when the
// destination names a new file or directory. The old base is taken from the
// first entry's leading path component and replaced in every entry. Rewriting
// happens in a goroutine so the stream stays fully pipelined (no buffering).
func rebaseTarStream(src io.Reader, newBase string) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		tr := tar.NewReader(src)
		tw := tar.NewWriter(pw)
		oldBase := ""
		first := true
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			if first {
				oldBase = firstPathComponent(hdr.Name)
				first = false
			}
			hdr.Name = rebaseName(hdr.Name, oldBase, newBase)
			if err := tw.WriteHeader(hdr); err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := io.Copy(tw, tr); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		if err := tw.Close(); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.Close()
	}()
	return pr
}

// firstPathComponent returns the first segment of a slash-separated tar entry
// name (e.g. "dir/sub/f" -> "dir", "file" -> "file").
func firstPathComponent(name string) string {
	name = strings.TrimPrefix(name, "./")
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[:i]
	}
	return name
}

// rebaseName replaces a leading oldBase component in name with newBase.
func rebaseName(name, oldBase, newBase string) string {
	name = strings.TrimPrefix(name, "./")
	if name == oldBase {
		return newBase
	}
	if strings.HasPrefix(name, oldBase+"/") {
		return newBase + name[len(oldBase):]
	}
	return name
}
