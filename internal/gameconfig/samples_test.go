package gameconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// formatFor guesses the format of a sample file.
func formatFor(name string, data []byte) string {
	low := strings.ToLower(name)
	switch {
	case bytes.Contains(data, []byte("OptionSettings=(")):
		return "palworld"
	case strings.HasSuffix(low, ".json"):
		return "json"
	case strings.HasSuffix(low, ".xml") && bytes.Contains(data, []byte("<property name=")):
		return "xmlprop"
	case strings.HasSuffix(low, ".ini"):
		if bytes.Contains(data, []byte("\n[")) || bytes.HasPrefix(data, []byte("[")) {
			return "ini"
		}
		return "properties"
	case strings.HasSuffix(low, ".properties"):
		return "properties"
	case strings.HasSuffix(low, ".cfg") && (bytes.Contains(data, []byte("\n[")) || bytes.HasPrefix(data, []byte("["))):
		return "ini"
	case strings.HasSuffix(low, ".cfg") && (strings.Contains(low, "arma") || strings.Contains(low, "dayz")):
		return "armacfg"
	case strings.HasSuffix(low, ".cfg"):
		return "cvar"
	}
	return ""
}

// roundTrip: every listed value reads back the same, and writing it again
// leaves the file byte for byte unchanged.
func roundTrip(t *testing.T, name, format string, data []byte) int {
	t.Helper()
	es, err := List(format, data)
	if err != nil {
		t.Errorf("%s (%s): %v", name, format, err)
		return 0
	}
	for _, e := range es {
		v, ok, err := Get(format, data, e.Key)
		if err != nil || !ok || v != e.Value {
			t.Errorf("%s: Get(%s) = %q %v %v, List sagt %q", name, e.Key, v, ok, err, e.Value)
			continue
		}
		out, err := SetKinds(format, data, map[string]string{e.Key: e.Value}, []string{e.Key}, map[string]string{e.Key: e.Kind})
		if err != nil {
			t.Errorf("%s: Set(%s): %v", name, e.Key, err)
			continue
		}
		if format == "json" { // JSON is written back normalised; values must survive
			if v2, _, _ := Get(format, out, e.Key); v2 != e.Value {
				t.Errorf("%s: %s nach Set %q", name, e.Key, v2)
			}
			continue
		}
		if !bytes.Equal(out, data) {
			t.Errorf("%s: Set(%s=%q) ändert die Datei", name, e.Key, e.Value)
		}
	}
	return len(es)
}

var placeholder = regexp.MustCompile(`\{\{[A-Za-z0-9_$.]+\}\}`)

func TestSamples(t *testing.T) {
	files, _ := filepath.Glob("testdata/samples/*")
	if dir := os.Getenv("GP_CONFIG_SAMPLES"); dir != "" {
		more, _ := filepath.Glob(filepath.Join(dir, "*"))
		files = append(files, more...)
	}
	total, n := 0, 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		low := strings.ToLower(filepath.Base(f))
		if strings.HasPrefix(low, "amp") || strings.HasSuffix(low, "config.json") || strings.HasSuffix(low, "ports.json") ||
			strings.HasSuffix(low, "updates.json") || strings.HasSuffix(low, "start.json") || strings.HasSuffix(low, "downloadurl.json") {
			continue // AMP's own description files, not game configs
		}
		// AMP's samples still contain placeholders like {{hostname}}
		data = placeholder.ReplaceAllFunc(data, func(m []byte) []byte {
			if bytes.Contains(m, []byte("pass")) || bytes.Contains(m, []byte("name")) {
				return []byte("Mein Server")
			}
			return []byte("5")
		})
		format := formatFor(f, data)
		if format == "" {
			continue
		}
		if format == "json" {
			if _, err := parseOrdered(data); err != nil {
				continue // some templates contain placeholders that are not JSON yet
			}
		}
		if os.Getenv("GP_SAMPLES_VERBOSE") != "" {
			t.Logf("%s %s %d", filepath.Base(f), format, len(data))
		}
		total += roundTrip(t, filepath.Base(f), format, data)
		n++
	}
	t.Logf("%d Dateien, %d Werte", n, total)
}
