package handler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"host-panel-api/internal/services"
	"host-panel-api/internal/services/postgres"
)

type mockServicesManager struct {
	err       error
	created   *createResourceRequest
	deleted   [2]string
	retired   string
	lastActor string
	networks  []string
	exposure  *services.Exposure
	exposed   bool
	updated   string
	checked   bool
}

func (m *mockServicesManager) UpdateNetworks(_ context.Context, key string, nets []string, actor string) ([]string, error) {
	m.networks, m.lastActor = nets, actor
	return nets, m.err
}
func (m *mockServicesManager) SetExposure(_ context.Context, key string, exp *services.Exposure, actor string) (*services.InstanceSummary, error) {
	m.exposure, m.exposed, m.lastActor = exp, true, actor
	return &services.InstanceSummary{Key: key}, m.err
}
func (m *mockServicesManager) Update(_ context.Context, key, version, actor string) (*services.UpdateResult, error) {
	m.updated, m.lastActor = key+"@"+version, actor
	if m.err != nil {
		return nil, m.err
	}
	return &services.UpdateResult{Updated: true, Version: version}, nil
}
func (m *mockServicesManager) CheckUpdates(context.Context) (services.CatalogStatus, error) {
	m.checked = true
	return services.CatalogStatus{Failed: []string{"nats"}}, m.err
}
func (m *mockServicesManager) ExposureLimits() services.ExposureLimits {
	return services.ExposureLimits{AllowedCIDRs: []string{"127.0.0.1/32"}, MinPort: 1024, MaxPort: 32767, TLSMode: services.TLSAuto}
}

func (m *mockServicesManager) Instances(context.Context) ([]services.InstanceSummary, error) {
	return []services.InstanceSummary{{Key: "postgres-16"}}, m.err
}
func (m *mockServicesManager) Resources(_ context.Context, key string) ([]services.ResourceSummary, error) {
	return []services.ResourceSummary{{Instance: key, Name: "shop"}}, m.err
}
func (m *mockServicesManager) Create(_ context.Context, req services.CreateRequest, actor string) (*services.CreatedResource, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.created = &createResourceRequest{Engine: req.Engine, Version: req.Version, Name: req.Name, Options: req.Options, LegacyAuth: req.LegacyAuth, Networks: req.Networks}
	m.lastActor = actor
	return &services.CreatedResource{Credentials: []services.Credential{{Key: "password", Value: "secret", Secret: true}}}, nil
}
func (m *mockServicesManager) UpdateOptions(_ context.Context, key string, changes map[string]string, actor string) (services.Options, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.lastActor = actor
	return services.Options(changes), nil
}
func (m *mockServicesManager) Delete(_ context.Context, key, name, actor string) (*services.DeleteResult, error) {
	m.deleted = [2]string{key, name}
	return &services.DeleteResult{InstanceRetired: true}, m.err
}
func (m *mockServicesManager) RotatePassword(_ context.Context, key, name, actor string) ([]services.Credential, error) {
	return []services.Credential{{Key: "password", Value: "new", Secret: true}}, m.err
}
func (m *mockServicesManager) Retire(_ context.Context, key, actor string) error {
	m.retired = key
	return m.err
}
func (m *mockServicesManager) Purge(_ context.Context, key, actor string) error {
	m.retired = "purged:" + key
	return m.err
}

