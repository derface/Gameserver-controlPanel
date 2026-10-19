// Package query asks running game servers how many players are online.
// Supported: Steam A2S_INFO (Source engine query over UDP) and the
// Minecraft Java server list ping (TCP).
package query

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"
)

type Result struct {
	Players int
	Max     int
	Name    string // server name as the game shows it in its server list
	Map     string
}

const timeout = 1500 * time.Millisecond

// A2S sends A2S_INFO to host:port and handles the challenge handshake.
func A2S(host string, port int) (Result, error) {
	conn, err := net.DialTimeout("udp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close()
	req := append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x54}, []byte("Source Engine Query\x00")...)
	buf := make([]byte, 1400)
	for attempt := 0; attempt < 3; attempt++ {
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if _, err := conn.Write(req); err != nil {
			return Result{}, err
		}
		n, err := conn.Read(buf)
		if err != nil {
			return Result{}, err
		}
		if n < 5 {
			return Result{}, errors.New("a2s: kurze Antwort")
		}
		switch buf[4] {
		case 0x41: // challenge
			if n < 9 {
				return Result{}, errors.New("a2s: challenge ungültig")
			}
			req = append(req[:25:25], buf[5:9]...)
		case 0x49:
			return parseInfo(buf[5:n])
		default:
			return Result{}, fmt.Errorf("a2s: unerwarteter Typ 0x%x", buf[4])
		}
	}
	return Result{}, errors.New("a2s: keine Antwort")
}

func parseInfo(b []byte) (Result, error) {
	r := bytes.NewReader(b)
	if _, err := r.ReadByte(); err != nil { // protocol
		return Result{}, err
	}
	name, err := readString(r)
	if err != nil {
		return Result{}, err
	}
	mapName, err := readString(r)
	if err != nil {
		return Result{}, err
	}
	for i := 0; i < 2; i++ { // folder, game
		if err := skipString(r); err != nil {
			return Result{}, err
		}
	}
	var id uint16
	if err := binary.Read(r, binary.LittleEndian, &id); err != nil {
		return Result{}, err
	}
	players, err1 := r.ReadByte()
	max, err2 := r.ReadByte()
	if err1 != nil || err2 != nil {
		return Result{}, errors.New("a2s: Spielerzahl fehlt")
	}
	return Result{Players: int(players), Max: int(max), Name: CleanName(name), Map: CleanName(mapName)}, nil
}

func readString(r *bytes.Reader) (string, error) {
	var b []byte
	for {
		c, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if c == 0 {
			return string(b), nil
		}
		if len(b) < 512 {
			b = append(b, c)
		}
	}
}

var reColor = regexp.MustCompile(`§.|\^[0-9]|<color[^>]*>|</color>|<[biu]>|</[biu]>`)

// CleanName removes colour codes and control characters and keeps one
// line of at most 100 characters.
func CleanName(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = reColor.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(strings.Map(func(c rune) rune {
		if c < 32 || c == 127 {
			return ' '
		}
		return c
	}, s)), " ")
	if r := []rune(s); len(r) > 100 {
		s = string(r[:100])
	}
	return s
}

func skipString(r *bytes.Reader) error {
	for {
		c, err := r.ReadByte()
		if err != nil {
			return err
		}
		if c == 0 {
			return nil
		}
	}
}

// Minecraft performs the Java edition server list ping.
func Minecraft(host string, port int) (Result, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * timeout))

	var hs bytes.Buffer
	hs.WriteByte(0x00)
	writeVarInt(&hs, 767)
	writeVarInt(&hs, len(host))
	hs.WriteString(host)
	_ = binary.Write(&hs, binary.BigEndian, uint16(port))
	writeVarInt(&hs, 1)
	var pkt bytes.Buffer
	writeVarInt(&pkt, hs.Len())
	pkt.Write(hs.Bytes())
	pkt.Write([]byte{0x01, 0x00}) // status request
	if _, err := conn.Write(pkt.Bytes()); err != nil {
		return Result{}, err
	}
	br := &byteReader{r: conn}
	if _, err := readVarInt(br); err != nil { // packet length
		return Result{}, err
	}
	if id, err := readVarInt(br); err != nil || id != 0 {
		return Result{}, errors.New("minecraft: unerwartete Antwort")
	}
	n, err := readVarInt(br)
	if err != nil || n <= 0 || n > 1<<20 {
		return Result{}, errors.New("minecraft: ungültige Länge")
	}
	js := make([]byte, n)
	if _, err := io.ReadFull(conn, js); err != nil {
		return Result{}, err
	}
	var st struct {
		Players struct {
			Online int `json:"online"`
			Max    int `json:"max"`
		} `json:"players"`
		Description json.RawMessage `json:"description"`
	}
	if err := json.Unmarshal(js, &st); err != nil {
		return Result{}, err
	}
	return Result{Players: st.Players.Online, Max: st.Players.Max, Name: CleanName(chatText(st.Description))}, nil
}

// chatText flattens a Minecraft text component (string, object with
// text/extra, or array) to plain text.
func chatText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		var b strings.Builder
		for _, x := range arr {
			b.WriteString(chatText(x))
		}
		return b.String()
	}
	var obj struct {
		Text  string            `json:"text"`
		Extra []json.RawMessage `json:"extra"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		b := obj.Text
		for _, x := range obj.Extra {
			b += chatText(x)
		}
		return b
	}
	return ""
}

type byteReader struct{ r io.Reader }

func (b *byteReader) ReadByte() (byte, error) {
	var x [1]byte
	_, err := io.ReadFull(b.r, x[:])
	return x[0], err
}

func writeVarInt(w *bytes.Buffer, v int) {
	u := uint32(v)
	for {
		if u&^0x7F == 0 {
			w.WriteByte(byte(u))
			return
		}
		w.WriteByte(byte(u&0x7F | 0x80))
		u >>= 7
	}
}

func readVarInt(r io.ByteReader) (int, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint32(c&0x7F) << (7 * i)
		if c&0x80 == 0 {
			return int(int32(v)), nil
		}
	}
	return 0, errors.New("varint zu lang")
}
