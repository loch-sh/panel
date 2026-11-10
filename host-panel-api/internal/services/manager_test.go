package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

func TestFirstResourceCreatesTheInstance(t *testing.T) {
	env := newEnv(t)
	res := mustCreate(t, env, "shop")
	if !res.InstanceCreated || res.Resource.Instance != "fake-2" || res.Resource.Host != "svc-fake-2" || res.Resource.Port != 1234 {
		t.Fatalf("res = %+v", res)
	}
	var password string
	for _, c := range res.Credentials {
		if c.Key == "password" {
			password = c.Value
		}
	}
	if !IsGeneratedPassword(password) || env.prov.passwords[0] != password {
		t.Fatal("the generated password must be returned once")
	}
	if strings.Contains(env.audit.String(), password) || strings.Contains(env.logs.String(), password) {
		t.Fatal("password logged")
	}
	if env.runner.count("svc-fake-2 up") != 1 {
		t.Fatalf("calls=%v", env.runner.commands())
	}
	dir := env.instanceDir()
	for _, f := range []string{".loch-managed", ComposeFile, "fake.conf"} {
		if !exists(filepath.Join(dir, f)) {
			t.Fatalf("%s missing", f)
		}
	}
	assertMode(t, filepath.Join(dir, SecretsDir), os.ModeDir|0o700)
	assertMode(t, filepath.Join(dir, SecretsDir, AdminSecretFile), 0o400)
	assertMode(t, env.dataDir(), os.ModeDir|0o700)
	secret, _ := os.ReadFile(filepath.Join(dir, SecretsDir, AdminSecretFile))
	if !IsGeneratedPassword(string(secret)) || string(secret) == password {
		t.Fatal("admin secret must be its own generated password")
	}
	compose, _ := os.ReadFile(filepath.Join(dir, ComposeFile))
	if strings.Contains(string(compose), string(secret)) {
		t.Fatal("admin secret written in the compose file")
	}

	// Second resource: same instance, no new compose up.
	if res := mustCreate(t, env, "blog"); res.InstanceCreated {
		t.Fatal("second resource must reuse the instance")
	}
	if env.runner.count("svc-fake-2 up") != 1 {
		t.Fatalf("calls = %v", env.runner.commands())
	}
	if !strings.Contains(env.audit.String(), "service instance created") || !strings.Contains(env.audit.String(), "resource=blog") {
		t.Fatalf("audit = %s", env.audit.String())
	}
}

func assertMode(t *testing.T, p string, want os.FileMode) {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode() != want {
		t.Fatalf("%s: mode %v, want %v", p, fi.Mode(), want)
	}
}

