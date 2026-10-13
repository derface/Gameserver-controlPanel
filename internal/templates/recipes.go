package templates

// How games load Steam Workshop items the panel downloads with SteamCMD.
// Kept apart from the (partly generated) template files.

const armaLike = `rm -f /data/.gp-ws-mods
find . -maxdepth 1 -name '@ws*' -type l -delete
mkdir -p keys
mods=""
for id in $GP_WS_IDS; do
  d="$GP_WS_DIR/$id"; [ -d "$d" ] || continue
  # the Linux server only finds lower-case file names
  find "$d" -depth -name '*[A-Z]*' | while read -r f; do
    n="$(dirname "$f")/$(basename "$f" | tr 'A-Z' 'a-z')"; [ "$f" = "$n" ] || mv -f "$f" "$n"
  done
  ln -sfn "$d" "@ws$id"
  find "$d" -iname '*.bikey' -exec cp -f {} keys/ \;
  mods="$mods@ws$id;"
done
printf '%s' "$mods" > /data/.gp-ws-mods
`

var workshopRecipes = map[string]WorkshopCfg{
	"lgsm-arma3": {App: 107410, After: armaLike,
		Note:         "Jede Mod wird als @ws<ID> neben den Server gelegt, ihre Schlüssel nach keys/ kopiert und beim Start mit -mod geladen. Arma 3 gibt den Workshop nur an Steam-Konten heraus, die das Spiel besitzen.",
		StartReplace: [][2]string{{`tr , ';')"`, `tr , ';' | sed 's/[^;]$/&;/')$GP_WS_MODS"`}}},
	"lgsm-dayz": {App: 221100, After: armaLike,
		Note:         "Jede Mod wird als @ws<ID> neben den Server gelegt, ihre Schlüssel nach keys/ kopiert und beim Start mit -mod geladen. DayZ gibt den Workshop nur an Steam-Konten heraus, die das Spiel besitzen.",
		StartReplace: [][2]string{{"-mod= ", `-mod="$GP_WS_MODS" `}}},
	"lgsm-sb": {App: 211820,
		After: `mkdir -p mods
find mods -maxdepth 1 -name 'ws-*.pak' -type l -delete
for id in $GP_WS_IDS; do for f in "$GP_WS_DIR/$id"/*.pak; do [ -f "$f" ] && ln -sfn "$f" "mods/ws-$id-$(basename "$f")"; done; done
`,
		Note: "Die .pak-Dateien der Mods werden in den Ordner mods verlinkt."},
	"gmod": {App: 4000,
		After: `mkdir -p garrysmod/addons garrysmod/lua/autorun/server
find garrysmod/addons -maxdepth 1 -name 'ws-*.gma' -type l -delete
: > garrysmod/lua/autorun/server/gp_workshop.lua
for id in $GP_WS_IDS; do
  for f in "$GP_WS_DIR/$id"/*.gma; do [ -f "$f" ] && ln -sfn "$f" "garrysmod/addons/ws-$id.gma"; done
  echo "resource.AddWorkshop(\"$id\")" >> garrysmod/lua/autorun/server/gp_workshop.lua
done
`,
		Note: "Die Addons werden nach garrysmod/addons verlinkt, und Spieler laden sie beim Beitreten automatisch (resource.AddWorkshop)."},
	"amp-conan-exiles": {App: 440900,
		After: `mkdir -p ConanSandbox/Mods
: > ConanSandbox/Mods/modlist.txt
for id in $GP_WS_IDS; do for f in "$GP_WS_DIR/$id"/*.pak; do [ -f "$f" ] && echo "Z:${f//\//\\}" >> ConanSandbox/Mods/modlist.txt; done; done
`,
		Note: "Die Mods werden in ConanSandbox/Mods/modlist.txt eingetragen (in der Reihenfolge der Liste). Eigene Einträge dort überschreibt das Panel."},
}
