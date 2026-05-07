# Escalation Matrix

Use `escalate_to_human` (supervisor mode only) when one of the
following thresholds is hit. The agent should still file a ticket
first so the human has context on landing.

| Trigger                                                  | Priority | Route to        |
|----------------------------------------------------------|----------|-----------------|
| Account compromise / suspicious logins                   | urgent   | Security        |
| Chargeback threat or legal language ("lawyer", "sue")    | urgent   | Billing + Legal |
| Reported data loss or accidental deletion                | urgent   | On-call SRE     |
| Service downtime claim with customer impact > 1 hour     | high     | On-call SRE     |
| Refund request > $100 outside the discretionary window   | high     | Billing         |
| Customer explicitly asks to speak to a manager / human   | high     | Senior support  |
| Repeated (3rd+) ticket for the same root cause           | medium   | Tier 2          |

## Escalation note conventions

When escalating, add an internal note that includes:

1. **One-sentence summary** of what the customer wants.
2. **What you've already tried / verified** (e.g. "confirmed propagation
   delay via /status endpoint at 14:32 UTC").
3. **The KB article(s)** you cited.
4. **Anything the customer was promised** ("told them we'd respond
   within 30 min").

Format: `ESCALATION: <one-line summary> | did: <…> | promised: <…>`
