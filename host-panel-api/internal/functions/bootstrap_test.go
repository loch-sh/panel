package functions

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// elfOpts describes a tiny ELF64 fixture: a header and, when interp is
// set, one PT_INTERP program header followed by the interpreter path.
type elfOpts struct {
	class   elf.Class
	machine elf.Machine
	osabi   elf.OSABI
	typ     elf.Type
	interp  string
}

func tinyELF(o elfOpts) []byte {
	if o.class == 0 {
		o.class = elf.ELFCLASS64
	}
	if o.typ == 0 {
		o.typ = elf.ET_EXEC
	}
	le := binary.LittleEndian
	const ehsize, phsize = 64, 56
	b := make([]byte, ehsize)
	copy(b, elf.ELFMAG)
	b[elf.EI_CLASS], b[elf.EI_DATA], b[elf.EI_VERSION], b[elf.EI_OSABI] = byte(o.class), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT), byte(o.osabi)
	le.PutUint16(b[16:], uint16(o.typ))
	le.PutUint16(b[18:], uint16(o.machine))
	le.PutUint32(b[20:], uint32(elf.EV_CURRENT))
	le.PutUint64(b[24:], 0x401000)
	le.PutUint16(b[52:], ehsize)
	le.PutUint16(b[54:], phsize)
	le.PutUint16(b[58:], 64)
	if o.interp != "" {
		le.PutUint64(b[32:], ehsize) // e_phoff
		le.PutUint16(b[56:], 1)      // e_phnum
		ph := make([]byte, phsize)
		path := append([]byte(o.interp), 0)
		le.PutUint32(ph[0:], uint32(elf.PT_INTERP))
		le.PutUint32(ph[4:], uint32(elf.PF_R))
		le.PutUint64(ph[8:], ehsize+phsize)
		le.PutUint64(ph[32:], uint64(len(path)))
		le.PutUint64(ph[40:], uint64(len(path)))
		le.PutUint64(ph[48:], 1)
		b = append(append(b, ph...), path...)
	}
	return append(b, make([]byte, 64)...) // some "code"
}

func TestInspectBootstrap(t *testing.T) {
	for name, tc := range map[string]struct {
		data    []byte
		arch    string
		code    string
		warning bool
		msg     string
	}{
		"static arm64":        {data: tinyELF(elfOpts{machine: elf.EM_AARCH64}), arch: "arm64"},
		"static amd64":        {data: tinyELF(elfOpts{machine: elf.EM_X86_64}), arch: "amd64"},
		"static-pie":          {data: tinyELF(elfOpts{machine: elf.EM_AARCH64, typ: elf.ET_DYN}), arch: "arm64"},
		"linux osabi":         {data: tinyELF(elfOpts{machine: elf.EM_AARCH64, osabi: elf.ELFOSABI_LINUX}), arch: "arm64"},
		"script":              {data: []byte("#!/bin/sh\nexec ./app\n"), arch: "arm64"},
		"tiny script":         {data: []byte("#!x"), arch: "arm64"},
		"glibc arm64":         {data: tinyELF(elfOpts{machine: elf.EM_AARCH64, typ: elf.ET_DYN, interp: "/lib/ld-linux-aarch64.so.1"}), arch: "arm64", warning: true},
		"glibc amd64":         {data: tinyELF(elfOpts{machine: elf.EM_X86_64, interp: "/lib64/ld-linux-x86-64.so.2"}), arch: "amd64", warning: true},
		"amd64 on arm64":      {data: tinyELF(elfOpts{machine: elf.EM_X86_64}), arch: "arm64", code: CodeBootstrapArch, msg: "built for amd64, the function runs on arm64: GOOS=linux GOARCH=arm64"},
		"arm64 on amd64":      {data: tinyELF(elfOpts{machine: elf.EM_AARCH64}), arch: "amd64", code: CodeBootstrapArch, msg: "GOARCH=amd64"},
		"32-bit arm":          {data: tinyELF(elfOpts{machine: elf.EM_ARM}), arch: "arm64", code: CodeBootstrapArch},
		"32-bit class":        {data: tinyELF(elfOpts{machine: elf.EM_AARCH64, class: elf.ELFCLASS32}), arch: "arm64", code: CodeBootstrapInvalid},
		"freebsd":             {data: tinyELF(elfOpts{machine: elf.EM_AARCH64, osabi: elf.ELFOSABI_FREEBSD}), arch: "arm64", code: CodeBootstrapInvalid},
		"object file":         {data: tinyELF(elfOpts{machine: elf.EM_AARCH64, typ: elf.ET_REL}), arch: "arm64", code: CodeBootstrapInvalid},
		"musl":                {data: tinyELF(elfOpts{machine: elf.EM_AARCH64, interp: "/lib/ld-musl-aarch64.so.1"}), arch: "arm64", code: CodeBootstrapInvalid, msg: "ld-musl"},
		"glibc of other arch": {data: tinyELF(elfOpts{machine: elf.EM_AARCH64, interp: "/lib64/ld-linux-x86-64.so.2"}), arch: "arm64", code: CodeBootstrapInvalid},
		"mach-o":              {data: []byte{0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0, 0, 1}, arch: "arm64", code: CodeBootstrapInvalid, msg: "GOOS=linux"},
		"windows":             {data: []byte("MZ\x90\x00rest"), arch: "arm64", code: CodeBootstrapInvalid},
		"empty":               {data: nil, arch: "arm64", code: CodeBootstrapInvalid},
		"truncated elf":       {data: tinyELF(elfOpts{machine: elf.EM_AARCH64})[:20], arch: "arm64", code: CodeBootstrapInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			warnings, err := inspectBootstrap(bytes.NewReader(tc.data), int64(len(tc.data)), tc.arch)
			if CodeOf(err) != tc.code {
				t.Fatalf("error = %v, want %q", err, tc.code)
			}
			if err != nil && !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("message %q lacks %q", err, tc.msg)
			}
			if got := slices.Equal(warnings, []string{WarnBootstrapDynamic}); got != tc.warning {
				t.Fatalf("warnings = %v", warnings)
			}
		})
	}
	big := tinyELF(elfOpts{machine: elf.EM_AARCH64})
	if _, err := inspectBootstrap(bytes.NewReader(big), maxBootstrapBytes+1, "arm64"); CodeOf(err) != CodeBootstrapInvalid {
		t.Fatalf("oversized bootstrap: %v", err)
	}
}

