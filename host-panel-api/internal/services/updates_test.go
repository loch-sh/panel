package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"host-panel-api/internal/composekit"
)

// discover runs a discovery of the test registry (kv 7.0.4 and 8.0.10
// published, see newFakeSource).
func (env *testEnv) discover(t *testing.T, src *fakeSource) {
	t.Helper()
	env.m.cfg.Releases = src
	env.m.lastCheck = env.m.lastCheck.AddDate(-1, 0, 0) // no cooldown in tests
	if _, err := env.m.CheckUpdates(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (env *testEnv) summaryOf(t *testing.T, key string) InstanceSummary {
	t.Helper()
	list, err := env.m.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("instance %s not listed", key)
	return InstanceSummary{}
}

func composeImage(t *testing.T, env *testEnv, ref InstanceRef) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(env.cfg.StacksDir, ref.Project(), ComposeFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if img, ok := strings.CutPrefix(strings.TrimSpace(line), "image: "); ok {
			return img
		}
	}
	return ""
}

func TestUpdateToNewerPatchTag(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil) // built-in catalog: kv:8.0.1
	if s := env.summaryOf(t, "kv-cache"); s.Update == nil || s.Update.Available() {
		t.Fatalf("no discovery, no update: %+v", s.Update)
	}
	env.discover(t, newFakeSource())

	// The catalog moved to 8.0.10: the instance keeps its image (not
	// modified) and an update is offered.
	s := env.summaryOf(t, "kv-cache")
	if s.Modified || s.Image != "kv:8.0.1" || s.Update == nil || !s.Update.Image || s.Update.LatestImage != "kv:8.0.10" || len(s.Update.Versions) != 0 {
		t.Fatalf("summary = %+v update = %+v", s, s.Update)
	}

	res, err := env.m.Update(context.Background(), "kv-cache", "", "admin")
	if err != nil || !res.Updated || res.Image != "kv:8.0.10" || res.Version != "8" {
		t.Fatalf("update = %+v %v", res, err)
	}
	// Pulled by the digest read on the mirror, and pinned to it.
	if !slices.Contains(env.docker.pulls, "kv@"+digestOf('b')) || composeImage(t, env, kvRef) != "kv:8.0.10@"+digestOf('b') {
		t.Fatalf("pulled and rendered: %v %s", env.docker.pulls, composeImage(t, env, kvRef))
	}
	if r, _ := env.m.readReference(kvRef); r.Image != "kv:8.0.10" || r.Digest != digestOf('b') {
		t.Fatalf("recorded image = %s@%s", r.Image, r.Digest)
	}
	s = env.summaryOf(t, "kv-cache")
	if s.Modified || !s.Ready || s.Update.Available() {
		t.Fatalf("after update: %+v %+v", s, s.Update)
	}
	if !strings.Contains(env.audit.String(), "service instance updated") || !strings.Contains(env.audit.String(), "image_after=kv:8.0.10") {
		t.Fatalf("audit: %s", env.audit.String())
	}
	// Nothing new: no restart.
	ups := env.runner.count("svc-kv-cache up")
	if res, err := env.m.Update(context.Background(), "kv-cache", "", "admin"); err != nil || res.Updated || env.runner.count("svc-kv-cache up") != ups {
		t.Fatalf("idempotent: %+v %v", res, err)
	}
}

func TestUpdateSameTagNewDigest(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	src := newFakeSource()
	env.discover(t, src)
	if _, err := env.m.Update(context.Background(), "kv-cache", "", "admin"); err != nil {
		t.Fatal(err)
	}
	// The container runs kv:8.0.10 pinned to b; the tag is republished.
	running := env.docker.byProject(kvRef.Project()).inspect.Image
	src.digests["kv:8.0.10"] = digestOf('c')
	env.discover(t, src)
	s := env.summaryOf(t, "kv-cache")
	if !s.Update.Image || s.Update.LatestImage != "kv:8.0.10" {
		t.Fatalf("new release of the same tag: %+v", s.Update)
	}
	tag := env.docker.imageID("kv:8.0.10")
	res, err := env.m.Update(context.Background(), "kv-cache", "", "admin")
	if err != nil || !res.Updated {
		t.Fatalf("update = %+v %v", res, err)
	}
	c := env.docker.byProject(kvRef.Project())
	if c == nil || c.inspect.Image == running || c.inspect.Image != env.docker.imageID("kv@"+digestOf('c')) {
		t.Fatal("container recreated on the new image")
	}
	if composeImage(t, env, kvRef) != "kv:8.0.10@"+digestOf('c') || env.docker.imageID("kv:8.0.10") != tag {
		t.Fatal("pinned to the new digest, the tag left alone")
	}
	if s := env.summaryOf(t, "kv-cache"); s.Update.Image {
		t.Fatalf("up to date: %+v", s.Update)
	}
}

