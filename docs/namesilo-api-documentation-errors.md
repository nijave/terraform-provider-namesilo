# NameSilo API documentation errors

Comparison of NameSilo's official Domain API reference against the live API,
compiled while building this provider. Every "live behavior" row below was
verified by direct calls against the production API in September 2026; every
"documentation says" row quotes the official reference at
`https://www.namesilo.com/api-reference` (per-operation pages fetched from its
`/api-reference/pages?uid=<group>/<operation>` backend).

"Documentation error" means the reference states something the live API does
not do. Items where our code was wrong and the reference was right are listed
in the appendix, not here.

## Verified documentation errors

| # | Operation | Documentation says | Live API does | Severity |
|---|-----------|--------------------|---------------|----------|
| 1 | `dnsSecListRecords` | Sample XML and JSON replies spell the record fields `<key_tag>` and `<digest_type>` | The XML reply uses `<keyTag>` and `<digestType>` (`<algorithm>` and `<digest>` match). A parser built on the documented spelling reads empty values and fails | High — silently breaks parsing |
| 2 | `contactUpdate` | Documents `nn` (Nickname) as an optional field, with "If you do not provide a nickname the existing nickname will be maintained" — implying providing one updates it | `nn` (and `nickname`) are ignored on update. The call returns `300 success` and the nickname is unchanged; nicknames are only settable at `contactAdd` | High — a documented write that silently no-ops |
| 3 | `listDomains` | Sample XML pager element order is `total`, `pageSize`, `page` | The paged reply emits `page`, `pageSize`, `total`. The reference's own JSON sample shows the live order, so the two samples contradict each other | Low — element order is not significant to conformant XML, but the samples disagree |
| 4 | `contactDelete` | The sample reply echoes `<operation>contactUpdate</operation>`, and the parameter text says "the contact profile record to update" | The operation is `contactDelete`; the sample was copied from the update page | Low — copy-paste error |
| 5 | `dnsSecListRecords` | The sample XML reply opens a third `<ds_record>` element and never closes it before `</reply>` | — | Low — malformed sample |
| 6 | `domainLock`, `domainUnlock`, `addAutoRenewal`, `removeAutoRenewal` | Each page's sample reply shows the already-in-state code (252/253/250/251) as *the* response | A first-time change returns `300 success`; the 25x codes appear only on repeats. The codes and detail strings themselves match live exactly | Low — misleading sample, correct codes |
| 7 | `contactAdd` | The `ct` (country) field is annotated "(4)" | ISO 3166-1 alpha-2 codes are two characters; the API has only ever been observed storing two. The "(4)" looks like a typo for "(2)" | Unverified — no four-character country was submitted to confirm which side is right |

## Broken or missing reference sections

| Section | Observation |
|---|---------|
| Response Codes tab | The reference site's content backend returns HTTP 404 for the response-codes page; the table is unreachable on the live site (retrieved 2026-09-23). Third-party mirrors carry partial copies |
| Available Operations index | The operations index XHR also 404s; per-operation pages remain reachable by direct `uid`, which is how the table above was compiled |
| `contactDetail` | Not documented — consistent with live behavior: the operation does not exist (code 107 "Invalid API operation") |

## Undocumented live behaviors

These are behaviors the reference does not describe. They are not errors in
the text; they are gaps a client implementer has to discover by probing.

| # | Area | Live behavior | Documentation status |
|---|------|---------------|---------------------|
| 1 | `dnsSecDeleteRecord` | Deleting a still-pending record returns `210 "There are no active records specified for deletion"` and the deletion nevertheless takes effect; deleting a settled record returns `300` | Not documented anywhere |
| 2 | `dnsSecDeleteRecord` | The `digest` is matched case-sensitively against the uppercased stored form, while `dnsSecAddRecord` accepts any case and `dnsSecListRecords` echoes uppercase | Not documented |
| 3 | `dnsSecAddRecord` | Adding a duplicate record returns `210 "SRS Error - Unable to update DS record: Parameter value policy error"`, not a 25x already-in-state code | Not documented |
| 4 | HTTP transport | Request bursts receive HTTP 503 with an XML body; waiting and retrying succeeds. NameSilo's support pages describe standard-vs-batch monitoring but the reference documents no 503 semantics or limits | Not documented in the reference |
| 5 | `listDomains` | A call without `page`/`pageSize` parameters returns no `<pager>` element at all; the paged form returns it | The reference only shows the paged form |
| 6 | `contactList` | The reply paginates at 1000 contacts via an `offset` parameter | Documented on the operation page; noted here because this provider's client does not use it (accounts in scope hold far fewer contacts) |
| 7 | `changeNameServers` | — | The parameter table says `domain` accepts "a comma-delimited list of up to 200 domains" while the description above it says "the provided domain name" (singular). Bulk mode was not tested; this provider sends one domain |

## Appendix: the reference was right, our code was wrong

Recorded so the mistakes are not repeated, and because item 1 corrected a
provider assumption rather than the reverse.

| # | Area | Documentation says | What actually happened |
|---|------|--------------------|------------------------|
| 1 | `contactList` reply fields | Base contact fields in replies use full snake_case (`first_name`, `last_name`, `nickname`, `company`, …); the TLD-specific fields use the abbreviated request names (`usnc`, `usap`, `calf`, `caln`, `caag`, `cawd`, `eucs`) | The original client parsed the reply with the abbreviated request names for all fields — wrong for every base field (live-verified fix in this provider). The TLD-specific spelling is taken from the documentation sample; no live account has ever returned those fields to confirm it |
| 2 | `contactDomainAssociate` | "This request is processed asynchronously. A successful API response only confirms that the contact update request has been accepted and queued for processing" | The original client assumed synchronous visibility and read the roles back immediately. The reference described the propagation window correctly; this provider now waits for it |
| 3 | `getDomainInfo`, `contactAdd`/`contactList` base fields, `changeNameServers`, toggle operations | Envelopes, fields, 25x codes and their detail strings | Verified live to match the reference exactly |
