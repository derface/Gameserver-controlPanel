package query

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"time"
)

// Player is one entry of the A2S_PLAYER list.
type Player struct {
	Name    string  `json:"name"`
	Score   int     `json:"score"`
	Seconds float64 `json:"seconds"`
}

// A2SPlayers asks for the player list (A2S_PLAYER with challenge).
func A2SPlayers(host string, port int) ([]Player, error) {
	conn, err := net.DialTimeout("udp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	req := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x55, 0xFF, 0xFF, 0xFF, 0xFF}
	buf := make([]byte, 4096)
	for attempt := 0; attempt < 3; attempt++ {
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if _, err := conn.Write(req); err != nil {
			return nil, err
		}
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		if n < 5 {
			return nil, errors.New("a2s: kurze Antwort")
		}
		switch buf[4] {
		case 0x41:
			if n < 9 {
				return nil, errors.New("a2s: challenge ungültig")
			}
			req = append(req[:5:5], buf[5:9]...)
		case 0x44:
			return parsePlayers(buf[5:n])
		default:
			return nil, fmt.Errorf("a2s: unerwarteter Typ 0x%x", buf[4])
		}
	}
	return nil, errors.New("a2s: keine Antwort")
}

func parsePlayers(b []byte) ([]Player, error) {
	if len(b) < 1 {
		return nil, errors.New("a2s: leere Spielerliste")
	}
	r := bytes.NewReader(b[1:])
	var out []Player
	for i := 0; i < int(b[0]) && r.Len() > 0; i++ {
		if _, err := r.ReadByte(); err != nil { // index
			break
		}
		name, err := readCString(r)
		if err != nil {
			break
		}
		var score int32
		var dur float32
		if binary.Read(r, binary.LittleEndian, &score) != nil || binary.Read(r, binary.LittleEndian, &dur) != nil {
			break
		}
		if math.IsNaN(float64(dur)) || dur < 0 {
			dur = 0
		}
		out = append(out, Player{Name: name, Score: int(score), Seconds: float64(dur)})
	}
	return out, nil
}

func readCString(r *bytes.Reader) (string, error) {
	var b []byte
	for {
		c, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if c == 0 {
			return string(b), nil
		}
		b = append(b, c)
	}
}