func TestDeletingTheLastResourceRetiresTheInstance(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	mustCreate(t, env, "blog")
	ctx := context.Background()

	res, err := env.m.Delete(ctx, "fake-2", "blog", "admin")
	if err != nil || res.InstanceRetired {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	res, err = env.m.Delete(ctx, "fake-2", "shop", "admin")
	if err != nil || !res.InstanceRetired {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	cmds := env.runner.commands()
	if got := strings.Join(cmds[len(cmds)-2:], "|"); got != "svc-fake-2 stop|svc-fake-2 down --remove-orphans" {
		t.Fatalf("retire commands = %v", cmds)
	}
	if exists(env.instanceDir()) || exists(env.dataDir()) {
		t.Fatal("instance and data directories must be removed")
	}
	if !strings.Contains(env.audit.String(), "service instance retired") {
		t.Fatal("retirement not audited")
	}
	// A new resource brings the instance back from scratch.
	if res := mustCreate(t, env, "shop"); !res.InstanceCreated {
		t.Fatal("instance must be recreated")
	}
}

func TestArchiveOnRetire(t *testing.T) {
	env := newEnv(t)
	env.m.cfg.ArchiveOnRetire = true
	env.m.now = func() time.Time { return time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC) }
	mustCreate(t, env, "shop")
	must(t, os.WriteFile(filepath.Join(env.dataDir(), "PG_VERSION"), []byte("2"), 0o600))
	if res, err := env.m.Delete(context.Background(), "fake-2", "shop", "admin"); err != nil || !res.InstanceRetired {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !exists(env.dataDir()+"-retired-20261104T100000.000000000Z/PG_VERSION") || exists(env.dataDir()) {
		t.Fatal("data directory must be archived")
	}
}

func TestUnmanagedResourceKeepsTheInstance(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	env.prov.add("id-svc-fake-2", "legacy", false)
	ctx := context.Background()
	wantCode(t, func() error { _, err := env.m.Delete(ctx, "fake-2", "legacy", "admin"); return err }(), CodeResourceNotManaged)
	wantCode(t, func() error { _, err := env.m.RotatePassword(ctx, "fake-2", "legacy", "admin"); return err }(), CodeResourceNotManaged)
	res, err := env.m.Delete(ctx, "fake-2", "shop", "admin")
	if err != nil || res.InstanceRetired || !exists(env.dataDir()) {
		t.Fatalf("an instance holding an unmanaged resource must never be retired: %+v %v", res, err)
	}
}

func TestModifiedInstanceIsNotRetired(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	p := filepath.Join(env.instanceDir(), ComposeFile)
	data, _ := os.ReadFile(p)
	must(t, os.WriteFile(p, append(data, []byte("# tuned by hand\n")...), 0o640))

	res, err := env.m.Delete(context.Background(), "fake-2", "shop", "admin")
	if err != nil || res.InstanceRetired || res.RetireBlocked != "modified" || !exists(env.dataDir()) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	wantCode(t, env.m.Retire(context.Background(), "fake-2", "admin"), CodeInstanceModified)
	// Generated config files count too.
	must(t, os.WriteFile(p, data, 0o640))
	must(t, os.WriteFile(filepath.Join(env.instanceDir(), "fake.conf"), []byte("edited\n"), 0o644))
	if inst, _ := env.m.load("fake-2"); !inst.modified {
		t.Fatal("an edited generated file must be detected")
	}
}

func TestListFailureAfterDeleteKeepsTheInstance(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	env.prov.fail["list"] = errors.New("boom")
	env.prov.failNth["list"] = env.prov.calls["list"] + 2 // the re-list after the deletion
	res, err := env.m.Delete(context.Background(), "fake-2", "shop", "admin")
	if err != nil || res.InstanceRetired || !exists(env.instanceDir()) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestFailedFirstResourceRemovesTheNewInstance(t *testing.T) {
	env := newEnv(t)
	env.prov.fail["provision"] = &EngineError{Op: "provision", Detail: "sqlstate 53100"}
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
	wantCode(t, err, CodeEngineError)
	if exists(env.instanceDir()) || exists(env.dataDir()) || env.runner.count("svc-fake-2 down") != 1 {
		t.Fatalf("new instance not rolled back: %v", env.runner.commands())
	}
}

func TestFailureOnAnExistingInstanceNeverRemovesIt(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	env.prov.fail["provision"] = &EngineError{Op: "provision", Detail: "x"}
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "blog"}, "admin")
	wantCode(t, err, CodeEngineError)
	if !exists(env.instanceDir()) || !exists(env.dataDir()) || env.runner.count("svc-fake-2 down") != 0 || env.runner.count("svc-fake-2 stop") != 0 {
		t.Fatalf("an existing instance must never be touched: %v", env.runner.commands())
	}
}

func TestRollbackAtEachStepOfANewInstance(t *testing.T) {
	for _, op := range []string{"init", "list"} {
		t.Run(op, func(t *testing.T) {
			env := newEnv(t)
			env.prov.fail[op] = errors.New("boom")
			_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
			wantCode(t, err, CodeEngineError)
			if exists(env.instanceDir()) || exists(env.dataDir()) {
				t.Fatal("not rolled back")
			}
		})
	}
	t.Run("unhealthy", func(t *testing.T) {
		env := newEnv(t)
		env.runner.health = container.Unhealthy
		_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
		wantCode(t, err, CodeNotReady)
		if exists(env.instanceDir()) || exists(env.dataDir()) {
			t.Fatal("not rolled back")
		}
	})
	t.Run("compose up", func(t *testing.T) {
		env := newEnv(t)
		env.runner.fail["up"] = errors.New("exit status 1")
		_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
		wantCode(t, err, CodeStartFailed)
		if exists(env.instanceDir()) || exists(env.dataDir()) || env.runner.count("svc-fake-2 down") != 1 {
			t.Fatalf("not rolled back: %v", env.runner.commands())
		}
		if !strings.Contains(env.logs.String(), "compose output") {
			t.Fatal("compose output must be logged")
		}
	})
	t.Run("rollback down fails", func(t *testing.T) {
		env := newEnv(t)
		env.runner.fail["up"] = errors.New("exit status 1")
		env.runner.fail["down"] = errors.New("exit status 1")
		_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
		wantCode(t, err, CodeStartFailed)
		if !exists(env.instanceDir()) {
			t.Fatal("files must be kept when the project may still run")
		}
	})
}

// An unknown outcome is never a success, even when the engine lists the
// resource afterwards (it may be half created).
func TestUnknownProvisioningOutcomeIsNeverASuccess(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "seed")
	env.prov.fail["provision"] = ErrOutcomeUnknown
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
	wantCode(t, err, CodeOutcomeUnknown)
	if !exists(env.instanceDir()) {
		t.Fatal("an existing instance must be kept")
	}
	// What was created stays visible, hence deletable.
	list, _ := env.m.Resources(context.Background(), "fake-2")
	if len(list) != 2 {
		t.Fatalf("resources = %+v", list)
	}
	// On a fresh instance, the instance is undone.
	env2 := newEnv(t)
	env2.prov.fail["provision"] = ErrOutcomeUnknown
	_, err = env2.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
	wantCode(t, err, CodeOutcomeUnknown)
	if exists(env2.instanceDir()) || exists(env2.dataDir()) {
		t.Fatal("fresh instance not undone")
	}
}

