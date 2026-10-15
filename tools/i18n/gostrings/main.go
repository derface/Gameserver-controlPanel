// gostrings prints the German user-facing string literals of the Go code
// (one JSON string per line), for the English dictionary.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var german = regexp.MustCompile(`[äöüÄÖÜß]|\b(der|die|das|den|dem|des|und|oder|nicht|ist|sind|ein|eine|einen|mit|für|auf|noch|wird|werden|bitte|Bitte|kein|keine|gibt|beim|vom|zum|zur|nur|schon|läuft|Spieler|Server|gestartet|gestoppt|Fehler|fehlgeschlagen|Datei|Ordner|Neustart|Starten|Stoppen|Aufgabe|Ungültig\w*|Unbekannt\w*|Zu|zu|nach|vor|bei|Sekunden|Minuten|Stunden|Tagen?)\b`)

func main() {
	seen := map[string]bool{}
	var out []string
	_ = filepath.Walk(os.Args[1], func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.Contains(p, "/tools/") {
			return nil
		}
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, p, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			bl, ok := n.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(bl.Value)
			if err != nil || len(s) < 2 || !german.MatchString(s) || strings.Contains(s, "\n#") {
				return true
			}
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
			return true
		})
		return nil
	})
	sort.Strings(out)
	for _, s := range out {
		b, _ := json.Marshal(s)
		fmt.Println(string(b))
	}
}
