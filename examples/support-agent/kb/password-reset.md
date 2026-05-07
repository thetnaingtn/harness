# Password Reset

For the standard "I can't log in / forgot my password" case. If the
customer reports unfamiliar activity in addition to being locked out,
treat it as a compromise — see `account-recovery`.

## The standard flow

1. Direct the customer to https://app.acme.example/reset and ask them
   to enter the email address they signed up with.
2. The reset email arrives within 2 minutes from `noreply@acme.example`.
3. The link is single-use and expires in 1 hour.

## Common failure modes

| Symptom                                     | Likely cause              | Action                                                              |
|---------------------------------------------|---------------------------|---------------------------------------------------------------------|
| No email after 5 minutes                    | Corporate spam filter     | Have customer check spam, then ask IT to allowlist `acme.example`.  |
| "Email not found" error                     | Wrong address or typo     | Confirm the spelling; check whether SSO is configured for them.     |
| Link expired                                | > 1 hour since send       | Issue a fresh reset; verify the customer is acting on the new one.  |
| SSO error after reset                       | SSO required by org       | Reset doesn't apply — customer must log in via their SSO provider.  |
| 2FA prompt after reset, no device available | Lost authenticator        | File a ticket; only Security can reset 2FA. **Do not** disable it.  |

## Don'ts

- Don't email a temporary password.
- Don't disable 2FA on the customer's behalf — always escalate.
- Don't ask the customer for any portion of their password "to verify".
