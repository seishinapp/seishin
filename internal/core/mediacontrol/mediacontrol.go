// Package mediacontrol allocates channels to SFU home nodes (consistent hash
// on channel ID, single-node only in v1) and hands each SFU node its
// participant/subscription table. It never touches Postgres and makes no
// policy decision itself.
package mediacontrol
