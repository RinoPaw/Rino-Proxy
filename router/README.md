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
