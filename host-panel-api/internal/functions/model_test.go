package functions

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"host-panel-api/internal/networks"
)

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"a", "resize", "img-resize-2", strings.Repeat("a", 21)} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "1abc", "-a", "a-", "A", "a_b", "a.b", strings.Repeat("a", 22), "x/../y", "config", "new", "a b", "é"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	n := nm("resize")
	if n.Project() != "fn-resize" || n.DedicatedNetwork() != "fn-resize" {
		t.Fatal("naming")
	}
	// The default network of the longest name is a valid network name.
	long := nm(strings.Repeat("a", MaxNameLen))
	if err := networks.ValidateName(long.DedicatedNetwork()); err != nil {
		t.Fatalf("%s: %v", long.DedicatedNetwork(), err)
	}
	if _, err := ParseName("../x"); err == nil {
		t.Fatal("ParseName accepted a traversal")
	}
}

func TestValidateHandler(t *testing.T) {
	for _, ok := range []string{"index.handler", "src/index.handler", "app.main.handler", "lambda_function.lambda_handler", "my-file.run"} {
		if err := runtimes["nodejs"].ValidateHandler(ok); err != nil {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "index", "../x.handler", "/abs.handler", "a/./b.handler", "a b.handler", "x.$fn",
		"x.1fn", ".handler", "a//b.h", "index.handler;rm", strings.Repeat("a", 125) + ".abc"} {
		if err := runtimes["python"].ValidateHandler(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestValidateEnv(t *testing.T) {
	ok := []EnvVar{{"OUTPUT_BUCKET", "thumbs"}, {"AWS_ENDPOINT_URL_S3", "http://svc-s3-media:9000"}, {"NODE_OPTIONS", "--enable-source-maps"}}
	if err := ValidateEnv(ok, []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AWS_LAMBDA_FUNCTION_MEMORY_SIZE", "AWS_LAMBDA_RUNTIME_API", "AWS_LAMBDA_MAX_CONCURRENCY",
		"_HANDLER", "_X_AMZN_TRACE_ID", "LAMBDA_TASK_ROOT", "LD_PRELOAD", "LD_LIBRARY_PATH", "LOCH_RUNTIME_AUTH",
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_EXECUTION_ENV", "AWS_EC2_METADATA_DISABLED", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"PATH", "path", "HOME", "PYTHONDONTWRITEBYTECODE", "ld_preload"} {
		if err := ValidateEnv([]EnvVar{{name, "x"}}, nil); CodeOf(err) != CodeReservedEnv {
			t.Errorf("%s: %v", name, err)
		}
		if err := ValidateEnv(nil, []string{name}); CodeOf(err) != CodeReservedEnv {
			t.Errorf("secret %s: %v", name, err)
		}
	}
	for name, env := range map[string][]EnvVar{
		"bad name":     {{"1X", "x"}},
		"dash":         {{"A-B", "x"}},
		"newline":      {{"A", "x\ny"}},
		"CR":           {{"A", "x\ry"}},
		"NUL":          {{"A", "x\x00"}},
		"not UTF-8":    {{"A", "\xff"}},
		"too long":     {{"A", strings.Repeat("x", maxEnvValue+1)}},
		"duplicate":    {{"A", "1"}, {"A", "2"}},
		"long name":    {{strings.Repeat("A", 129), "x"}},
		"empty name":   {{"", "x"}},
		"equals sign":  {{"A=B", "x"}},
		"space":        {{"A B", "x"}},
		"many":         manyVars(maxEnvVars + 1),
		"total budget": bigVars(),
	} {
		if err := ValidateEnv(env, nil); CodeOf(err) != CodeInvalidEnv {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := ValidateEnv([]EnvVar{{"A", "1"}}, []string{"A"}); err == nil {
		t.Error("a variable and a secret with the same name")
	}
	if err := ValidateEnv(nil, []string{"A", "A"}); err == nil {
		t.Error("duplicate secret")
	}
}

func manyVars(n int) []EnvVar {
	var out []EnvVar
	for i := range n {
		out = append(out, EnvVar{Name: "V" + strings.Repeat("X", i), Value: "x"})
	}
	return out
}

func bigVars() []EnvVar {
	var out []EnvVar
	for i := range 20 {
		out = append(out, EnvVar{Name: "BIG" + strings.Repeat("X", i), Value: strings.Repeat("v", maxEnvValue)})
	}
	return out
}

func TestValidateLimits(t *testing.T) {
	good := Limits{MemoryMB: 256, CPUs: 0.29, TimeoutSeconds: 900, TmpSizeMB: 512}
	if err := ValidateLimits(good, testConfig); err != nil {
		t.Fatal(err)
	}
	for name, l := range map[string]Limits{
		"memory low":      {MemoryMB: 64, CPUs: 1, TimeoutSeconds: 1, TmpSizeMB: 512},
		"memory over max": {MemoryMB: 4096, CPUs: 1, TimeoutSeconds: 1, TmpSizeMB: 512},
		"cpus low":        {MemoryMB: 256, CPUs: 0.05, TimeoutSeconds: 1, TmpSizeMB: 512},
		"cpus step":       {MemoryMB: 256, CPUs: 0.125, TimeoutSeconds: 1, TmpSizeMB: 512},
		"timeout zero":    {MemoryMB: 256, CPUs: 1, TimeoutSeconds: 0, TmpSizeMB: 512},
		"timeout high":    {MemoryMB: 256, CPUs: 1, TimeoutSeconds: 901, TmpSizeMB: 512},
		"tmp small":       {MemoryMB: 256, CPUs: 1, TimeoutSeconds: 1, TmpSizeMB: 1},
	} {
		if err := ValidateLimits(l, testConfig); CodeOf(err) != CodeInvalidLimits {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReferenceValidate(t *testing.T) {
	if err := sampleRef().Validate(testConfig); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Reference){
		"version":            func(r *Reference) { r.Version = 2 },
		"name":               func(r *Reference) { r.Name = "Bad" },
		"runtime":            func(r *Reference) { r.Runtime = "java" },
		"runtime version":    func(r *Reference) { r.RuntimeVersion = "latest" },
		"tag other version":  func(r *Reference) { r.ImageTag = "24-arm64" },
		"tag prefix":         func(r *Reference) { r.ImageTag = "220-arm64" },
		"tag other arch":     func(r *Reference) { r.ImageTag = "22-x86_64" },
		"tag plain":          func(r *Reference) { r.ImageTag = "22" },
		"tag injection":      func(r *Reference) { r.ImageTag = "22-arm64@sha256:x" },
		"digest":             func(r *Reference) { r.ImageDigest = "sha256:xyz" },
		"image id":           func(r *Reference) { r.ImageID = "abc" },
		"arch":               func(r *Reference) { r.Arch, r.RuntimeBinary.Arch = "riscv64", "riscv64" },
		"entrypoint":         func(r *Reference) { r.Entrypoint = []string{"/bin/sh"} },
		"handler":            func(r *Reference) { r.Handler = "../x.y" },
		"limits":             func(r *Reference) { r.MemoryMB = 1 },
		"reserved env":       func(r *Reference) { r.Env = []EnvVar{{"LD_PRELOAD", "/tmp/x"}} },
		"unsorted env":       func(r *Reference) { r.Env = []EnvVar{{"B", "1"}, {"A", "2"}} },
		"unsorted secrets":   func(r *Reference) { r.Secrets = []string{"B", "A"} },
		"no network":         func(r *Reference) { r.Networks = nil },
		"unsorted networks":  func(r *Reference) { r.Networks = []string{"images", "fn-resize"} },
		"duplicate networks": func(r *Reference) { r.Networks = []string{"images", "images"} },
		"deployment id":      func(r *Reference) { r.Deployment.ID = "../x" },
		"tree hash":          func(r *Reference) { r.Deployment.TreeHash = "x" },
		"runtime version v":  func(r *Reference) { r.RuntimeBinary.Version = "1.0.0" },
		"runtime arch":       func(r *Reference) { r.RuntimeBinary.Arch = "amd64" },
		"runtime sha":        func(r *Reference) { r.RuntimeBinary.SHA256 = "" },
	} {
		r := sampleRef()
		mutate(r)
		if err := r.Validate(testConfig); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	py, node := runtimes["python"], runtimes["nodejs"]
	if !py.validTag("3.13", "amd64", "3.13.2026.11.05.18-x86_64") || py.validTag("3.13", "amd64", "3.131-x86_64") ||
		node.validTag("22", "arm64", "al22-arm64") || node.validTag("al2023", "arm64", "al2023-arm64") {
		t.Fatal("validTag")
	}
	if got := sampleRef().Image(); got != "mirror.gcr.io/amazon/aws-lambda-nodejs:22.2026.11.05.18-arm64@"+testDigest {
		t.Fatalf("image = %s", got)
	}
}

func TestDeploymentID(t *testing.T) {
	id := NewDeploymentID(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC))
	if !ValidDeploymentID(id) || !strings.HasPrefix(id, "20261106T120000Z-") {
		t.Fatalf("id = %s", id)
	}
	if ValidDeploymentID("../../etc") || ValidDeploymentID("20261106T120000Z-0123ABCD") {
		t.Fatal("invalid ids accepted")
	}
}

func TestReferenceRoundTrip(t *testing.T) {
	p := Paths{DataDir: t.TempDir(), StacksDir: t.TempDir()}
	if err := p.EnsureFunctionDirs(nm("resize")); err != nil {
		t.Fatal(err)
	}
	for dir, perm := range map[string]os.FileMode{
		p.PrivateRoot(): 0o700, p.Private(nm("resize")): 0o700,
		p.FunctionData(nm("resize")): 0o700, p.Deployments(nm("resize")): 0o700, p.Uploads(nm("resize")): 0o700,
	} {
		if m := modeOf(t, dir).Perm(); m != perm {
			t.Errorf("%s: mode %v", dir, m)
		}
	}
	ref := sampleRef()
	ref.Env = []EnvVar{{"Z", "1"}, {"A", "2"}} // written in canonical order
	if err := p.WriteReference(*ref, testConfig); err != nil {
		t.Fatal(err)
	}
	if m := modeOf(t, filepath.Join(p.Private(nm("resize")), ReferenceFile)).Perm(); m != 0o600 {
		t.Fatalf("reference mode %v", m)
	}
	got, err := p.ReadReference(nm("resize"), testConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got.Env[0].Name != "A" || got.Image() != ref.Image() || got.Deployment.ID != ref.Deployment.ID {
		t.Fatalf("read back %+v", got)
	}
	bad := sampleRef()
	bad.Handler = "../x"
	if err := p.WriteReference(*bad, testConfig); err == nil {
		t.Fatal("invalid reference written")
	}
	// A tampered file is refused (unknown field, another name).
	file := filepath.Join(p.Private(nm("resize")), ReferenceFile)
	data, _ := os.ReadFile(file)
	mustWriteFile(t, file, []byte(strings.Replace(string(data), `"version": 1,`, `"version": 1, "privileged": true,`, 1)), 0o600)
	if _, err := p.ReadReference(nm("resize"), testConfig); CodeOf(err) != CodeInvalidReference {
		t.Fatalf("unknown field: %v", err)
	}
	mustWriteFile(t, file, []byte(strings.Replace(string(data), `"name": "resize"`, `"name": "other"`, 1)), 0o600)
	if _, err := p.ReadReference(nm("resize"), testConfig); CodeOf(err) != CodeInvalidReference {
		t.Fatalf("other name: %v", err)
	}
	// Never through a symlinked private directory.
	os.RemoveAll(p.Private(nm("resize")))
	elsewhere := t.TempDir()
	mustWriteFile(t, filepath.Join(elsewhere, ReferenceFile), data, 0o600)
	mustSymlink(t, elsewhere, p.Private(nm("resize")))
	if _, err := p.ReadReference(nm("resize"), testConfig); err == nil {
		t.Fatal("read through a symlink")
	}
	if err := p.WriteReference(*sampleRef(), testConfig); err == nil {
		t.Fatal("written through a symlink")
	}
}

func TestSecretsEnv(t *testing.T) {
	names := []string{"B_KEY", "A_KEY"}
	out, err := RenderSecretsEnv(names, map[string]string{"A_KEY": "x=y $z 'q'", "B_KEY": "2"})
	if err != nil || string(out) != "A_KEY=x=y $z 'q'\nB_KEY=2\n" {
		t.Fatalf("env file %q, %v", out, err)
	}
	for name, values := range map[string]map[string]string{
		"missing value": {"A_KEY": "1"},
		"extra value":   {"A_KEY": "1", "B_KEY": "2", "C": "3"},
		"other name":    {"A_KEY": "1", "C": "2"},
		"newline":       {"A_KEY": "1\nLD_PRELOAD=/tmp/x", "B_KEY": "2"},
		"NUL":           {"A_KEY": "1\x00", "B_KEY": "2"},
	} {
		if _, err := RenderSecretsEnv(names, values); CodeOf(err) != CodeInvalidSecret {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := RenderSecretsEnv([]string{"LD_PRELOAD"}, map[string]string{"LD_PRELOAD": "x"}); err == nil {
		t.Error("reserved secret name")
	}

	p := Paths{DataDir: t.TempDir()}
	if err := p.EnsureFunctionDirs(nm("resize")); err != nil {
		t.Fatal(err)
	}
	if err := p.WriteSecretsEnv(nm("resize"), names, map[string]string{"A_KEY": "1", "B_KEY": "2"}); err != nil {
		t.Fatal(err)
	}
	if m := modeOf(t, p.SecretsEnv(nm("resize"))).Perm(); m != 0o400 {
		t.Fatalf("env file mode %v", m)
	}
	if err := p.RemoveFunctionDirs(nm("resize")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p.Private(nm("resize"))); !os.IsNotExist(err) {
		t.Fatal("private directory not removed")
	}
}

func TestParseEnvelope(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString
	good := `{"statusCode":200,"functionError":"Unhandled","requestId":"5e1c-0a","durationMs":12,"payload":"` +
		b64([]byte(`{"errorType":"Error"}`)) + `","logTail":"` + b64([]byte("START")) + `"}`
	r, err := ParseEnvelope([]byte(good))
	if err != nil || r.StatusCode != 200 || r.FunctionError != "Unhandled" || string(r.Payload) != `{"errorType":"Error"}` || string(r.LogTail) != "START" {
		t.Fatalf("envelope = %+v, %v", r, err)
	}
	for name, doc := range map[string]string{
		"not JSON":       "Task timed out",
		"unknown field":  `{"statusCode":200,"payload":"","logTail":"","contentType":"text/html"}`,
		"trailing data":  `{"statusCode":200,"payload":"","logTail":""} {"x":1}`,
		"bad status":     `{"statusCode":42,"payload":"","logTail":""}`,
		"bad error type": `{"statusCode":200,"functionError":"<script>","payload":"","logTail":""}`,
		"bad request id": `{"statusCode":200,"requestId":"a\nb","payload":"","logTail":""}`,
		"bad base64":     `{"statusCode":200,"payload":"%%%","logTail":""}`,
		"negative time":  `{"statusCode":200,"durationMs":-1,"payload":"","logTail":""}`,
		"big log tail":   `{"statusCode":200,"payload":"","logTail":"` + b64(make([]byte, maxLogTailBytes+1)) + `"}`,
	} {
		if _, err := ParseEnvelope([]byte(doc)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := ParseEnvelope(make([]byte, maxEnvelopeBytes+1)); err == nil {
		t.Error("oversized envelope accepted")
	}
}
