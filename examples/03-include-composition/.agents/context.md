Internal CLI for managing fleet inventory across three datacenters. Go
binary; no GUI; talks to the inventory API via gRPC.

<!-- include: shared/style.md -->

## Project specifics

- Inventory API contracts live in `proto/`; regenerate with
  `make proto`.
- The CLI is shipped as a single static binary; cgo is off.