func TestProvisionReportsAnExistingUser(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "seed")
	env.prov.fail["provision"] = ErrResourceExists
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
	wantCode(t, err, CodeResourceExists)
}

func TestUnknownDeprovisionOutcome(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "seed")
	mustCreate(t, env, "shop")
	// The command did not report its end and the resource is still listed.
	env.prov.fail["deprovision"] = ErrOutcomeUnknown
	_, err := env.m.Delete(context.Background(), "fake-2", "shop", "admin")
	wantCode(t, err, CodeOutcomeUnknown)
	if strings.Contains(env.audit.String(), "resource=shop") && strings.Contains(env.audit.String(), "service resource deleted") {
		t.Fatal("an unconfirmed deletion must not be audited as done")
	}
}

func TestDirectoryAndNameConflicts(t *testing.T) {
	t.Run("directory without marker", func(t *testing.T) {
		env := newEnv(t)
		must(t, os.Mkdir(env.instanceDir(), 0o755))
		must(t, os.WriteFile(filepath.Join(env.instanceDir(), "compose.yaml"), []byte("services: {}\n"), 0o644))
		_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
		wantCode(t, err, CodeDirectoryConflict)
		if !exists(filepath.Join(env.instanceDir(), "compose.yaml")) {
			t.Fatal("a foreign directory must never be removed")
		}
	})
	t.Run("container with the name", func(t *testing.T) {
		env := newEnv(t)
		env.docker.extra = []container.Summary{{ID: "x", Names: []string{"/svc-fake-2"}, Labels: map[string]string{composeProjectLabel: "ci"}}}
		_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
		wantCode(t, err, CodeDirectoryConflict)
		if exists(env.instanceDir()) {
			t.Fatal("nothing must be created")
		}
	})
	t.Run("data directory not empty", func(t *testing.T) {
		env := newEnv(t)
		must(t, os.Mkdir(env.dataDir(), 0o700))
		must(t, os.WriteFile(filepath.Join(env.dataDir(), "old"), []byte("x"), 0o600))
		_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
		wantCode(t, err, CodeDataDirNotEmpty)
		if !exists(filepath.Join(env.dataDir(), "old")) || exists(env.instanceDir()) {
			t.Fatal("existing data must be kept and the reservation released")
		}
	})
	t.Run("empty data directory is reused", func(t *testing.T) {
		env := newEnv(t)
		must(t, os.Mkdir(env.dataDir(), 0o700))
		mustCreate(t, env, "shop")
	})
	t.Run("symlinked data path", func(t *testing.T) {
		env := newEnv(t)
		target := t.TempDir()
		must(t, os.Symlink(target, env.dataDir()))
		_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
		wantCode(t, err, CodeDataDirNotEmpty)
		if !exists(target) {
			t.Fatal("symlink target touched")
		}
	})
}