func TestUpdateToNewerVersion(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	if _, err := env.m.Create(ctx, CreateRequest{Engine: "kv", Version: "7", Name: "old"}, "admin"); err != nil {
		t.Fatal(err)
	}
	env.discover(t, newFakeSource())
	s := env.summaryOf(t, "kv-old")
	if s.Update == nil || len(s.Update.Versions) != 1 || s.Update.Versions[0].Version != "8" || !s.Update.Image {
		t.Fatalf("update targets: %+v", s.Update)
	}
	_, err := env.m.Update(ctx, "kv-old", "9", "admin")
	wantCode(t, err, CodeInvalidVersion)
	res, err := env.m.Update(ctx, "kv-old", "8", "admin")
	if err != nil || res.Version != "8" || res.Image != "kv:8.0.10" {
		t.Fatalf("update = %+v %v", res, err)
	}
	ref := InstanceRef{Engine: "kv", Version: "8", Name: "old"}
	inst, lerr := env.m.load("kv-old")
	if lerr != nil || inst.modified || inst.version.Version != "8" || composeImage(t, env, ref) != "kv:8.0.10@"+digestOf('b') {
		t.Fatalf("instance after upgrade: %+v %v", inst, lerr)
	}
	// Versions never go back.
	_, err = env.m.Update(ctx, "kv-old", "7", "admin")
	wantCode(t, err, CodeVersionDowngrade)
}

func TestUpdateRollsBack(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	env.discover(t, newFakeSource())
	compose := filepath.Join(env.cfg.StacksDir, kvRef.Project(), ComposeFile)
	before, _ := os.ReadFile(compose)
	previousID := env.docker.byProject(kvRef.Project()).inspect.Image
	env.runner.failTimes["up"] = 1 // the new image does not start
	_, err := env.m.Update(context.Background(), "kv-cache", "", "admin")
	wantCode(t, err, CodeStartFailed)
	after, _ := os.ReadFile(compose)
	inst, _ := env.m.load("kv-cache")
	if string(after) != string(before) || inst.modified || inst.version.Image != "kv:8.0.1" {
		t.Fatal("the previous configuration is restored")
	}
	if c := env.docker.byProject(kvRef.Project()); c == nil || c.inspect.Image != previousID {
		t.Fatal("running again on the previous image")
	}
	if !strings.Contains(env.audit.String(), "service instance update rolled back") {
		t.Fatal("rollback audited")
	}
	if _, rerr := env.m.ready(context.Background(), inst); rerr != nil {
		t.Fatalf("running again on the previous image: %v", rerr)
	}
}

func TestUpdateRefusals(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	mustCreate(t, env, "shop")
	_, err := env.m.Update(ctx, "fake-2", "", "admin")
	wantCode(t, err, CodeUpdateNotSupported)

	createKV(t, env, "cache", nil)
	env.discover(t, newFakeSource())
	env.docker.pullErr = errors.New("manifest unknown")
	ups := env.runner.count("svc-kv-cache up")
	_, err = env.m.Update(ctx, "kv-cache", "", "admin")
	wantCode(t, err, CodePullFailed)
	if env.runner.count("svc-kv-cache up") != ups {
		t.Fatal("nothing recreated when the pull fails")
	}
	env.docker.pullErr = nil

	// Edited by hand: never run.
	compose := filepath.Join(env.cfg.StacksDir, kvRef.Project(), ComposeFile)
	data, _ := os.ReadFile(compose)
	os.WriteFile(compose, append(data, []byte("# edited\n")...), 0o640)
	_, err = env.m.Update(ctx, "kv-cache", "", "admin")
	wantCode(t, err, CodeInstanceModified)
	if s := env.summaryOf(t, "kv-cache"); s.Update != nil {
		t.Fatal("a modified instance offers no update")
	}
}

