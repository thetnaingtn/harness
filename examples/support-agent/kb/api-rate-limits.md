# API Rate Limits

Default limits for Acme API clients. When a customer reports 429s or
unexpected throttling, check their plan tier first via the dashboard
before escalating.

## Default limits per plan

| Plan       | Requests / minute | Requests / day | Concurrent streams |
|------------|-------------------|----------------|--------------------|
| Free       | 20                | 1,000          | 1                  |
| Starter    | 120               | 50,000         | 5                  |
| Growth     | 600               | 500,000        | 25                 |
| Enterprise | Custom (≥ 1,200)  | Custom         | Custom             |

## How to read the headers

Every response includes:

- `X-RateLimit-Limit` — the per-minute cap
- `X-RateLimit-Remaining` — calls left in the current window
- `X-RateLimit-Reset` — Unix epoch (seconds) when the window rolls over
- `Retry-After` — present on 429 responses; seconds to wait

Tell customers to back off using `Retry-After`, not arbitrary delays.

## When 429s aren't actually rate limits

A surprising fraction of "rate limit" tickets are something else. Rule
these out before escalating:

1. **Auth failure cascading**: 401s in a tight retry loop look like
   429s when the customer reads aggregate request graphs. Confirm by
   checking response codes individually.
2. **Same key on multiple processes**: the limit is per-key, not
   per-process. A customer running 4 workers on a Starter plan will
   share 120 RPM across all of them.
3. **Quota vs rate**: hitting the daily quota returns 429 too. Check
   `X-RateLimit-Limit` — if it's the daily number, they need a higher
   plan, not a rate-limit increase.

## Requesting an increase

- Free / Starter / Growth: increases are not granted; suggest upgrading.
- Enterprise: file a ticket with priority `high`, subject
  `Rate-limit increase request`, and CC the account's CSM.
