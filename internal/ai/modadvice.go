package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// ModAdviceInput describes a downloaded Workshop item the panel does not
// know how to load.
type ModAdviceInput struct {
	Game        string
	Template    string
	Start       string
	ModTitle    string
	ModDesc     string
	Files       []string // relative to the item folder
	ServerFiles []string // top of the server folder
	ItemDir     string   // where the item lies inside the container
}

type ModAdvice struct {
	Summary string   `json:"summary"`
	Steps   []string `json:"steps"`
	Sure    bool     `json:"sure"`
}

const adviceSystem = `Du hilfst in einem Gameserver-Panel. Ein Steam-Workshop-Inhalt wurde mit SteamCMD für einen Dedicated Server heruntergeladen, aber das Panel weiß nicht, wie dieses Spiel Mods lädt.
Antworte ausschließlich mit JSON:
{"summary":"ein bis zwei Sätze: wie lädt der Server diese Mod","steps":["konkreter Schritt", "..."],"sure":true|false}
Regeln:
- Stütze dich auf die Dateiliste und dein Wissen über das Spiel. Wenn du es nicht sicher weißt, sure=false und sag, was man prüfen sollte.
- steps beziehen sich auf Dateien im Server-Ordner (Reiter „Dateien“) oder Startwerte. Höchstens 6 Schritte, Deutsch, sachlich.
- Der Server-Ordner liegt im Container unter /data/server, die Mod im genannten Ordner; Pfade in den Schritten bitte relativ zum Server-Ordner angeben.
- Lädt der Server Workshop-Inhalte ohnehin selbst (eigene Konfig-Liste), sag das und nenne die Einstellung.`

// ModAdvise asks the provider how a game loads a Workshop item.
func ModAdvise(ctx context.Context, s store.Settings, in ModAdviceInput) (ModAdvice, error) {
	var a ModAdvice
	var b strings.Builder
	b.WriteString("Spiel: " + in.Game + " (Vorlage " + in.Template + ")\n")
	if in.Start != "" {
		b.WriteString("Startbefehl: " + in.Start + "\n")
	}
	b.WriteString("Mod: " + in.ModTitle + "\n")
	if in.ModDesc != "" {
		b.WriteString("Beschreibung: " + in.ModDesc + "\n")
	}
	b.WriteString("Ordner der Mod im Container: " + in.ItemDir + "\n\nDateien der Mod:\n")
	for _, f := range in.Files {
		b.WriteString("- " + f + "\n")
	}
	if len(in.ServerFiles) > 0 {
		b.WriteString("\nOberste Ebene des Server-Ordners (/data/server):\n")
		for _, f := range in.ServerFiles {
			b.WriteString("- " + f + "\n")
		}
	}
	txt, err := completeN(ctx, s, adviceSystem, b.String(), 1200)
	if err != nil {
		return a, err
	}
	i, j := strings.Index(txt, "{"), strings.LastIndex(txt, "}")
	if i < 0 || j <= i {
		return a, errors.New("Die KI hat keinen Vorschlag geliefert. Bitte erneut versuchen")
	}
	if err := json.Unmarshal([]byte(txt[i:j+1]), &a); err != nil {
		return a, errors.New("Die Antwort der KI war unlesbar. Bitte erneut versuchen")
	}
	if len(a.Steps) > 6 {
		a.Steps = a.Steps[:6]
	}
	if a.Steps == nil {
		a.Steps = []string{}
	}
	return a, nil
}