func TestRecordImagesOfOlderInstances(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	// An instance created before images were recorded (nor pinned).
	env.unpin(t, kvRef, false)
	r, _ := env.m.readReference(kvRef)
	r.Image = ""
	env.m.writeReference(kvRef, r)
	// The catalog moves on: the instance is still the panel's (built-in
	// image), not modified.
	env.discover(t, newFakeSource())
	if inst, _ := env.m.load("kv-cache"); inst.modified || inst.version.Image != "kv:8.0.1" {
		t.Fatalf("older instance: %+v", inst)
	}
	env.m.RecordImages(context.Background())
	if r, _ := env.m.readReference(kvRef); r.Image != "kv:8.0.1" {
		t.Fatalf("recorded image = %q", r.Image)
	}
	// A recorded image that is not one of the version is tampering.
	r.Image = "evil:8.0.1"
	env.m.writeReference(kvRef, r)
	if inst, _ := env.m.load("kv-cache"); !inst.modified {
		t.Fatal("an image outside the engine catalog is never run")
	}
}

func TestSummaryCarriesSupportDates(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	s := env.summaryOf(t, "fake-2")
	if s.SupportUntil != "2030-01-01" || s.Update != nil {
		t.Fatalf("shared instance: %+v", s)
	}
	env.discover(t, newFakeSource())
	// Discovered dates replace the built-in ones (none known for fake 2).
	if s := env.summaryOf(t, "fake-2"); s.SupportUntil != "" {
		t.Fatalf("discovered: %+v", s)
	}
}

func TestUpdatedInstanceSurvivesALostCatalog(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	env.discover(t, newFakeSource())
	if _, err := env.m.Update(context.Background(), "kv-cache", "", "admin"); err != nil {
		t.Fatal(err)
	}
	// The catalog cache is lost: the built-in catalog says 8.0.1, the
	// instance runs 8.0.10.
	env.m.reg.mu.Lock()
	env.m.reg.state = catalogState{Engines: map[string]*engineCatalog{}}
	env.m.reg.mu.Unlock()
	s := env.summaryOf(t, "kv-cache")
	if s.Modified || s.Image != "kv:8.0.10" || s.Update.Available() {
		t.Fatalf("never an older patch: %+v %+v", s, s.Update)
	}
	pulls := len(env.docker.pulls)
	env.m.cfg.Releases = newFakeSource()
	if _, err := env.m.Update(context.Background(), "kv-cache", "", "admin"); err != nil {
		t.Fatal(err)
	}
	if last := env.docker.pulls[len(env.docker.pulls)-1]; len(env.docker.pulls) != pulls+1 || !strings.HasPrefix(last, "kv@") {
		t.Fatalf("same image checked again, never 8.0.1: %v", env.docker.pulls)
	}
	if inst, _ := env.m.load("kv-cache"); inst.version.Image != "kv:8.0.10" {
		t.Fatalf("image = %s", inst.version.Image)
	}
}

func TestResourcesInAnExistingSharedInstanceOfAnOldVersion(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop") // fake 2, built-in catalog
	env.discover(t, newFakeSource())
	// 2 is no longer offered (2.1 is), its instance keeps accepting resources.
	if _, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "blog"}, "admin"); err != nil {
		t.Fatal(err)
	}
	// A new instance needs an offered version.
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "3", Name: "x"}, "admin")
	wantCode(t, err, CodeInvalidVersion)
}

func TestUpdateChecksTheDigestFileFirst(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	env.discover(t, newFakeSource())
	os.Remove(filepath.Join(env.cfg.Render.PrivatePath(kvRef), digestFile))
	_, err := env.m.Update(context.Background(), "kv-cache", "", "admin")
	wantCode(t, err, CodePasswordResetRequired)
	if len(env.docker.pulls) != 0 {
		t.Fatalf("nothing pulled: %v", env.docker.pulls)
	}
}

func TestUpdateVerifiesTheRunningImage(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	env.discover(t, newFakeSource())
	// The pinned reference resolves to another image than the one pulled,
	// even one claiming the same digest: never trusted (the image ID is
	// compared with the pulled one).
	env.docker.tags["kv:8.0.10@"+digestOf('b')] = "sha256:kv#impostor"
	env.docker.more["sha256:kv#impostor"] = []string{digestOf('b')}
	_, err := env.m.Update(context.Background(), "kv-cache", "", "admin")
	wantCode(t, err, CodeStartFailed)
	inst, _ := env.m.load("kv-cache")
	if inst.modified || inst.version.Image != "kv:8.0.1" || inst.version.Digest != localDigestOf("kv:8.0.1") {
		t.Fatalf("rolled back: %+v", inst)
	}
	if _, rerr := env.m.ready(context.Background(), inst); rerr != nil {
		t.Fatalf("running again on its pinned image: %v", rerr)
	}
}

