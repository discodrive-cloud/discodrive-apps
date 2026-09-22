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
function onUnpaired() { ready.value = false }
const browserRef = ref(null)
const vaultsRef = ref(null)
let unsubDrop = null

onMounted(async () => {
  // Apply saved preferences before showing the UI.
  const s = (await api.getSettings()) || {}
  applyTheme(s.theme || 'dark')
  setLocale(s.lang || 'en')

  ready.value = await api.ready()
  // Route native file drops to the active view (Files → current folder, Vaults → vault).
  unsubDrop = api.onEvent('upload:drop', (paths) => {
    if (view.value === 'vaults') vaultsRef.value?.dropFiles?.(paths)
    else browserRef.value?.dropFiles?.(paths)
  })
})
onUnmounted(() => unsubDrop && unsubDrop())

// Close any open vaults when leaving the Vaults view (no-op if none open), so plaintext
// does not linger and changes are saved.
watch(view, (next, prev) => {
  if (next === 'vaults') vaultsRef.value?.reload?.()
  if (prev === 'vaults' && next !== 'vaults') {
    api.closeAllVaults().then(() => vaultsRef.value?.reload?.()).catch(() => {})
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
