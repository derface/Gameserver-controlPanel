// Package geoip looks up the rough location of an IP address in a local
// MaxMind-format database (DB-IP Lite or GeoLite2 City). Nothing leaves the
// machine: the panel downloads the database once a month and reads it from
// disk, a few hundred bytes per lookup.
package geoip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"sync"
)

// Reader reads an .mmdb file without loading it into memory.
type Reader struct {
	f          *os.File
	size       int64
	nodeCount  uint64
	recordSize int
	ipVersion  int
	nodeBytes  int64
	treeSize   int64
	dataStart  int64
	v4Start    uint64
	Meta       map[string]any

	mu    sync.Mutex
	pages map[int64][]byte
}

const pageSize = 4096

var metaMarker = []byte("\xab\xcd\xefMaxMind.com")

// Open checks the file and reads its metadata.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	r := &Reader{f: f, size: st.Size(), pages: map[int64][]byte{}}
	tail := int64(128 << 10)
	if tail > r.size {
		tail = r.size
	}
	buf := make([]byte, tail)
	if _, err := f.ReadAt(buf, r.size-tail); err != nil && err != io.EOF {
		f.Close()
		return nil, err
	}
	i := bytes.LastIndex(buf, metaMarker)
	if i < 0 {
		f.Close()
		return nil, errors.New("keine MaxMind-Datenbank")
	}
	md := &decoder{buf: buf, base: 0}
	v, _, err := md.decode(i + len(metaMarker))
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("Metadaten unlesbar: %w", err)
	}
	meta, _ := v.(map[string]any)
	r.Meta = meta
	r.nodeCount = toU64(meta["node_count"])
	r.recordSize = int(toU64(meta["record_size"]))
	r.ipVersion = int(toU64(meta["ip_version"]))
	if r.nodeCount == 0 || (r.recordSize != 24 && r.recordSize != 28 && r.recordSize != 32) {
		f.Close()
		return nil, errors.New("Datenbankformat unbekannt")
	}
	r.nodeBytes = int64(r.recordSize) * 2 / 8
	r.treeSize = int64(r.nodeCount) * r.nodeBytes
	r.dataStart = r.treeSize + 16
	if r.ipVersion == 6 {
		// IPv4 addresses live under ::/96
		n := uint64(0)
		for i := 0; i < 96 && n < r.nodeCount; i++ {
			if n, err = r.record(n, 0); err != nil {
				f.Close()
				return nil, err
			}
		}
		r.v4Start = n
	}
	return r, nil
}

func (r *Reader) Close() error { return r.f.Close() }

// readAt returns n bytes at off, through a small page cache.
func (r *Reader) readAt(off int64, n int) ([]byte, error) {
	if off < 0 || off+int64(n) > r.size {
		return nil, errors.New("außerhalb der Datei")
	}
	out := make([]byte, 0, n)
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(out) < n {
		p := (off + int64(len(out))) / pageSize * pageSize
		page, ok := r.pages[p]
		if !ok {
			sz := int64(pageSize)
			if p+sz > r.size {
				sz = r.size - p
			}
			page = make([]byte, sz)
			if _, err := r.f.ReadAt(page, p); err != nil && err != io.EOF {
				return nil, err
			}
			if len(r.pages) > 512 {
				r.pages = map[int64][]byte{}
			}
			r.pages[p] = page
		}
		start := off + int64(len(out)) - p
		take := min(int64(n-len(out)), int64(len(page))-start)
		out = append(out, page[start:start+take]...)
	}
	return out, nil
}

func (r *Reader) record(node uint64, bit int) (uint64, error) {
	b, err := r.readAt(int64(node)*r.nodeBytes, int(r.nodeBytes))
	if err != nil {
		return 0, err
	}
	switch r.recordSize {
	case 24:
		if bit == 0 {
			return uint64(b[0])<<16 | uint64(b[1])<<8 | uint64(b[2]), nil
		}
		return uint64(b[3])<<16 | uint64(b[4])<<8 | uint64(b[5]), nil
	case 28:
		if bit == 0 {
			return uint64(b[3]&0xf0)<<20 | uint64(b[0])<<16 | uint64(b[1])<<8 | uint64(b[2]), nil
		}
		return uint64(b[3]&0x0f)<<24 | uint64(b[4])<<16 | uint64(b[5])<<8 | uint64(b[6]), nil
	default:
		if bit == 0 {
			return uint64(binary.BigEndian.Uint32(b[0:4])), nil
		}
		return uint64(binary.BigEndian.Uint32(b[4:8])), nil
	}
}

// Lookup returns the raw record for ip, or nil if the database has none.
func (r *Reader) Lookup(ip net.IP) (map[string]any, error) {
	var bits []byte
	node := uint64(0)
	if v4 := ip.To4(); v4 != nil {
		bits = v4
		if r.ipVersion == 6 {
			node = r.v4Start
		}
	} else if r.ipVersion == 6 {
		bits = ip.To16()
	} else {
		return nil, nil
	}
	for i := 0; i < len(bits)*8 && node < r.nodeCount; i++ {
		bit := int(bits[i/8]>>(7-uint(i%8))) & 1
		var err error
		if node, err = r.record(node, bit); err != nil {
			return nil, err
		}
	}
	if node <= r.nodeCount {
		return nil, nil
	}
	off := int64(node-r.nodeCount) - 16
	d := &decoder{r: r, base: r.dataStart}
	v, _, err := d.decode(int(off))
	if err != nil {
		return nil, err
	}
	m, _ := v.(map[string]any)
	return m, nil
}

