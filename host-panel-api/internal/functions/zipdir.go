package functions

import (
	"bufio"
	"encoding/binary"
	"io"
)

// archive/zip reads every central directory record into memory before the
// archive can be inspected, and only compares the declared count modulo
// 65536 with what it read: a 50 MiB zip can hold about a million minimal
// records. countDirectoryRecords walks the directory the way archive/zip
// locates it, without allocating, and stops past the limit.

const (
	eocdSig       = 0x06054b50
	eocdLen       = 22
	zip64LocSig   = 0x07064b50
	zip64LocLen   = 20
	zip64EOCDSig  = 0x06064b50
	zip64EOCDLen  = 56
	dirHeaderSig  = 0x02014b50
	dirHeaderLen  = 46
	maxEOCDSearch = 65 * 1024
)

type directoryEnd struct {
	records, size, offset uint64
	endOffset             int64 // where the (zip64) end record starts
}

// countDirectoryRecords returns an upper bound of the number of central
// directory records archive/zip will read, and whether the end of the
// directory was found. It stops counting once limit is exceeded.
//
// It walks the directory from the start archive/zip computes
// (archiveZipStarts, a line-by-line port of its readDirectoryEnd), and also
// from every other start archive/zip could use (the 32-bit end record and
// any zip64 record, whatever the disk fields of its locator say, each with
// and without a base offset), and keeps the highest count: a decoy record
// cannot lower it. FuzzPreCountNeverBelowArchiveZip checks the invariant.
func countDirectoryRecords(r io.ReaderAt, size int64, limit int) (int, bool) {
	var starts []int64
	found := false
	if s, records, ok := archiveZipStarts(r, size); ok {
		if records > uint64(limit) {
			return limit + 1, true
		}
		found = true
		starts = append(starts, s...)
	}
	if end, ok := findDirectoryEnd(r, size); ok {
		found = true
		ends := []directoryEnd{end}
		if z, ok := readZip64End(r, end.endOffset); ok {
			ends = append(ends, z)
		}
		for _, e := range ends {
			// base+offset = endOffset-size: depends on the size only
			// (archive/zip accepts any offset up to 2^63-1).
			starts = append(starts, int64(e.offset))
			if e.size <= uint64(e.endOffset) {
				starts = append(starts, e.endOffset-int64(e.size))
			}
		}
	}
	if !found {
		return 0, false
	}
	count := 0
	for _, start := range starts {
		count = max(count, walkDirectory(r, size, start, limit))
		if count > limit {
			break
		}
	}
	return count, true
}

// archiveZipStarts ports readDirectoryEnd, findDirectory64End and
// readDirectory64End of archive/zip (Go 1.27, src/archive/zip/reader.go)
// exactly, quirks included (the zip64 trigger compares the size with
// 0xffff). It returns where archive/zip starts reading the directory (two
// starts when it may reset a positive base offset to 0, which depends on a
// full header parse) and the declared record count. ok is false when
// archive/zip would refuse the archive.
func archiveZipStarts(r io.ReaderAt, size int64) (starts []int64, records uint64, ok bool) {
	var buf []byte
	var directoryEndOffset int64
	for i, bLen := range []int64{1024, 65 * 1024} {
		if bLen > size {
			bLen = size
		}
		buf = make([]byte, int(bLen))
		if _, err := r.ReadAt(buf, size-bLen); err != nil && err != io.EOF {
			return nil, 0, false
		}
		if p := findEOCDInBlock(buf); p >= 0 {
			buf = buf[p:]
			directoryEndOffset = size - bLen + int64(p)
			break
		}
		if i == 1 || bLen == size {
			return nil, 0, false
		}
	}
	le := binary.LittleEndian
	b := buf[4:] // skip signature
	records = uint64(le.Uint16(b[6:]))
	dirSize := uint64(le.Uint32(b[8:]))
	dirOffset := uint64(le.Uint32(b[12:]))
	if int(le.Uint16(b[16:])) > len(b)-18 {
		return nil, 0, false // invalid comment length
	}
	if records == 0xffff || dirSize == 0xffff || dirOffset == 0xffffffff {
		p, err := archiveZipFind64End(r, directoryEndOffset)
		if err == nil && p >= 0 {
			directoryEndOffset = p
			var z [zip64EOCDLen]byte
			if _, err := r.ReadAt(z[:], p); err != nil || le.Uint32(z[:4]) != zip64EOCDSig {
				return nil, 0, false
			}
			records, dirSize, dirOffset = le.Uint64(z[32:]), le.Uint64(z[40:]), le.Uint64(z[48:])
		}
		if err != nil {
			return nil, 0, false
		}
	}
	const maxInt64 = uint64(1<<63 - 1)
	if dirSize > maxInt64 || dirOffset > maxInt64 {
		return nil, 0, false
	}
	baseOffset := directoryEndOffset - int64(dirSize) - int64(dirOffset)
	if o := baseOffset + int64(dirOffset); o < 0 || o >= size {
		return nil, 0, false
	}
	starts = []int64{baseOffset + int64(dirOffset)}
	if baseOffset > 0 {
		starts = append(starts, int64(dirOffset))
	}
	return starts, records, true
}

