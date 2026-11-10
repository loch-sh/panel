package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"host-panel-api/internal/hosting"
	"host-panel-api/internal/networks"
	"host-panel-api/internal/services"
)

type mockNetworksManager struct {
	err      error
	created  *networks.CreateRequest
	attached [3]string
	actor    string
}

func (m *mockNetworksManager) List(context.Context) ([]networks.Summary, error) {
	return []networks.Summary{{Network: networks.Network{Name: "jobs"}, State: networks.StateOK}}, m.err
}
func (m *mockNetworksManager) Get(_ context.Context, name string) (*networks.Detail, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &networks.Detail{Summary: networks.Summary{Network: networks.Network{Name: name}}, Members: []networks.Member{{Kind: networks.KindSite, Name: "shop", IPv4: "10.20.0.2"}}}, nil
}
func (m *mockNetworksManager) Create(_ context.Context, req networks.CreateRequest, actor string) (*networks.Network, error) {
	m.created, m.actor = &req, actor
	return &networks.Network{Name: req.Name}, m.err
}
func (m *mockNetworksManager) Update(name, description, actor string) (*networks.Network, error) {
	return &networks.Network{Name: name, Description: description}, m.err
}
func (m *mockNetworksManager) Delete(context.Context, string, string) error { return m.err }
func (m *mockNetworksManager) Attach(_ context.Context, name, kind, member, actor string) (string, error) {
	m.attached = [3]string{name, kind, member}
	return "op", m.err
}
func (m *mockNetworksManager) Detach(_ context.Context, name, kind, member, actor string) (string, error) {
	m.attached = [3]string{name, kind, member}
	return "", m.err
}
func (m *mockNetworksManager) Disconnect(context.Context, string, string, string) error { return m.err }
func (m *mockNetworksManager) Limits() networks.Limits {
	return networks.Limits{Max: 50, ReservedRanges: []string{"10.8.0.0/16"}}
}
func (m *mockNetworksManager) ListExternal(context.Context) ([]networks.External, error) {
	return []networks.External{{ID: "abc", Name: "ci_default", Origin: networks.OriginCompose}}, m.err
}
func (m *mockNetworksManager) GetExternal(_ context.Context, id string) (*networks.ExternalDetail, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &networks.ExternalDetail{External: networks.External{ID: id}}, nil
}

func serveNetworks(h *NetworksHandler, req *http.Request) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /networks", h.List)
	mux.HandleFunc("POST /networks", h.Create)
	mux.HandleFunc("GET /networks/config", h.Config)
	mux.HandleFunc("GET /networks/docker", h.ListDocker)
	mux.HandleFunc("GET /networks/docker/{id}", h.GetDocker)
	mux.HandleFunc("GET /networks/{name}", h.Get)
	mux.HandleFunc("PATCH /networks/{name}", h.Update)
	mux.HandleFunc("DELETE /networks/{name}", h.Delete)
	mux.HandleFunc("POST /networks/{name}/members", h.Attach)
	mux.HandleFunc("DELETE /networks/{name}/members/{kind}/{member}", h.Detach)
	mux.HandleFunc("POST /networks/{name}/disconnect", h.Disconnect)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

var networksRoutes = []struct{ method, path, body, scope string }{
	{"GET", "/networks", "", "networks:read"},
	{"GET", "/networks/jobs", "", "networks:read"},
	{"GET", "/networks/config", "", "networks:read"},
	{"GET", "/networks/docker", "", "networks:read"},
	{"GET", "/networks/docker/abc", "", "networks:read"},
	{"POST", "/networks", `{"name":"jobs"}`, "networks:write"},
	{"PATCH", "/networks/jobs", `{"description":"x"}`, "networks:write"},
	{"DELETE", "/networks/jobs", "", "networks:write"},
	{"POST", "/networks/jobs/members", `{"kind":"site","name":"shop"}`, "networks:write"},
	{"DELETE", "/networks/jobs/members/site/shop", "", "networks:write"},
	{"POST", "/networks/jobs/disconnect", `{"containerId":"abc"}`, "networks:write"},
}