// decoder reads the MaxMind data format, either from buf (metadata) or
// from the file (records). Offsets are relative to base.
type decoder struct {
	r    *Reader
	buf  []byte
	base int64
}

func (d *decoder) bytes(off, n int) ([]byte, error) {
	if d.r == nil {
		if off < 0 || off+n > len(d.buf) {
			return nil, errors.New("Daten abgeschnitten")
		}
		return d.buf[off : off+n], nil
	}
	return d.r.readAt(d.base+int64(off), n)
}

func (d *decoder) decode(off int) (any, int, error) {
	return d.decodeDepth(off, 0)
}

func (d *decoder) decodeDepth(off, depth int) (any, int, error) {
	if depth > 32 {
		return nil, 0, errors.New("zu tief verschachtelt")
	}
	b, err := d.bytes(off, 1)
	if err != nil {
		return nil, 0, err
	}
	ctrl := b[0]
	off++
	typ := int(ctrl >> 5)
	if typ == 1 { // pointer
		ss, vvv := int(ctrl>>3)&3, int(ctrl&7)
		pb, err := d.bytes(off, ss+1)
		if err != nil {
			return nil, 0, err
		}
		var p int
		switch ss {
		case 0:
			p = vvv<<8 | int(pb[0])
		case 1:
			p = (vvv<<16 | int(pb[0])<<8 | int(pb[1])) + 2048
		case 2:
			p = (vvv<<24 | int(pb[0])<<16 | int(pb[1])<<8 | int(pb[2])) + 526336
		default:
			p = int(binary.BigEndian.Uint32(pb))
		}
		v, _, err := d.decodeDepth(p, depth+1)
		return v, off + ss + 1, err
	}
	if typ == 0 {
		eb, err := d.bytes(off, 1)
		if err != nil {
			return nil, 0, err
		}
		typ = 7 + int(eb[0])
		off++
	}
	size := int(ctrl & 0x1f)
	if size >= 29 {
		n := size - 28
		sb, err := d.bytes(off, n)
		if err != nil {
			return nil, 0, err
		}
		off += n
		switch n {
		case 1:
			size = 29 + int(sb[0])
		case 2:
			size = 285 + (int(sb[0])<<8 | int(sb[1]))
		default:
			size = 65821 + (int(sb[0])<<16 | int(sb[1])<<8 | int(sb[2]))
		}
	}
	switch typ {
	case 2, 4: // string, bytes
		v, err := d.bytes(off, size)
		if err != nil {
			return nil, 0, err
		}
		if typ == 2 {
			return string(v), off + size, nil
		}
		return append([]byte(nil), v...), off + size, nil
	case 3: // double
		v, err := d.bytes(off, 8)
		if err != nil {
			return nil, 0, err
		}
		return math.Float64frombits(binary.BigEndian.Uint64(v)), off + 8, nil
	case 15: // float
		v, err := d.bytes(off, 4)
		if err != nil {
			return nil, 0, err
		}
		return float64(math.Float32frombits(binary.BigEndian.Uint32(v))), off + 4, nil
	case 5, 6, 9, 10, 8: // unsigned ints, int32
		if size > 16 {
			return nil, 0, errors.New("Zahl zu lang")
		}
		v, err := d.bytes(off, size)
		if err != nil {
			return nil, 0, err
		}
		var u uint64
		for _, x := range v {
			u = u<<8 | uint64(x)
		}
		if typ == 8 {
			return int64(int32(uint32(u))), off + size, nil
		}
		return u, off + size, nil
	case 14: // bool
		return size != 0, off, nil
	case 7: // map
		m := make(map[string]any, size)
		for i := 0; i < size; i++ {
			k, next, err := d.decodeDepth(off, depth+1)
			if err != nil {
				return nil, 0, err
			}
			ks, _ := k.(string)
			v, next2, err := d.decodeDepth(next, depth+1)
			if err != nil {
				return nil, 0, err
			}
			m[ks] = v
			off = next2
		}
		return m, off, nil
	case 11: // array
		a := make([]any, 0, size)
		for i := 0; i < size; i++ {
			v, next, err := d.decodeDepth(off, depth+1)
			if err != nil {
				return nil, 0, err
			}
			a = append(a, v)
			off = next
		}
		return a, off, nil
	case 12, 13:
		return nil, off, nil
	}
	return nil, 0, fmt.Errorf("unbekannter Datentyp %d", typ)
}

func toU64(v any) uint64 {
	switch x := v.(type) {
	case uint64:
		return x
	case int64:
		return uint64(x)
	case float64:
		return uint64(x)
	}
	return 0
}
