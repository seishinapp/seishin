# Architecture

## 1. Project constraints, stated up front

Every decision below answers to these constraints.

1. **A hobbyist has to be able to run this.** One binary, one config file, one Postgres
   URL, done. If self-hosting is harder than TeamSpeak, the project has no audience.
2. **The same code has to scale horizontally.** Not a rewrite for large deployments — a
   different composition of the same services.
3. **The core has to outlive its protocols.** TS3 compatibility will be a footnote in five
   years. The domain model can't carry those scars.
4. **Latency is the product.** Every architectural choice that adds a hop to the audio path
   has to justify itself in milliseconds.
5. **Everything a client does, a bot does.** No internal privileged path that the public
   API can't express.

## 2. The hourglass

The system is an hourglass. Many protocols on top, many services underneath, a narrow
waist in the middle.

```
┌─────────────────────────────────────────────────────────────────────┐
│  EDGE — protocol adapters, one process per protocol                 │
│                                                                     │
│  TS3 Gateway    Native Gateway    Matrix Bridge    Discord Bridge   │
│  (UDP, legacy)  (QUIC / WebTr.)   (appservice)     (bot API)        │
│                                                                     │
│  Public API Gateway  (REST + WebSocket + webhooks, for bots)        │
└────────────────────────────────┬────────────────────────────────────┘
                                 │  gRPC / protobuf
┌────────────────────────────────▼────────────────────────────────────┐
│  CANONICAL PLATFORM API — the waist. Contract-first, versioned.     │
└────────────────────────────────┬────────────────────────────────────┘
                                 │
┌────────────────────────────────▼────────────────────────────────────┐
│  CORE SERVICES                                                      │
│                                                                     │
│  Identity    Directory   Session &   Policy &    Messaging          │
│  & Auth      (spaces,    Presence    Capability                     │
│              channels)                                              │
│                                                                     │
│  Media Control   Moderation & Audit   Plugin Host                   │
│  (SFU allocator)                                                    │
└────────────┬───────────────────────────────────┬────────────────────┘
             │                                   │
┌────────────▼─────────────┐      ┌──────────────▼────────────────────┐
│  STATE                   │      │  MEDIA DATA PLANE                 │
│  Postgres (truth)        │      │  SFU nodes (Rust)                 │
│  NATS JetStream (events) │      │  TURN / relay                     │
│  Object store (blobs)    │      │  Cascade links between nodes      │
└──────────────────────────┘      └───────────────────────────────────┘
```

**Why an hourglass and not layers.** Layers let a protocol detail leak one layer down at a
time, until the domain model has a `ts3_client_id` field. A waist is a wall: the Canonical
Platform API's protobuf schema is the only vocabulary the core accepts. A gateway that
needs something the waist doesn't express has two ways out — propose a change to the waist
(reviewed, versioned), or handle it in the gateway itself. That review friction is the
mechanism, not a side effect.

**Concretely, what "protocol-independent core" means in code:**

- Domain packages import nothing from `net`, no codec library, no protocol struct.
- Legacy identifiers (a TS3 `clid`, a Matrix `@user:server`) live in a per-gateway mapping
  table, never in the core's user record. The core only ever sees canonical IDs.
- Legacy permission integers are translated into capabilities at the gateway. The core's
  policy engine has never heard of `i_channel_needed_join_power`.
- A gateway is an ordinary API client with an elevated set of capabilities. Nothing it does
  is impossible for a third-party bot with the same capabilities.

## 3. Control plane in Go, media plane in Rust

The boundary is the Opus packet. If an Opus byte passes through it, it's Rust. Everything
else is Go.

| | Go (control) | Rust (media) |
| --- | --- | --- |
| Handles | API gateway, core services, gateways/bridges, admin | SFU nodes, jitter/pacing, cascade links, TS3 crypto |
| Why | contributors, gRPC ecosystem, fast iteration | no GC pauses on a 20 ms budget, fine-grained memory control |
| Speaks | gRPC with the services, protobuf with the SFU | QUIC/UDP with clients, gRPC received from Media Control |

The interface between the two is deliberately thin: the Media Control service tells an SFU
node "this channel exists, these participants may publish, forward these subscriptions".
The SFU never touches Postgres and never makes a policy decision. It is a dumb, fast pipe
that applies a table it was handed.

## 4. The voice path

### 4.1 Transport

Three transports, one media format.

