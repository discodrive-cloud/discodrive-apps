import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { errorText, setLocale, languages } from '../src/lib/i18n.js'

const keys = ['vaults.location', 'vaults.changeLocation', 'vaults.locationTitle', 'vaults.createHere',
  'errors.vaultNameTaken', 'errors.vaultParentMissing']

test('vault creation errors are localized in every language', () => {
  const codes = languages.map((l) => l.code ?? l)
  assert.equal(codes.length, 7)
  const seen = new Set()
  for (const code of codes) {
    setLocale(code)
    for (const err of ['vault_name_taken: a file or folder with this name already exists',
      new Error('vault_parent_missing: the destination folder no longer exists')]) {
      const text = errorText(err)
      assert.doesNotMatch(text, /^vault_|^errors\./, `${code}: ${text}`)
      seen.add(text)
    }
  }
  assert.equal(seen.size, 14, 'each language has its own two texts')
  setLocale('en')
})

test('every language has the new vault keys', () => {
  const src = readFileSync(new URL('../src/lib/i18n.js', import.meta.url), 'utf8')
  for (const k of keys) assert.equal(src.split(`'${k}':`).length - 1, 7, k)
})

test('the create dialog passes the chosen parent folder to the backend', () => {
  const src = readFileSync(new URL('../src/views/Vaults.vue', import.meta.url), 'utf8')
  assert.match(src, /api\.createVault\(d\.parent, d\.name\.trim\(\), d\.password\)/)
  const api = readFileSync(new URL('../src/lib/api.js', import.meta.url), 'utf8')
  assert.match(api, /CreateVault\(parentRelPath, name, password\)/)
})
