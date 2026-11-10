package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"host-panel-api/internal/sftpgo"
)

// mockSFTPClient is an in-memory SFTPClient for tests.
type mockSFTPClient struct {
	users       map[string]*sftpgo.User
	createErr   error
	lastCreate  *sftpgo.User
	tokenErr    error
	dirErr      error
	tokenPass   string
	createdDirs []string
}

func newMockSFTPClient() *mockSFTPClient {
	return &mockSFTPClient{users: make(map[string]*sftpgo.User)}
}

func (m *mockSFTPClient) ListUsers(ctx context.Context) ([]sftpgo.User, error) {
	out := make([]sftpgo.User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, *u)
	}
	return out, nil
}

func (m *mockSFTPClient) GetUser(ctx context.Context, username string) (*sftpgo.User, error) {
	u, ok := m.users[username]
	if !ok {
		return nil, &sftpgo.APIError{StatusCode: http.StatusNotFound, Body: "not found"}
	}
	copy := *u
	return &copy, nil
}

func (m *mockSFTPClient) CreateUser(ctx context.Context, user *sftpgo.User) (*sftpgo.User, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	copy := *user
	m.lastCreate = &copy
	m.users[user.Username] = &copy
	return &copy, nil
}

func (m *mockSFTPClient) UpdateUser(ctx context.Context, username string, user *sftpgo.User) error {
	if _, ok := m.users[username]; !ok {
		return &sftpgo.APIError{StatusCode: http.StatusNotFound, Body: "not found"}
	}
	copy := *user
	m.users[username] = &copy
	return nil
}

func (m *mockSFTPClient) DeleteUser(ctx context.Context, username string) error {
	if _, ok := m.users[username]; !ok {
		return &sftpgo.APIError{StatusCode: http.StatusNotFound, Body: "not found"}
	}
	delete(m.users, username)
	return nil
}

func (m *mockSFTPClient) UserToken(ctx context.Context, username, password string) (string, error) {
	if m.tokenErr != nil {
		return "", m.tokenErr
	}
	m.tokenPass = password
	return "test-token", nil
}

func (m *mockSFTPClient) CreateUserDir(ctx context.Context, token, dirPath string) error {
	if m.dirErr != nil {
		return m.dirErr
	}
	m.createdDirs = append(m.createdDirs, dirPath)
	return nil
}

func newTestSFTPHandler(client SFTPClient, enabled bool) *SFTPHandler {
	return &SFTPHandler{
		Client:           client,
		Enabled:          enabled,
		Directory:        "/data/websites/hosted",
		Domain:           "sftp.example.com",
		Port:             "2222",
		WebUserInterface: "https://sftp.example.com/web/client",
		Logger:           testLogger(),
		Audit:            testLogger(),
	}
}

func TestSFTPHandler_Disabled_Returns404(t *testing.T) {
	h := newTestSFTPHandler(newMockSFTPClient(), false)

	r := adminCtx(httptest.NewRequest(http.MethodGet, "/sftp/users", nil))
	w := httptest.NewRecorder()
	h.ListUsers(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	var resp map[string]string
	decodeJSON(t, w.Body, &resp)
	if resp["code"] != ErrSFTPDisabled {
		t.Errorf("code = %q, want %q", resp["code"], ErrSFTPDisabled)
	}
}

func TestSFTPHandler_Config_Disabled(t *testing.T) {
	h := newTestSFTPHandler(newMockSFTPClient(), false)

	r := adminCtx(httptest.NewRequest(http.MethodGet, "/sftp/config", nil))
	w := httptest.NewRecorder()
	h.Config(w, r)

	var resp sftpConfigResponse
	decodeJSON(t, w.Body, &resp)
	if resp.Enabled {
		t.Error("enabled should be false")
	}
	if resp.Domain != "" {
		t.Error("domain should be hidden when disabled")
	}
}

func TestSFTPHandler_Create_MapsFields(t *testing.T) {
	mock := newMockSFTPClient()
	h := newTestSFTPHandler(mock, true)

	payload := sftpCreateRequest{
		Username:              "client1",
		Site:                  "acme",
		Password:              "s3cretPass",
		PublicKey:             "ssh-ed25519 AAAA",
		QuotaSizeBytes:        1073741824, // 1 GiB
		QuotaFiles:            1000,
		BandwidthKbps:         512,
		RequirePasswordChange: true,
	}
	body, _ := json.Marshal(payload)
	r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}

	got := mock.lastCreate
	if got == nil {
		t.Fatal("CreateUser was not called")
	}
	if got.HomeDir != "/data/websites/hosted/acme" {
		t.Errorf("home_dir = %q, want /data/websites/hosted/acme", got.HomeDir)
	}
	if got.Status != 1 {
		t.Errorf("status = %d, want 1", got.Status)
	}
	if got.QuotaSize != 1073741824 {
		t.Errorf("quota_size = %d", got.QuotaSize)
	}
	if got.UploadBandwidth != 512 || got.DownloadBandwidth != 512 {
		t.Errorf("bandwidth = %d/%d, want 512/512", got.UploadBandwidth, got.DownloadBandwidth)
	}
	if !got.Filters.RequirePasswordChange {
		t.Error("require_password_change should be true")
	}
	if perms := got.Permissions["/"]; len(perms) != 1 || perms[0] != "*" {
		t.Errorf("permissions = %v, want {/:[*]}", got.Permissions)
	}
	if len(got.PublicKeys) != 1 || got.PublicKeys[0] != "ssh-ed25519 AAAA" {
		t.Errorf("public_keys = %v", got.PublicKeys)
	}
}