func TestPurgeNeverFollowsASymlinkedDataDir(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	victim := t.TempDir()
	must(t, os.WriteFile(filepath.Join(victim, "keep"), []byte("x"), 0o600))
	os.RemoveAll(env.dataDir())
	must(t, os.Symlink(victim, env.dataDir()))
	res, err := env.m.Delete(context.Background(), "fake-2", "shop", "admin")
	if err != nil || res.InstanceRetired || res.RetireBlocked != "error" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !exists(filepath.Join(victim, "keep")) || !exists(env.instanceDir()) {
		t.Fatal("purge followed a symlink, or the instance was removed despite the failure")
	}
}

func TestUntrustedContainersAreRefused(t *testing.T) {
	cases := map[string]func(c *fakeContainer){
		"wrong image":       func(c *fakeContainer) { c.inspect.Config.Image = "evil:latest" },
		"wrong data mount":  func(c *fakeContainer) { c.inspect.Mounts[0].Source = "/tmp/evil" },
		"wrong name":        func(c *fakeContainer) { c.inspect.Name = "/evil" },
		"wrong working dir": func(c *fakeContainer) { c.inspect.Config.Labels[composeWorkingDirLabel] = "/srv/evil" },
		"wrong service":     func(c *fakeContainer) { c.summary.Labels[composeServiceLabel] = "web" },
		"unhealthy":         func(c *fakeContainer) { c.inspect.State.Health.Status = container.Unhealthy },
		"stopped":           func(c *fakeContainer) { c.inspect.State.Running = false },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			mustCreate(t, env, "shop")
			spoil(env.docker.byProject("svc-fake-2"))
			calls := env.prov.calls["list"]
			_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "blog"}, "admin")
			wantCode(t, err, CodeNotReady)
			_, err = env.m.Resources(context.Background(), "fake-2")
			wantCode(t, err, CodeNotReady)
			if env.prov.calls["list"] != calls {
				t.Fatal("no command may reach an untrusted container")
			}
			list, _ := env.m.Instances(context.Background())
			if len(list) != 1 || list[0].Ready || list[0].Resources != nil {
				t.Fatalf("instances = %+v", list)
			}
		})
	}
	t.Run("second container in the project", func(t *testing.T) {
		env := newEnv(t)
		mustCreate(t, env, "shop")
		env.docker.extra = []container.Summary{{ID: "evil", Labels: map[string]string{composeProjectLabel: "svc-fake-2", composeServiceLabel: serviceName}}}
		_, err := env.m.Resources(context.Background(), "fake-2")
		wantCode(t, err, CodeNotReady)
	})
}

