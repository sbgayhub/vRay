# Third-party notices

vRay embeds an entire Xray core in its process and builds its interface from two
Go UI libraries. Their licences and the licences of everything they pull in are
reproduced or referenced here, as those licences require.

This file is generated from the module graph, not written from memory: the
versions below match `go.mod`, and a version bump that forgets this file is a
release blocker (see `scripts/check-notices.sh`).

## Direct dependencies

| Module | Version | Licence |
|---|---|---|
| `github.com/xtls/xray-core` | v1.260327.1-0.20261029083850-0afec66085a8 | MPL-2.0 |
| `github.com/egoist/mygo` | v0.7.1 | MIT |
| `github.com/ELY-Open-Source/CrychicUI` | v0.0.0-20261027050735-bf15ddf6600a | MIT |

### MPL-2.0 and what it means here

Xray-core is licensed under the Mozilla Public License 2.0, which is
file-scoped: modifications to Xray's own source files must be published under the
same licence, but a program that merely links to it is not thereby relicensed.

vRay does not modify Xray. It calls `core.StartInstance` and reads statistics
through Xray's own gRPC service, so the MPL obligations here are the ones a
combined work carries: the licence text is included below, and Xray's source is
available from the module path named above or from
<https://github.com/XTLS/Xray-core>.

## Wintun

`resources/windows-<arch>/wintun.dll` is the WireGuard project's Wintun driver,
preloaded by Xray's TUN inbound on Windows. It is fetched by
`scripts/fetch-wintun.ps1`, not committed: the committed `wintun.sha256` pins the
exact binary that ships.

- Project: <https://www.wintun.net/>
- Licence: WireGuard's Wintun is distributed under the MIT licence.

## Icon and font assets

`resources/icon.png` and `resources/tray*.png` are vRay's own artwork, © 2026
sbgayhub, all rights reserved.

Text is rendered by MyGO's own text stack (`github.com/go-text/typesetting`),
which ships no fonts. vRay uses the system UI font, so no font licence travels
with this application.

## Transitive dependencies

Every module whose packages end up in the vRay binary, as `go list -deps`
reports it — not the whole module graph, which would list modules only a
dependency's tests use and bury the ones that ship. `scripts/check-notices.sh`
compares this table with the build in both directions and
`scripts/check-notices.sh --update` rewrites it.

<!-- BEGIN GENERATED -->
| Module | Version |
|---|---|
| `github.com/ELY-Open-Source/CrychicUI` | v0.0.0-20261027050735-bf15ddf6600a |
| `github.com/andybalholm/brotli` | v1.2.3 |
| `github.com/apernet/quic-go` | v0.61.1-0.20260806010916-184d081eef3e |
| `github.com/ebitengine/purego` | v0.11.1 |
| `github.com/egoist/mygo` | v0.7.1 |
| `github.com/ghodss/yaml` | v1.0.1-0.20220118164431-d8423dcdf344 |
| `github.com/go-text/typesetting` | v0.3.5 |
| `github.com/google/gopacket` | v1.1.19 |
| `github.com/google/uuid` | v1.6.0 |
| `github.com/gorilla/websocket` | v1.5.3 |
| `github.com/huin/goupnp` | v1.2.0 |
| `github.com/jackpal/go-nat-pmp` | v1.0.2 |
| `github.com/juju/ratelimit` | v1.0.2 |
| `github.com/klauspost/compress` | v1.20.0 |
| `github.com/klauspost/cpuid/v2` | v2.4.1 |
| `github.com/koron/go-ssdp` | v0.0.4 |
| `github.com/libp2p/go-nat` | v1.0.1-0.20250821073202-01afc089f138 |
| `github.com/libp2p/go-netroute` | v0.2.1 |
| `github.com/miekg/dns` | v1.1.73 |
| `github.com/pelletier/go-toml` | v1.9.5 |
| `github.com/pion/dtls/v3` | v3.1.5 |
| `github.com/pion/logging` | v0.2.4 |
| `github.com/pion/stun/v3` | v3.1.7 |
| `github.com/pion/transport/v4` | v4.1.0 |
| `github.com/pires/go-proxyproto` | v0.16.0 |
| `github.com/quic-go/qpack` | v0.6.0 |
| `github.com/refraction-networking/utls` | v1.8.3-0.20260301010127-aa6edf4b11af |
| `github.com/robfig/cron/v3` | v3.0.1 |
| `github.com/wlynxg/anet` | v0.0.5 |
| `github.com/xtls/reality` | v0.0.0-20261014101318-83f621788ca8 |
| `github.com/xtls/xray-core` | v1.260327.1-0.20261029083850-0afec66085a8 |
| `github.com/yuin/gopher-lua` | v1.1.2 |
| `go4.org/netipx` | v0.0.0-20231129151722-fdeea329fbba |
| `golang.org/x/crypto` | v0.58.0 |
| `golang.org/x/crypto/x509roots/fallback` | v0.0.0-20261020153306-9f2b28d4331a |
| `golang.org/x/exp` | v0.0.0-20240506185415-9bf2ced13842 |
| `golang.org/x/image` | v0.46.0 |
| `golang.org/x/net` | v0.61.0 |
| `golang.org/x/sync` | v0.24.0 |
| `golang.org/x/sys` | v0.49.0 |
| `golang.org/x/text` | v0.43.0 |
| `golang.zx2c4.com/wintun` | v0.0.0-20230126152724-0fa3db229ce2 |
| `golang.zx2c4.com/wireguard` | v0.0.0-20250521234502-f333402bd9cb |
| `golang.zx2c4.com/wireguard/windows` | v1.1.1 |
| `google.golang.org/genproto/googleapis/rpc` | v0.0.0-20260706201446-f0a921348800 |
| `google.golang.org/grpc` | v1.84.0 |
| `google.golang.org/protobuf` | v1.36.11 |
| `gopkg.in/yaml.v2` | v2.4.0 |
| `layeh.com/gopher-luar` | v1.0.11 |
| `lukechampine.com/blake3` | v1.4.1 |
<!-- END GENERATED -->

## Xray-core's licence

The Mozilla Public License, version 2.0, is reproduced in full at
<https://www.mozilla.org/media/MPL/2.0/index.txt>. It is not copied here because
a hand-copied licence text drifts from the real one; the URL above is the
authoritative text Xray-core itself carries.

## Reporting

If anything in this file is wrong or missing, that is a licence defect and worth
reporting: see the issue tracker linked from `docs/README.md`.