func TestSFTPHandler_Create_InitializesDirs(t *testing.T) {
	mock := newMockSFTPClient()
	h := newTestSFTPHandler(mock, true)
	h.InitDirs = []string{"web", "logs"}

	// InitializeDirs omitted -> defaults to true.
	payload := sftpCreateRequest{Username: "client1", Site: "acme", Password: "s3cretPass"}
	body, _ := json.Marshal(payload)
	r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	if mock.tokenPass != "s3cretPass" {
		t.Errorf("token requested with password %q, want s3cretPass", mock.tokenPass)
	}
	if len(mock.createdDirs) != 2 || mock.createdDirs[0] != "/web" || mock.createdDirs[1] != "/logs" {
		t.Errorf("created dirs = %v, want [/web /logs]", mock.createdDirs)
	}
	var resp sftpUserResponse
	decodeJSON(t, w.Body, &resp)
	if len(resp.InitializedDirs) != 2 || resp.InitDirsError != "" {
		t.Errorf("response init = %v err=%q, want [web logs] and no error", resp.InitializedDirs, resp.InitDirsError)
	}
}

func TestSFTPHandler_Create_SkipsDirsWhenDisabled(t *testing.T) {
	mock := newMockSFTPClient()
	h := newTestSFTPHandler(mock, true)
	h.InitDirs = []string{"web", "logs"}

	disabled := false
	payload := sftpCreateRequest{Username: "u1", Site: "acme", Password: "validpass", InitializeDirs: &disabled}
	body, _ := json.Marshal(payload)
	r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	if len(mock.createdDirs) != 0 {
		t.Errorf("created dirs = %v, want none", mock.createdDirs)
	}
}

func TestSFTPHandler_Create_DirInitFailureIsNonFatal(t *testing.T) {
	mock := newMockSFTPClient()
	mock.tokenErr = &sftpgo.APIError{StatusCode: http.StatusForbidden, Body: "forbidden"}
	h := newTestSFTPHandler(mock, true)
	h.InitDirs = []string{"web"}

	payload := sftpCreateRequest{Username: "u1", Site: "acme", Password: "validpass"}
	body, _ := json.Marshal(payload)
	r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (creation must not fail when dir init fails)", w.Code)
	}
	var resp sftpUserResponse
	decodeJSON(t, w.Body, &resp)
	if resp.InitDirsError == "" {
		t.Error("expected a non-fatal initDirsError in the response")
	}
}

func TestSFTPHandler_Create_WithSubPath(t *testing.T) {
	mock := newMockSFTPClient()
	h := newTestSFTPHandler(mock, true)

	payload := sftpCreateRequest{
		Username: "client1",
		Site:     "acme",
		SubPath:  "clients/dupont",
		Password: "s3cretPass",
	}
	body, _ := json.Marshal(payload)
	r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	if got := mock.lastCreate.HomeDir; got != "/data/websites/hosted/acme/clients/dupont" {
		t.Errorf("home_dir = %q, want /data/websites/hosted/acme/clients/dupont", got)
	}

	var resp sftpUserResponse
	decodeJSON(t, w.Body, &resp)
	if resp.Site != "acme" || resp.SubPath != "clients/dupont" {
		t.Errorf("got site=%q subPath=%q, want acme / clients/dupont", resp.Site, resp.SubPath)
	}
}