func TestNamesAndLookups(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	for _, n := range []string{"Shop", "shop;drop", "shop\n", "root", "loch_x"} {
		_, err := env.m.Create(ctx, CreateRequest{Engine: "fake", Version: "2", Name: n}, "admin")
		if c := CodeOf(err); c != CodeInvalidName && c != CodeNameReserved {
			t.Fatalf("%q: %v", n, err)
		}
	}
	_, err := env.m.Create(ctx, CreateRequest{Engine: "fake", Version: "2", Name: "forbidden"}, "admin")
	wantCode(t, err, CodeNameReserved)
	_, err = env.m.Create(ctx, CreateRequest{Engine: "nope", Version: "2", Name: "shop"}, "admin")
	wantCode(t, err, CodeInvalidEngine)
	_, err = env.m.Create(ctx, CreateRequest{Engine: "fake", Version: "9", Name: "shop"}, "admin")
	wantCode(t, err, CodeInvalidVersion)
	if exists(env.instanceDir()) {
		t.Fatal("invalid requests must not create anything")
	}
	_, err = env.m.Delete(ctx, "fake-2", "shop", "admin")
	wantCode(t, err, CodeInstanceNotFound)
	_, err = env.m.Delete(ctx, "../etc", "shop", "admin")
	wantCode(t, err, CodeInstanceNotFound)
	mustCreate(t, env, "shop")
	_, err = env.m.Create(ctx, CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
	wantCode(t, err, CodeResourceExists)
	_, err = env.m.Delete(ctx, "fake-2", "nothere", "admin")
	wantCode(t, err, CodeResourceNotFound)
}

func TestResourceLimit(t *testing.T) {
	env := newEnv(t)
	env.m.cfg.MaxResourcesPerInstance = 2
	mustCreate(t, env, "a")
	mustCreate(t, env, "b")
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "c"}, "admin")
	wantCode(t, err, CodeResourceLimit)
}

func TestRotatePassword(t *testing.T) {
	env := newEnv(t)
	first := mustCreate(t, env, "shop")
	creds, err := env.m.RotatePassword(context.Background(), "fake-2", "shop", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if creds[2].Value == first.Credentials[2].Value || !IsGeneratedPassword(creds[2].Value) {
		t.Fatal("a new generated password is expected")
	}
	env.prov.fail["set password"] = ErrOutcomeUnknown
	_, err = env.m.RotatePassword(context.Background(), "fake-2", "shop", "admin")
	wantCode(t, err, CodeOutcomeUnknown)
}

func TestConcurrentCreatesStartOneInstance(t *testing.T) {
	env := newEnv(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "site_" + string(rune('a'+i))}, "admin")
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := env.runner.count("svc-fake-2 up"); n != 1 {
		t.Fatalf("%d compose up: the instance must be created once", n)
	}
	list, _ := env.m.Resources(context.Background(), "fake-2")
	if len(list) != 8 {
		t.Fatalf("%d resources", len(list))
	}
}

func TestConcurrentSameName(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "seed")
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, exist := 0, 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case CodeOf(err) == CodeResourceExists:
				exist++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || exist != 5 {
		t.Fatalf("ok=%d exists=%d", ok, exist)
	}
}

func TestDeleteAndCreateAreSerialized(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	env.prov.hook = func(op string) {
		if op == "deprovision" {
			once.Do(func() { close(entered); <-release })
		}
	}
	done := make(chan *DeleteResult)
	go func() {
		res, err := env.m.Delete(context.Background(), "fake-2", "shop", "admin")
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	<-entered
	created := make(chan error)
	go func() {
		_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "blog"}, "admin")
		created <- err
	}()
	select {
	case err := <-created:
		t.Fatalf("create must wait for the deletion holding the instance lock (err = %v)", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if res := <-done; !res.InstanceRetired {
		t.Fatal("last resource deleted first: the instance is retired")
	}
	if err := <-created; err != nil {
		t.Fatalf("create after the retirement must recreate the instance: %v", err)
	}
	if env.runner.count("svc-fake-2 up") != 2 {
		t.Fatalf("calls = %v", env.runner.commands())
	}
}

func TestLockTimeout(t *testing.T) {
	env := newEnv(t)
	env.m.cfg.LockTimeout = 30 * time.Millisecond
	unlock, err := env.m.lock(context.Background(), "fake-2")
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
	wantCode(t, err, CodeInProgress)
	unlock()
	mustCreate(t, env, "shop")
	env.m.mu.Lock()
	n := len(env.m.locks)
	env.m.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d locks left: entries must be released", n)
	}
}

