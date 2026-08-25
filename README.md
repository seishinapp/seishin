# Seishin

A self-hostable, low-latency voice + text platform — a real replacement for TeamSpeak and
Discord, not a Discord clone.

- One hobbyist should be able to run this with one binary, one config file, one Postgres
  URL. The same code scales horizontally by recomposing the same services, not by rewrite.
- The control plane is Go; the media plane (SFU, jitter buffer, audio pacing) is Rust — see
  [`docs/architecture.md`](docs/architecture.md).
- Native voice runs over a QUIC-based protocol, CXP/1 — see [`docs/protocol.md`](docs/protocol.md).
- Voice is forward-only: the SFU never mixes, which is what makes per-speaker spatial audio
  possible. See `docs/architecture.md` section 4.3.
- Everything a first-party client or gateway can do, a third-party bot can do too — no
  privileged internal path. See `docs/architecture.md` section 1, restriction #5.

## Scope

This repository is **the server**. It builds to a single binary (`seishin serve`, or
`seishin serve --role=X` for a distributed deployment) and holds everything that ships
inside that binary:

- `internal/core/*` — Identity, Directory, Session/Presence, Policy/Capability
- `api/proto` — the Canonical Platform API (protobuf, the "waist" of the hourglass)
- `gateway/native` — the CXP/1 QUIC/WebTransport gateway
- `media/sfu`, `media/audio` — the Rust SFU and audio pipeline

Everything that is *not* the server lives in its own repository in the `seishinapp` org —
clients, the TS3 gateway, and any dependency extracted along the way get their own repo,
same pattern LiveKit uses:

- [`seishinapp/seishin-web`](https://github.com/seishinapp/seishin-web) — web client
- [`seishinapp/seishin-desktop`](https://github.com/seishinapp/seishin-desktop) — native desktop client
- [`seishinapp/seishin-gateway-ts3`](https://github.com/seishinapp/seishin-gateway-ts3) — TS3 protocol bridge

## Building

```sh
go build ./...             # control plane
cd media && cargo check    # media plane
```

## License

Apache License 2.0 — see [`LICENSE`](LICENSE).