// unpin makes an instance look created before images were pinned by
// digest: no digest in its reference, a compose file rendered without it
// and, with recreate, a container created from that file.
func (env *testEnv) unpin(t *testing.T, ref InstanceRef, recreate bool) {
	t.Helper()
	inst, lerr := env.m.load(ref.Key())
	if lerr != nil {
		t.Fatal(lerr)
	}
	r, _ := env.m.readReference(inst.ref)
	r.Digest = ""
	if err := env.m.writeReference(inst.ref, r); err != nil {
		t.Fatal(err)
	}
	env.writeComposeOf(t, inst, "")
	if recreate {
		env.recreate(t, ref)
	}
}

// writeComposeOf renders the compose file of an instance with a digest
// ("" for none), as the panel would.
func (env *testEnv) writeComposeOf(t *testing.T, inst *instance, digest string) {
	t.Helper()
	v := inst.version
	v.Digest = digest
	compose, err := Render(inst.engine, inst.ref, v, inst.opts, inst.networks, env.cfg.Render)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst.dir, ComposeFile), compose, 0o640); err != nil {
		t.Fatal(err)
	}
}

// recreate runs compose on the files of an instance, as an options,
// exposure or network change (or a reboot without the panel) would.
func (env *testEnv) recreate(t *testing.T, ref InstanceRef) {
	t.Helper()
	if _, err := env.runner.Run(context.Background(), filepath.Join(env.cfg.StacksDir, ref.Project()), recreateArgs...); err != nil {
		t.Fatal(err)
	}
}

func (env *testEnv) runningImage(t *testing.T, ref InstanceRef) string {
	t.Helper()
	c := env.docker.byProject(ref.Project())
	if c == nil {
		t.Fatalf("no container for %s", ref.Project())
	}
	return c.inspect.Image
}

func TestNewDedicatedInstanceIsPinned(t *testing.T) {
	env := newEnv(t)
	// The tag is not present locally: pulled, as compose would.
	env.docker.absent["kv:8.0.1"] = true
	createKV(t, env, "cache", nil)
	if !slices.Contains(env.docker.pulls, "kv:8.0.1") {
		t.Fatalf("the tag is pulled first: %v", env.docker.pulls)
	}
	want := localDigestOf("kv:8.0.1")
	if r, _ := env.m.readReference(kvRef); r.Image != "kv:8.0.1" || r.Digest != want {
		t.Fatalf("reference = %+v", r)
	}
	if got := composeImage(t, env, kvRef); got != "kv:8.0.1@"+want {
		t.Fatalf("compose image = %s", got)
	}
	inst, _ := env.m.load("kv-cache")
	if _, rerr := env.m.ready(context.Background(), inst); inst.modified || rerr != nil {
		t.Fatalf("pinned instance: modified=%v %v", inst.modified, rerr)
	}
	// Shared instances keep the line tag of their version.
	mustCreate(t, env, "shop")
	if got := composeImage(t, env, ref2); strings.Contains(got, "@") {
		t.Fatalf("shared instance image = %s", got)
	}
}

func TestNewDedicatedInstanceWithoutDigest(t *testing.T) {
	env := newEnv(t)
	// An image built or loaded locally carries no digest: run by its tag.
	env.docker.mu.Lock()
	env.docker.tags["kv:8.0.1"] = "sha256:local-build"
	env.docker.mu.Unlock()
	createKV(t, env, "cache", nil)
	if r, _ := env.m.readReference(kvRef); r.Digest != "" || composeImage(t, env, kvRef) != "kv:8.0.1" {
		t.Fatalf("unpinned: %+v %s", r, composeImage(t, env, kvRef))
	}
	if !strings.Contains(env.logs.String(), "no digest for its repository") {
		t.Fatal("logged")
	}
	env.docker.mu.Lock()
	env.docker.pullErr = errors.New("network down")
	env.docker.mu.Unlock()
	env.docker.absent["kv:8.0.1"] = true
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "kv", Version: "8", Name: "other"}, "admin")
	wantCode(t, err, CodePullFailed)
}

