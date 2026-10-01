<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { HardDrive, Files, Shield, Trash2, Settings as SettingsIcon } from 'lucide-vue-next'
import { api } from './lib/api.js'
import { t, setLocale } from './lib/i18n.js'
import { applyTheme } from './lib/theme.js'
import Recovery from './components/Recovery.vue'
import Browser from './views/Browser.vue'
import Vaults from './views/Vaults.vue'
import Pairing from './views/Pairing.vue'
import Settings from './views/Settings.vue'

const ready = ref(false)
const view = ref('files')
function onPaired() { view.value = 'files'; ready.value = true }
function onUnpaired() { ready.value = false; vaultCloseError.value = ''; quitError.value = '' }
const browserRef = ref(null)
const vaultsRef = ref(null)
let unsubDrop = null
let unsubQuit = null
const quitError = ref('')
const vaultCloseError = ref('')
const legacyVaultPath = ref('')
const legacyError = ref('')
async function refreshLegacyVaults() {
  try { legacyVaultPath.value = await api.legacyVaultPath(); legacyError.value = '' }
  catch (e) { legacyError.value = String(e) }
}
async function revealLegacyVaults() {
  try { await api.revealLegacyVaults() }
  catch (e) { legacyError.value = String(e) }
}

onMounted(async () => {
  // Apply saved preferences before showing the UI.
  const s = (await api.getSettings()) || {}
  applyTheme(s.theme || 'dark')
  setLocale(s.lang || 'en')

  unsubQuit = api.onEvent('quit:blocked', (error) => { quitError.value = String(error) })
  ready.value = await api.ready()
  await refreshLegacyVaults()
  // Route native file drops to the active view (Files → current folder, Vaults → vault).
  unsubDrop = api.onEvent('upload:drop', (paths) => {
    if (view.value === 'vaults') vaultsRef.value?.dropFiles?.(paths)
    else browserRef.value?.dropFiles?.(paths)
  })
})
onUnmounted(() => { unsubDrop?.(); unsubQuit?.() })

// Close any open vaults when leaving the Vaults view (no-op if none open), so plaintext
// does not linger and changes are saved. If closing fails, surface the error and
// keep navigation available: Settings contains the recovery/forced sign-out flow.
watch(view, (next, prev) => {
  if (next === 'vaults') vaultsRef.value?.reload?.()
  if (prev === 'vaults' && next !== 'vaults') {
    api.closeAllVaults().then(() => {
      vaultCloseError.value = ''
      vaultsRef.value?.reload?.()
    }).catch((e) => {
      vaultCloseError.value = String(e)
      vaultsRef.value?.showError?.(String(e))
    })
  }
})

const tabs = [
  { id: 'files', icon: Files, label: 'nav.files' },
  { id: 'vaults', icon: Shield, label: 'nav.vaults' },
  { id: 'trash', icon: Trash2, label: 'recovery.trash' },
  { id: 'settings', icon: SettingsIcon, label: 'nav.settings' },
]
</script>

<template>
  <div class="flex h-full flex-col">
    <header class="flex items-center gap-3 border-b border-line bg-panel/60 px-4 py-3 backdrop-blur">
      <HardDrive :size="18" class="text-accent" />
      <div class="text-sm font-semibold tracking-wide text-ink">DiscoDrive</div>
      <nav v-if="ready" class="ml-4 flex items-center gap-1">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          class="flex items-center gap-1.5 rounded-md px-2.5 py-1 text-sm transition"
          :class="view === tab.id ? 'bg-accent/15 text-accent' : 'text-muted hover:text-ink hover:bg-ink/5'"
          @click="view = tab.id"
        >
          <component :is="tab.icon" :size="14" /> {{ t(tab.label) }}
        </button>
      </nav>
    </header>
    <div v-if="legacyVaultPath || legacyError" role="alert" class="border-b border-danger/30 bg-danger/10 px-4 py-3 text-sm">
      <p>{{ t('app.legacyVaults') }}</p>
      <p class="mt-1 break-words text-muted">{{ legacyVaultPath || legacyError }}</p>
      <p v-if="legacyVaultPath && legacyError" class="mt-1 break-words text-muted">{{ legacyError }}</p>
      <button v-if="legacyVaultPath" class="btn-ghost mt-2" @click="revealLegacyVaults">{{ t('vaults.openFolder') }}</button>
      <button class="btn-ghost mt-2" @click="refreshLegacyVaults">{{ t('common.refresh') }}</button>
    </div>
    <div v-if="vaultCloseError" role="alert" class="border-b border-danger/30 bg-danger/10 px-4 py-3 text-sm">
      <p class="break-words">{{ vaultCloseError }}</p>
      <button class="btn-ghost mt-2" @click="vaultCloseError = ''">{{ t('common.close') }}</button>
    </div>
    <div v-if="quitError" role="alert" class="border-b border-danger/30 bg-danger/10 px-4 py-3 text-sm">
      <p>{{ t('app.quitBlocked') }}</p>
      <p class="mt-1 break-words text-muted">{{ quitError }}</p>
      <button class="btn-ghost mt-2" @click="quitError = ''">{{ t('common.close') }}</button>
    </div>
    <main class="min-h-0 flex-1">
      <Pairing v-if="!ready" @paired="onPaired" />
      <template v-else>
        <Browser ref="browserRef" v-show="view === 'files'" class="h-full" />
        <Vaults ref="vaultsRef" v-show="view === 'vaults'" class="h-full" />
        <Recovery v-if="view === 'trash'" class="h-full p-4" @changed="browserRef?.refresh?.()" />
        <Settings v-if="view === 'settings'" class="h-full" @unpaired="onUnpaired" />
      </template>
    </main>
  </div>
</template>
