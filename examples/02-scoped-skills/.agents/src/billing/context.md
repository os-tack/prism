Billing-specific rules.

  - Every monetary value is `int64` cents. Never floats.
  - Stripe IDs are opaque strings; never parse them or compare prefixes.
  - Webhook handlers MUST verify `Stripe-Signature` before reading the
    body — see `skills/stripe-webhook` for the canonical pattern.
  - Refund flows go through `internal/billing/refund.go`; do not call
    Stripe's refund API from anywhere else.
