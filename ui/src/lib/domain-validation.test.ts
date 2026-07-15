import { describe, expect, it } from 'vitest'
import { isLoopbackDomain, isValidDomain, MAX_DOMAIN_LENGTH } from './domain-validation'

describe('isLoopbackDomain', () => {
  it.each([
    ['localhost'],
    ['LOCALHOST'],
    ['  localhost  '],
    ['app.localhost'],
    ['a.b.localhost'],
    ['127.0.0.1'],
    ['127.255.255.254'],
    ['::1'],
  ])('accepts %s as loopback', (d) => {
    expect(isLoopbackDomain(d)).toBe(true)
  })

  it.each([
    ['app.example.com'],
    ['example.com'],
    ['myapp.local'], // mDNS, not loopback
    ['127.999.0.0'], // invalid IPv4 octet
    [''],
    ['127.0.0'], // too few octets
    ['127.0.0.0.1'], // too many
    ['::2'],
  ])('rejects %s as non-loopback', (d) => {
    expect(isLoopbackDomain(d)).toBe(false)
  })
})

describe('isValidDomain', () => {
  it.each([
    'app.example.com',
    'APP.Example.com',
    'my-app.example.com',
    'foo.xx',
    'localhost',
    'app.localhost',
    '127.0.0.1',
    '::1',
  ])('accepts %s', (d) => {
    expect(isValidDomain(d)).toBe(true)
  })

  it.each([
    '',
    '   ',
    'intranet', // no dot, not loopback
    'foo.x', // single-letter tld
    'app example.com',
    'app/example.com',
    'http://app.example.com',
    '-bad.example.com',
    'bad-.example.com',
  ])('rejects %s', (d) => {
    expect(isValidDomain(d)).toBe(false)
  })

  it('enforces the RFC 1035 length cap', () => {
    const tooLong = 'a' + 'b'.repeat(MAX_DOMAIN_LENGTH) + '.com'
    expect(isValidDomain(tooLong)).toBe(false)
  })
})