func newServicesTestHandler(t *testing.T, enabled bool) (*ServicesHandler, *mockServicesManager) {
	reg, err := services.NewRegistry([]services.Engine{postgres.New()}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mgr := &mockServicesManager{}
	return &ServicesHandler{
		Manager: mgr, Registry: reg, Enabled: enabled, Network: "loch-services",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, mgr
}

func serveServices(h *ServicesHandler, req *http.Request) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /services/config", h.Config)
	mux.HandleFunc("GET /services/instances", h.ListInstances)
	mux.HandleFunc("DELETE /services/instances/{instance}", h.RetireInstance)
	mux.HandleFunc("PATCH /services/instances/{instance}", h.UpdateInstance)
	mux.HandleFunc("POST /services/instances/{instance}/update", h.UpdateInstanceImage)
	mux.HandleFunc("POST /services/updates/check", h.CheckUpdates)
	mux.HandleFunc("GET /services/instances/{instance}/resources", h.ListResources)
	mux.HandleFunc("POST /services/resources", h.CreateResource)
	mux.HandleFunc("POST /services/instances/{instance}/resources/{name}/password", h.RotatePassword)
	mux.HandleFunc("DELETE /services/instances/{instance}/resources/{name}", h.DeleteResource)
	mux.HandleFunc("PUT /services/instances/{instance}/networks", h.UpdateInstanceNetworks)
	mux.HandleFunc("PUT /services/instances/{instance}/exposure", h.SetExposure)
	mux.HandleFunc("DELETE /services/instances/{instance}/exposure", h.DeleteExposure)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

var servicesRoutes = []struct{ method, path, body, scope string }{
	{"POST", "/services/instances/redis-cache/update", `{"version":"8.4"}`, "services:write"},
	{"POST", "/services/updates/check", "", "services:write"},
	{"GET", "/services/instances", "", "services:read"},
	{"GET", "/services/instances/postgres-16/resources", "", "services:read"},
	{"POST", "/services/resources", `{"engine":"postgres","version":"16","name":"shop"}`, "services:write"},
	{"POST", "/services/instances/postgres-16/resources/shop/password", "", "services:write"},
	{"DELETE", "/services/instances/postgres-16/resources/shop", "", "services:write"},
	{"DELETE", "/services/instances/postgres-16", "", "services:write"},
	{"PUT", "/services/instances/postgres-16/networks", `{"networks":["services"]}`, "networks:write"},
	{"PUT", "/services/instances/postgres-16/exposure", `{"address":"127.0.0.1","port":5416}`, "services:expose"},
	{"DELETE", "/services/instances/postgres-16/exposure", "", "services:expose"},
}

func TestServicesConfig(t *testing.T) {
	h, _ := newServicesTestHandler(t, true)
	body := decodeBody(t, serveServices(h, hostingRequest("GET", "/services/config", "")))
	engines, _ := body["engines"].([]any)
	if body["enabled"] != true || body["network"] != "loch-services" || len(engines) != 1 {
		t.Fatalf("body = %v", body)
	}
	exp, _ := body["exposure"].(map[string]any)
	if exp == nil || exp["tlsMode"] != "auto" || exp["minPort"] != float64(1024) || len(exp["allowedCidrs"].([]any)) != 1 {
		t.Fatalf("exposure = %v", body)
	}
	pg := engines[0].(map[string]any)
	if v := pg["versions"].([]any)[0].(map[string]any); v["tls"] != true || v["tlsOptional"] != true {
		t.Fatalf("TLS capabilities of the versions: %v", v)
	}
	if pg["id"] != "postgres" || pg["defaultPort"] != float64(5432) || len(pg["versions"].([]any)) != 4 {
		t.Fatalf("engine = %v", pg)
	}

	// API keys need services:read, like the other routes.
	if rec := serveServices(h, hostingRequest("GET", "/services/config", "", "hosting:read")); rec.Code != http.StatusForbidden {
		t.Fatalf("config without scope: %d", rec.Code)
	}

	off, _ := newServicesTestHandler(t, false)
	off.ConfigError = "SERVICES_NETWORK: boom"
	body = decodeBody(t, serveServices(off, hostingRequest("GET", "/services/config", "")))
	if body["enabled"] != false || body["engines"] != nil || body["error"] != "SERVICES_NETWORK: boom" {
		t.Fatalf("disabled body = %v", body)
	}
}

func TestServicesDisabledRoutes(t *testing.T) {
	h, _ := newServicesTestHandler(t, false)
	for _, rt := range servicesRoutes {
		rec := serveServices(h, hostingRequest(rt.method, rt.path, rt.body))
		if rec.Code != http.StatusNotFound || decodeBody(t, rec)["code"] != services.CodeDisabled {
			t.Fatalf("%s %s: %d %s", rt.method, rt.path, rec.Code, rec.Body)
		}
	}
}

func TestServicesScopes(t *testing.T) {
	h, _ := newServicesTestHandler(t, true)
	for _, rt := range servicesRoutes {
		other := "services:read"
		if rt.scope == "services:read" {
			other = "hosting:read"
		}
		if rec := serveServices(h, hostingRequest(rt.method, rt.path, rt.body, other)); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s with %s: %d", rt.method, rt.path, other, rec.Code)
		}
		if rec := serveServices(h, hostingRequest(rt.method, rt.path, rt.body, rt.scope)); rec.Code >= 300 {
			t.Fatalf("%s %s with %s: %d %s", rt.method, rt.path, rt.scope, rec.Code, rec.Body)
		}
	}
}

func TestServicesCreate(t *testing.T) {
	h, mgr := newServicesTestHandler(t, true)
	rec := serveServices(h, hostingRequest("POST", "/services/resources", `{"engine":"postgres","version":"16","name":"shop"}`))
	if rec.Code != http.StatusCreated || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if c := mgr.created; c.Engine != "postgres" || c.Version != "16" || c.Name != "shop" || c.Options != nil || mgr.lastActor != "admin@example.com" {
		t.Fatalf("created = %+v by %s", mgr.created, mgr.lastActor)
	}
	rec = serveServices(h, hostingRequest("POST", "/services/resources", `{"engine":"redis","version":"8.4","name":"cache","options":{"memory":"512"}}`))
	if rec.Code != http.StatusCreated || mgr.created.Options["memory"] != "512" {
		t.Fatalf("options: %d %+v", rec.Code, mgr.created)
	}
	for _, body := range []string{`{"engine":"postgres","version":"16","name":"shop","privileged":true}`, `not json`, strings.Repeat("x", 5000), `{"engine":"postgres","version":"16","name":"shop"}{"name":"x"}`, `{"engine":"redis","options":{"a":"1","b":"1","c":"1","d":"1","e":"1","f":"1","g":"1","h":"1","i":"1","j":"1","k":"1","l":"1","m":"1","n":"1","o":"1","p":"1","q":"1"}}`} {
		if rec := serveServices(h, hostingRequest("POST", "/services/resources", body)); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %.20q: %d", body, rec.Code)
		}
	}
	rec = serveServices(h, hostingRequest("POST", "/services/instances/postgres-16/resources/shop/password", ""))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rotate: %d %v", rec.Code, rec.Header())
	}
}

