package vpnconf

import (
	"context"
	"encoding/base64"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLookup answers like net.Resolver.LookupNetIP for a fixed table.
func fakeLookup(table map[string][]string) Resolver {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		addrs, ok := table[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		var out []netip.Addr
		for _, a := range addrs {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

func TestResolveRemotes(t *testing.T) {
	lookup := fakeLookup(map[string][]string{
		"vpn.example.net":   {"203.0.113.7"},
		"multi.example.net": {"2001:db8::1", "203.0.113.8", "203.0.113.9"},
		"v6.example.net":    {"2001:db8::2"},
	})
	cases := []struct {
		name     string
		in       string
		want     string
		resolved int
		wantErr  string
	}{
		{
			name:     "host name replaced, port and proto kept",
			in:       "client\nremote vpn.example.net 1194 udp\ndev tun\n",
			want:     "client\nremote 203.0.113.7 1194 udp\ndev tun\n",
			resolved: 1,
		},
		{
			name: "ip literal untouched",
			in:   "remote 198.51.100.2 1194\n",
			want: "remote 198.51.100.2 1194\n",
		},
		{
			name:     "comments and inline blocks ignored",
			in:       "# remote old.example.net 1\n<ca>\nremote not-a-directive\n</ca>\nremote vpn.example.net 9002\n",
			want:     "# remote old.example.net 1\n<ca>\nremote not-a-directive\n</ca>\nremote 203.0.113.7 9002\n",
			resolved: 1,
		},
		{
			name:     "first IPv4 address wins",
			in:       "remote multi.example.net 1194\n",
			want:     "remote 203.0.113.8 1194\n",
			resolved: 1,
		},
		{
			name:     "CRLF and indentation preserved",
			in:       "client\r\n  remote vpn.example.net 1194\r\n",
			want:     "client\r\n  remote 203.0.113.7 1194\r\n",
			resolved: 1,
		},
		{
			name:    "no remote line",
			in:      "client\ndev tun\n",
			wantErr: "no remote",
		},
		{
			name:    "unknown host",
			in:      "client\nremote missing.example.net 1194\n",
			wantErr: "line 2",
		},
		{
			name:    "IPv6 only",
			in:      "remote v6.example.net 1194\n",
			wantErr: "no IPv4 address",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, resolved, err := ResolveRemotes(context.Background(), []byte(tc.in), lookup)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				// Host names identify the VPN endpoint; they stay out of logs.
				if strings.Contains(err.Error(), ".example.net") {
					t.Fatalf("error leaks the host name: %v", err)
				}
				return
			}
			if err != nil || string(got) != tc.want || resolved != tc.resolved {
				t.Fatalf("got %q (resolved %d, err %v), want %q (resolved %d)", got, resolved, err, tc.want, tc.resolved)
			}
		})
	}
}

const sampleProfile = "client\nremote vpn.example.net 1194 udp\n<ca>\nMIIB\n</ca>\n"

func TestDecode(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte(sampleProfile))
	wrapped := enc[:10] + "\n" + enc[10:20] + "\r\n " + enc[20:] + "\n"
	for name, in := range map[string]string{"single line": enc, "wrapped like GNU base64": wrapped} {
		got, err := Decode(in)
		if err != nil || string(got) != sampleProfile {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}

	_, err := Decode("not base64 at all!!")
	if err == nil || strings.Contains(err.Error(), "not base64") {
		t.Errorf("invalid input: err = %v (must fail without echoing the value)", err)
	}
	if _, err := Decode(" \n"); err == nil {
		t.Error("blank input decoded without error")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	fallback := filepath.Join(dir, "client.ovpn")
	if err := os.WriteFile(fallback, []byte("from file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString([]byte("from env\n"))

	got, source, err := Load(enc, fallback)
	if err != nil || string(got) != "from env\n" || source != "OPENVPN_CONFIG_BASE64" {
		t.Errorf("env set: got %q from %q, %v", got, source, err)
	}
	got, source, err = Load("", fallback)
	if err != nil || string(got) != "from file\n" || source != fallback {
		t.Errorf("env empty: got %q from %q, %v", got, source, err)
	}
	_, _, err = Load("", filepath.Join(dir, "missing.ovpn"))
	if err == nil || !strings.Contains(err.Error(), "OPENVPN_CONFIG_BASE64") {
		t.Errorf("nothing configured: err = %v, want a hint about OPENVPN_CONFIG_BASE64", err)
	}
}

func TestWriteFileReplacesAtomicallyWithOwnerOnlyMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.ovpn")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new profile")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new profile" {
		t.Fatalf("content = %q, %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("mode = %o, want 600", mode)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("leftover files in output dir: %v", entries)
	}
}