// Finding of the review of §19: instances of a version share its tag; an
// update (successful or rolled back) of one of them must never change the
// image another one runs, now or at its next recreation.
func TestUpdateLeavesOtherInstancesAlone(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	refA := InstanceRef{Engine: "kv", Version: "8", Name: "a"}
	refB := InstanceRef{Engine: "kv", Version: "8", Name: "b"}
	createKV(t, env, "a", nil)
	createKV(t, env, "b", nil)
	original := env.runningImage(t, refB)
	tag := env.docker.imageID("kv:8.0.1")
	composeB, _ := os.ReadFile(filepath.Join(env.cfg.StacksDir, refB.Project(), ComposeFile))
	env.discover(t, newFakeSource())

	// A is updated: B keeps its files and, recreated, its image.
	if _, err := env.m.Update(ctx, "kv-a", "", "admin"); err != nil {
		t.Fatal(err)
	}
	updated := env.runningImage(t, refA)
	if updated == original {
		t.Fatal("A runs the new image")
	}
	if got, _ := os.ReadFile(filepath.Join(env.cfg.StacksDir, refB.Project(), ComposeFile)); string(got) != string(composeB) {
		t.Fatal("B's compose file is untouched")
	}
	env.recreate(t, refB)
	if env.runningImage(t, refB) != original {
		t.Fatal("B recreated still runs its own image")
	}

	// B's update fails and is rolled back: A, recreated, keeps the new image.
	env.runner.failTimes["up"] = 1
	_, err := env.m.Update(ctx, "kv-b", "", "admin")
	wantCode(t, err, CodeStartFailed)
	if env.runningImage(t, refB) != original {
		t.Fatal("B is back on its image")
	}
	env.recreate(t, refA)
	if env.runningImage(t, refA) != updated {
		t.Fatal("A recreated after B's rollback still runs the new image")
	}
	for _, key := range []string{"kv-a", "kv-b"} {
		inst, _ := env.m.load(key)
		if _, rerr := env.m.ready(ctx, inst); inst.modified || rerr != nil {
			t.Fatalf("%s: modified=%v %v", key, inst.modified, rerr)
		}
	}
	// No tag was moved.
	if env.docker.imageID("kv:8.0.1") != tag || env.docker.imageID("kv:8.0.10") != "sha256:kv:8.0.10" {
		t.Fatal("tags left alone")
	}
}

