package query

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

// fake A2S server: first answers with a challenge, then with an info packet.
func TestA2S(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 1400)
		for i := 0; i < 2; i++ {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n == 25 { // no challenge yet
				pc.WriteTo([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x41, 1, 2, 3, 4}, addr)
				continue
			}
			if !bytes.Equal(buf[25:29], []byte{1, 2, 3, 4}) {
				return
			}
			var b bytes.Buffer
			b.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x49, 17})
			for _, s := range []string{"Mein Server", "Midgard", "valheim", "Valheim"} {
				b.WriteString(s)
				b.WriteByte(0)
			}
			binary.Write(&b, binary.LittleEndian, uint16(0))
			b.Write([]byte{3, 10, 0})
			pc.WriteTo(b.Bytes(), addr)
		}
	}()
	r, err := A2S("127.0.0.1", pc.LocalAddr().(*net.UDPAddr).Port)
	if err != nil || r.Players != 3 || r.Max != 10 || r.Name != "Mein Server" || r.Map != "Midgard" {
		t.Fatalf("A2S = %+v, %v", r, err)
	}
}

func TestMinecraft(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := &byteReader{r: c}
		n, _ := readVarInt(br) // handshake
		c.Read(make([]byte, n))
		readVarInt(br) // status request length + id
		br.ReadByte()
		js := `{"version":{"name":"26.3"},"players":{"max":20,"online":2},"description":{"text":"§aDavids ","extra":[{"text":"Welt"},"\n§7Willkommen"]}}`
		var body bytes.Buffer
		body.WriteByte(0)
		writeVarInt(&body, len(js))
		body.WriteString(js)
		var out bytes.Buffer
		writeVarInt(&out, body.Len())
		out.Write(body.Bytes())
		c.Write(out.Bytes())
	}()
	r, err := Minecraft("127.0.0.1", ln.Addr().(*net.TCPAddr).Port)
	if err != nil || r.Players != 2 || r.Max != 20 || r.Name != "Davids Welt Willkommen" {
		t.Fatalf("Minecraft = %+v, %v", r, err)
	}
}
