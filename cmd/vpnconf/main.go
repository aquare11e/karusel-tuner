// Command vpnconf prepares the OpenVPN profile for Gluetun. It runs once
// before the vpn service starts: it takes the profile from
// OPENVPN_CONFIG_BASE64 (or the mounted fallback file), resolves remote host
// names to IPv4 addresses and writes the result to the volume Gluetun reads.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/aquare11e/karusel-tuner/internal/vpnconf"
)

func main() {
	fallback := flag.String("fallback", "/vpn/client.ovpn", "profile file used when OPENVPN_CONFIG_BASE64 is empty")
	out := flag.String("out", "/vpn-config/client.ovpn", "profile file Gluetun reads")
	flag.Parse()
	log.SetPrefix("vpnconf: ")

	profile, source, err := vpnconf.Load(os.Getenv("OPENVPN_CONFIG_BASE64"), *fallback)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	profile, resolved, err := vpnconf.ResolveRemotes(ctx, profile, vpnconf.SystemResolver)
	if err != nil {
		log.Fatal(err)
	}
	if err := vpnconf.WriteFile(*out, profile); err != nil {
		log.Fatal(err)
	}
	log.Printf("profile from %s written to %s (%d remote host name(s) resolved to IPv4)", source, *out, resolved)
}