func (e *deployEnv) deployCustom(t *testing.T, data []byte) (*Deployment, error) {
	t.Helper()
	return e.x.Deploy(context.Background(), bytes.NewReader(data), e.paths, e.name, time.Now(), CodeCheck{BootstrapArch: "arm64"})
}

func TestDeployBootstrapChecks(t *testing.T) {
	arm := tinyELF(elfOpts{machine: elf.EM_AARCH64})
	for name, tc := range map[string]struct {
		entries []zentry
		code    string
		msg     string
	}{
		"missing":         {entries: []zentry{{name: "main.go", body: []byte("package main")}}, code: CodeBootstrapMissing, msg: "GOOS=linux GOARCH=arm64"},
		"in a folder":     {entries: []zentry{{name: "build/bootstrap", body: arm, mode: 0o755}}, code: CodeBootstrapMissing, msg: "build/bootstrap"},
		"symlink":         {entries: []zentry{{name: "app", body: arm, mode: 0o755}, {name: "bootstrap", body: []byte("app"), mode: fs.ModeSymlink | 0o777}}, code: CodeBootstrapInvalid, msg: "symlink"},
		"symlink to root": {entries: []zentry{{name: "bin/bootstrap", body: arm, mode: 0o755}, {name: "bootstrap", body: []byte("bin/bootstrap"), mode: fs.ModeSymlink | 0o777}}, code: CodeBootstrapInvalid, msg: "symlink"},
		"directory":       {entries: []zentry{{name: "bootstrap/"}, {name: "bootstrap/x", body: arm}}, code: CodeBootstrapInvalid, msg: "directory"},
		"wrong arch":      {entries: []zentry{{name: "bootstrap", body: tinyELF(elfOpts{machine: elf.EM_X86_64}), mode: 0o755}}, code: CodeBootstrapArch, msg: "built for amd64"},
		"not a binary":    {entries: []zentry{{name: "bootstrap", body: []byte("hello"), mode: 0o755}}, code: CodeBootstrapInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			env := newDeployEnv(t, DefaultZipLimits(0))
			_, err := env.deployCustom(t, buildZip(t, tc.entries...))
			if CodeOf(err) != tc.code || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("error = %v, want %s (%q)", err, tc.code, tc.msg)
			}
			env.assertClean(t)
		})
	}
}

