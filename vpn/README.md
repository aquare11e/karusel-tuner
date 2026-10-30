Put **your own** OpenVPN profile at `vpn/client.ovpn`. It is gitignored.

If it references certificates/keys/files, place them here too and change paths
inside the profile to absolute paths, e.g. `ca /gluetun/vpn/ca.crt`.
Gluetun may need `remote` entries resolved to provider-supplied IP addresses
because its firewall blocks DNS lookups before the tunnel is established.

Never commit VPN keys/passwords or bundle them into a support archive.