func TestSFTPHandler_Create_AllowsHiddenSubDir(t *testing.T) {
	mock := newMockSFTPClient()
	h := newTestSFTPHandler(mock, true)

	payload := sftpCreateRequest{Username: "u1", Site: "acme", SubPath: ".well-known/acme", Password: "validpass"}
	body, _ := json.Marshal(payload)
	r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	if got := mock.lastCreate.HomeDir; got != "/data/websites/hosted/acme/.well-known/acme" {
		t.Errorf("home_dir = %q, want .../acme/.well-known/acme", got)
	}
}

func TestSFTPHandler_Create_RejectsSubPathTraversal(t *testing.T) {
	for _, sub := range []string{"../escape", "a/../../b", "foo/..", "a/\\b", "a/./b", "bad\x00name", "ctrl\tchar"} {
		mock := newMockSFTPClient()
		h := newTestSFTPHandler(mock, true)

		payload := sftpCreateRequest{Username: "u1", Site: "acme", SubPath: sub, Password: "validpass"}
		body, _ := json.Marshal(payload)
		r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
		w := httptest.NewRecorder()
		h.CreateUser(w, r)

		if w.Code != http.StatusBadRequest {
			t.Errorf("subPath %q: status = %d, want 400", sub, w.Code)
		}
		if mock.lastCreate != nil {
			t.Errorf("subPath %q: user should not have been created", sub)
		}
	}
}