func TestDeployBootstrapModes(t *testing.T) {
	env := newDeployEnv(t, DefaultZipLimits(0))
	// No exec bit in the zip (as made by Windows tools): bootstrap only is
	// made executable, the other files keep theirs.
	d, err := env.deployCustom(t, buildZip(t,
		zentry{name: "bootstrap", body: tinyELF(elfOpts{machine: elf.EM_AARCH64}), mode: 0o644},
		zentry{name: "data.bin", body: []byte("x"), mode: 0o644},
		zentry{name: "lib/bootstrap", body: []byte("x"), mode: 0o644},
		zentry{name: "tool", body: []byte("#!/bin/sh"), mode: 0o755},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Warnings) != 0 {
		t.Fatalf("warnings = %v", d.Warnings)
	}
	dir := env.paths.Deployment(env.name, d.ID)
	for file, want := range map[string]fs.FileMode{"bootstrap": roExecMode, "data.bin": roFileMode, "lib/bootstrap": roFileMode, "tool": roExecMode, "lib": fs.ModeDir | roDirMode, ".": fs.ModeDir | roDirMode} {
		if got := modeOf(t, filepath.Join(dir, file)); got != want {
			t.Errorf("%s: mode %v, want %v", file, got, want)
		}
	}
	if err := env.paths.VerifyDeployment(env.name, d); err != nil {
		t.Fatalf("tree hash: %v", err)
	}
	// The same zip for another runtime keeps the zip's modes.
	d2, err := env.deploy(t, buildZip(t, zentry{name: "bootstrap", body: []byte("x"), mode: 0o644}))
	if err != nil {
		t.Fatal(err)
	}
	if got := modeOf(t, filepath.Join(env.paths.Deployment(env.name, d2.ID), "bootstrap")); got != roFileMode {
		t.Fatalf("bootstrap of a non-custom runtime made executable: %v", got)
	}
	// A dynamic glibc binary is deployed, with a warning.
	d3, err := env.deployCustom(t, buildZip(t, zentry{name: "bootstrap", mode: 0o755,
		body: tinyELF(elfOpts{machine: elf.EM_AARCH64, interp: "/lib/ld-linux-aarch64.so.1"})}))
	if err != nil || !slices.Equal(d3.Warnings, []string{WarnBootstrapDynamic}) {
		t.Fatalf("dynamic bootstrap: %+v %v", d3, err)
	}
}

// A root bootstrap next to nested ones: the root one is used, with no hint.
func TestDeployBootstrapRootAndNested(t *testing.T) {
	env := newDeployEnv(t, DefaultZipLimits(0))
	d, err := env.deployCustom(t, buildZip(t,
		zentry{name: "vendor/tool/bootstrap", body: []byte("not a binary"), mode: 0o644},
		zentry{name: "bootstrap", body: tinyELF(elfOpts{machine: elf.EM_AARCH64}), mode: 0o755},
	))
	if err != nil {
		t.Fatal(err)
	}
	dir := env.paths.Deployment(env.name, d.ID)
	if got := modeOf(t, filepath.Join(dir, "vendor/tool/bootstrap")); got != roFileMode {
		t.Fatalf("nested bootstrap mode %v", got)
	}
}

// Warnings survive a write and a read of the reference; codes unknown to
// this version (a newer panel) are dropped at load, logged once.
func TestDeploymentWarningsRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.m.Create(bg, CreateRequest{Name: "gofn", Runtime: "provided", RuntimeVersion: "al2023"}, actor); err != nil {
		t.Fatal(err)
	}
	dynamic := buildZip(t, zentry{name: "bootstrap", mode: 0o755,
		body: tinyELF(elfOpts{machine: elf.EM_AARCH64, interp: "/lib/ld-linux-aarch64.so.1"})})
	if _, err := e.m.Deploy(bg, "gofn", bytes.NewReader(dynamic), actor); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.m.paths.Private(nm("gofn")), ReferenceFile)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"warnings": [`)) {
		t.Fatalf("warnings not written:\n%s", data)
	}
	ref, err := e.m.load(nm("gofn"))
	if err != nil || !slices.Equal(ref.Deployment.Warnings, []string{WarnBootstrapDynamic}) {
		t.Fatalf("read back: %+v %v", ref.Deployment, err)
	}
	future := bytes.Replace(data, []byte(`"`+WarnBootstrapDynamic+`"`), []byte(`"`+WarnBootstrapDynamic+`", "FUNCTION_FUTURE_WARNING"`), 1)
	if err := os.WriteFile(file, future, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		ref, err = e.m.load(nm("gofn"))
		if err != nil || !slices.Equal(ref.Deployment.Warnings, []string{WarnBootstrapDynamic}) {
			t.Fatalf("unknown warning not dropped: %+v %v", ref, err)
		}
	}
	if n := strings.Count(e.logs.String(), "FUNCTION_FUTURE_WARNING"); n != 1 {
		t.Fatalf("dropped warning logged %d times", n)
	}
	// Only unknown codes: the field is emptied.
	only := bytes.Replace(data, []byte(`"`+WarnBootstrapDynamic+`"`), []byte(`"FUNCTION_FUTURE_WARNING"`), 1)
	mustWriteFile(t, file, only, 0o600)
	if ref, err = e.m.load(nm("gofn")); err != nil || ref.Deployment.Warnings != nil {
		t.Fatalf("only unknown warnings: %+v %v", ref, err)
	}
}

func TestPatchHandlerReset(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	empty := ""
	_, err := e.m.Update(bg, "resize", UpdateRequest{Handler: &empty}, actor)
	wantCode(t, err, CodeInvalidHandler)
	if _, err := e.m.Create(bg, CreateRequest{Name: "gofn", Runtime: "provided", RuntimeVersion: "al2023", Handler: "custom"}, actor); err != nil {
		t.Fatal(err)
	}
	d, err := e.m.Update(bg, "gofn", UpdateRequest{Handler: &empty}, actor)
	if err != nil || d.Handler != "bootstrap" {
		t.Fatalf("reset: %+v %v", d, err)
	}
}

func TestProvidedCatalog(t *testing.T) {
	rt, ok := LookupRuntime("provided")
	if !ok || !rt.Custom || rt.DefaultHandler != "bootstrap" || !slices.Equal(rt.Entrypoint, []string{"/lambda-entrypoint.sh"}) {
		t.Fatalf("provided = %+v", rt)
	}
	if !slices.Equal(RuntimeVersions["provided"], []string{"al2023"}) || !slices.Contains(RuntimeIDs(), "provided") {
		t.Fatal("al2023 not offered")
	}
	for v, want := range map[string]bool{"al2023": true, "al2": false, "2023": false, "al2023.1": false, "22": false} {
		if rt.ValidVersion(v) != want {
			t.Errorf("ValidVersion(%q) = %v", v, !want)
		}
	}
	if LineTag("al2023", "arm64") != "al2023-arm64" || LineTag("al2023", "amd64") != "al2023-x86_64" {
		t.Fatal("line tags")
	}
	for tag, want := range map[string]bool{
		"al2023-arm64": true, "al2023.2026.11.05.18-arm64": true, "al2023-x86_64": false, "al20231-arm64": false,
		"al2-arm64": false, "2023-arm64": false, "al2023": false,
	} {
		if rt.validTag("al2023", "arm64", tag) != want {
			t.Errorf("validTag(%q) = %v", tag, !want)
		}
	}
	if got := rt.ImageRef("al2023-arm64", testDigest); got != "mirror.gcr.io/amazon/aws-lambda-provided:al2023-arm64@"+testDigest {
		t.Fatalf("image = %s", got)
	}
	if rt.HandlerOrDefault("") != "bootstrap" || rt.HandlerOrDefault("my-handler") != "my-handler" || runtimes["nodejs"].HandlerOrDefault("") != "" {
		t.Fatal("default handler")
	}
	for _, h := range []string{"bootstrap", "handler", "my-func.v2", "pkg/handler", "Handler_1"} {
		if err := rt.ValidateHandler(h); err != nil {
			t.Errorf("%q refused", h)
		}
	}
	for _, h := range []string{"", "-x", ".hidden", "a/../b", "a//b", "/abs", "a b", "x$y", "a;b", strings.Repeat("a", 129)} {
		if err := rt.ValidateHandler(h); err == nil {
			t.Errorf("%q accepted", h)
		}
	}
	ref := providedRef()
	if err := ref.Validate(testConfig); err != nil {
		t.Fatal(err)
	}
	ref.Deployment.Warnings = []string{"SOMETHING"}
	if ref.Validate(testConfig) == nil {
		t.Fatal("unknown warning accepted")
	}
	// Creation offers RuntimeVersions only (the form admits later ones
	// for existing references).
	e := newTestEnv(t)
	_, err := e.m.Create(bg, CreateRequest{Name: "later", Runtime: "provided", RuntimeVersion: "al2025"}, actor)
	wantCode(t, err, CodeInvalidRuntime)
	ref = providedRef()
	ref.Deployment.Warnings = []string{WarnBootstrapDynamic, WarnBootstrapDynamic}
	if ref.Validate(testConfig) == nil {
		t.Fatal("duplicate warning accepted")
	}
	if !slices.Equal(command(providedRef()), []string{"--", "/lambda-entrypoint.sh", "bootstrap"}) {
		t.Fatalf("command = %v", command(providedRef()))
	}
}

func TestManagerProvidedFunction(t *testing.T) {
	e := newTestEnv(t)
	// The handler is optional: bootstrap by default (never empty: the
	// image entrypoint needs exactly one non-empty argument).
	c, err := e.m.Create(bg, CreateRequest{Name: "gofn", Runtime: "provided", RuntimeVersion: "al2023"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if c.Function.Handler != "bootstrap" || c.Function.Runtime != "provided" {
		t.Fatalf("created = %+v", c.Function)
	}
	if _, err := e.m.Create(bg, CreateRequest{Name: "nohandler", Runtime: "nodejs", RuntimeVersion: "22"}, actor); CodeOf(err) != CodeInvalidHandler {
		t.Fatalf("node without handler: %v", err)
	}
	info := e.m.Info()
	i := slices.IndexFunc(info.Runtimes, func(r RuntimeInfo) bool { return r.ID == "provided" })
	if i < 0 || !info.Runtimes[i].Custom || info.Runtimes[i].DefaultHandler != "bootstrap" {
		t.Fatalf("info runtimes = %+v", info.Runtimes)
	}
	// The Node code of the other tests has no bootstrap.
	_, err = e.m.Deploy(bg, "gofn", bytes.NewReader(codeZip(t, "x")), actor)
	wantCode(t, err, CodeBootstrapMissing)
	_, err = e.m.Deploy(bg, "gofn", bytes.NewReader(buildZip(t, zentry{name: "bootstrap", mode: 0o755, body: tinyELF(elfOpts{machine: elf.EM_X86_64})})), actor)
	wantCode(t, err, CodeBootstrapArch)

	good := buildZip(t, zentry{name: "bootstrap", mode: 0o755, body: tinyELF(elfOpts{machine: elf.EM_AARCH64})})
	d, err := e.m.Deploy(bg, "gofn", bytes.NewReader(good), actor)
	if err != nil {
		t.Fatal(err)
	}
	compose, _ := os.ReadFile(filepath.Join(e.m.paths.ProjectDir(nm("gofn")), ComposeFile))
	if !strings.Contains(string(compose), "mirror.gcr.io/amazon/aws-lambda-provided:al2023-arm64@") ||
		!strings.Contains(string(compose), "- /lambda-entrypoint.sh\n      - bootstrap\n") {
		t.Fatalf("compose:\n%s", compose)
	}
	// A bootstrap that passes the checks but does not start (unhealthy:
	// compose up --wait fails) fails the deployment and keeps the previous
	// one.
	e.runner.failUps = 1
	script := buildZip(t, zentry{name: "bootstrap", mode: 0o755, body: []byte("#!/bin/sh\nexit 3\n")})
	_, err = e.m.Deploy(bg, "gofn", bytes.NewReader(script), actor)
	wantCode(t, err, CodeStartFailed)
	ref, _ := e.m.load(nm("gofn"))
	if ref.Deployment == nil || ref.Deployment.ID != d.ID {
		t.Fatalf("previous deployment not kept: %+v", ref.Deployment)
	}
	// Resetting the handler restores the default.
	empty := ""
	h := "custom"
	if _, err := e.m.Update(bg, "gofn", UpdateRequest{Handler: &h}, actor); err != nil {
		t.Fatal(err)
	}
	det, err := e.m.Update(bg, "gofn", UpdateRequest{Handler: &empty}, actor)
	if err != nil || det.Handler != "bootstrap" {
		t.Fatalf("handler reset: %+v %v", det, err)
	}
}
