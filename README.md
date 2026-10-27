# Rino Proxy

A small, cross-platform control plane for Mihomo. **v0.1 flattens local YAML profiles; v0.2 checks website reachability; v0.3 adds isolated node-by-site matrix probing.** It does not take over your live proxy or change OpenWrt.

## Requirements

Go 1.23+ to build. Optional: Mihomo binary for native config validation.

## Run

```sh
go run ./cmd/rino profile merge testdata/star.yaml testdata/fubuki.yaml --output merged.yaml --manifest nodes.json
go test ./...
mihomo -t -f merged.yaml        # optional, if Mihomo is installed
```

Windows build: `go build -o rino.exe ./cmd/rino`.

## Flatten semantics

- Reads inline `proxies:` from multiple local YAML Profiles. Every protocol-specific option is preserved.
- Creates namespaced labels `[source] original-name`; two servers using the same display name coexist.
- Collapses nodes only when their connection configurations are **exactly equal** (ignoring display name). Passwords, UUIDs, TLS and transport settings participate in identity.
- With `--manifest`, records the output label plus all source filenames and old aliases. No credentials are put into the manifest.
- Ignores airport-provided rules, DNS and proxy-groups. Those configurations may conflict, so v0.1 creates one `Rino` selector and a default `MATCH,Rino` rule.
- **DIRECT is the first selection**. Output uses `allow-lan: false`, no TUN and no remote admin API.
- Writes output files with restrictive permissions, using temporary files and atomic rename. Refuses to overwrite input files and symlinks.

## Website checks through an existing HTTP proxy

Use the Tailnet proxy after it has been deployed and verified (this command does not change proxies or router rules):

```sh
go run ./cmd/rino health check --proxy http://PROXY_HOST:PORT
go run ./cmd/rino health check --proxy http://PROXY_HOST:PORT --json
```

Each run checks Bilibili API (200 with code 0 or unauthenticated -101), GitHub home (200), Google generate_204 (204), YouTube (200), and ChatGPT (200 without challenge markers). Cloudflare challenges, rate limits, and unrecognized responses are distinguished. The final acceptance expression is `bilibili && github && (google || youtube || chatgpt)`.

A successful website check only proves reachability from this proxy at that moment; it does not verify browser sign-in, video playback, Git operations, or all app features. The command requires an explicit HTTP proxy; it does not silently route traffic through Windows system proxy settings.

## Isolated node-by-site matrix

Run matrix checks **only against a separate Mihomo instance** you started for experiments. Its configuration must have a `RinoTest` selector containing the nodes to inspect, with `DIRECT` as its initial selection, plus:

```yaml
mixed-port: 18993
bind-address: 127.0.0.1
allow-lan: false
external-controller: 127.0.0.1:18994
mode: rule
# Add your own local proxies, a RinoTest selector, and:
rules:
  - MATCH,RinoTest
```

Then run on the **same machine** as that isolated core:

```sh
go run ./cmd/rino health matrix --limit 12 --timeout 8s
go run ./cmd/rino health matrix --match 'Fubuki' --limit 0 --json
```

The matrix changes the `RinoTest` selector one node at a time, requests Bilibili, GitHub, Google, YouTube and ChatGPT through that node, and restores the previous selection afterwards. Both controller and proxy URLs are restricted to loopback to reduce accidental production changes. The experiment does not enable a public proxy listener, TUN, system proxy or transparent routing.

The output distinguishes **single-node pass** (one tested node satisfies the full expression) from **coverage pass** (the union of tested nodes covers all required sites). Coverage alone cannot guarantee automatic site routing: separate per-site policy groups are still needed. Results are time-sensitive and reflect the network of the machine running the isolated Mihomo core. Keep node-bearing configs and raw results private, and never commit subscription credentials.


## Deployed beta gateway on OpenWrt

Rino Proxy can generate a LAN-scoped Mihomo configuration that uses a reachable, private HTTP proxy on Tailscale as upstream. This early deployment uses a **single Tailnet fallback**; it does not yet choose different airport nodes automatically per website.

```sh
go run ./cmd/rino router render --output rino-router.generated.yaml \
  --upstream http://100.64.0.3:17890
mihomo -t -f rino-router.generated.yaml
```

On the JCG Q30 PRO OpenWrt router, the deployed files are:

- `/etc/rino-proxy.yaml`: generated Mihomo configuration
- `/etc/init.d/rino-proxy`: procd service (see [rino-proxy.init](rino-proxy.init))
- `/usr/bin/rino`: ARM64 Rino Proxy diagnostic CLI

The live LAN proxy listens at `172.27.207.1:7890`. Its HTTP/SOCKS mixed-port accepts connections from the local `172.27.207.0/24` network; **there is currently no LAN proxy password**, so keep the Wi-Fi/LAN trusted. WAN and Tailscale interfaces have no new proxy listener. DNS, firewall, default route, TUN and transparent interception are unchanged.

Domestic Bilibili domains route DIRECT; GitHub, Google, YouTube and ChatGPT domains go through the Tencent Tailnet upstream. Everything else defaults to DIRECT. This is an explicit-proxy beta; clients must configure the LAN proxy manually. The upstream Tencent server handles its own node pool. Cloudflare challenges may still block ChatGPT.

```sh
# On the router, as root:
/etc/init.d/rino-proxy status
/etc/init.d/rino-proxy enabled
rino health check --proxy http://172.27.207.1:7890 --timeout 12s --json
cat /tmp/rino-health.json  # monitoring snapshot (every 10 minutes)
```

Emergency rollback (removes only this beta gateway; does not touch Tailscale, WAN or Tencent):

```sh
/etc/init.d/rino-proxy stop
/etc/init.d/rino-proxy disable
```

The service uses OpenWrt's procd (late boot start, respawn). A single `RINO_PROXY_HEALTH` root crontab entry writes a snapshot under `/tmp` every 10 minutes; monitoring does not change routing or reboot the device. The router retains the existing Mihomo package. Real provider secrets must stay off GitHub.

## Limits

Only inline YAML Profile nodes are supported. Provider-only YAML, remote URLs, encrypted sharing links and automatic subscription refresh are future work. Matrix mode switches individual nodes of a **dedicated, disposable** Mihomo selector; it does not yet control live traffic, automatically choose a production node, or make per-site routing decisions.

## Security

The `testdata/` profiles use fake credentials and intentionally invalid hostnames. Never commit real subscriptions, subscription tokens, server credentials or merged outputs. The ignore file covers common paths, but Git cannot identify arbitrary private files automatically. Always review `git status` before pushing.

## Next steps

- v0.2: node × site probes, `OK/FAIL/CHALLENGE/RATE_LIMIT`, and the acceptance target `bilibili && github && (google || youtube || chatgpt)`.
- v0.3: isolated node-by-site matrix (implemented); site-specific policy groups, automatic selection, fallback and cooldowns remain future work.
- v0.4: production-safe Mihomo API control, reload/rollback and core adapters.
- v0.5: Web dashboard and controlled Windows/OpenWrt deployment.
