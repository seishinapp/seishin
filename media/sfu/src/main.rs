//! Seishin SFU entrypoint: forward-only routing, active-speaker cutoff,
//! per-subscriber redundancy. Receives its participant/subscription table
//! from Media Control (Go) over gRPC and never queries Postgres or
//! evaluates policy itself — see docs/architecture.md section 3.

fn main() {
    eprintln!("seishin-sfu: not yet implemented");
    std::process::exit(1);
}
