# Rino Proxy

A small, cross-platform control plane for Mihomo. **v0.1 flattens multiple local Mihomo YAML profiles into one standalone configuration.** It does not take over your live proxy or change OpenWrt.

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

## Limits

Only inline YAML Profile nodes are supported. Provider-only YAML, remote URLs, encrypted sharing links and automatic subscription refresh are future work. v0.1 does not detect real website availability or switch nodes dynamically. Keep your generated configuration outside your live Nikki/Mihomo directories.

## Security

The `testdata/` profiles use fake credentials and intentionally invalid hostnames. Never commit real subscriptions, subscription tokens, server credentials or merged outputs. The ignore file covers common paths, but Git cannot identify arbitrary private files automatically. Always review `git status` before pushing.

## Next steps

- v0.2: node × site probes, `OK/FAIL/CHALLENGE/RATE_LIMIT`, and the acceptance target `bilibili && github && (google || youtube || chatgpt)`.
- v0.3: site-specific policy groups, automatic selection, fallback and cooldowns.
- v0.4: Mihomo API control, reload/rollback and core adapters.
- v0.5: Web dashboard and controlled Windows/OpenWrt deployment.
