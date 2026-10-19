package manager

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/ai"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestRedact(t *testing.T) {
	in := "join 192.168.178.23 steam 76561198012345678 pw geheim123 mail a@b.de token " + strings.Repeat("a1", 20) + " bind 0.0.0.0"
	out := redact(in, []string{"geheim123"})
	for _, bad := range []string{"192.168", "7656119801", "geheim123", "a@b.de", "a1a1a1a1a1"} {
		if strings.Contains(out, bad) {
			t.Fatalf("%q in %q", bad, out)
		}
	}
	if !strings.Contains(out, "0.0.0.0") {
		t.Fatal(out)
	}
}

func TestLogExcerpt(t *testing.T) {
	var lines []string
	for i := 0; i < 300; i++ {
		lines = append(lines, "WARN : Sprite duplicate texture d_x_"+string(rune('0'+i%10)))
		lines = append(lines, "LOG  : General loading "+string(rune('0'+i%10)))
	}
	lines = append(lines, "ERROR: Mod CompanionDogs failed to load item 17", "ERROR: Mod CompanionDogs failed to load item 18", "./start-server.sh: line 18:    35 Killed  ./ProjectZomboid64")
	out, bad := logExcerpt(lines, nil)
	if strings.Contains(out, "duplicate texture") {
		t.Fatal("Rauschen drin")
	}
	if !strings.Contains(out, "(×2)") || !strings.Contains(out, "Killed") {
		t.Fatal(out)
	}
	if len(bad) != 2 {
		t.Fatalf("%v", bad)
	}
}

func TestSustained(t *testing.T) {
	var s []loadSample
	now := time.Now()
	for i := 0; i < 20; i++ {
		s = append(s, loadSample{T: now.Add(time.Duration(i-20) * time.Minute), MemMB: 5900, LimitMB: 6144, CPU: 40})
	}
	if got := sustained(s, now.Add(-time.Hour).Format(time.RFC3339Nano)); !strings.Contains(got, "RAM") {
		t.Fatal(got)
	}
	if got := sustained(s, now.Add(-5*time.Minute).Format(time.RFC3339Nano)); got != "" {
		t.Fatal("über einen Neustart hinweg", got)
	}
	s[10].MemMB = 3000
	if got := sustained(s, ""); got != "" {
		t.Fatal(got)
	}
}

func TestDiagnoseWithFakeAI(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		asked = req.Messages[1].Content
		ans := `{"severity":"kritisch","summary":"Zu wenig RAM.","findings":[{"title":"RAM","detail":"Killed"}],
		"actions":[{"title":"Mehr RAM","why":"x","kind":"memory","memory_mb":10000},{"title":"Name","why":"x","kind":"var","key":"server_name","value":"neu"},
		{"title":"PW","why":"x","kind":"var","key":"admin_password","value":"x"},{"title":"Neustart","why":"x","kind":"restart"}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": ans}})
	}))
	defer srv.Close()
	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Settings.AIProvider, d.Settings.OllamaURL = "ollama", srv.URL
		d.Servers = append(d.Servers, store.Server{ID: "pz", Name: "Zomboid", Template: "zomboid", MemoryMB: 6144,
			Vars: map[string]string{"server_name": "servertest", "admin_password": "supergeheim"}})
		return nil
	})
	m.Event("pz", "warn", "Server wurde vom System beendet (oft zu wenig RAM) und wird automatisch neu gestartet.")
	d, err := m.Diagnose(context.Background(), "pz", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(asked, "supergeheim") || !strings.Contains(asked, "server_name") || !strings.Contains(asked, "vom System beendet") {
		t.Fatalf("Anfrage:\n%s", asked)
	}
	if d.Severity != "kritisch" || len(d.Actions) != 4 {
		t.Fatalf("%+v", d)
	}
	if a := d.Actions[0]; a.Kind != "memory" || a.MemoryMB%256 != 0 {
		t.Fatalf("%+v", a)
	}
	if d.Actions[1].Kind != "var" || d.Actions[2].Kind != "manual" {
		t.Fatalf("Passwort-Vorschlag nicht entschärft: %+v", d.Actions)
	}
	if got := m.Diagnoses("pz"); len(got) != 1 || got[0].Summary != "Zu wenig RAM." {
		t.Fatalf("%+v", got)
	}
	var _ ai.Diagnosis = d
}

func TestLogExcerptHarmless(t *testing.T) {
	lines := []string{
		"[ecss] Could not add component 'Gravity' to entity 12",
		"[ecss] Could not add component 'Gravity' to entity 13",
		"[tracking] Post Request returned an HTTP error: 502 Bad Gateway",
		"fixme:ntdll:NtQuerySystemInformation info_class SYSTEM_PERFORMANCE_INFORMATION",
		"[Session] 'HostOnline' (up)!",
		"ERROR: failed to load savegame",
	}
	out, bad := logExcerpt(lines, nil, regexp.MustCompile(`\[ecss\] Could not add component`), regexp.MustCompile(`\[tracking\]`))
	if len(bad) != 1 || !strings.Contains(bad[0], "savegame") {
		t.Fatalf("%v", bad)
	}
	if !strings.Contains(out, "[Bekannt harmlos") || !strings.Contains(out, "(×2)") {
		t.Fatal(out)
	}
}
