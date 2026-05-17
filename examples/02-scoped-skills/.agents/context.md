Payments service. Receives orders, charges via Stripe, emits receipts.

Two boundaries matter:

  - `src/billing/`  — money flows; touched only with a paired test and a
                      written justification in the PR description.
  - everything else — normal-rules apply.
