package query

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// raknetMagic is the "offline message" marker of RakNet.
var raknetMagic = []byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}

// Bedrock sends a RakNet unconnected ping (Minecraft Bedrock, UDP). The pong
// carries "MCPE;motd;protocol;version;players;max;…".
func Bedrock(host string, port int) (Result, error) {
	conn, err := net.DialTimeout("udp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close()
	var req bytes.Buffer
	req.WriteByte(0x01)
	_ = binary.Write(&req, binary.BigEndian, time.Now().UnixMilli())
	req.Write(raknetMagic)
	_ = binary.Write(&req, binary.BigEndian, uint64(0x6761_6d65_7061_6e6c)) // client GUID
	buf := make([]byte, 1500)
	for attempt := 0; attempt < 3; attempt++ {
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if _, err := conn.Write(req.Bytes()); err != nil {
			return Result{}, err
		}
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return Result{}, err
		}
		return parsePong(buf[:n])
	}
	return Result{}, errors.New("bedrock: keine Antwort")
}

func parsePong(b []byte) (Result, error) {
	// 0x1c, time(8), server GUID(8), magic(16), length(2), text
	if len(b) < 35 || b[0] != 0x1c || !bytes.Equal(b[17:33], raknetMagic) {
		return Result{}, errors.New("bedrock: unerwartete Antwort")
	}
	l := int(binary.BigEndian.Uint16(b[33:35]))
	if 35+l > len(b) {
		return Result{}, errors.New("bedrock: Antwort zu kurz")
	}
	f := strings.Split(string(b[35:35+l]), ";")
	if len(f) < 6 {
		return Result{}, errors.New("bedrock: unvollständige Antwort")
	}
	p, err1 := strconv.Atoi(f[4])
	mx, err2 := strconv.Atoi(f[5])
	if err1 != nil || err2 != nil {
		return Result{}, errors.New("bedrock: Spielerzahl unlesbar")
	}
	return Result{Players: p, Max: mx}, nil
}

// Ask runs the query of the given type ("a2s", "minecraft", "bedrock").
func Ask(typ, host string, port int) (Result, error) {
	switch typ {
	case "a2s":
		return A2S(host, port)
	case "minecraft":
		return Minecraft(host, port)
	case "bedrock":
		return Bedrock(host, port)
	}
	return Result{}, errors.New("unbekannte Abfrageart")
}