func TestServicesErrorStatus(t *testing.T) {
	cases := map[string]int{
		services.CodeInvalidName:       http.StatusBadRequest,
		services.CodeResourceExists:    http.StatusConflict,
		services.CodeInstanceNotFound:  http.StatusNotFound,
		services.CodeNotReady:          http.StatusServiceUnavailable,
		services.CodeEngineError:       http.StatusBadGateway,
		services.CodeOutcomeUnknown:    http.StatusGatewayTimeout,
		services.CodeInProgress:        http.StatusConflict,
		services.CodeDirectoryConflict: http.StatusConflict,
	}
	for code, status := range cases {
		h, mgr := newServicesTestHandler(t, true)
		mgr.err = &services.Error{Code: code, Field: "name", Message: "msg"}
		rec := serveServices(h, hostingRequest("POST", "/services/resources", `{"engine":"postgres","version":"16","name":"shop"}`))
		body := decodeBody(t, rec)
		if rec.Code != status || body["code"] != code || body["field"] != "name" {
			t.Fatalf("%s: %d %v", code, rec.Code, body)
		}
	}
	// Storage errors and unexpected errors never expose their message.
	h, mgr := newServicesTestHandler(t, true)
	mgr.err = &services.Error{Code: services.CodeStorage, Message: "open /data/services/x: permission denied"}
	rec := serveServices(h, hostingRequest("GET", "/services/instances", ""))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "/data") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	mgr.err = errors.New("raw failure with /secret/path")
	rec = serveServices(h, hostingRequest("GET", "/services/instances", ""))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestServicesPathValues(t *testing.T) {
	h, mgr := newServicesTestHandler(t, true)
	serveServices(h, hostingRequest("DELETE", "/services/instances/mariadb-11-4/resources/blog", ""))
	if mgr.deleted != [2]string{"mariadb-11-4", "blog"} {
		t.Fatalf("deleted = %v", mgr.deleted)
	}
	rec := serveServices(h, hostingRequest("DELETE", "/services/instances/postgres-16", ""))
	if rec.Code != http.StatusNoContent || mgr.retired != "postgres-16" {
		t.Fatalf("%d %s", rec.Code, mgr.retired)
	}
}