| Transport | Used by | Notes |
| --- | --- | --- |
| **QUIC** (RFC 9000) + datagrams (RFC 9221) | native desktop/mobile client | control on streams, audio on datagrams, one handshake, one congestion controller |
| **WebTransport over HTTP/3** | web client | same datagram format; Baseline across all browsers since Safari 26.4 (March 2026) |
| **WebRTC** (ICE/DTLS/SRTP) | fallback browsers, MatrixRTC and Discord interop | costs a translation step at the edge; out of v1 scope |

QUIC is the native choice for reasons that aren't fashion:

- **Encryption and key rotation come with the handshake.** TS3 had to invent its own EAX-mode
  packet crypto with its own key schedule. We inherit TLS 1.3 instead.
- **Datagrams are unreliable *by design*.** Late audio is useless audio; a datagram that
  misses its deadline simply isn't retransmitted. Control frames on the same connection
  still get reliable, ordered delivery on streams.
- **Port 443 survives corporate firewalls**, and connection migration survives a phone
  moving from Wi-Fi to LTE without a new handshake — today that drops a TS3 call.

The honest caveat: QUIC's congestion controllers are tuned for bulk transfer, and WebRTC's
decade of work on real-time bandwidth estimation has no direct equivalent in QUIC stacks.
We disable CC for datagram traffic within a bitrate ceiling and run our own audio rate
controller (loss-driven and one-way delay-gradient-driven), which is a well-understood
problem for a 16–96 kbps stream and a hard one for video. Video (P1) rides on WebRTC, where
that work has already been done.

### 4.2 Codec and framing

- **Opus at 48 kHz**, 20 ms frames by default, 10 ms as a low-latency option, mono for
  voice. Bitrate adapts between 16 and 96 kbps.
- **DTX** on by default: silence costs ~1 packet every 400 ms.
- **In-band FEC (LBRR)** for the first tier of loss, RED-style duplication of the previous
  frame above ~8% loss. Both decided **per subscriber** by the SFU, not by the publisher —
  one listener on a bad link gains redundancy without penalizing everyone else.
- **No transcoding on the native path.** The publisher's Opus frames arrive byte-for-byte
  identical at every subscriber. Transcoding exists only where a legacy gateway requires it.

### 4.3 Forward, never mix

The SFU forwards each speaker as a separate stream. It does not mix. This is the single
most consequential decision in the whole audio design, because:

- **Spatial audio requires it.** Mixed audio no longer has a per-speaker channel to
  position in 3D. Client-side HRTF rendering needs N decoded streams plus each speaker's
  pose.
- **It removes a decode/encode round trip** from the server path — somewhere between 5 and
  10 ms and a generation of quality loss per hop.
- **The client applies per-speaker gain, mute, and effects** with no round trip to the
  server.

The cost is client bandwidth and CPU: 8 simultaneous speakers means 8 Opus decoders.
Mitigated by an **active-speaker cutoff** — the SFU forwards the top N (default 6,
configurable) by a continuous energy estimate and drops the rest, which is inaudible in
practice and what every large conferencing system does.

### 4.4 Spatial audio

The server delivers two things and renders nothing:

1. the per-speaker Opus streams (above), and
2. a **pose lane** — a separate datagram type carrying quantized per-participant position
   and orientation at ~10 Hz, forwarded unmodified.

Rendering happens on the client. On the web client that's WebAudio's `PannerNode` with an
HRTF panning model; on the native client, an HRTF convolver with a decent room model. Each
channel carries a **spatial policy** (`flat` | `room` | `world`) that tells clients whether
poses carry meaning, and what the room's dimensions and reverb parameters are. Clients that
don't understand poses ignore the lane and mix flat — exactly what TS3 Gateway clients will
do.

Pose data is small enough (~16 bytes/participant/update) to be worth the generosity, and is
the hook a future Game SDK (P2) plugs into: the engine writes poses, the voice layer
renders them.

### 4.5 Latency budget

The number to defend, word of mouth, on a same-region link:

| Step | Budget |
| --- | --- |
| Capture + frame assembly | 20 ms (one 20 ms frame; 10 ms in low-latency mode) |
| Opus encode | 2–5 ms |
| Network, client→SFU | RTT/2 |
| SFU forwarding | < 1 ms (no decode) |
| Network, SFU→client | RTT/2 |
| Adaptive jitter buffer | 20–60 ms, the real variable |
| Decode + spatial render + output | 5–15 ms |

