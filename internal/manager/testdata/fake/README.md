# Fake-Vorlagen für den Selbsttest

`TestSelfTestDocker` braucht keinen Steam-Download. Ein Mini-Programm
(`../fakegp/main.go`) spielt SteamCMD und einen Spielserver nach, der auf
A2S antwortet. Es wird als `gamepanel/steam-runtime:7` getaggt.

Nur auf einem Testrechner ausführen, das echte Laufzeit-Image wird überschrieben:

    cd internal/manager/testdata/fakegp
    printf 'module fake\ngo 1.24\n' > go.mod
    CGO_ENABLED=0 go build -o fakegp . && rm go.mod
    printf 'FROM scratch\nCOPY fakegp /fakegp\nCOPY fakegp /opt/steamcmd/steamcmd.sh\nENTRYPOINT ["/fakegp"]\n' | docker build -t gamepanel/steam-runtime:7 -f - .
    cd ../../../.. && GP_DOCKER_TEST=1 go test ./internal/manager/ -run SelfTest -v

Danach `docker rmi gamepanel/steam-runtime:7`, das Panel baut das echte Image neu.
