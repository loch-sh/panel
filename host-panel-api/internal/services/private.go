package services

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"host-panel-api/internal/composekit"
)

// reference is what the panel recorded about an instance, in its private
// directory. The compose manifest (in the stacks directory, writable by CI)
// must match it: otherwise the instance is reported as modified and never
// run by the panel (a rewritten manifest could change the version run on
// existing data, or lift the limits). The networks and the exposure are
// rendered from it: the compose files on disk must be exactly that.
type reference struct {
	Engine   string          `json:"engine"`
	Version  string          `json:"version"`
	Name     string          `json:"name"`
	Options  Options         `json:"options"`
	Networks []string        `json:"networks"`
	Exposure *ExposureRecord `json:"exposure,omitempty"`
	// Image run by a dedicated instance: the catalog moves on (new patch
	// tag), the instance keeps its image until it is updated. Absent for
	// instances created before it was recorded (the built-in catalog image)
	// and for shared instances (always the line tag of their version).
	Image string `json:"image,omitempty"`
	// Digest pins Image (dedicated instances): the compose file runs
	// Image@Digest, never what the shared local tag points to. Absent for
	// instances whose digest could not be derived yet (see PinImages).
	Digest string `json:"digest,omitempty"`
}

// ExposureRecord is the published port chosen in the panel.
type ExposureRecord struct {
	Address     string `json:"address"`
	Port        int    `json:"port"`
	TLSOptional bool   `json:"tlsOptional,omitempty"`
}

// exposure returns the parsed record (nil when the instance is not
// published, false when the record is invalid).
func (r reference) exposure() (*Exposure, bool) {
	if r.Exposure == nil {
		return nil, true
	}
	exp, ok := AppliedExposure{Address: r.Exposure.Address, Port: r.Exposure.Port, TLSOptional: r.Exposure.TLSOptional}.exposure()
	if !ok || exp == nil {
		return nil, false
	}
	return exp, true
}

// recordOf returns the record of an exposure (nil for none).
func recordOf(exp *Exposure) *ExposureRecord {
	if exp == nil {
		return nil
	}
	return &ExposureRecord{Address: exp.HostIP(), Port: exp.Port, TLSOptional: exp.TLSOptional}
}

const privatePerm = 0o700

// digestFile holds the digest of the password of a dedicated instance, so
// that its state can be rendered again when its options change.
const digestFile = "password.digest"

// reference returns what the panel records about a loaded instance.
func (i *instance) reference() reference {
	r := reference{Engine: i.ref.Engine, Version: i.ref.Version, Name: i.ref.Name, Options: i.opts,
		Networks: slices.Clone(i.networks), Exposure: recordOf(i.desired)}
	if i.engine.Lifecycle() == Dedicated {
		r.Image, r.Digest = i.version.Image, i.version.Digest
	}
	return r
}

// sameInstance reports whether the reference describes this instance
// (engine, version, name and options).
func (r reference) sameInstance(ref InstanceRef, opts Options) bool {
	return r.Engine == ref.Engine && r.Version == ref.Version && r.Name == ref.Name && maps.Equal(r.Options, opts)
}

// equal compares two references entirely.
func (r reference) equal(o reference) bool {
	if !r.sameInstance(InstanceRef{Engine: o.Engine, Version: o.Version, Name: o.Name}, o.Options) || !slices.Equal(r.Networks, o.Networks) || r.Image != o.Image || r.Digest != o.Digest {
		return false
	}
	if (r.Exposure == nil) != (o.Exposure == nil) {
		return false
	}
	return r.Exposure == nil || *r.Exposure == *o.Exposure
}

// ensurePrivate creates the private directory of an instance (and the
// shared one), never through a symlink, with its state directory when the
// engine has state.
func (m *Manager) ensurePrivate(ref InstanceRef, state bool) error {
	if err := composekit.RealDir(m.cfg.Render.DataDir); err != nil {
		return err
	}
	if _, err := composekit.EnsureDir(filepath.Join(m.cfg.Render.DataDir, PrivateDir), privatePerm); err != nil {
		return err
	}
	dir := m.cfg.Render.PrivatePath(ref)
	if _, err := composekit.EnsureDir(dir, privatePerm); err != nil {
		return err
	}
	if !state {
		return nil
	}
	// Read by the engine user through the bind mount: the directory itself
	// is readable by all, its parents are not.
	_, err := composekit.EnsureDir(filepath.Join(dir, StateDir), 0o755)
	return err
}

func (m *Manager) writeReference(ref InstanceRef, r reference) error {
	slices.Sort(r.Networks)
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return composekit.WriteFileAtomic(m.cfg.Render.PrivatePath(ref), referenceFile, data, filePerm)
}

// readReference returns the recorded reference of an instance.
func (m *Manager) readReference(ref InstanceRef) (reference, error) {
	for _, p := range []string{filepath.Join(m.cfg.Render.DataDir, PrivateDir), m.cfg.Render.PrivatePath(ref)} {
		if err := composekit.RealDir(p); err != nil {
			return reference{}, err
		}
	}
	data, err := composekit.ReadFileNoFollow(filepath.Join(m.cfg.Render.PrivatePath(ref), referenceFile))
	if err != nil {
		return reference{}, err
	}
	var r reference
	if err := json.Unmarshal(data, &r); err != nil {
		return reference{}, err
	}
	return r, nil
}

// removePrivate deletes the private directory of an instance.
func (m *Manager) removePrivate(ref InstanceRef) error {
	for _, p := range []string{filepath.Join(m.cfg.Render.DataDir, PrivateDir), m.cfg.Render.PrivatePath(ref)} {
		if err := composekit.RealDir(p); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
	}
	return os.RemoveAll(m.cfg.Render.PrivatePath(ref))
}

func (m *Manager) writeDigest(ref InstanceRef, digest string) error {
	return composekit.WriteFileAtomic(m.cfg.Render.PrivatePath(ref), digestFile, []byte(digest), 0o600)
}

// readDigest returns the recorded password digest of a dedicated instance.
func (m *Manager) readDigest(ref InstanceRef) (string, error) {
	for _, p := range []string{filepath.Join(m.cfg.Render.DataDir, PrivateDir), m.cfg.Render.PrivatePath(ref)} {
		if err := composekit.RealDir(p); err != nil {
			return "", err
		}
	}
	data, err := composekit.ReadFileNoFollow(filepath.Join(m.cfg.Render.PrivatePath(ref), digestFile))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