**Target: under 60 ms on a same-region link, under 120 ms cross-region.** The jitter buffer
is where the fight is; everything else is nearly fixed. That's why transport choice matters
more than codec tuning — a transport with no head-of-line blocking lets the buffer run
shorter.

## 5. Distribution model

### 5.1 One binary, several shapes

The same build runs as:

- **`seishin serve`** — every service in one process, embedded SQLite or Postgres, SFU
  in-process. This is the TeamSpeak-equivalent experience, and it's the default.
- **`seishin serve --role=api|core|sfu|gateway-ts3|...`** — the same components as
  separate processes, talking gRPC over the network instead of in-process.

Composition is a wiring decision, not a code path. Services are written against interfaces
and registered in a registry that resolves either locally or over the wire. If the
single-binary mode is slower to develop than the distributed mode, we lose the self-hoster;
the CI matrix runs the full suite in both shapes.

### 5.2 How state is split

| State | Where | Why |
| --- | --- | --- |
| Spaces, channels, members, roles, capabilities | Postgres | durable, transactional, small |
| Messages | Postgres, partitioned by channel | write-heavy, occasional read |
| Presence, sessions, speaking state | NATS JetStream KV + memory | high churn, tolerable to lose on restart |
| Events (everything that happened) | NATS JetStream stream, per space | substrate for bots, audit, and bridges |
| Media session allocation | Media Control, consistent hash on channel ID | needs to be fast and deterministic |
| Blobs (avatars, files, recordings) | S3-compatible object store, or local FS | |

**The event log is the integration point.** Every state change is published as a canonical
event. Bots consume it, bridges consume it, the audit trail *is* it, and the moderation
tooling replays it. That's what makes bots first-class instead of bolted on: a bot and the
Discord bridge subscribe to the same stream with the same API.

### 5.3 Media allocation and cascade

A channel is allocated to one SFU node — its **home node**. Participants connect to the
nearest node; if that isn't the home node, the two nodes open a **cascade link** (a QUIC
connection carrying every stream between them, per speaker, still unmixed).

```
 São Paulo                      Frankfurt
 ┌──────────┐   cascade link    ┌──────────┐
 │  SFU-BR  │◀─────────────────▶│  SFU-EU  │
 └──▲────▲──┘                   └──▲────▲──┘
    │    │                         │    │
   c1   c2                        c3   c4
```

Cascade trades one extra hop for a much shorter first hop, and is the only way an
intercontinental channel is usable. The allocator's job is placement: pick the home node by
where the first participants are, and migrate the channel when the population shifts (a
migration is a re-publish, ~200 ms of attenuated audio, done in a natural pause).

> **v1 scope:** the Media Control allocator ships from v1 on (consistent hash,
> single-node); cascade links do not. A v1 deployment runs at most one SFU node.

## 6. Identity

The account is not owned by the server.

- **The root of identity is a keypair** (Ed25519). The user ID derives from the public key.
  This is TS3's identity idea, kept, because it's genuinely good: no email, no signup,
  works offline, portable between servers by copying a key.
- **Optional home provider** for anyone who wants recovery and a readable handle: an
  OIDC-compatible identity server, self-hosted or third-party, issuing
  `@user:home.example`. The server validates both the token *and* the key signature.
- **Guest entry** is a first-class path, not a hack: a link opens the web client, generates
  an ephemeral key, and joins with a display name and guest capabilities. This is what
  makes "join without installing" real, and is probably the single feature that most
  decides whether a server gets used.
- **Servers assign roles to identities**, and roles never leave the server. Portability is
  about *who you are*, not *what you're allowed to do*.

Reputation, bans, and moderation therefore bind to the identity key, with the usual caveat
that key rotation is a ban-evasion vector — mitigated by optional proof-of-work on new
identities and configurable "requires a home-provider token" policies.

## 7. Interoperability

### 7.1 TS3 / legacy client gateway

A separate process, a separate repository (`seishinapp/seishin-gateway-ts3`), a license of
its own if needed. Speaks the legacy protocol on one side and
the Canonical Platform API on the other, and concentrates all the ugliness: UDP packet
crypto, the puzzle handshake, QuickLZ command compression, permission-integer mapping,
codec negotiation.

It's the adoption bridge, and it's explicitly disposable. Nothing in the core may depend on
it existing. **It's the only interop gateway in v1 scope** — Matrix and Discord (below) are
deferred.

