package query

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

func TestBedrock(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 1500)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil || n != 33 || buf[0] != 0x01 || !bytes.Equal(buf[9:25], raknetMagic) {
			return
		}
		txt := "MCPE;Mein Bedrock-Server;712;1.21.40;2;10;123456;Bedrock level;Survival;1;19132;19133;"
		var b bytes.Buffer
		b.WriteByte(0x1c)
		b.Write(buf[1:9])
		binary.Write(&b, binary.BigEndian, uint64(42))
		b.Write(raknetMagic)
		binary.Write(&b, binary.BigEndian, uint16(len(txt)))
		b.WriteString(txt)
		pc.WriteTo(b.Bytes(), addr)
	}()
	r, err := Ask("bedrock", "127.0.0.1", pc.LocalAddr().(*net.UDPAddr).Port)
	if err != nil || r.Players != 2 || r.Max != 10 {
		t.Fatalf("Bedrock = %+v, %v", r, err)
	}
	if _, err := parsePong([]byte{0x1c, 1, 2}); err == nil {
		t.Fatal("kurze Antwort angenommen")
	}
}