func TestSFTPHandler_Create_PasswordNotInResponse(t *testing.T) {
	h := newTestSFTPHandler(newMockSFTPClient(), true)

	payload := sftpCreateRequest{Username: "u1", Site: "acme", Password: "supersecret"}
	body, _ := json.Marshal(payload)
	r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if strings.Contains(w.Body.String(), "supersecret") {
		t.Errorf("response leaked the password: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"password"`) {
		t.Errorf("response should not contain a password field: %s", w.Body.String())
	}
}

func TestSFTPHandler_Create_RejectsPathTraversal(t *testing.T) {
	for _, site := range []string{"../etc", "a/b", "..", "", "foo/../bar"} {
		mock := newMockSFTPClient()
		h := newTestSFTPHandler(mock, true)

		payload := sftpCreateRequest{Username: "u1", Site: site, Password: "pw"}
		body, _ := json.Marshal(payload)
		r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
		w := httptest.NewRecorder()
		h.CreateUser(w, r)

		if w.Code != http.StatusBadRequest {
			t.Errorf("site %q: status = %d, want 400", site, w.Code)
		}
		if mock.lastCreate != nil {
			t.Errorf("site %q: user should not have been created", site)
		}
	}
}

func TestSFTPHandler_Create_RequiresPassword(t *testing.T) {
	h := newTestSFTPHandler(newMockSFTPClient(), true)

	payload := sftpCreateRequest{Username: "u1", Site: "acme"}
	body, _ := json.Marshal(payload)
	r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	decodeJSON(t, w.Body, &resp)
	if resp["code"] != ErrPasswordRequired {
		t.Errorf("code = %q, want %q", resp["code"], ErrPasswordRequired)
	}
}

func TestSFTPHandler_Create_RejectsShortPassword(t *testing.T) {
	mock := newMockSFTPClient()
	h := newTestSFTPHandler(mock, true)

	payload := sftpCreateRequest{Username: "u1", Site: "acme", Password: "short"}
	body, _ := json.Marshal(payload)
	r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if mock.lastCreate != nil {
		t.Error("user should not have been created with a short password")
	}
}

func TestSFTPHandler_Create_RejectsNegativeLimits(t *testing.T) {
	mock := newMockSFTPClient()
	h := newTestSFTPHandler(mock, true)

	payload := sftpCreateRequest{
		Username:       "u1",
		Site:           "acme",
		Password:       "validpass",
		QuotaSizeBytes: -1,
	}
	body, _ := json.Marshal(payload)
	r := adminCtx(httptest.NewRequest(http.MethodPost, "/sftp/users", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if mock.lastCreate != nil {
		t.Error("user should not have been created with a negative quota")
	}
}

func TestSFTPHandler_ListUsers_FiltersOutsideDirectory(t *testing.T) {
	mock := newMockSFTPClient()
	mock.users["inside"] = &sftpgo.User{Username: "inside", HomeDir: "/data/websites/hosted/acme"}
	mock.users["outside"] = &sftpgo.User{Username: "outside", HomeDir: "/home/other"}
	h := newTestSFTPHandler(mock, true)

	r := adminCtx(httptest.NewRequest(http.MethodGet, "/sftp/users", nil))
	w := httptest.NewRecorder()
	h.ListUsers(w, r)

	var resp []sftpUserResponse
	decodeJSON(t, w.Body, &resp)
	if len(resp) != 1 {
		t.Fatalf("got %d users, want 1", len(resp))
	}
	if resp[0].Username != "inside" || resp[0].Site != "acme" {
		t.Errorf("got %+v, want username=inside site=acme", resp[0])
	}
}

func TestSFTPHandler_ListUsers_DerivesSubPath(t *testing.T) {
	mock := newMockSFTPClient()
	mock.users["root"] = &sftpgo.User{Username: "root", HomeDir: "/data/websites/hosted/acme"}
	mock.users["scoped"] = &sftpgo.User{Username: "scoped", HomeDir: "/data/websites/hosted/acme/clients/dupont"}
	h := newTestSFTPHandler(mock, true)

	r := adminCtx(httptest.NewRequest(http.MethodGet, "/sftp/users", nil))
	w := httptest.NewRecorder()
	h.ListUsers(w, r)

	var resp []sftpUserResponse
	decodeJSON(t, w.Body, &resp)
	byName := map[string]sftpUserResponse{}
	for _, u := range resp {
		byName[u.Username] = u
	}
	if got := byName["root"]; got.Site != "acme" || got.SubPath != "" {
		t.Errorf("root = site %q subPath %q, want acme / \"\"", got.Site, got.SubPath)
	}
	if got := byName["scoped"]; got.Site != "acme" || got.SubPath != "clients/dupont" {
		t.Errorf("scoped = site %q subPath %q, want acme / clients/dupont", got.Site, got.SubPath)
	}
}

func TestSFTPHandler_ListSites_Aggregates(t *testing.T) {
	mock := newMockSFTPClient()
	mock.users["a1"] = &sftpgo.User{Username: "a1", HomeDir: "/data/websites/hosted/acme", UsedQuotaSize: 100}
	mock.users["a2"] = &sftpgo.User{Username: "a2", HomeDir: "/data/websites/hosted/acme", UsedQuotaSize: 50}
	mock.users["b1"] = &sftpgo.User{Username: "b1", HomeDir: "/data/websites/hosted/blog", UsedQuotaSize: 10}
	h := newTestSFTPHandler(mock, true)

	r := adminCtx(httptest.NewRequest(http.MethodGet, "/sftp/sites", nil))
	w := httptest.NewRecorder()
	h.ListSites(w, r)

	var resp []sftpSiteResponse
	decodeJSON(t, w.Body, &resp)
	if len(resp) != 2 {
		t.Fatalf("got %d sites, want 2", len(resp))
	}
	byName := map[string]sftpSiteResponse{}
	for _, s := range resp {
		byName[s.Name] = s
	}
	if byName["acme"].UserCount != 2 || byName["acme"].TotalUsedQuotaSize != 150 {
		t.Errorf("acme = %+v, want count=2 quota=150", byName["acme"])
	}
	if byName["blog"].UserCount != 1 {
		t.Errorf("blog count = %d, want 1", byName["blog"].UserCount)
	}
}

func TestSFTPHandler_Delete_RejectsUnmanagedUser(t *testing.T) {
	mock := newMockSFTPClient()
	mock.users["outsider"] = &sftpgo.User{Username: "outsider", HomeDir: "/home/outsider"}
	h := newTestSFTPHandler(mock, true)

	r := adminCtx(httptest.NewRequest(http.MethodDelete, "/sftp/users/outsider", nil))
	r.SetPathValue("username", "outsider")
	w := httptest.NewRecorder()
	h.DeleteUser(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if _, stillThere := mock.users["outsider"]; !stillThere {
		t.Error("unmanaged user should not have been deleted")
	}
}

func TestSFTPHandler_Delete_Success(t *testing.T) {
	mock := newMockSFTPClient()
	mock.users["client1"] = &sftpgo.User{Username: "client1", HomeDir: "/data/websites/hosted/acme"}
	h := newTestSFTPHandler(mock, true)

	r := adminCtx(httptest.NewRequest(http.MethodDelete, "/sftp/users/client1", nil))
	r.SetPathValue("username", "client1")
	w := httptest.NewRecorder()
	h.DeleteUser(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if _, stillThere := mock.users["client1"]; stillThere {
		t.Error("user should have been deleted")
	}
}
