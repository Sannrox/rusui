# Timed macOS walkthrough record

> **Historical evidence.** This record is unverified and describes an
> unavailable clean-account run; it is not an operator procedure.

Record each duration on a clean macOS user account; exclude guest image
download time. Keep the account, credentials, repository, guest version, and
policy revision with the private run notes, not in this public guide.

| Step | Duration |
| --- | --- |
| `make all` and `rusui setup plan` | Not measured on a clean account |
| Fill model and scoped GitHub credentials; set the bound repo's implement policy | Not measured on a clean account |
| `rusui setup apply` and readiness check | Not measured on a clean account |
| Start pinned task and record session/turn IDs | Not measured on a clean account |
| Wait for the plane outcome and confirm the PR SHA | Not measured on a clean account |
| Total, excluding image download | Unverified; the clean-account run was unavailable in this environment |

The current evidence is the earlier live run recorded on #181, not a timed
clean-account walkthrough. This leaves the fifteen-minute target unverified;
no isolated clean account or model/write credentials were available here, so
the gap starts before `make all` and no later step was timed.
