package query

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

func TestA2SPlayers(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 100)
		for i := 0; i < 2; i++ {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n == 9 && bytes.Equal(buf[5:9], []byte{0xFF, 0xFF, 0xFF, 0xFF}) {
				_, _ = pc.WriteTo([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x41, 1, 2, 3, 4}, addr)
				continue
			}
			var b bytes.Buffer
			b.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x44, 2})
			for _, p := range []struct {
				n string
				s int32
				d float32
			}{{"Anna", 5, 120.5}, {"Bob", 0, 3600}} {
				b.WriteByte(0)
				b.WriteString(p.n + "\x00")
				_ = binary.Write(&b, binary.LittleEndian, p.s)
				_ = binary.Write(&b, binary.LittleEndian, p.d)
			}
			_, _ = pc.WriteTo(b.Bytes(), addr)
		}
	}()
	ps, err := A2SPlayers("127.0.0.1", pc.LocalAddr().(*net.UDPAddr).Port)
	if err != nil || len(ps) != 2 || ps[0].Name != "Anna" || ps[1].Seconds != 3600 {
		t.Fatalf("%v %+v", err, ps)
	}
}