func TestInstancesListing(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	mustCreate(t, env, "blog")
	// Directories that are not managed instances are ignored.
	must(t, os.Mkdir(filepath.Join(env.cfg.StacksDir, "svc-other-1"), 0o755))
	must(t, os.Mkdir(filepath.Join(env.cfg.StacksDir, "hosted-site"), 0o755))
	list, err := env.m.Instances(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	s := list[0]
	if s.Key != "fake-2" || !s.Ready || s.Resources == nil || *s.Resources != 2 || s.Modified || s.Image != "fake:2" {
		t.Fatalf("summary = %+v", s)
	}
}

func TestRetireExplicit(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	wantCode(t, env.m.Retire(context.Background(), "fake-2", "admin"), CodeInstanceNotEmpty)
	env.prov.mu.Lock()
	delete(env.prov.resources["id-svc-fake-2"], "shop")
	env.prov.mu.Unlock()
	if err := env.m.Retire(context.Background(), "fake-2", "admin"); err != nil {
		t.Fatal(err)
	}
	if exists(env.instanceDir()) {
		t.Fatal("instance not retired")
	}
}

func TestInitRunsOnEveryCreate(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "a")
	mustCreate(t, env, "b")
	if env.prov.initCount() != 2 {
		t.Fatalf("init ran %d times: it must harden the instance before every provisioning", env.prov.initCount())
	}
}

func TestRetireResumesAnInterruptedRetirement(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	// The purge fails once (symlink planted), after stop and down.
	victim := t.TempDir()
	saved := env.dataDir() + ".saved"
	must(t, os.Rename(env.dataDir(), saved))
	must(t, os.Symlink(victim, env.dataDir()))
	res, err := env.m.Delete(context.Background(), "fake-2", "shop", "admin")
	if err != nil || res.RetireBlocked != "error" || env.docker.byProject("svc-fake-2") != nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// The operator fixes the data directory; Retire starts the instance
	// again, reads its (empty) catalog and finishes.
	os.Remove(env.dataDir())
	must(t, os.Rename(saved, env.dataDir()))
	if err := env.m.Retire(context.Background(), "fake-2", "admin"); err != nil {
		t.Fatal(err)
	}
	if exists(env.instanceDir()) || exists(env.dataDir()) {
		t.Fatal("retirement not finished")
	}
}

func TestRollbackNeverArchives(t *testing.T) {
	env := newEnv(t)
	env.m.cfg.ArchiveOnRetire = true
	env.prov.fail["provision"] = &EngineError{Op: "provision", Detail: "x"}
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop"}, "admin")
	wantCode(t, err, CodeEngineError)
	entries, _ := os.ReadDir(env.cfg.Render.DataDir)
	// Only the (empty) private directory shared by every instance remains.
	if len(entries) != 1 || entries[0].Name() != PrivateDir {
		t.Fatalf("a rolled-back instance must leave nothing behind: %v", entries)
	}
	if left, _ := os.ReadDir(filepath.Join(env.cfg.Render.DataDir, PrivateDir)); len(left) != 0 {
		t.Fatalf("private directory left: %v", left)
	}
}

func TestArchiveNeverReplacesAnExistingDirectory(t *testing.T) {
	env := newEnv(t)
	env.m.cfg.ArchiveOnRetire = true
	env.m.now = func() time.Time { return time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC) }
	mustCreate(t, env, "shop")
	must(t, os.Mkdir(env.dataDir()+"-retired-20261104T100000.000000000Z", 0o700))
	res, err := env.m.Delete(context.Background(), "fake-2", "shop", "admin")
	if err != nil || res.InstanceRetired || !exists(env.dataDir()) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestExistingEmptyDataDirIsReset(t *testing.T) {
	env := newEnv(t)
	must(t, os.Mkdir(env.dataDir(), 0o777))
	must(t, os.Chmod(env.dataDir(), 0o777))
	mustCreate(t, env, "shop")
	assertMode(t, env.dataDir(), os.ModeDir|0o700)
}

