import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { ref, watch, nextTick } from 'vue'

// Execute the actual navigation watcher so the test covers the caller, not a
// disconnected helper with the same logic. No browser or extra test dependency.
const source = readFileSync(new URL('../src/App.vue', import.meta.url), 'utf8')
const watcher = source.slice(source.indexOf('watch(view,'), source.indexOf('\nconst tabs'))
test('failed vault save leaves recovery settings accessible and reports the error', async () => {
  const view = ref('vaults'), vaultCloseError = ref('')
  let offline = true
  const api = { closeAllVaults: async () => { if (offline) throw new Error('server offline') } }
  const vaultsRef = ref({ reload() {}, showError() {} })
  const stop = new Function('watch', 'view', 'api', 'vaultsRef', 'vaultCloseError', `return ${watcher}`)(watch, view, api, vaultsRef, vaultCloseError)
  try {
    view.value = 'settings'
    await nextTick(); await new Promise(resolve => setImmediate(resolve)); await nextTick()
    assert.equal(view.value, 'settings')
    assert.match(vaultCloseError.value, /server offline/)
    offline = false
    view.value = 'vaults'; await nextTick()
    view.value = 'settings'; await nextTick(); await new Promise(resolve => setImmediate(resolve))
    assert.equal(vaultCloseError.value, '')
  } finally { stop() }
})
