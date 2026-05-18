---
name: stripe-webhook
description: |
  Verify and dispatch a Stripe webhook payload.
  Use when implementing or reviewing a Stripe webhook handler.
activation:
  modes: [glob, model_decision]
  globs:
    - src/billing/**
allowed_tools:
  - Bash
  - Read
  - Edit
---

# Verifying a Stripe webhook

1. Read the raw request body before any JSON decoding. The signature is
   computed over bytes, not over the re-marshaled JSON.

2. Pull `Stripe-Signature` from the headers. Pass it, the raw body, and
   the endpoint secret (`STRIPE_WEBHOOK_SECRET`) to
   `webhook.ConstructEvent`. If that returns an error, respond `400` and
   log the signature header verbatim.

3. Switch on `event.Type`. Implemented handlers live under
   `internal/billing/webhooks/`. Unknown event types respond `200` (so
   Stripe stops retrying) and emit a `webhook.unknown_type` metric.

4. Webhook handlers are idempotent. Stripe redelivers on any 5xx, and a
   single event may arrive twice. Key idempotency off `event.ID`.

5. Never call back into Stripe's API from a webhook handler. Enqueue
   follow-up work; the handler must return within Stripe's 30-second
   timeout.