// archiveZipFind64End ports findDirectory64End.
func archiveZipFind64End(r io.ReaderAt, directoryEndOffset int64) (int64, error) {
	locOffset := directoryEndOffset - zip64LocLen
	if locOffset < 0 {
		return -1, nil
	}
	var b [zip64LocLen]byte
	if _, err := r.ReadAt(b[:], locOffset); err != nil {
		return -1, err
	}
	le := binary.LittleEndian
	if le.Uint32(b[:4]) != zip64LocSig || le.Uint32(b[4:]) != 0 || le.Uint32(b[16:]) != 1 {
		return -1, nil
	}
	return int64(le.Uint64(b[8:])), nil
}

// walkDirectory counts consecutive directory headers from start, as
// archive/zip reads them (it stops at the first invalid header).
func walkDirectory(r io.ReaderAt, size, start int64, limit int) int {
	if start < 0 || start >= size {
		return 0
	}
	br := bufio.NewReader(io.NewSectionReader(r, start, size-start))
	var h [dirHeaderLen]byte
	n := 0
	for n <= limit {
		if _, err := io.ReadFull(br, h[:]); err != nil || binary.LittleEndian.Uint32(h[:4]) != dirHeaderSig {
			break
		}
		n++
		skip := int64(binary.LittleEndian.Uint16(h[28:])) + int64(binary.LittleEndian.Uint16(h[30:])) + int64(binary.LittleEndian.Uint16(h[32:]))
		if _, err := br.Discard(int(skip)); err != nil {
			break
		}
	}
	return n
}

// findDirectoryEnd locates the 32-bit end of central directory record as
// archive/zip does (last 1 KiB, then last 65 KiB).
func findDirectoryEnd(r io.ReaderAt, size int64) (directoryEnd, bool) {
	for _, blockLen := range []int64{1024, maxEOCDSearch} {
		blockLen = min(blockLen, size)
		buf := make([]byte, blockLen)
		if _, err := r.ReadAt(buf, size-blockLen); err != nil && err != io.EOF {
			return directoryEnd{}, false
		}
		p := findEOCDInBlock(buf)
		if p < 0 {
			if blockLen == size {
				return directoryEnd{}, false
			}
			continue
		}
		b := buf[p:]
		le := binary.LittleEndian
		return directoryEnd{
			records:   uint64(le.Uint16(b[10:])),
			size:      uint64(le.Uint32(b[12:])),
			offset:    uint64(le.Uint32(b[16:])),
			endOffset: size - blockLen + int64(p),
		}, true
	}
	return directoryEnd{}, false
}

func findEOCDInBlock(b []byte) int {
	for i := len(b) - eocdLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(b[i:]) == eocdSig {
			n := int(binary.LittleEndian.Uint16(b[i+eocdLen-2:]))
			if n+eocdLen+i > len(b) {
				return -1
			}
			return i
		}
	}
	return -1
}

// readZip64End reads the zip64 end record designated by the locator just
// before the 32-bit end record, if any. The locator's disk fields are not
// checked on purpose (see countDirectoryRecords).
func readZip64End(r io.ReaderAt, eocdOffset int64) (directoryEnd, bool) {
	loc := eocdOffset - zip64LocLen
	if loc < 0 {
		return directoryEnd{}, false
	}
	var l [zip64LocLen]byte
	if _, err := r.ReadAt(l[:], loc); err != nil || binary.LittleEndian.Uint32(l[:4]) != zip64LocSig {
		return directoryEnd{}, false
	}
	off := binary.LittleEndian.Uint64(l[8:])
	if off > 1<<63-1 {
		return directoryEnd{}, false
	}
	var z [zip64EOCDLen]byte
	if _, err := r.ReadAt(z[:], int64(off)); err != nil || binary.LittleEndian.Uint32(z[:4]) != zip64EOCDSig {
		return directoryEnd{}, false
	}
	return directoryEnd{
		records:   binary.LittleEndian.Uint64(z[32:]),
		size:      binary.LittleEndian.Uint64(z[40:]),
		offset:    binary.LittleEndian.Uint64(z[48:]),
		endOffset: int64(off),
	}, true
}
