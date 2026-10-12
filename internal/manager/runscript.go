package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// runScriptHash identifies the start script version; containers made with
// another version are recreated on their next start.
var runScriptHash = func() string {
	h := sha256.Sum256([]byte(steamRunScript))
	return hex.EncodeToString(h[:])[:12]
}()

func runScriptPath(dataDir string) string { return filepath.Join(dataDir, "runtime", "gp-run") }

// writeRunScript puts the current start script where the containers mount it.
func writeRunScript(dataDir string) error {
	p := runScriptPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if b, err := os.ReadFile(p); err == nil && string(b) == steamRunScript {
		return nil
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(steamRunScript), 0o755); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o755)
	return os.Rename(tmp, p)
}

// requestUpdate lets the next start of a Steam server look for game
// updates. Restarts by Docker after a crash do not, see gp-run.
func (m *Manager) requestUpdate(id string) {
	sv, ok := m.st.Server(id)
	if !ok {
		return
	}
	if tpl, ok := m.tpls.Get(sv.Template); !ok || tpl.Kind != "steam" {
		return
	}
	dir := m.ServerDir(id)
	if _, err := os.Stat(dir); err != nil {
		return
	}
	p := filepath.Join(dir, ".gp-update-now")
	if os.WriteFile(p, nil, 0o666) == nil {
		_ = os.Chown(p, 1000, 1000)
	}
}
