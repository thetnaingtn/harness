# Refund Policy

Acme issues refunds under the following conditions. The agent should
quote the matching clause verbatim when discussing a refund with a
customer.

## Subscription charges

- **Within 14 days of renewal**: full refund, no questions asked.
- **Day 15–30 of renewal**: prorated refund for unused portion of the
  billing period.
- **After day 30**: no refund. Customer may downgrade to take effect at
  next renewal.

## Usage / overage charges

- Refunds are **discretionary** and capped at **$100 per billing
  period** when one of the following applies:
  1. The customer did not receive the 80% / 100% rate-limit warning
     emails (verify via `tickets_get` notes or the email-events
     dashboard).
  2. A documented platform incident on our side caused the spike (cross-
     reference status.acme.example).
  3. The overage is the customer's first ever and the amount is < $50.
- Overages > $100 require supervisor approval. File a ticket with
  priority `high` and escalate.

## One-time purchases (add-ons, professional services)

- Refunds are not available once the engagement begins or the add-on is
  consumed.
- Unused engagements may be transferred once to a different team within
  the same account.

## Disputes

- If the customer threatens a chargeback or mentions their bank, do
  **not** discuss refund eligibility further. File a ticket with
  priority `urgent`, status `escalated`, and add an internal note
  flagging "billing dispute — chargeback risk". Hand off to billing.
