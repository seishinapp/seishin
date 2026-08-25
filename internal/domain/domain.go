// Package domain holds Seishin's pure domain types.
//
// Nothing here imports net, a codec library, or a protocol struct — see
// docs/architecture.md section 2 ("core independente de protocolo"). Legacy
// identifiers (a TS3 clid, a Matrix @user:server) never appear in this
// package; they live in gateway-local mapping tables instead.
package domain
