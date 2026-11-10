//go:build integration

package s3

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

const clientImage = "mirror.gcr.io/amazon/aws-cli:2.31.0"

func credential(creds []services.Credential, key string) string {
	for _, c := range creds {
		if c.Key == key {
			return c.Value
		}
	}
	return ""
}

func TestEndToEnd(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	ctx := context.Background()
	root, _ := os.MkdirTemp(os.Getenv("HOME"), ".loch-services-it-")
	root, _ = filepath.EvalSymlinks(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	stacks, data := filepath.Join(root, "compose"), filepath.Join(root, "services")
	os.Mkdir(stacks, 0o755)
	os.Mkdir(data, 0o755)
	network := "loch-services-it-s3"
	exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).Run()
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })

	reg, err := services.NewRegistry([]services.Engine{New()}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	cfg := services.Config{StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)}}
	m := services.NewManager(cfg, reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	project := services.InstanceRef{Engine: "s3", Name: "media"}.Project()
	dir := filepath.Join(stacks, project)
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", dir,
			"-f", filepath.Join(dir, "compose.yaml"), "-f", filepath.Join(dir, services.OverrideFile), "down").Run()
	})

	res, err := m.Create(ctx, services.CreateRequest{Engine: "s3", Version: "1.4", Name: "media"}, "it")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, logs.String())
	}
	servicestest.CheckPinning(t, m, stacks, data, "s3-media", services.InstanceRef{Engine: "s3", Name: "media"}.Project())
	endpoint, key, secret := credential(res.Credentials, "endpoint"), credential(res.Credentials, "accessKeyId"), credential(res.Credentials, "secretAccessKey")
	host := credential(res.Credentials, "host")
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "hello.txt"), []byte("hello"), 0o644)
	aws := func(k, s string, args ...string) (string, error) {
		full := []string{"run", "--rm", "--network", network, "-v", work + ":/w",
			"-e", "AWS_ACCESS_KEY_ID=" + k, "-e", "AWS_SECRET_ACCESS_KEY=" + s, "-e", "AWS_DEFAULT_REGION=" + Region,
			clientImage, "--endpoint-url", endpoint}
		out, err := exec.Command("docker", append(full, args...)...).CombinedOutput()
		return string(out), err
	}
	if b := credential(res.Credentials, "bucket"); b != "media" {
		t.Fatalf("bucket credential: %q", b)
	}
	// The bucket exists from the start (S3_INIT_BUCKETS).
	if out, err := aws(key, secret, "s3api", "head-bucket", "--bucket", "media"); err != nil {
		t.Fatalf("bucket created at start: %v %s\n%s", err, out, logs.String())
	}
	if out, err := aws(key, secret, "s3", "cp", "/w/hello.txt", "s3://media/hello.txt"); err != nil {
		t.Fatalf("put: %v %s", err, out)
	}
	get := func(k, s string) (string, error) {
		out, err := aws(k, s, "s3", "cp", "s3://media/hello.txt", "-")
		return strings.TrimSpace(out), err
	}
	if out, err := get(key, secret); err != nil || out != "hello" {
		t.Fatalf("get: %v %q", err, out)
	}
	if out, err := get(key, "WrongSecretWrongSecretWrongSecre"); err == nil {
		t.Fatalf("wrong secret accepted: %s", out)
	}
	curl := func(args ...string) string {
		out, _ := exec.Command("docker", append([]string{"run", "--rm", "--network", network, "mirror.gcr.io/curlimages/curl:8.16.0",
			"-s", "-o", "/dev/null", "-w", "%{http_code}"}, args...)...).Output()
		return string(out)
	}
	if code := curl("http://" + host + ":8080/media/hello.txt"); code != "403" {
		t.Fatalf("anonymous read: %s", code)
	}
	if code := curl("-H", "Authorization: Bearer anything", "http://"+host+":8080/_loch/users"); code != "403" {
		t.Fatalf("the admin API must stay off: %s", code)
	}

	// Large object: multipart upload and parallel ranged download.
	big := make([]byte, 25_000_000)
	for i := range big {
		big[i] = byte(i * 7 % 251)
	}
	os.WriteFile(filepath.Join(work, "big.bin"), big, 0o644)
	if out, err := aws(key, secret, "s3", "cp", "/w/big.bin", "s3://media/big.bin"); err != nil {
		t.Fatalf("multipart upload: %v %s", err, out)
	}
	if out, err := aws(key, secret, "s3", "cp", "s3://media/big.bin", "/w/big.back"); err != nil {
		t.Fatalf("ranged download: %v %s", err, out)
	}
	if back, _ := os.ReadFile(filepath.Join(work, "big.back")); string(back) != string(big) {
		t.Fatalf("download differs from the upload (%d bytes instead of %d)", len(back), len(big))
	}
	if out, err := aws(key, secret, "s3api", "get-object", "--bucket", "media", "--key", "big.bin", "--range", "bytes=-10", "/w/tail", "--query", "ContentRange", "--output", "text"); err != nil || strings.TrimSpace(out) != "bytes 24999990-24999999/25000000" {
		t.Fatalf("range: %v %q", err, out)
	}
	if code := curl("http://" + host + ":8080/_loch/health"); code != "200" {
		t.Fatalf("health: %s", code)
	}

	// Hardening and secret placement.
	var info []struct {
		Config struct {
			User string
			Env  []string
		}
		HostConfig struct {
			ReadonlyRootfs bool
			CapDrop        []string
		}
	}
	out, _ := exec.Command("docker", "inspect", host).Output()
	if json.Unmarshal(out, &info) != nil || len(info) != 1 || info[0].Config.User != "10001:10001" || !info[0].HostConfig.ReadonlyRootfs || len(info[0].HostConfig.CapDrop) != 1 {
		t.Fatalf("container hardening: %s", out)
	}
	if strings.Contains(string(out), secret) {
		t.Fatal("the secret must not appear in docker inspect")
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if strings.Contains(string(compose), secret) {
		t.Fatal("the secret must not appear in the stacks directory")
	}
	st, err := os.Stat(filepath.Join(data, services.PrivateDir, project, services.StateDir, "users.json"))
	// 0400 owned by the container user when the API runs as root; 0444
	// behind private directories otherwise.
	want := os.FileMode(0o444)
	if os.Geteuid() == 0 {
		want = 0o400
	}
	if err != nil || st.Mode().Perm() != want {
		t.Fatalf("users file: %v %v", st, err)
	}

	// Resize: recreated, data and credentials kept.
	if _, err := m.UpdateOptions(ctx, "s3-media", map[string]string{"memory": "512"}, "it"); err != nil {
		t.Fatalf("update: %v\n%s", err, logs.String())
	}
	if out, err := get(key, secret); err != nil || out != "hello" {
		t.Fatalf("data or credentials lost by the update: %v %q", err, out)
	}

	// Rotation: new key pair, old one refused, data kept.
	creds, err := m.RotatePassword(ctx, "s3-media", "media", "it")
	if err != nil {
		t.Fatalf("rotate: %v\n%s", err, logs.String())
	}
	key2, secret2 := credential(creds, "accessKeyId"), credential(creds, "secretAccessKey")
	if key2 == key || secret2 == secret {
		t.Fatal("rotation changes the key pair")
	}
	if _, err := get(key, secret); err == nil {
		t.Fatal("old key still accepted")
	}
	if out, err := get(key2, secret2); err != nil || out != "hello" {
		t.Fatalf("new key refused or data lost: %v %q", err, out)
	}

	serverLogsOut, _ := exec.Command("docker", "logs", host).CombinedOutput()
	serverLogs := string(serverLogsOut)
	if _, err := m.Delete(ctx, "s3-media", "media", "it"); err != nil {
		t.Fatalf("delete: %v\n%s", err, logs.String())
	}
	if err := exec.Command("docker", "inspect", host).Run(); err == nil {
		t.Fatal("container still present")
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), secret2) {
		t.Fatal("secret in the logs")
	}
	if strings.Contains(serverLogs, secret) || strings.Contains(serverLogs, secret2) {
		t.Fatal("secret in the container logs")
	}
}

