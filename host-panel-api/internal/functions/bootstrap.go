package functions

import (
	"bytes"
	"debug/elf"
	"io"
	"os"
	"path"
	"slices"
	"syscall"
)

// Deploy-time checks of a custom runtime (provided.al2023): the image
// starts /var/task/bootstrap (then /opt/bootstrap, never another name), so
// the code must hold, at its root, a Linux executable for the function's
// architecture or a "#!" script. Refusing a wrong binary at deploy time
// gives a clear message instead of a container that never becomes healthy
// (a deploy whose bootstrap does not start fails and is rolled back anyway:
// compose up --wait).

// CodeCheck holds the runtime-specific checks of an upload.
type CodeCheck struct {
	// BootstrapArch requires a bootstrap ELF executable for this
	// architecture at the root of the code (custom runtimes); empty for
	// the other runtimes.
	BootstrapArch string
}

// CodeCheckFor returns the checks of a function's code.
func CodeCheckFor(ref *Reference) CodeCheck {
	if ref.runtime().Custom {
		return CodeCheck{BootstrapArch: ref.Arch}
	}
	return CodeCheck{}
}

// WarnBootstrapDynamic: the bootstrap is dynamically linked against glibc.
// al2023 has glibc, so it may run, but only if it was not built against a
// newer one; a static build (CGO_ENABLED=0) does not depend on it.
const WarnBootstrapDynamic = "FUNCTION_BOOTSTRAP_DYNAMIC"

var knownWarnings = map[string]bool{WarnBootstrapDynamic: true}

// dropUnknownWarnings removes, from a reference read on disk, the warning
// codes this version does not know (written by a newer panel: forward
// compatible) and duplicates, and returns the codes dropped.
func dropUnknownWarnings(r *Reference) []string {
	var dropped []string
	clean := func(d *Deployment) {
		if d == nil || d.Warnings == nil {
			return
		}
		kept := make([]string, 0, len(d.Warnings))
		for _, c := range d.Warnings {
			if knownWarnings[c] && !slices.Contains(kept, c) {
				kept = append(kept, c)
			} else if !knownWarnings[c] {
				dropped = append(dropped, c)
			}
		}
		if len(kept) == 0 {
			kept = nil
		}
		d.Warnings = kept
	}
	clean(r.Deployment)
	for i := range r.Kept {
		clean(&r.Kept[i])
	}
	return dropped
}

// validWarnings checks the warnings recorded with a deployment.
func validWarnings(w []string) bool {
	if len(w) > len(knownWarnings) {
		return false
	}
	for i, c := range w {
		if !knownWarnings[c] || slices.Contains(w[:i], c) {
			return false
		}
	}
	return true
}

// maxBootstrapBytes bounds the bootstrap (Lambda's limit for the whole
// unzipped code).
const maxBootstrapBytes = 250 << 20

// elfMachines are the ELF machines of the supported architectures.
var elfMachines = map[string]elf.Machine{"arm64": elf.EM_AARCH64, "amd64": elf.EM_X86_64}

// glibcInterpreters are the dynamic loaders al2023 provides.
var glibcInterpreters = map[string]string{"arm64": "/lib/ld-linux-aarch64.so.1", "amd64": "/lib64/ld-linux-x86-64.so.2"}

// buildHint is the build command for an architecture.
func buildHint(arch string) string {
	return "GOOS=linux GOARCH=" + arch + " CGO_ENABLED=0 go build -tags lambda.norpc -o bootstrap"
}

// requireBootstrap checks, before extraction, that the plan holds a regular
// bootstrap file at its root, and forces it executable (0555): zips made on
// Windows (or by tools that drop the Unix modes) lose the exec bit. Other
// files keep the exec bit they have in the zip (native addons, helpers).
func (p *archivePlan) requireBootstrap(arch string) error {
	nested := ""
	for i := range p.entries {
		e := &p.entries[i]
		if e.name != BootstrapFile {
			if nested == "" && e.kind == kindFile && path.Base(e.name) == BootstrapFile {
				nested = e.name
			}
			continue
		}
		switch e.kind {
		case kindFile:
			e.exec = true
			return nil
		case kindLink:
			return newError(CodeBootstrapInvalid, "code", "bootstrap is a symlink: it must be a regular file at the root of the zip")
		default:
			return newError(CodeBootstrapInvalid, "code", "bootstrap is a directory: it must be an executable file at the root of the zip")
		}
	}
	if nested != "" {
		return newError(CodeBootstrapMissing, "code", "the zip has no bootstrap at its root (found %s): zip the file itself, not its folder", quoteName(nested))
	}
	return newError(CodeBootstrapMissing, "code", "a custom runtime runs the bootstrap file at the root of the zip, which is missing: build it with %s", buildHint(arch))
}