// Every error code of the services package is mapped to an HTTP status.
func TestServicesErrorStatusIsComplete(t *testing.T) {
	codes := services.AllCodes
	for _, c := range codes {
		if _, ok := servicesErrorStatus[c]; !ok {
			t.Errorf("%s has no HTTP status", c)
		}
	}
}

func TestServicesUpdateInstance(t *testing.T) {
	h, mgr := newServicesTestHandler(t, true)
	rec := serveServices(h, hostingRequest("PATCH", "/services/instances/redis-cache", `{"options":{"memory":"512"}}`))
	if rec.Code != http.StatusOK || mgr.lastActor != "admin@example.com" || !strings.Contains(rec.Body.String(), `"memory":"512"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	for _, body := range []string{`{}`, `{"options":{}}`, `{"options":{"memory":"512"},"name":"x"}`, `not json`} {
		if rec := serveServices(h, hostingRequest("PATCH", "/services/instances/redis-cache", body)); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: %d", body, rec.Code)
		}
	}
}

func TestServicesUpdateInstanceImage(t *testing.T) {
	h, mgr := newServicesTestHandler(t, true)
	rec := serveServices(h, hostingRequest("POST", "/services/instances/redis-cache/update", `{"version":"8.4"}`))
	if rec.Code != http.StatusOK || mgr.updated != "redis-cache@8.4" || mgr.lastActor != "admin@example.com" || !strings.Contains(rec.Body.String(), `"updated":true`) {
		t.Fatalf("%d %s %q", rec.Code, rec.Body.String(), mgr.updated)
	}
	// No body: the latest image of the current version.
	if rec := serveServices(h, hostingRequest("POST", "/services/instances/redis-cache/update", "")); rec.Code != http.StatusOK || mgr.updated != "redis-cache@" {
		t.Fatalf("%d %q", rec.Code, mgr.updated)
	}
	for _, body := range []string{`{"version":"8.4","x":1}`, `not json`} {
		if rec := serveServices(h, hostingRequest("POST", "/services/instances/redis-cache/update", body)); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: %d", body, rec.Code)
		}
	}
	for code, status := range map[string]int{
		services.CodeVersionDowngrade: http.StatusBadRequest, services.CodeUpdateNotSupported: http.StatusBadRequest,
		services.CodePullFailed: http.StatusBadGateway, services.CodeStartFailed: http.StatusBadGateway,
	} {
		mgr.err = &services.Error{Code: code, Message: "x"}
		if rec := serveServices(h, hostingRequest("POST", "/services/instances/redis-cache/update", "{}")); rec.Code != status || !strings.Contains(rec.Body.String(), code) {
			t.Fatalf("%s: %d %s", code, rec.Code, rec.Body.String())
		}
	}
}

func TestServicesCheckUpdates(t *testing.T) {
	h, mgr := newServicesTestHandler(t, true)
	rec := serveServices(h, hostingRequest("POST", "/services/updates/check", ""))
	if rec.Code != http.StatusOK || !mgr.checked || !strings.Contains(rec.Body.String(), `"failed":["nats"]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = serveServices(h, hostingRequest("GET", "/services/config", ""))
	if !strings.Contains(rec.Body.String(), `"catalog":{}`) {
		t.Fatalf("catalog status in the config: %s", rec.Body.String())
	}
}

func TestServicesPurgeInstance(t *testing.T) {
	h, mgr := newServicesTestHandler(t, true)
	if rec := serveServices(h, hostingRequest("DELETE", "/services/instances/redis-cache?purge=true", "")); rec.Code != http.StatusNoContent || mgr.retired != "purged:redis-cache" {
		t.Fatalf("%d %q", rec.Code, mgr.retired)
	}
	if rec := serveServices(h, hostingRequest("DELETE", "/services/instances/redis-cache", "")); rec.Code != http.StatusNoContent || mgr.retired != "redis-cache" {
		t.Fatalf("without purge, the usual retirement: %d %q", rec.Code, mgr.retired)
	}
	if rec := serveServices(h, hostingRequest("DELETE", "/services/instances/redis-cache?purge=yes", "")); rec.Code != http.StatusBadRequest {
		t.Fatalf("%d", rec.Code)
	}
}

func TestServicesLegacyAuth(t *testing.T) {
	h, mgr := newServicesTestHandler(t, true)
	rec := serveServices(h, hostingRequest("POST", "/services/resources", `{"engine":"postgres","version":"17","name":"shop","legacyAuth":true}`))
	if rec.Code != http.StatusCreated || mgr.created == nil || !mgr.created.LegacyAuth {
		t.Fatalf("%d %+v", rec.Code, mgr.created)
	}
	// The configuration tells which versions offer it.
	rec = serveServices(h, hostingRequest("GET", "/services/config", ""))
	if !strings.Contains(rec.Body.String(), `"legacyAuth":true`) {
		t.Fatalf("legacyAuth not announced: %s", rec.Body.String())
	}
}

func TestServicesNetworksAndExposure(t *testing.T) {
	h, mgr := newServicesTestHandler(t, true)
	rec := serveServices(h, hostingRequest("PUT", "/services/instances/postgres-16/networks", `{"networks":["jobs","services"]}`))
	if rec.Code != http.StatusOK || len(mgr.networks) != 2 {
		t.Fatalf("networks: %d %s", rec.Code, rec.Body)
	}
	rec = serveServices(h, hostingRequest("PUT", "/services/instances/postgres-16/networks", `{"networks":["a"],"x":1}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	rec = serveServices(h, hostingRequest("PUT", "/services/instances/postgres-16/exposure", `{"address":"127.0.0.1","port":5416,"tlsOptional":true}`))
	if rec.Code != http.StatusOK || mgr.exposure == nil || mgr.exposure.Port != 5416 || !mgr.exposure.TLSOptional {
		t.Fatalf("exposure: %d %s %+v", rec.Code, rec.Body, mgr.exposure)
	}
	mgr.exposed = false
	rec = serveServices(h, hostingRequest("PUT", "/services/instances/postgres-16/exposure", `{"address":"localhost","port":5416}`))
	if rec.Code != http.StatusBadRequest || mgr.exposed {
		t.Fatalf("invalid address: %d", rec.Code)
	}
	rec = serveServices(h, hostingRequest("DELETE", "/services/instances/postgres-16/exposure", ""))
	if rec.Code != http.StatusOK || !mgr.exposed || mgr.exposure != nil {
		t.Fatalf("close: %d", rec.Code)
	}
	mgr.err = &services.Error{Code: services.CodePortInUse, Message: "taken"}
	rec = serveServices(h, hostingRequest("PUT", "/services/instances/postgres-16/exposure", `{"address":"127.0.0.1","port":5416}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("port in use: %d", rec.Code)
	}
}