// TestEncryption: with encryption=on, every object is stored encrypted with
// the key generated by the panel, never put in the environment.
func TestEncryption(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	ctx := context.Background()
	root, _ := os.MkdirTemp(os.Getenv("HOME"), ".loch-services-it-")
	root, _ = filepath.EvalSymlinks(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	stacks, data := filepath.Join(root, "compose"), filepath.Join(root, "services")
	os.Mkdir(stacks, 0o755)
	os.Mkdir(data, 0o755)
	network := "loch-services-it-s3enc"
	exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).Run()
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })
	reg, err := services.NewRegistry([]services.Engine{New()}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	m := services.NewManager(services.Config{StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)}},
		reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	project := services.InstanceRef{Engine: "s3", Name: "vault"}.Project()
	dir := filepath.Join(stacks, project)
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", dir,
			"-f", filepath.Join(dir, "compose.yaml"), "-f", filepath.Join(dir, services.OverrideFile), "down").Run()
	})
	res, err := m.Create(ctx, services.CreateRequest{Engine: "s3", Version: "1.4", Name: "vault", Options: map[string]string{"encryption": "on"}}, "it")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, logs.String())
	}
	endpoint, key, secret := credential(res.Credentials, "endpoint"), credential(res.Credentials, "accessKeyId"), credential(res.Credentials, "secretAccessKey")
	work := t.TempDir()
	marker := "LOCH-PLAINTEXT-MARKER-" + strings.Repeat("x", 64)
	os.WriteFile(filepath.Join(work, "secret.txt"), []byte(marker), 0o644)
	aws := func(args ...string) (string, error) {
		full := []string{"run", "--rm", "--network", network, "-v", work + ":/w",
			"-e", "AWS_ACCESS_KEY_ID=" + key, "-e", "AWS_SECRET_ACCESS_KEY=" + secret, "-e", "AWS_DEFAULT_REGION=" + Region,
			clientImage, "--endpoint-url", endpoint}
		out, err := exec.Command("docker", append(full, args...)...).CombinedOutput()
		return string(out), err
	}
	if out, err := aws("s3", "cp", "/w/secret.txt", "s3://vault/secret.txt"); err != nil {
		t.Fatalf("put: %v %s", err, out)
	}
	if out, err := aws("s3api", "head-object", "--bucket", "vault", "--key", "secret.txt", "--query", "ServerSideEncryption", "--output", "text"); err != nil || strings.TrimSpace(out) != "AES256" {
		t.Fatalf("encrypted by default: %v %q", err, out)
	}
	if out, err := aws("s3", "cp", "s3://vault/secret.txt", "-"); err != nil || strings.TrimSpace(out) != marker {
		t.Fatalf("read back: %v %q", err, out)
	}
	// Nothing in clear on disk.
	filepath.Walk(filepath.Join(data, project), func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "LOCH-PLAINTEXT-MARKER") {
				t.Errorf("plaintext found in %s", p)
			}
		}
		return nil
	})
	// The key: generated in the private state, never in the environment.
	k, err := os.ReadFile(filepath.Join(data, services.PrivateDir, project, services.StateDir, "sse.key"))
	if err != nil || len(strings.TrimSpace(string(k))) != 44 {
		t.Fatalf("key: %v %q", err, k)
	}
	inspect, _ := exec.Command("docker", "inspect", project).Output()
	srvLogs, _ := exec.Command("docker", "logs", project).CombinedOutput()
	if strings.Contains(string(inspect), strings.TrimSpace(string(k))) || strings.Contains(string(srvLogs), strings.TrimSpace(string(k))) {
		t.Fatal("the key must appear neither in docker inspect nor in the logs")
	}
	// The key survives a rotation (the data stays readable).
	creds, err := m.RotatePassword(ctx, "s3-vault", "vault", "it")
	if err != nil {
		t.Fatal(err)
	}
	key, secret = credential(creds, "accessKeyId"), credential(creds, "secretAccessKey")
	if out, err := aws("s3", "cp", "s3://vault/secret.txt", "-"); err != nil || strings.TrimSpace(out) != marker {
		t.Fatalf("read after rotation: %v %q", err, out)
	}
}
