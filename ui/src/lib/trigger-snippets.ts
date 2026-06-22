// buildTriggerCurl renders a copy-paste shell snippet that POSTs to the
// trigger URL. `bearer` is the value that goes after `Authorization: ` —
// the caller decides whether to embed a real token or use a
// `$NANOKU_TRIGGER_TOKEN` env var placeholder.
export function buildTriggerCurl(url: string, bearer: string): string {
  const body = JSON.stringify({ tag: '<sha>', commit_message: '<msg>' })
  return [
    'BODY=' + shellQuote(body),
    'curl -fsS -X POST ' + shellQuote(url) + ' \\',
    '  -H "Authorization: Bearer ' + bearer + '" \\',
    '  -H "Content-Type: application/json" \\',
    '  -d "$BODY"',
  ].join('\n')
}

export function shellQuote(s: string): string {
  return "'" + s.replace(/'/g, "'\\''") + "'"
}

// buildWorkflowYaml is the GitHub Actions deploy template.
//
// IMPORTANT: the YAML never embeds the actual token. It references the
// user's repo secret via `${{ secrets.NANOKU_TRIGGER_TOKEN }}` — public
// repos are fine to share this file as-is. The token's first appearance
// to the user is in the modal, where they copy it into the secret.
export function buildWorkflowYaml(appName: string, url: string): string {
  // GitHub Actions uses ${{ ... }} for expressions. In JS template literals
  // we must escape every literal `$` so it isn't treated as interpolation.
  const $ = '$'
  return [
    'name: deploy',
    'on:',
    '  push:',
    '    branches: [main]',
    '',
    'jobs:',
    '  build:',
    '    runs-on: ubuntu-latest',
    '    permissions:',
    '      contents: read',
    '      packages: write',
    '    steps:',
    '      - uses: actions/checkout@v4',
    '      - uses: docker/setup-buildx-action@v3',
    '      - uses: docker/login-action@v3',
    '        with:',
    '          registry: ghcr.io',
    `          username: ${$}{{ github.actor }}`,
    `          password: ${$}{{ secrets.GITHUB_TOKEN }}`,
    '      - uses: docker/build-push-action@v5',
    '        with:',
    '          push: true',
    `          tags: ghcr.io/${$}{{ github.repository_owner }}/${$}{{ github.event.repository.name }}:${$}{{ github.sha }}`,
    '',
    `      - name: Notify Nanoku (${appName})`,
    '        env:',
    `          URL: ${url}`,
    `          TOKEN: ${$}{{ secrets.NANOKU_TRIGGER_TOKEN }}`,
    `          SHA: ${$}{{ github.sha }}`,
    `          MSG: ${$}{{ github.event.head_commit.message }}`,
    '        run: |',
    '          BODY=$(jq -nc --arg t "$SHA" --arg m "$MSG" \'{tag: $t, commit_message: $m}\')',
    '          curl -fsS -X POST "$URL" \\',
    '            -H "Authorization: Bearer $TOKEN" \\',
    '            -H "Content-Type: application/json" \\',
    '            -d "$BODY"',
    '',
  ].join('\n')
}