func newNetworksTestHandler() (*NetworksHandler, *mockNetworksManager) {
	m := &mockNetworksManager{}
	return &NetworksHandler{Manager: m, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, m
}

func TestNetworksScopes(t *testing.T) {
	h, _ := newNetworksTestHandler()
	for _, rt := range networksRoutes {
		other := "services:write"
		if rec := serveNetworks(h, hostingRequest(rt.method, rt.path, rt.body, other)); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s with %s: %d", rt.method, rt.path, other, rec.Code)
		}
		if rec := serveNetworks(h, hostingRequest(rt.method, rt.path, rt.body, rt.scope)); rec.Code >= 300 {
			t.Fatalf("%s %s with %s: %d %s", rt.method, rt.path, rt.scope, rec.Code, rec.Body)
		}
	}
	// A read key cannot write.
	if rec := serveNetworks(h, hostingRequest("POST", "/networks", `{"name":"x"}`, "networks:read")); rec.Code != http.StatusForbidden {
		t.Fatalf("%d", rec.Code)
	}
}

func TestNetworksHandler(t *testing.T) {
	h, m := newNetworksTestHandler()
	rec := serveNetworks(h, hostingRequest("POST", "/networks", `{"name":"jobs","subnet":"10.20.0.0/24","description":"q"}`))
	if rec.Code != http.StatusCreated || m.created.Subnet != "10.20.0.0/24" || m.actor == "" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := serveNetworks(h, hostingRequest("POST", "/networks", `{"name":"jobs","driver":"host"}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	rec = serveNetworks(h, hostingRequest("POST", "/networks/jobs/members", `{"kind":"instance","name":"elasticmq-jobs"}`))
	body := decodeBody(t, rec)
	if rec.Code != http.StatusOK || body["operation"] != "op" || m.attached != [3]string{"jobs", "instance", "elasticmq-jobs"} {
		t.Fatalf("%d %v", rec.Code, body)
	}
	if rec := serveNetworks(h, hostingRequest("POST", "/networks/jobs/disconnect", `{}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty container: %d", rec.Code)
	}
	if body := decodeBody(t, serveNetworks(h, hostingRequest("GET", "/networks/config", ""))); body["max"] != float64(50) {
		t.Fatalf("config = %v", body)
	}
	body = decodeBody(t, serveNetworks(h, hostingRequest("GET", "/networks/docker/abc", "")))
	if body["id"] != "abc" {
		t.Fatalf("docker detail = %v", body)
	}
	body = decodeBody(t, serveNetworks(h, hostingRequest("GET", "/networks/jobs", "")))
	if members, _ := body["members"].([]any); len(members) != 1 {
		t.Fatalf("detail = %v", body)
	}
}

func TestNetworksErrors(t *testing.T) {
	h, m := newNetworksTestHandler()
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&networks.Error{Code: networks.CodeInUse, Message: "x"}, http.StatusConflict, networks.CodeInUse},
		{&networks.Error{Code: networks.CodeNotFound}, http.StatusNotFound, networks.CodeNotFound},
		{&networks.Error{Code: networks.CodeStorage, Message: "/secret/path"}, http.StatusInternalServerError, networks.CodeStorage},
		// Errors of the member changed by an attach.
		{&services.Error{Code: services.CodeInvalidNetwork}, http.StatusBadRequest, services.CodeInvalidNetwork},
		{&hosting.Error{Code: hosting.CodeOperationRunning}, http.StatusConflict, hosting.CodeOperationRunning},
	}
	for _, c := range cases {
		m.err = c.err
		rec := serveNetworks(h, hostingRequest("POST", "/networks/jobs/members", `{"kind":"site","name":"shop"}`))
		body := decodeBody(t, rec)
		if rec.Code != c.status || body["code"] != c.code {
			t.Errorf("%v: %d %v", c.err, rec.Code, body)
		}
		if c.status == http.StatusInternalServerError && rec.Body.String() != "" && body["error"] != "Storage error" {
			t.Errorf("a storage error never leaks its message: %v", body)
		}
	}
	for _, code := range networks.AllCodes {
		if _, ok := networksErrorStatus[code]; !ok {
			t.Errorf("unmapped code %s", code)
		}
	}
}
