# Pro & Team Licensing (Polar.sh)

- **Purchase & Key Delivery:** Subscribe to [Pro or Team](./#pricing) via Polar.sh. Your license key (`AFCPRO-...` or `AFCTEAM-...`) is issued immediately in the Polar Customer Portal and confirmation email.
- **Pass via Environment Variable (Recommended):** Set `AFC_LICENSE_KEY` in your shell or GitHub Actions repository secret (`${{ secrets.AFC_LICENSE_KEY }}`). Avoid hardcoding keys in `chaos-spec.yaml` or workflow files.
- **Automatic Secret Redaction:** `afc` automatically redacts `AFCPRO-*`, `AFCTEAM-*`, Bearer tokens, and common API keys from captured agent `stderr` and reports.
- **Offline Grace Window:** Validated keys are cached locally for 24 hours (with a 72-hour grace window if `api.polar.sh` is temporarily unreachable).
- **Test Your Key:**

```bash
AFC_LICENSE_KEY="AFCPRO-..." afc activate
```
