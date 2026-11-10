package functions

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// rawCD builds n minimal central directory records (no name, no local
// header: archive/zip.NewReader reads the directory only).
func rawCD(n, extra int) []byte {
	rec := make([]byte, dirHeaderLen+extra)
	binary.LittleEndian.PutUint32(rec, dirHeaderSig)
	binary.LittleEndian.PutUint16(rec[30:], uint16(extra))
	out := make([]byte, 0, n*len(rec))
	for range n {
		out = append(out, rec...)
	}
	return out
}

// tail describes the end records written after a directory.
type tail struct {
	prefix                 int
	zip64                  bool
	z64Records, z64Size    uint64
	z64Offset              uint64
	locDisk, locTotalDisks uint32
	locOffsetDelta         uint64 // added to the real zip64 record offset
	eocdRecords            uint16
	eocdSize, eocdOffset   uint32
	comment                []byte
}

func buildArchive(cd []byte, o tail) []byte {
	le := binary.LittleEndian
	out := make([]byte, o.prefix, o.prefix+len(cd)+120+len(o.comment))
	out = append(out, cd...)
	if o.zip64 {
		z64 := len(out)
		z := make([]byte, zip64EOCDLen)
		le.PutUint32(z, zip64EOCDSig)
		le.PutUint64(z[4:], 44)
		le.PutUint64(z[24:], o.z64Records)
		le.PutUint64(z[32:], o.z64Records)
		le.PutUint64(z[40:], o.z64Size)
		le.PutUint64(z[48:], o.z64Offset)
		out = append(out, z...)
		l := make([]byte, zip64LocLen)
		le.PutUint32(l, zip64LocSig)
		le.PutUint32(l[4:], o.locDisk)
		le.PutUint64(l[8:], uint64(z64)+o.locOffsetDelta)
		le.PutUint32(l[16:], o.locTotalDisks)
		out = append(out, l...)
	}
	e := make([]byte, eocdLen)
	le.PutUint32(e, eocdSig)
	le.PutUint16(e[8:], o.eocdRecords)
	le.PutUint16(e[10:], o.eocdRecords)
	le.PutUint32(e[12:], o.eocdSize)
	le.PutUint32(e[16:], o.eocdOffset)
	le.PutUint16(e[20:], uint16(len(o.comment)))
	out = append(out, e...)
	return append(out, o.comment...)
}

// assertPreCount checks the invariant: whenever archive/zip opens an
// archive, the pre-count is at least the number of records it loaded.
func assertPreCount(t *testing.T, data []byte, limit int) (files, count int) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	count, found := countDirectoryRecords(bytes.NewReader(data), int64(len(data)), limit)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return -1, count
	}
	files = len(zr.File)
	if !found || (count < files && count <= limit) {
		t.Fatalf("archive/zip loads %d records, pre-count %d (found %v)", files, count, found)
	}
	return files, count
}

