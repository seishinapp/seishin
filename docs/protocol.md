# Native protocol (working name: CXP/1)

Status: **draft, not frozen.** The wire formats below are a starting point for
implementation, not a compatibility promise. The protocol version is negotiated and may
change without notice until the 1.0 milestone.

## 1. Transport

| Client | Transport | ALPN / path |
| --- | --- | --- |
| Native desktop/mobile | QUIC (RFC 9000) | ALPN `cxp/1` |
| Web | WebTransport over HTTP/3 | `https://host/cxp/1` |
| Legacy fallback (post-v1) | WebRTC (ICE/DTLS/SRTP) | signaled over the same control channel |

One connection carries everything: control on QUIC **streams**, audio and pose on QUIC
**datagrams** (RFC 9221). No second socket, no second TLS handshake, no ICE on the native
path.

Transport encryption is the TLS 1.3 provided by QUIC. **The protocol defines no
packet-level encryption of its own.** Server authentication is the TLS certificate; client
authentication happens at the application layer (§3).

MTU: datagrams are sized to fit a 1200-byte QUIC payload without fragmentation. An audio
frame that exceeds this is a bug, not a case to handle.

## 2. Lanes within the connection

| Lane | QUIC object | Reliability | Content |
| --- | --- | --- | --- |
| Control | bidirectional stream 0 | reliable, ordered | requests, responses, server events |
| Bulk | unidirectional streams on demand | reliable | file transfer, avatars, log sync |
| Voice | datagrams, type `0x1` | unreliable | Opus frames |
| Pose | datagrams, type `0x3` | unreliable | position/orientation |
| Probe | datagrams, type `0x4` | unreliable | bandwidth/RTT estimation |

## 3. Handshake

```
client                                              server
  │  QUIC handshake (TLS 1.3, ALPN cxp/1)              │
  │◀──────────────────────────────────────────────────▶│
  │                                                    │
  │  Hello { version, client_info, capabilities }      │
  │───────────────────────────────────────────────────▶│
  │                                                    │
  │  HelloAck { server_info, nonce, auth_methods }     │
  │◀───────────────────────────────────────────────────│
  │                                                    │
  │  Authenticate {                                    │
  │    identity_pubkey,                                │
  │    signature over (nonce ‖ transcript_hash),       │
  │    home_provider_token optional (JWT OIDC)         │
  │  }                                                 │
  │───────────────────────────────────────────────────▶│
  │                                                    │
  │  Session { session_id, user, capabilities, ... }   │
  │◀───────────────────────────────────────────────────│
```

Notes:

- The signature covers the TLS exporter/transcript hash, which binds the identity proof to
  this specific connection and kills replay.
- **There is no proof-of-work puzzle on the handshake path.** TS3 put one there to price
  connection floods; QUIC's own address validation (Retry tokens) does that job better and
  more cheaply. Proof-of-work, if a server wants it, applies to *identity creation*, not to
  connecting.
- Guests omit `home_provider_token` and send a freshly generated ephemeral key.

## 4. Control frames

Length-prefixed protobuf on stream 0.

```
┌────────────────────┬─────────────────────────────┐
│ uvarint length     │  ControlFrame (protobuf)    │
└────────────────────┴─────────────────────────────┘
```

```protobuf
message ControlFrame {
  uint32 id = 1;              // correlation id; 0 = server-initiated event
  oneof body {
    Request  request  = 2;
    Response response = 3;
    Event    event    = 4;
  }
}
```

Requests and events are the Canonical Platform API's messages in full (see
`api/proto/seishin/v1/`) — the native protocol adds framing and nothing else. A new API
method becomes available to native clients the day it exists, with no protocol work.

## 5. Voice datagram

```
 0               1               2               3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
| ver   | type  |     flags     |          sequence             |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        source_id (u32)                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                 timestamp (u32, 48 kHz clock)                 |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|  energy (u8)  |                                               |
+-+-+-+-+-+-+-+-+       Opus payload                            |
|                            ...                                |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

- `ver` = 1. `type` = `0x1` voice, `0x2` redundant voice.
- `flags`: bit 0 `START` (start of a speech burst), bit 1 `END`, bit 2 `DTX`, bit 3 `EXT`
  (an extension block precedes the payload), bit 4 `WHISPER`.
- `source_id`: server-assigned, per participant and per media session. **In the
  client-to-server direction it is 0** — the server knows who you are from the connection
  and stamps the real id on the way out. This is a deliberate departure from RTP-style
  symmetric headers, and removes a spoofing surface.
- `sequence`: per source, with wraparound.
- `timestamp`: 48 kHz sampling clock, the jitter buffer's input.
- `energy`: log-scale frame energy, 0–255, computed by the publisher, used by the server
  for active-speaker selection **without decoding**, and by clients for speaking
  indicators. A lying client can only lie about its own priority; the server cross-checks
  against packet rate and rate-limits abusers.

Header cost: 12 fixed bytes + 1 energy byte, comparable to RTP, with no need for SRTP
header extensions because QUIC already encrypted it.

## 6. Pose datagram

```
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
| ver   | 0x3   |    count      |         sequence              |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                    repeated `count` times:                    |
|   source_id (u32) | x,y,z (3 × i16, centimeters)              |
|   orientation (u32, smallest-three quaternion)                |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

14 bytes per participant. Client→server sends only its own pose (`count` = 1, `source_id` =
0); server→client batches every participant in the media session into one datagram at
~10 Hz.

Coordinates live in the channel's own space, defined by its spatial policy:

| Policy | Meaning |
| --- | --- |
| `flat` | poses ignored; clients mix with no panning |
| `room` | a fixed room with published dimensions and reverb parameters; clients can auto-position participants |
| `world` | poses are authoritative and come from outside (a game engine via the SDK, post-v1) |

## 7. Congestion and quality control

- The **probe lane** carries send timestamps; receivers report arrival timestamps over the
  control channel at 2 Hz. From that: RTT, one-way delay gradient, loss rate.
- The publisher adjusts Opus bitrate within `[min, max]` from its own uplink report.
- The **server** decides redundancy per subscriber: below ~3% loss, plain forwarding;
  between 3 and 8%, it asks the publisher to enable in-band FEC (LBRR); above 8%, it
  duplicates the previous frame only toward that subscriber.
- Datagram traffic is exempt from the QUIC connection's bulk congestion controller and is
  bounded by the audio rate controller instead. Bulk streams on the same connection use
  normal congestion control.

## 8. Reconnection

QUIC's connection migration handles network changes transparently. An actual connection
loss is resumed with `Resume { session_id, resume_token }` in place of `Authenticate`,
valid for 60 s, restoring channel membership without a storm of rejoin events. Clients
should treat a failed resume as a new connection.

## 9. What is deliberately absent

- **No packet-level encryption.** QUIC has it. Reimplementing it is how TS3 ended up with a
  handshake nobody can safely modify.
- **No server-mixed audio stream.** The SFU is forward-only by design.
- **No codec negotiation matrix.** Opus, one codec, in three frame sizes. Legacy codecs
  exist only inside the TS3 Gateway.
- **No permission integer in the protocol.** Capabilities are named strings in the API.
- **No control-channel compression** in v1. Protobuf on a QUIC stream is small enough
  already; TS3's QuickLZ layer exists because it sent stringly-typed commands.
