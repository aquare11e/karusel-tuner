Preferred: put your OpenVPN profile into `.env` as `OPENVPN_CONFIG_BASE64`
(macOS: `base64 -i client.ovpn`, Linux: `base64 -w0 client.ovpn`).

Fallback: when `OPENVPN_CONFIG_BASE64` is empty, `vpn/client.ovpn` is used.
It is gitignored.

The profile must be self-contained: certificates and keys inline (`<ca>`,
`<cert>`, `<key>`). Host names in `remote` are resolved to IPv4 automatically
on every deploy (Gluetun accepts only IP addresses there).

Never commit VPN keys/passwords or bundle them into a support archive.