// A failed version change restores the previous pinned reference and never
// touches the instances of the previous version.
func TestVersionChangeRollback(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	for _, name := range []string{"old", "other"} {
		if _, err := env.m.Create(ctx, CreateRequest{Engine: "kv", Version: "7", Name: name}, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	refOld := InstanceRef{Engine: "kv", Version: "7", Name: "old"}
	refOther := InstanceRef{Engine: "kv", Version: "7", Name: "other"}
	env.discover(t, newFakeSource())
	composeOld := composeImage(t, env, refOld)
	imageOld, imageOther := env.runningImage(t, refOld), env.runningImage(t, refOther)
	if !strings.Contains(composeOld, "@sha256:") {
		t.Fatalf("pinned: %s", composeOld)
	}

	env.runner.failTimes["up"] = 1
	_, err := env.m.Update(ctx, "kv-old", "8", "admin")
	wantCode(t, err, CodeStartFailed)
	inst, lerr := env.m.load("kv-old")
	if lerr != nil || inst.modified || inst.version.Version != "7" || inst.version.Reference() != composeOld || composeImage(t, env, refOld) != composeOld {
		t.Fatalf("previous version and pinned image restored: %+v %v", inst, lerr)
	}
	if _, rerr := env.m.ready(ctx, inst); rerr != nil || env.runningImage(t, refOld) != imageOld {
		t.Fatalf("running again on its previous image: %v", rerr)
	}
	env.recreate(t, refOther)
	if env.runningImage(t, refOther) != imageOther {
		t.Fatal("the other instance of the previous version keeps its image")
	}
}

func TestPinImagesMigratesOlderInstances(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	createKV(t, env, "cache", nil)
	env.unpin(t, kvRef, true)
	inst, _ := env.m.load("kv-cache")
	if _, rerr := env.m.ready(ctx, inst); inst.modified || inst.version.Digest != "" || rerr != nil {
		t.Fatalf("older instance: modified=%v digest=%q %v", inst.modified, inst.version.Digest, rerr)
	}
	ups := env.runner.count("svc-kv-cache up")

	env.m.PinImages(ctx)
	want := localDigestOf("kv:8.0.1")
	if r, _ := env.m.readReference(kvRef); r.Digest != want || composeImage(t, env, kvRef) != "kv:8.0.1@"+want {
		t.Fatalf("pinned to the running image: %+v %s", r, composeImage(t, env, kvRef))
	}
	inst, _ = env.m.load("kv-cache")
	// Not modified by the new rendering, and its container (created from
	// the tag) is still trusted: it runs the pinned digest. Not recreated.
	if _, rerr := env.m.ready(ctx, inst); inst.modified || inst.unpinnedCompose || rerr != nil {
		t.Fatalf("migrated: modified=%v unpinned=%v %v", inst.modified, inst.unpinnedCompose, rerr)
	}
	if env.runner.count("svc-kv-cache up") != ups {
		t.Fatal("not recreated")
	}
	env.m.PinImages(ctx) // idempotent
	if r, _ := env.m.readReference(kvRef); r.Digest != want {
		t.Fatal("idempotent")
	}
}

func TestPinImagesNeverTrustsAMismatchingDigest(t *testing.T) {
	ctx := context.Background()
	t.Run("running image without a digest of its repository", func(t *testing.T) {
		env := newEnv(t)
		createKV(t, env, "cache", nil)
		env.unpin(t, kvRef, true)
		other := "sha256:other#" + digestOf('e')
		env.docker.mu.Lock()
		env.docker.digests[other] = digestOf('e') // RepoDigests "other@..."
		env.docker.byProjectLocked(kvRef.Project()).inspect.Image = other
		env.docker.mu.Unlock()
		env.m.PinImages(ctx)
		if r, _ := env.m.readReference(kvRef); r.Digest != "" || composeImage(t, env, kvRef) != "kv:8.0.1" {
			t.Fatalf("kept on its tag: %+v %s", r, composeImage(t, env, kvRef))
		}
		if !strings.Contains(env.logs.String(), "image not pinned by digest") {
			t.Fatal("logged")
		}
		if inst, _ := env.m.load("kv-cache"); inst.modified {
			t.Fatal("behaviour unchanged: not modified")
		}
	})
	t.Run("untrusted container", func(t *testing.T) {
		env := newEnv(t)
		createKV(t, env, "cache", nil)
		env.unpin(t, kvRef, true)
		env.docker.mu.Lock()
		env.docker.byProjectLocked(kvRef.Project()).inspect.HostConfig.Privileged = true
		env.docker.mu.Unlock()
		env.m.PinImages(ctx)
		if r, _ := env.m.readReference(kvRef); r.Digest != "" {
			t.Fatal("never pinned from an untrusted container")
		}
	})
	t.Run("recorded digest the container does not run", func(t *testing.T) {
		env := newEnv(t)
		createKV(t, env, "cache", nil)
		inst, _ := env.m.load("kv-cache")
		r, _ := env.m.readReference(kvRef)
		r.Digest = digestOf('f')
		env.m.writeReference(kvRef, r)
		env.writeComposeOf(t, inst, digestOf('f'))
		inst, _ = env.m.load("kv-cache")
		_, rerr := env.m.ready(ctx, inst)
		wantCode(t, rerr, CodeNotReady)
	})
	t.Run("invalid recorded digest", func(t *testing.T) {
		env := newEnv(t)
		createKV(t, env, "cache", nil)
		r, _ := env.m.readReference(kvRef)
		r.Digest = "sha256:short"
		env.m.writeReference(kvRef, r)
		if inst, _ := env.m.load("kv-cache"); !inst.modified {
			t.Fatal("an invalid digest is tampering")
		}
	})
}

// PinImages writes the reference, then compose.yaml: interrupted in
// between, the instance is modified (nothing runs compose on it) and
// PinImages finishes it at the next start.
func TestInterruptedPinning(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	createKV(t, env, "cache", nil)
	inst, _ := env.m.load("kv-cache")
	env.writeComposeOf(t, inst, "") // reference pinned, compose not yet
	inst, _ = env.m.load("kv-cache")
	if !inst.modified || !inst.unpinnedCompose {
		t.Fatalf("interrupted: modified=%v unpinned=%v", inst.modified, inst.unpinnedCompose)
	}
	env.m.PinImages(ctx)
	if composeImage(t, env, kvRef) != "kv:8.0.1@"+localDigestOf("kv:8.0.1") {
		t.Fatal("compose file finished")
	}
	inst, _ = env.m.load("kv-cache")
	if _, rerr := env.m.ready(ctx, inst); inst.modified || inst.unpinnedCompose || rerr != nil {
		t.Fatalf("pinned: modified=%v %v", inst.modified, rerr)
	}
	// Any other digest in the compose file is an edit.
	env.writeComposeOf(t, inst, digestOf('9'))
	if inst, _ := env.m.load("kv-cache"); !inst.modified {
		t.Fatal("another digest is a modification")
	}
}

// An image may carry several digests for its repository, each one naming
// it: an instance pinned to another one than the mirror's runs the latest
// image, offers no update, and stays trusted, whichever of its digests its
// container was created from.
func TestPinnedToAnotherDigestOfTheSameImage(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	createKV(t, env, "cache", nil)
	env.discover(t, newFakeSource())
	if _, err := env.m.Update(ctx, "kv-cache", "", "admin"); err != nil {
		t.Fatal(err)
	}
	// The running image (pulled as 'b') also carries 'd'; the instance is
	// pinned to 'd' and its container created from it.
	inst, _ := env.m.load("kv-cache")
	r, _ := env.m.readReference(kvRef)
	r.Digest = digestOf('d')
	env.m.writeReference(kvRef, r)
	env.writeComposeOf(t, inst, digestOf('d'))
	env.docker.mu.Lock()
	running := env.docker.byProjectLocked(kvRef.Project()).inspect.Image
	env.docker.tags["kv@"+digestOf('d')] = running
	env.docker.more[running] = []string{digestOf('d')}
	env.docker.mu.Unlock()
	env.recreate(t, kvRef)
	s := env.summaryOf(t, "kv-cache")
	if s.Modified || !s.Ready || s.Update.Image {
		t.Fatalf("no update offered: %+v %+v", s, s.Update)
	}
	ups := env.runner.count("svc-kv-cache up")
	if res, err := env.m.Update(ctx, "kv-cache", "", "admin"); err != nil || res.Updated || env.runner.count("svc-kv-cache up") != ups {
		t.Fatalf("no-op update = %+v %v", res, err)
	}
	inst, _ = env.m.load("kv-cache")
	if _, rerr := env.m.ready(ctx, inst); rerr != nil {
		t.Fatalf("trusted: %v", rerr)
	}
	// Pinned to 'b', the container still created from 'd': trusted (the
	// image carries both).
	r.Digest = digestOf('b')
	env.m.writeReference(kvRef, r)
	env.writeComposeOf(t, inst, digestOf('b'))
	inst, _ = env.m.load("kv-cache")
	if _, rerr := env.m.ready(ctx, inst); inst.modified || rerr != nil {
		t.Fatalf("created from another digest of the pinned image: %v", rerr)
	}
	// A digest the image does not carry, in the container, is refused.
	env.docker.mu.Lock()
	env.docker.byProjectLocked(kvRef.Project()).inspect.Config.Image = "kv:8.0.10@" + digestOf('e')
	env.docker.mu.Unlock()
	_, rerr := env.m.ready(ctx, inst)
	wantCode(t, rerr, CodeNotReady)
}

// Whoever writes the rendering without the digest (CI, at any time), the
// panel never runs compose on it: it would recreate the container from
// the shared tag. Only PinImages repairs it.
func TestUnpinnedComposeIsNeverRun(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	createKV(t, env, "cache", nil)
	env.discover(t, newFakeSource())
	inst, _ := env.m.load("kv-cache")
	env.writeComposeOf(t, inst, "")
	ups := env.runner.count("svc-kv-cache up")
	exp, _ := ParseExposure("127.0.0.1", 5416)
	_, err := env.m.SetExposure(ctx, "kv-cache", &exp, "admin")
	wantCode(t, err, CodeInstanceModified)
	_, err = env.m.UpdateOptions(ctx, "kv-cache", map[string]string{"memory": "128"}, "admin")
	wantCode(t, err, CodeInstanceModified)
	_, err = env.m.Update(ctx, "kv-cache", "", "admin")
	wantCode(t, err, CodeInstanceModified)
	_, err = env.m.UpdateNetworks(ctx, "kv-cache", []string{"services", "jobs"}, "admin")
	wantCode(t, err, CodeInstanceModified)
	env.m.applyAll(ctx) // the startup application of the exposures
	if env.runner.count("svc-kv-cache up") != ups {
		t.Fatalf("compose run on the unpinned file: %v", env.runner.commands())
	}
	if s := env.summaryOf(t, "kv-cache"); !s.Modified || s.Update != nil {
		t.Fatalf("reported modified: %+v", s)
	}
	// Repaired at the next start: operations run on the pinned file again.
	env.m.PinImages(ctx)
	if _, err := env.m.SetExposure(ctx, "kv-cache", &exp, "admin"); err != nil {
		t.Fatal(err)
	}
	want := "kv:8.0.1@" + localDigestOf("kv:8.0.1")
	if c := env.docker.byProject(kvRef.Project()); c.inspect.Config.Image != want {
		t.Fatalf("recreated from %s, want %s", c.inspect.Config.Image, want)
	}
}

// An update of an unpinned instance interrupted between its reference and
// compose writes leaves a recorded digest its container does not run:
// PinImages never commits it, it pins the image the container runs.
func TestPinImagesAfterAnInterruptedUpdate(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	createKV(t, env, "cache", nil)
	env.unpin(t, kvRef, true)
	r, _ := env.m.readReference(kvRef)
	r.Digest = digestOf('b') // the update's, never pulled nor run
	env.m.writeReference(kvRef, r)
	if inst, _ := env.m.load("kv-cache"); !inst.modified || !inst.unpinnedCompose {
		t.Fatal("interrupted update: modified, compose without its digest")
	}
	ups := env.runner.count("svc-kv-cache up")
	env.m.PinImages(ctx)
	want := localDigestOf("kv:8.0.1")
	if r, _ := env.m.readReference(kvRef); r.Digest != want || composeImage(t, env, kvRef) != "kv:8.0.1@"+want {
		t.Fatalf("pinned to the running image: %+v %s", r, composeImage(t, env, kvRef))
	}
	if !strings.Contains(env.logs.String(), "interrupted update") || env.runner.count("svc-kv-cache up") != ups {
		t.Fatal("logged, not recreated")
	}
	inst, _ := env.m.load("kv-cache")
	if _, rerr := env.m.ready(ctx, inst); inst.modified || rerr != nil {
		t.Fatalf("trusted: modified=%v %v", inst.modified, rerr)
	}
}

// When the catalog knows the mirror digest of the tag and the image carries
// it, creation and pinning prefer it (otherwise: the smallest digest).
func TestPinningPrefersTheMirrorDigest(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	src := newFakeSource()
	src.digests["kv:8.0.10"] = digestOf('f') // the largest: never the default pick
	env.discover(t, src)
	local := env.docker.imageID("kv:8.0.10")
	env.docker.mu.Lock()
	env.docker.more[local] = []string{digestOf('f')}
	env.docker.mu.Unlock()
	createKV(t, env, "cache", nil) // kv 8 is 8.0.10 now
	if r, _ := env.m.readReference(kvRef); r.Digest != digestOf('f') {
		t.Fatalf("created pinned to %s", r.Digest)
	}
	env.unpin(t, kvRef, true)
	env.m.PinImages(ctx)
	if r, _ := env.m.readReference(kvRef); r.Digest != digestOf('f') {
		t.Fatalf("migrated pinned to %s", r.Digest)
	}
	inst, _ := env.m.load("kv-cache")
	if _, rerr := env.m.ready(ctx, inst); inst.modified || rerr != nil {
		t.Fatalf("trusted: %v", rerr)
	}
}

// Update pins an instance that could not be pinned at startup when it
// already runs the latest image, without recreating it.
func TestUpdatePinsAnUnpinnedInstanceOnTheLatestImage(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	src := newFakeSource()
	src.digests["kv:8.0.1"] = localDigestOf("kv:8.0.1")
	env.m.cfg.Releases = src
	createKV(t, env, "cache", nil)
	env.unpin(t, kvRef, true)
	ups := env.runner.count("svc-kv-cache up")
	// No discovery: the same tag, whose mirror digest is the running image's.
	res, err := env.m.Update(ctx, "kv-cache", "", "admin")
	if err != nil || res.Updated {
		t.Fatalf("update = %+v %v", res, err)
	}
	if r, _ := env.m.readReference(kvRef); r.Digest != localDigestOf("kv:8.0.1") || env.runner.count("svc-kv-cache up") != ups {
		t.Fatalf("pinned without a restart: %+v", r)
	}
}

// Pinning an instance already on the latest image fails to write its
// compose file: the reference is given back and Update reports the error,
// never a success leaving the instance modified.
func TestUpdatePinFailureRestoresTheReference(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	src := newFakeSource()
	src.digests["kv:8.0.1"] = localDigestOf("kv:8.0.1")
	env.m.cfg.Releases = src
	createKV(t, env, "cache", nil)
	env.unpin(t, kvRef, true)
	// The compose file cannot be written (a permission would not stop
	// root, which runs the CI jobs).
	writePinnedCompose = func(string, string, []byte, os.FileMode) error {
		return errors.New("disk full")
	}
	t.Cleanup(func() { writePinnedCompose = composekit.WriteFileAtomic })
	_, err := env.m.Update(ctx, "kv-cache", "", "admin")
	writePinnedCompose = composekit.WriteFileAtomic
	wantCode(t, err, CodeStorage)
	if r, _ := env.m.readReference(kvRef); r.Digest != "" {
		t.Fatalf("reference given back: %+v", r)
	}
	inst, _ := env.m.load("kv-cache")
	if _, rerr := env.m.ready(ctx, inst); inst.modified || rerr != nil {
		t.Fatalf("unchanged: modified=%v %v", inst.modified, rerr)
	}
}
