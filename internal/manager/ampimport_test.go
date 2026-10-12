package manager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A fake AMP instance in AMP's layout, packed by the real export script.
func fakeAMP(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	inst := filepath.Join(root, "ProjectZomboid01")
	base := filepath.Join(inst, "project-zomboid", "380870")
	writeFile(t, filepath.Join(inst, "GenericModule.kvp"), "Meta.DisplayName=Project Zomboid\nApp.BaseDirectory=./project-zomboid/380870/\n"+
		`App.AppSettings={"PublicName":"Bytewise","adminpassword":"geheim123","$MaxUsers":"32","MaxMemory":"12288"}`+"\n"+
		`App.UpdateSources=[{"UpdateSource":4,"UpdateSourceData":"380870"}]`+"\n")
	writeFile(t, filepath.Join(inst, "metaconfig.json"), `[{"ConfigFile":"Zomboid/Server/{{servername}}.ini"}]`)
	writeFile(t, filepath.Join(base, "Zomboid", "Server", "servertest.ini"), "PublicName=Bytewise\n")
	writeFile(t, filepath.Join(base, "Zomboid", "Saves", "Multiplayer", "servertest", "map_1_1.bin"), "welt")
	writeFile(t, filepath.Join(base, "Zomboid", "Logs", "x.txt"), "log")
	writeFile(t, filepath.Join(base, ".trash", "Zomboid", "Saves", "old.bin"), "alt")
	writeFile(t, filepath.Join(base, "java", "big.jar"), "spiel")
	writeFile(t, filepath.Join(base, "server-console.txt"), "x")
	out := t.TempDir()
	cmd := exec.Command("python3", "ampexport.py", "--root", root, "--out", out, "ProjectZomboid01")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	m, _ := filepath.Glob(filepath.Join(out, "*.tar.gz"))
	if len(m) != 1 {
		t.Fatal("kein Archiv")
	}
	return m[0]
}

func TestAMPImportZomboid(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Export-Skript braucht root")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 fehlt")
	}
	m, _ := settingsMgr(t)
	f, _ := os.Open(fakeAMP(t))
	defer f.Close()
	it, err := m.ReceiveAMP(f, "ProjectZomboid01")
	if err != nil {
		t.Fatal(err)
	}
	if it.Template != "zomboid" || it.Files != 2 || it.AppID != 380870 {
		t.Fatalf("%+v", it)
	}
	if len(m.AMPImports()) != 1 {
		t.Fatal("Liste")
	}
	tpl, _ := m.tpls.Get("zomboid")
	a, err := readAMPArchive(filepath.Join(m.ampDir(), it.ID+".tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	vars := ampVars(tpl, a)
	if vars["server_name"] != "servertest" || vars["admin_password"] != "geheim123" {
		t.Fatalf("vars %v", vars)
	}
	if _, err := tpl.ResolveVars(vars); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := extractAMP(filepath.Join(m.ampDir(), it.ID+".tar.gz"), dir, tpl, 1000); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"home/Zomboid/Server/servertest.ini", "home/Zomboid/Saves/Multiplayer/servertest/map_1_1.bin"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatal(p, err)
		}
	}
	for _, p := range []string{"home/Zomboid/Logs", "server/java", "home/.trash"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Fatal("mitgenommen:", p)
		}
	}
	if err := m.DeleteAMPImport(it.ID); err != nil || len(m.AMPImports()) != 0 {
		t.Fatal("löschen", err)
	}
}

func TestAMPTargetAndVars(t *testing.T) {
	m, _ := settingsMgr(t)
	ic, _ := m.tpls.Get("amp-icarus")
	if got := ampTarget(ic, "Icarus/Saved/PlayerData/x.json"); got != "server/Icarus/Saved/PlayerData/x.json" {
		t.Fatal(got)
	}
	a := &ampArchive{settings: map[string]string{"SessionName": "Icarus der Helme", "JoinPassword": "pw", "AsyncTaskTimeout": "60.0"}}
	v := ampVars(ic, a)
	if v["session_name"] != "Icarus der Helme" || v["join_password"] != "pw" || v["startup_mode"] != "-ResumeProspect" || v["async_task_timeout"] != "60" {
		t.Fatalf("%v", v)
	}
	if _, ok := m.ampTemplate(2394010); !ok {
		t.Fatal("Palworld")
	}
	if tp, _ := m.ampTemplate(2278520); strings.HasPrefix(tp.ID, "amp-") {
		t.Fatal("eigene Vorlage bevorzugen")
	}
	if !m.AMPTokenValid(func() string { s, _ := m.NewAMPToken(); return s }()) || m.AMPTokenValid("x") {
		t.Fatal("Token")
	}
}
