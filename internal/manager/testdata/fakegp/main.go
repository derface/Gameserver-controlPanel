// fake steam runtime for testing the self-test runner
package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// steamLogin plays SteamCMD's login for the Steam account tests:
// "+login user pass [code] +quit"
func steamLogin(args []string) {
	fmt.Println("Redirecting stderr to '/home/steam/Steam/logs/stderr.txt'")
	fmt.Println("Loading Steam API...OK")
	if len(args) < 3 {
		fmt.Println("usage")
		os.Exit(1)
	}
	user, pass := args[1], args[2]
	fmt.Printf("Logging in user '%s' to Steam Public...\n", user)
	switch {
	case pass != "richtig":
		fmt.Println("FAILED (Invalid Password)")
		os.Exit(5)
	case len(args) > 4 && args[3] == "AB12C":
		os.WriteFile("/home/steam/Steam/config.vdf", []byte("cached "+user), 0o600)
		fmt.Println("Waiting for client config...OK")
		fmt.Println("Waiting for user info...OK")
	case len(args) > 4 && args[3] != "+quit":
		fmt.Println("FAILED (Invalid Login Auth Code)")
		os.Exit(5)
	default:
		fmt.Println("This computer has not been authenticated for your account using Steam Guard.")
		fmt.Println("Please check your email for the message from Steam, and enter the Steam Guard")
		fmt.Print(" code from that message.\nSteam Guard code:")
		select {}
	}
}

func main() {
	if strings.HasSuffix(os.Args[0], "steamcmd.sh") {
		steamLogin(os.Args[1:])
		return
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sig; os.Exit(0) }()
	start := os.Getenv("GP_START")
	f := strings.Fields(start)
	mode, port := "ok", "0"
	if len(f) > 1 {
		mode = f[1]
	}
	if len(f) > 2 {
		port = f[2]
	}
	fmt.Println("[panel] SteamCMD: App", os.Getenv("GP_APP_ID"))
	for _, p := range []string{"10.00", "55.50", "99.00"} {
		fmt.Printf(" Update state (0x61) downloading, progress: %s (1 / 2)\n", p)
		time.Sleep(1500 * time.Millisecond)
	}
	if mode == "installfail" {
		fmt.Println("[panel] Installation fehlgeschlagen")
		os.Exit(1)
	}
	fmt.Println("Success! App fully installed.")
	fmt.Println("[panel] Start:", start)
	time.Sleep(2 * time.Second)
	switch mode {
	case "crash":
		fmt.Println("Segmentation fault")
		os.Exit(3)
	case "noready":
		select {}
	case "noquery":
		fmt.Println("Fake ready")
		select {}
	}
	fmt.Println("Fake ready")
	pc, err := net.ListenPacket("udp", ":"+port)
	if err != nil {
		fmt.Println("listen:", err)
		os.Exit(2)
	}
	if mode == "hang" { // answers for 6 s, then stops answering but keeps running
		go func() { time.Sleep(6 * time.Second); pc.Close() }()
	}
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil && mode == "hang" {
			select {}
		}
		if err != nil || n < 5 {
			continue
		}
		resp := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x49, 17}
		for _, s := range []string{"Fake", "map", "fake", "Fake"} {
			resp = append(append(resp, s...), 0)
		}
		resp = append(resp, 0, 0, 3, 16)
		pc.WriteTo(resp, addr)
	}
}
