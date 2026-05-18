---
name: billing-subsystem
description: Billing subsystem conventions (cascade-scoped, non-override).
activation: cascade
priority: 10
tags:
  - billing
  - subsystem
extensions:
  agents-md:
    cascade_anchor: src/billing
---

# Billing subsystem

This subsystem owns:

- `pkg/billing` — domain model
- `cmd/billing-worker` — async charge reconciliation

## Subsystem-specific rules

- Charge amounts are integers (cents). Never floats.
- All charges go through `billing.Process()`; never the Stripe SDK directly.
- Migrations under `migrations/billing/` use `dbmate`, not `golang-migrate`.