### 7.2 Matrix (post-v1)

Chat interop via the **Matrix Application Service API** — portal rooms, ghost users, an
optional double-puppeting mode. We're a bridge, not a homeserver: implementing federation,
state resolution, and the full room DAG is a multi-year project with no payoff for a voice
platform.

Voice interop with MatrixRTC is a late-P1 item and should be scoped honestly. MatrixRTC
(MSC4143 and neighbors) was still stabilizing in 2026 — self-hosters were actively fighting
transport-discovery bugs between Synapse, Element Call, and LiveKit. The realistic path is
to expose a LiveKit-compatible SFU endpoint, so a Matrix room can pick our SFU as its focus
instead of us reimplementing MatrixRTC signaling.

### 7.3 Discord (post-v1)

A conventional bot-API bridge for text and presence. Voice bridging is possible but
one-directional in practice, and should stay a community project.

## 8. Extensibility

### 8.1 Bots and the public API

REST + WebSocket on the public surface, gRPC internally, outbound webhooks. Authentication
via **capability-scoped tokens** (Biscuit-style): a token can be attenuated by its own
holder without contacting the server, so a bot can hand a sub-agent a strictly weaker
token. Rate limits and audit records attach to the token, not the account.

### 8.2 Plugin runtime (post-v1)

Server-side plugins run as **WebAssembly components** (WASI Preview 2) with a
capability-based host API, memory and fuel limits, and no ambient filesystem or network
authority. The host API is a subset of the Canonical Platform API plus hooks
(`on_message`, `on_join`, `on_talk_start`). A plugin cannot block the audio path — it isn't
on it.

The default answer to "can I extend this?" should be "write a bot." Plugins exist for cases
where a network round trip isn't acceptable, or where the extension needs to ship bundled
with the server. Out of v1 scope.

## 9. Repository layout

The server is a single binary (`seishin serve` / `seishin serve --role=X`), so everything
that composes that binary lives in one repo, `seishinapp/seishin`:

```
/cmd/seishin              single entrypoint, every role
/internal/domain          pure domain types. no transport, no codec, no SQL
/internal/core/*          identity, directory, session, policy, messaging, media-control
/internal/store           Postgres + NATS adapters
/api/proto                the Canonical Platform API. protobuf. the waist.
/gateway/native           QUIC + WebTransport gateway (control side, Go)
/media/sfu               Rust: SFU node, sans-IO core + runtime shells
/media/audio             Rust: jitter buffer, energy estimation, FEC policy
/docs                    this directory
```

Everything that is *not* the server lives in its own repository inside the `seishinapp`
org — clients, protocol gateways, and any dependency extracted along the way. This is the
same organization pattern LiveKit uses (`protocol`, `server-sdk-go`, `client-sdk-js`,
`rust-sdks`, each its own repo):

| Repository | Contents |
| --- | --- |
| `seishinapp/seishin` | the server (this repo) |
| `seishinapp/seishin-web` | web client (TypeScript: WebTransport + WebCodecs + WebAudio) |
| `seishinapp/seishin-desktop` | native desktop client |
| `seishinapp/seishin-gateway-ts3` | TS3 gateway, the only interop gateway in v1 |

Mobile clients (Android/iOS) and the Matrix/Discord gateways don't exist yet. They follow
the same one-repo-each pattern when built.

## 10. Where this architecture is bad

Stated so nobody discovers it in month eight.

- **A single channel with 500 simultaneous speakers.** Forwarding means bandwidth grows
  with the number of speakers; the active-speaker cutoff bounds it, but a real
  broadcast/town-hall mode needs a different topology (mixing, or a broadcast tier). Not
  P0, and needs its own design.
- **Full end-to-end encryption.** A forwarding SFU can do E2EE with SFrame — but
  server-side features that need cleartext (recording, transcription, server-side spatial
  fallback) break, and the TS3 Gateway can't participate at all. Hop-by-hop encryption is
  the P0 answer; E2EE is a later, explicit mode, with documented trade-offs.
- **Federated voice between servers.** The cascade mechanism leaves this technically
  approachable, but the hard part is the trust, capability, and moderation model across
  administrative domains — and this document doesn't solve that.
- **Anything that assumes a global account directory.** Search, cross-server friends, and
  cross-server DMs are all awkward by construction. That's the price of the TeamSpeak
  model, paid on purpose.
