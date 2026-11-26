# GitHub Action & CI Merge Gates

## OSS Core Workflow (Console Output)

```yaml
- name: Run agent-fault-check (OSS)
  uses: evhu-dev/agent-fault-check@{{VERSION}}
  with:
    spec-path: "chaos-spec.yaml"
    version: "{{VERSION}}"
    report-mode: "console"
```

## Pro / Team Workflow (SARIF Code Scanning + JUnit)

```yaml
permissions:
  contents: read
  security-events: write

steps:
  - uses: actions/checkout@v4

  - name: Run agent-fault-check Pro
    id: afc
    uses: evhu-dev/agent-fault-check@{{VERSION}}
    with:
      spec-path: "chaos-spec.yaml"
      version: "{{VERSION}}"
      release-repository: "evhu-dev/agent-fault-check-releases"
      license-key: ${{ secrets.AFC_LICENSE_KEY }}
      report-mode: "both"

  - name: Upload SARIF to GitHub Code Scanning
    if: always() && steps.afc.outputs.sarif-file != ''
    uses: github/codeql-action/upload-sarif@v3
    with:
      sarif_file: ${{ steps.afc.outputs.sarif-file }}
```

## Action Inputs & Outputs

| Name | Kind | Description |
| :--- | :--- | :--- |
| `spec-path` | Input | Path to `chaos-spec.yaml` (default: `chaos-spec.yaml`). |
| `version` | Input (required) | Pinned release tag (e.g. `{{VERSION}}`). |
| `report-mode` | Input | `console` (OSS) or `sarif`, `junit`, `both` (requires `release-repository`). |
| `release-repository` | Input | Set to `evhu-dev/agent-fault-check-releases` to download Pro binaries. |
| `license-key` | Input | Your Polar license key from `${{ secrets.AFC_LICENSE_KEY }}`. |
| `exit-code` | Output | CLI exit code (`0`, `1`, or `2`). |
| `sarif-file` / `junit-file` | Output | Paths to generated `afc-results.sarif` and `afc-results.xml`. |