// checkBootstrap inspects the extracted bootstrap of a deployment directory
// and returns the warnings of a code that is not refused.
func checkBootstrap(dir, arch string) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, newError(CodeStorage, "", "the deployment directory cannot be opened")
	}
	defer root.Close()
	f, err := root.OpenFile(BootstrapFile, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, newError(CodeStorage, "", "the bootstrap cannot be read")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, newError(CodeBootstrapInvalid, "code", "bootstrap must be a regular file")
	}
	return inspectBootstrap(f, fi.Size(), arch)
}

// inspectBootstrap checks a bootstrap: a "#!" script (accepted as is, as on
// AWS), or a 64-bit little-endian Linux ELF executable of the architecture,
// static, or linked against glibc with the loader of al2023 (warning).
func inspectBootstrap(r io.ReaderAt, size int64, arch string) ([]string, error) {
	want, ok := elfMachines[arch]
	if !ok {
		return nil, newError(CodeBootstrapArch, "code", "unsupported architecture %s", arch)
	}
	notELF := func() error {
		return newError(CodeBootstrapInvalid, "code", "bootstrap is neither a Linux executable (ELF, static, or linked against glibc) nor a #! script: build it for Linux with %s", buildHint(arch))
	}
	if size > maxBootstrapBytes {
		return nil, newError(CodeBootstrapInvalid, "code", "bootstrap exceeds %s", sizeText(maxBootstrapBytes))
	}
	var magic [4]byte
	got, _ := r.ReadAt(magic[:], 0)
	if bytes.HasPrefix(magic[:got], []byte("#!")) {
		return nil, nil
	}
	if got < len(magic) || string(magic[:]) != elf.ELFMAG {
		return nil, notELF()
	}
	ef, err := elf.NewFile(io.NewSectionReader(r, 0, size))
	if err != nil {
		return nil, notELF()
	}
	defer ef.Close()
	if ef.Machine != want {
		got := ef.Machine.String()
		for a, m := range elfMachines {
			if m == ef.Machine {
				got = a
			}
		}
		return nil, newError(CodeBootstrapArch, "code", "bootstrap is built for %s, the function runs on %s: %s", got, arch, buildHint(arch))
	}
	if ef.Class != elf.ELFCLASS64 || ef.Data != elf.ELFDATA2LSB {
		return nil, notELF()
	}
	if ef.OSABI != elf.ELFOSABI_NONE && ef.OSABI != elf.ELFOSABI_LINUX {
		return nil, newError(CodeBootstrapInvalid, "code", "bootstrap is not built for Linux: %s", buildHint(arch))
	}
	if ef.Type != elf.ET_EXEC && ef.Type != elf.ET_DYN {
		return nil, newError(CodeBootstrapInvalid, "code", "bootstrap is not an executable (ELF type %s)", ef.Type)
	}
	for _, p := range ef.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		const maxInterp = 256
		if p.Filesz == 0 || p.Filesz > maxInterp {
			return nil, notELF()
		}
		buf := make([]byte, p.Filesz)
		if _, err := p.ReadAt(buf, 0); err != nil {
			return nil, notELF()
		}
		interp := string(bytes.TrimRight(buf, "\x00"))
		if interp != glibcInterpreters[arch] {
			return nil, newError(CodeBootstrapInvalid, "code",
				"bootstrap needs the loader %s, missing from al2023 (musl or another C library?): build it static, or linked against glibc, with %s", quoteName(interp), buildHint(arch))
		}
		return []string{WarnBootstrapDynamic}, nil
	}
	return nil, nil
}