func TestHostLevelAccessIsRefused(t *testing.T) {
	cases := map[string]func(c *fakeContainer){
		"privileged": func(c *fakeContainer) { c.inspect.HostConfig.Privileged = true },
		"published port": func(c *fakeContainer) {
			c.inspect.HostConfig.PortBindings = network.PortMap{network.MustParsePort("5432/tcp"): nil}
		},
		"host network": func(c *fakeContainer) { c.inspect.HostConfig.NetworkMode = "host" },
		"host pid":     func(c *fakeContainer) { c.inspect.HostConfig.PidMode = "host" },
		"capability":   func(c *fakeContainer) { c.inspect.HostConfig.CapAdd = []string{"SYS_ADMIN"} },
		"docker socket": func(c *fakeContainer) {
			c.inspect.Mounts = append(c.inspect.Mounts, container.MountPoint{Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"})
		},
		"missing config mount": func(c *fakeContainer) { c.inspect.Mounts = c.inspect.Mounts[:2] },
		"other secret":         func(c *fakeContainer) { c.inspect.Mounts[1].Source = "/tmp/secret" },
		"other command":        func(c *fakeContainer) { c.inspect.Config.Cmd = []string{"fake-server", "--trust-all"} },
		"no host config":       func(c *fakeContainer) { c.inspect.HostConfig = nil },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t)
			mustCreate(t, env, "shop")
			spoil(env.docker.byProject("svc-fake-2"))
			_, err := env.m.Resources(context.Background(), "fake-2")
			wantCode(t, err, CodeNotReady)
		})
	}
}

func TestUnknownKeysCreateNoLock(t *testing.T) {
	env := newEnv(t)
	for _, key := range []string{"../../etc", "fake-9", strings.Repeat("x", 4096)} {
		_, err := env.m.Delete(context.Background(), key, "shop", "admin")
		wantCode(t, err, CodeInstanceNotFound)
		wantCode(t, env.m.Retire(context.Background(), key, "admin"), CodeInstanceNotFound)
	}
	env.m.mu.Lock()
	defer env.m.mu.Unlock()
	if len(env.m.locks) != 0 {
		t.Fatal("lock entries created for unknown keys")
	}
}

func TestOtherManifestVersionIsModified(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	p := filepath.Join(env.instanceDir(), ComposeFile)
	data, _ := os.ReadFile(p)
	// Any content with another layout version must not escape the check
	// (the stacks directory is writable by CI).
	other := strings.Replace(string(data), "  version: 1\n", "  version: 0\n", 1) + "# privileged: true\n"
	must(t, os.WriteFile(p, []byte(other), 0o640))
	if inst, err := env.m.load("fake-2"); err != nil || !inst.modified {
		t.Fatalf("a manifest of another version must be reported as modified (%v)", err)
	}
}

func TestEditedOverrideIsModified(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	p := filepath.Join(env.instanceDir(), OverrideFile)
	data, _ := os.ReadFile(p)
	must(t, os.WriteFile(p, append(data, []byte("  evil:\n    image: alpine\n    privileged: true\n")...), 0o640))
	if inst, err := env.m.load("fake-2"); err != nil || !inst.modified {
		t.Fatalf("an edited override must be reported as modified (%v)", err)
	}
}

func TestContainerExtrasAreRefused(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	c := env.docker.byProject(ref2.Project())
	inst, _ := env.m.load("fake-2")
	for name, mutate := range map[string]func(){
		"env":        func() { c.inspect.Config.Env = append(c.inspect.Config.Env, "LD_PRELOAD=/x.so") },
		"entrypoint": func() { c.inspect.Config.Entrypoint = []string{"/bin/sh"} },
		"seccomp": func() {
			c.inspect.HostConfig.SecurityOpt = append(c.inspect.HostConfig.SecurityOpt, "seccomp=unconfined")
		},
		"device": func() { c.inspect.HostConfig.Devices = []container.DeviceMapping{{PathOnHost: "/dev/sda"}} },
	} {
		env.docker.mu.Lock()
		saved := *c
		cfg, hc := *c.inspect.Config, *c.inspect.HostConfig
		c.inspect.Config, c.inspect.HostConfig = &cfg, &hc
		mutate()
		env.docker.mu.Unlock()
		if _, rerr := env.m.ready(context.Background(), inst); rerr == nil {
			t.Fatalf("%s: container must be refused", name)
		}
		env.docker.mu.Lock()
		*c = saved
		env.docker.mu.Unlock()
	}
	if _, rerr := env.m.ready(context.Background(), inst); rerr != nil {
		t.Fatalf("restored container must be trusted: %v", rerr)
	}
}
