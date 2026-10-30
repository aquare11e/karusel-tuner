// Package vpnconf turns the OpenVPN profile from the environment into the
// file Gluetun reads.
package vpnconf

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// Resolver returns the addresses of host, like net.Resolver.LookupNetIP.
type Resolver func(ctx context.Context, host string) ([]netip.Addr, error)

// SystemResolver looks up IPv4 addresses with the system resolver.
func SystemResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
}

// Load returns the profile from OPENVPN_CONFIG_BASE64 or, when that is
// empty, from fallbackPath (the older ./vpn/client.ovpn mount), and names
// the source for the log.
func Load(encoded, fallbackPath string) ([]byte, string, error) {
	if strings.TrimSpace(encoded) != "" {
		data, err := Decode(encoded)
		return data, "OPENVPN_CONFIG_BASE64", err
	}
	data, err := os.ReadFile(fallbackPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("no VPN profile: set OPENVPN_CONFIG_BASE64 (base64 of the .ovpn file) or provide %s", fallbackPath)
	}
	if err != nil {
		return nil, "", err
	}
	return data, fallbackPath, nil
}

// Decode reads the value of OPENVPN_CONFIG_BASE64. Whitespace is ignored so
// that wrapped GNU base64 output works; missing padding is accepted. Errors
// never echo the value: it holds keys.
func Decode(encoded string) ([]byte, error) {
	compact := strings.Join(strings.Fields(encoded), "")
	if compact == "" {
		return nil, errors.New("OPENVPN_CONFIG_BASE64 is empty")
	}
	data, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(compact)
	}
	if err != nil {
		return nil, errors.New("OPENVPN_CONFIG_BASE64 is not valid base64")
	}
	return data, nil
}

// ResolveRemotes rewrites every `remote <host> ...` directive whose host is
// not an IP address to the host's first IPv4 address: Gluetun rejects host
// names there, and its firewall blocks DNS until the tunnel is up. It
// returns how many directives it rewrote. Errors name the line, not the
// host, because the host identifies the VPN endpoint.
func ResolveRemotes(ctx context.Context, profile []byte, lookup Resolver) ([]byte, int, error) {
	lines := strings.SplitAfter(string(profile), "\n")
	remotes, resolved := 0, 0
	block := "" // inline data block such as <ca>; its lines are not directives
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if block != "" {
			if fields[0] == "</"+block+">" {
				block = ""
			}
			continue
		}
		if tag, ok := strings.CutPrefix(fields[0], "<"); ok && strings.HasSuffix(tag, ">") && !strings.HasPrefix(tag, "/") {
			// <connection> blocks hold real directives, remote included.
			if tag = strings.TrimSuffix(tag, ">"); tag != "connection" {
				block = tag
			}
			continue
		}
		if fields[0] != "remote" || len(fields) < 2 {
			continue
		}
		remotes++
		host := fields[1]
		if _, err := netip.ParseAddr(host); err == nil {
			continue
		}
		ip, err := firstIPv4(ctx, lookup, host)
		if err != nil {
			return nil, 0, fmt.Errorf("remote on line %d: %w", i+1, err)
		}
		at := strings.Index(line, "remote") + len("remote")
		lines[i] = line[:at] + strings.Replace(line[at:], host, ip.String(), 1)
		resolved++
	}
	if remotes == 0 {
		return nil, 0, errors.New("profile has no remote line")
	}
	return []byte(strings.Join(lines, "")), resolved, nil
}

func firstIPv4(ctx context.Context, lookup Resolver, host string) (netip.Addr, error) {
	addrs, err := lookup(ctx, host)
	if err != nil {
		var dnsErr *net.DNSError
		switch {
		case errors.As(err, &dnsErr):
			return netip.Addr{}, fmt.Errorf("cannot resolve host: %s", dnsErr.Err)
		case ctx.Err() != nil:
			return netip.Addr{}, fmt.Errorf("cannot resolve host: %w", ctx.Err())
		default:
			return netip.Addr{}, errors.New("cannot resolve host")
		}
	}
	for _, a := range addrs {
		if a = a.Unmap(); a.Is4() {
			return a, nil
		}
	}
	return netip.Addr{}, errors.New("host has no IPv4 address")
}

// WriteFile replaces path atomically with an owner-only file: Gluetun must
// never read a half-written profile, and the profile holds keys.
func WriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".client-*.ovpn") // mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
