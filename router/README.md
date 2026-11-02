# OpenWrt UDP/443 TPROXY

Deployed 2026-11-02 on OpenWrt 25.12.6, Linux 6.12.112.

This is an independent **LAN IPv4 UDP/443** ingress. The existing
TCP REDIRECT (7893), other UDP ports, IPv6 and Mesh interception are unchanged.

## Data path

LAN IPv4 UDP/443 -> nftables table `ip rino_udp_tproxy` ->
packet mark `0x100` -> local routing table 100 ->
Mihomo TPROXY listener `7894/udp` -> existing rules.

Domestic GEOIP matches DIRECT locally. Other UDP traffic uses
`Tencent-SOCKS-UDP`, which forwards over Yggdrasil to the
Tencent Mihomo mixed/SOCKS5 port 7890. Tencent then uses its own rules.

The TPROXY listener does not override the outgoing proxy. The UDP route
is generic and is evaluated after CN DIRECT, before the final MATCH.

## Mihomo configuration additions

```yaml
proxies:
  - name: Tencent-SOCKS-UDP
    type: socks5
    server: 200:e9c5:447:8534:15f8:d467:f47d:6161
    port: 7890
    udp: true
    ip-version: ipv6
listeners:
  - name: rino-lan-udp
    type: tproxy
    listen: 0.0.0.0
    port: 7894
    udp: true
rules:
  # existing rules first; keep domestic DIRECT before this line
  - NETWORK,UDP,Tencent-SOCKS-UDP
  - MATCH,Tencent
```

Install prerequisites with `apk add kmod-nft-tproxy`; this pulls in
`kmod-nf-tproxy`. Place this directory's `rino-udp-tproxy.nft` at
`/etc/rino-udp-tproxy.nft`, and `rino-udp-tproxy.init` at
`/etc/init.d/rino-udp-tproxy`. Run `chmod 755` on the init file, then
`/etc/init.d/rino-udp-tproxy enable` and `start`.

The service waits for Mihomo's UDP listener before adding nftables rules.
It owns only its table and fwmark route; TCP rules stay untouched.
On this router it starts after `S99rino-proxy` by alphabetical order.

## Validation and rollback

```sh
/etc/init.d/rino-udp-tproxy status
nft list table ip rino_udp_tproxy
ip -4 rule
ip -4 route show table 100

# Stops UDP interception and restores the prior direct path:
 /etc/init.d/rino-udp-tproxy stop
 /etc/init.d/rino-udp-tproxy disable
```

Previous router Mihomo configuration backup:
`/etc/rino-proxy.yaml.before-udp-20261102`.

Before production: SOCKS5 UDP ASSOCIATE carried a real DNS exchange.
After production: Windows curl with HTTP3-only returned HTTP 204 from
Google (HTTP version 3); routing checks showed Chinese UDP -> DIRECT and
external UDP -> Tencent-SOCKS-UDP. Service stop/start rollback was tested.

## Current traffic coverage and IPv6 audit (2026-11-02)

| Traffic | Path |
|---|---|
| LAN IPv4 TCP 80/443 | Existing nftables REDIRECT to Mihomo :7893 |
| LAN IPv4 UDP 443 | Independent TPROXY to Mihomo :7894, fwmark 0x100, local route table 100 |
| LAN DNS UDP/TCP 53 | dnsmasq 172.27.207.1:53 forwards to Mihomo DNS 127.0.0.1:1053 |
| Other TCP/UDP ports | Direct; not intercepted |
| IPv6 | ULA and Yggdrasil only; no IPv6 Internet default route |

The rules apply only to LAN IPv4 source 172.27.207.0/24 entering br-lan. Router-originated traffic and Yggdrasil/campus WireGuard ingress are outside this capture path.

IPv6 findings:
- wan6 is down and there is no IPv6 default route.
- LAN ULA prefix is fd6a:cc30:5866::/60.
- Yggdrasil has its own 200::/7 route.
- Mihomo DNS has ipv6: false.

There is currently no routed IPv6 Internet traffic to capture. IPv6 TPROXY is deliberately deferred; keep ULA and Yggdrasil direct.

The active Tencent config has TX (47-node URL-test) and one generic 通用出口 fallback group (12 ordered candidates plus TX). No Rino-* groups are active. A fresh isolated matrix tested all 12 candidates on 2026-11-02; 11 returned HTTP 403 for ChatGPT and one Singapore node timed out (HTTP 0). HTTP 403 is valid transport reachability in health.Accepted; the timeout is the only candidate without an HTTP response in this sample. The matrix is stored locally in the ignored matrix-results-2026-11-02.local.json.

Verification note: service stop/start rollback passed, but a full router reboot has not yet been tested.
