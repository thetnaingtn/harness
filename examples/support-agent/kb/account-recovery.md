# Account Recovery

When a customer reports they've lost access or their account was
compromised, follow this script. Do **not** improvise: every step here
exists because of a past incident.

## Step 1 — Triage in one question

Ask: "Are you locked out of your account, or are you still logged in
but seeing activity you don't recognise?"

- **Locked out** → password-reset flow (see `password-reset` article).
- **Unfamiliar activity** → suspected compromise (continue below).

## Step 2 — Suspected compromise

1. **Do not ask for the password**, ever.
2. Tell the customer to immediately:
   - Sign out of every active session from Settings → Security → Sign
     out everywhere.
   - Rotate their password via the standard reset flow.
   - Rotate any API keys via Dashboard → API → Keys → Rotate.
   - Enable 2FA if not already on.
3. File a ticket with subject `Possible account compromise` and
   priority `urgent`.
4. Add an internal note listing what the customer reported (IPs,
   timestamps, what they noticed).
5. **Escalate to Security** via `escalate_to_human` — do not attempt
   to investigate access logs yourself.

## Step 3 — What we tell them

> "I've filed an urgent ticket and our security team will reach out
> within 30 minutes. While you wait, please rotate your credentials
> using the steps above. If you see any further suspicious activity,
> reply to this thread with timestamps and we'll add them to the
> investigation."

Never confirm or deny what the security team will find — that's their
call to make.
