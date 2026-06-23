import { describe, it, expect } from 'vitest'
import {
  buildTriggerCurl,
  buildWorkflowYaml,
  shellQuote,
} from './trigger-snippets'

describe('shellQuote', () => {
  it('wraps a plain string in single quotes', () => {
    expect(shellQuote('hello')).toBe("'hello'")
  })

  it("escapes embedded single quotes with '\\''", () => {
    expect(shellQuote("a'b")).toBe("'a'\\''b'")
  })

  it('handles empty string', () => {
    expect(shellQuote('')).toBe("''")
  })
})

describe('buildTriggerCurl', () => {
  it('renders a curl POST with the URL, bearer, and JSON body', () => {
    const out = buildTriggerCurl('https://nanoku.example/api/apps/blog/trigger', 'sek')
    expect(out).toContain("curl -fsS -X POST 'https://nanoku.example/api/apps/blog/trigger'")
    expect(out).toContain('Authorization: Bearer sek')
    expect(out).toContain('Content-Type: application/json')
    expect(out).toContain('<sha>')
    expect(out).toContain('<msg>')
    expect(out).toContain('BODY=')
    expect(out).toContain('-d "$BODY"')
  })

  it('shell-quotes the URL so special chars do not break the command', () => {
    const out = buildTriggerCurl("https://h/?a=b'c", 'sek')
    expect(out).toContain("'https://h/?a=b'\\''c'")
  })

  it('embeds the bearer verbatim (it lives inside double quotes, so single quotes stay literal)', () => {
    const out = buildTriggerCurl('https://h/trigger', "tok'en")
    expect(out).toContain('-H "Authorization: Bearer tok\'en"')
  })

  it('emits the body as a JSON-encoded payload with tag and commit_message', () => {
    const out = buildTriggerCurl('https://h/trigger', 'sek')
    const bodyLine = out.split('\n').find((l) => l.startsWith('BODY='))!
    const quoted = bodyLine.slice('BODY='.length)
    const inner = quoted.slice(1, -1).replace(/'\\''/g, "'")
    expect(JSON.parse(inner)).toEqual({ tag: '<sha>', commit_message: '<msg>' })
  })
})

describe('buildWorkflowYaml', () => {
  const yaml = buildWorkflowYaml('blog', 'https://nanoku.example/api/apps/blog/trigger')

  it('starts with the workflow header and triggers on push to main', () => {
    expect(yaml).toMatch(/^name: deploy\n/)
    expect(yaml).toContain('on:')
    expect(yaml).toContain('push:')
    expect(yaml).toContain('branches: [main]')
  })

  it('uses GitHub Actions expression syntax literally (no JS interpolation)', () => {
    // If `${{ ... }}` ever got interpreted as JS template-literal interpolation,
    // `github.actor` would render as `undefined`. Confirm the literals are intact.
    expect(yaml).toContain('${{ github.actor }}')
    expect(yaml).toContain('${{ secrets.GITHUB_TOKEN }}')
    expect(yaml).toContain('${{ github.repository_owner }}')
    expect(yaml).toContain('${{ github.sha }}')
    expect(yaml).toContain('${{ github.event.head_commit.message }}')
    expect(yaml).not.toContain('undefined')
  })

  it('references the trigger token via secrets, never inlined', () => {
    expect(yaml).toContain('${{ secrets.NANOKU_TRIGGER_TOKEN }}')
    expect(yaml).not.toMatch(/NANOKU_TRIGGER_TOKEN:\s*[A-Za-z0-9]/)
  })

  it('embeds the appName in the step name', () => {
    expect(yaml).toContain('Notify Nanoku (blog)')
  })

  it('exposes the URL as a step env var', () => {
    expect(yaml).toContain('URL: https://nanoku.example/api/apps/blog/trigger')
    expect(yaml).toContain('TOKEN:')
    expect(yaml).toContain('SHA:')
    expect(yaml).toContain('MSG:')
  })

  it('runs the curl inside the run block against $URL with Bearer $TOKEN', () => {
    expect(yaml).toContain('curl -fsS -X POST "$URL" \\')
    expect(yaml).toContain('Authorization: Bearer $TOKEN')
    expect(yaml).toContain('-d "$BODY"')
  })
})