// The cases of the security reviews, with 196,611 real records (3 modulo
// 65,536): each must be counted above the limit whenever archive/zip
// opens it.
func TestPreCountReviewCases(t *testing.T) {
	const n = 65537 * 3
	cd := rawCD(n, 0)
	size := uint64(len(cd))
	z64 := func(records, offset uint64) tail {
		return tail{zip64: true, z64Records: records, z64Size: size, z64Offset: offset, locTotalDisks: 1,
			eocdRecords: 0xffff, eocdSize: 0xffffffff, eocdOffset: 0xffffffff}
	}
	inner := make([]byte, eocdLen) // a second end record inside the comment
	binary.LittleEndian.PutUint32(inner, eocdSig)
	binary.LittleEndian.PutUint32(inner[16:], 0x7fffffff)
	withComment := z64(3, 0)
	withComment.comment = inner
	prefixed := z64(3, 0)
	prefixed.prefix = 4096
	cases := map[string][]byte{
		"honest zip64":        buildArchive(cd, z64(n, 0)),
		"zip64 records 3":     buildArchive(cd, z64(3, 0)),
		"zip64 offset 2^62+1": buildArchive(cd, z64(3, 1<<62+1)),
		"zip64 offset 2^63-1": buildArchive(cd, z64(3, 1<<63-1)),
		"zip64 offset 2^63":   buildArchive(cd, z64(3, 1<<63)),
		"prefix 4096":         buildArchive(cd, prefixed),
		"locator disk 1": buildArchive(cd, tail{zip64: true, z64Records: 1, z64Size: 46, locDisk: 1, locTotalDisks: 1,
			eocdRecords: 3, eocdSize: uint32(size), eocdOffset: 0xffffffff}),
		"locator two disks": buildArchive(cd, tail{zip64: true, z64Records: 1, z64Size: 46, locTotalDisks: 2,
			eocdRecords: 3, eocdSize: uint32(size)}),
		"32-bit wrong offset":   buildArchive(cd, tail{eocdRecords: 3, eocdSize: uint32(size), eocdOffset: 12345}),
		"end record in comment": buildArchive(cd, withComment),
		"big extra fields": buildArchive(rawCD(65536+3, 100), tail{eocdRecords: 3,
			eocdSize: uint32((65536 + 3) * (dirHeaderLen + 100))}),
	}
	opened := 0
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			files, count := assertPreCount(t, data, 50_000)
			if files > 50_000 {
				opened++
				if count <= 50_000 {
					t.Fatalf("archive/zip loads %d records, pre-count %d", files, count)
				}
			}
			t.Logf("archive/zip %d records, pre-count %d", files, count)
		})
	}
	if opened < 6 {
		t.Fatalf("only %d cases are opened by archive/zip: the test no longer exercises the bypasses", opened)
	}
}

// FuzzPreCountNeverBelowArchiveZip mutates the end records (32-bit, zip64,
// locator), the prefix and the comment around a small directory, and checks
// that the pre-count never falls below what archive/zip loads. Values are
// deltas from the honest ones, so the zero input is a valid archive.
func FuzzPreCountNeverBelowArchiveZip(f *testing.F) {
	f.Add(uint16(3), uint16(0), false, uint8(0), uint16(0), uint32(0), uint32(0),
		uint64(0), uint64(0), uint64(0), uint32(0), uint32(0), uint64(0), []byte(nil))
	f.Add(uint16(200), uint16(512), true, uint8(7), uint16(0), uint32(0), uint32(0),
		uint64(0), uint64(0), uint64(0), uint32(0), uint32(0), uint64(0), []byte("comment"))
	f.Add(uint16(70), uint16(0), true, uint8(7), uint16(0), uint32(0), uint32(0),
		uint64(0xfffffffffffffffd), uint64(0), uint64(1<<62+1), uint32(0), uint32(0), uint64(0), []byte(nil))
	f.Add(uint16(70), uint16(64), true, uint8(3), uint16(1), uint32(0), uint32(0),
		uint64(1), uint64(0), uint64(1<<63-1), uint32(1), uint32(0), uint64(0), []byte("PK\x05\x06"))
	f.Fuzz(func(t *testing.T, n, prefix uint16, zip64 bool, sentinels uint8, dRecords uint16,
		dSize, dOffset uint32, dZ64Records, dZ64Size, dZ64Offset uint64, locDisk, dLocDisks uint32,
		dLocOffset uint64, comment []byte) {
		records := int(n%300) + 1
		cd := rawCD(records, 0)
		pre := int(prefix % 4096)
		if len(comment) > 300 {
			comment = comment[:300]
		}
		o := tail{
			prefix: pre, zip64: zip64, comment: comment,
			eocdRecords: uint16(records) + dRecords,
			eocdSize:    uint32(len(cd)) + dSize,
			eocdOffset:  uint32(pre) + dOffset,
			z64Records:  uint64(records) + dZ64Records,
			z64Size:     uint64(len(cd)) + dZ64Size,
			z64Offset:   uint64(pre) + dZ64Offset,
			locDisk:     locDisk, locTotalDisks: 1 + dLocDisks, locOffsetDelta: dLocOffset,
		}
		if sentinels&1 != 0 {
			o.eocdRecords = 0xffff
		}
		if sentinels&2 != 0 {
			o.eocdSize = 0xffffffff
		}
		if sentinels&4 != 0 {
			o.eocdOffset = 0xffffffff
		}
		if sentinels&8 != 0 {
			o.eocdSize = 0xffff // archive/zip's zip64 trigger quirk
		}
		assertPreCount(t, buildArchive(cd, o), 1<<20)
	})
}
