<script setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { Sun, Moon, FolderOpen, Globe, ChevronDown } from 'lucide-vue-next'
import { api } from '../lib/api.js'
import { t, setLocale, languages, errorText } from '../lib/i18n.js'
import { applyTheme } from '../lib/theme.js'
import Dialog from '../components/Dialog.vue'
import DAVSetup from '../components/DAVSetup.vue'

const emit = defineEmits(['unpaired'])

const settings = ref({ theme: 'dark', lang: 'en', openAtLogin: false, startMinimized: false })
const server = ref('')
const cache = ref('')
const confirmUnpair = ref(false)
const unpairError = ref('')
const confirmUnpairAnyway = ref(false)
const unpairBusy = ref(false)
const recovered = ref(null) // { recoveryDir, recovered } after a forced sign-out kept work
const partialKept = ref(null) // the same, when the forced sign-out stopped part-way
const sync = ref({ folder: '', enabled: false, state: 'stopped' })
const syncBusy = ref(false)
const syncError = ref('')
const confirmSyncDelete = ref(false)
const syncStateKey = computed(() => {
  if (sync.value.errorKind === 'bulk_delete') return 'fullSync.bulkDelete'
  return 'fullSync.' + ({ idle: 'ready', offline: 'error' }[sync.value.state] || sync.value.state)
})
let syncTimer
let mounted = true
let readingSync = false
async function readSync() {
  if (readingSync) return
  readingSync = true
  try { const state = await api.getFullSync(); if (mounted) sync.value = state }
  catch { if (mounted) syncError.value = 'fullSync.error' }
  finally { readingSync = false }
}
onUnmounted(() => { mounted = false; clearInterval(syncTimer) })
async function chooseSyncFolder() {
  syncBusy.value = true; syncError.value = ''
  try { sync.value = await api.chooseSyncFolder(t('fullSync.chooseHint')) }
  catch { syncError.value = 'fullSync.folderError' }
  finally { syncBusy.value = false; await readSync() }
}
async function toggleSync(enabled) {
  syncBusy.value = true; syncError.value = ''
  try { sync.value = await api.setFullSync(enabled) }
  catch { syncError.value = 'fullSync.error' }
  finally { syncBusy.value = false; await readSync() }
}
async function confirmDeletions() {
  confirmSyncDelete.value = false
  syncBusy.value = true; syncError.value = ''
  try { await api.confirmSyncDeletions() }
  catch { syncError.value = 'fullSync.error' }
  finally { syncBusy.value = false; await readSync() }
}

onMounted(async () => {
  settings.value = (await api.getSettings()) || settings.value
  server.value = await api.serverURL()
  cache.value = await api.cachePath()
  await readSync()
  if (mounted) syncTimer = setInterval(readSync, 2000)
})

async function persist() {
  await api.saveSettings(settings.value)
}

function setTheme(theme) {
  settings.value.theme = theme
  applyTheme(theme)
  persist()
}
function setLang(lang) {
  settings.value.lang = lang
  setLocale(lang)
  persist()
}
function toggleOpenAtLogin() {
  settings.value.openAtLogin = !settings.value.openAtLogin
  persist()
}
function toggleStartMinimized() {
  settings.value.startMinimized = !settings.value.startMinimized
  persist()
}
// tr is t() with an English fallback, for messages whose translations are still to come.
function tr(key, english, params) {
  const s = t(key, params)
  if (s !== key) return s
  let out = english
  for (const k in params || {}) out = out.replaceAll(`{${k}}`, () => String(params[k]))
  return out
}
function errString(e) {
  return typeof e === 'string' ? e : (e?.message ?? String(e))
}

// Unpairing stops, with nothing changed, when an open vault cannot be saved or decrypted
// files of an earlier session are still on disk: the backend tags that
// "vault_save_failed:". The user may then sign out anyway, which keeps that work in a
// recovery folder first.
async function doChangeServer() {
  confirmUnpair.value = false
  unpairError.value = ''
  try {
    await api.unpair()
  } catch (e) {
    if (errString(e).startsWith('vault_save_failed:')) {
      unpairError.value = tr('settings.unpairVaultSaveFailed',
        "Couldn't save an open vault, or decrypted vault files from an earlier session are still on this computer, so this device stays signed in. Close your vaults and try again, or sign out anyway.")
      confirmUnpairAnyway.value = true
    } else {
      unpairError.value = errorText(e)
    }
    return
  }
  emit('unpaired')
}

async function doUnpairAnyway() {
  unpairBusy.value = true
  unpairError.value = ''
  partialKept.value = null
  let res
  try {
    res = await api.unpairAnyway()
  } catch (e) {
    res = { error: errString(e) }
  } finally {
    unpairBusy.value = false
  }
  confirmUnpairAnyway.value = false
  if (res?.error) {
    // Still signed in. Vaults closed before the failure have their changes in the
    // recovery folder: say where, or the user would not know they left the vault.
    unpairError.value = res.error.startsWith('vault_recovery_failed:')
      ? tr('settings.unpairRecoveryFailed',
        "Couldn't keep the unsaved vault changes, so this device stays signed in: {error}",
        { error: res.error.replace(/^vault_recovery_failed:\s*/, '') })
      : errorText(res.error)
    if (res.recovered?.length) partialKept.value = res
    return
  }
  if (res?.recovered?.length) {
    recovered.value = res // shown until the user has read where the work is
    return
  }
  emit('unpaired')
}

function closeRecovered() {
  recovered.value = null
  emit('unpaired')
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div class="flex items-center gap-2 border-b border-line px-4 py-2">
      <div class="text-sm font-medium text-ink">{{ t('settings.title') }}</div>
    </div>

    <div class="min-h-0 flex-1 overflow-auto p-4">
      <div class="mx-auto max-w-md space-y-5">
        <!-- Theme -->
        <div>
          <div class="mb-1.5 text-xs font-medium text-muted">{{ t('settings.theme') }}</div>
          <div class="flex gap-2">
            <button
              class="btn flex-1 justify-center ring-1"
              :class="settings.theme === 'dark' ? 'bg-accent/15 text-accent ring-accent/30' : 'text-muted ring-line hover:text-ink'"
              @click="setTheme('dark')"
            >
              <Moon :size="14" /> {{ t('settings.dark') }}
            </button>
            <button
              class="btn flex-1 justify-center ring-1"
              :class="settings.theme === 'light' ? 'bg-accent/15 text-accent ring-accent/30' : 'text-muted ring-line hover:text-ink'"
              @click="setTheme('light')"
            >
              <Sun :size="14" /> {{ t('settings.light') }}
            </button>
          </div>
        </div>

        <!-- Language -->
        <div>
          <div class="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-muted">
            <Globe :size="13" /> {{ t('settings.language') }}
          </div>
          <div class="relative">
            <select
              :value="settings.lang"
              class="w-full appearance-none rounded border border-line bg-panel2 px-2 py-1.5 pr-8 text-sm text-ink outline-none focus:border-accent"
              @change="setLang($event.target.value)"
            >
              <option v-for="l in languages" :key="l.code" :value="l.code">{{ l.label }}</option>
            </select>
            <ChevronDown
              :size="15"
              class="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-muted"
            />
          </div>
        </div>

        <!-- Server -->
        <div>
          <div class="mb-1.5 text-xs font-medium text-muted">{{ t('settings.server') }}</div>
          <div class="flex items-center gap-2">
            <input :value="server" readonly class="min-w-0 flex-1 truncate rounded border border-line bg-panel2 px-2 py-1.5 text-sm text-ink outline-none" />
            <button class="btn-ghost shrink-0" @click="confirmUnpair = true">{{ t('settings.changeServer') }}</button>
          </div>
          <p v-if="unpairError" role="alert" class="mt-1.5 break-words text-xs text-danger">{{ unpairError }}</p>
          <div v-if="partialKept" class="mt-1.5 text-xs text-muted">
            <p>{{ t('settings.unpairPartialKept', { dir: partialKept.recoveryDir }) }}</p>
            <ul class="mt-1 space-y-1 text-ink">
              <li v-for="p in partialKept.recovered" :key="p" class="break-all font-mono">{{ p }}</li>
            </ul>
          </div>
        </div>

        <DAVSetup />

        <!-- Cache -->
        <div>
          <div class="mb-1.5 text-xs font-medium text-muted">{{ t('settings.cache') }}</div>
          <div class="flex items-center gap-2">
            <input :value="cache" readonly class="min-w-0 flex-1 truncate rounded border border-line bg-panel2 px-2 py-1.5 text-xs text-ink outline-none" />
            <button class="btn-ghost shrink-0" @click="api.revealCache()"><FolderOpen :size="14" /> {{ t('settings.openCache') }}</button>
          </div>
        </div>

        <section class="rounded-lg border border-line bg-panel2 p-4 space-y-3" aria-labelledby="folder-sync-title">
          <h2 id="folder-sync-title" class="text-sm font-semibold text-ink">{{ t('fullSync.title') }}</h2>
          <p class="text-xs leading-relaxed text-muted">{{ t('fullSync.description') }}</p>
          <div class="flex items-center gap-2">
            <span class="min-w-0 flex-1 break-all text-xs text-ink">{{ sync.folder || t('fullSync.noFolder') }}</span>
            <button class="btn-ghost shrink-0 disabled:opacity-50" :disabled="syncBusy || sync.enabled" @click="chooseSyncFolder">
              <FolderOpen :size="14" /> {{ t('fullSync.choose') }}
            </button>
          </div>
          <label class="flex items-center gap-2 text-sm text-ink">
            <input type="checkbox" class="accent-accent" :checked="sync.enabled"
              :disabled="syncBusy || (!sync.enabled && (!sync.folder || !server))"
              @change="toggleSync($event.target.checked)" />
            {{ t('fullSync.enable') }}
          </label>
          <p role="status" class="text-xs text-muted">{{ t(syncStateKey) }}</p>
          <p v-if="syncError" role="alert" class="text-xs text-danger">{{ t(syncError) }}</p>
          <button v-if="sync.errorKind === 'bulk_delete'" class="btn-ghost !text-danger" :disabled="syncBusy" @click="confirmSyncDelete = true">
            {{ t('fullSync.confirmDelete') }}
          </button>
          <button v-if="sync.backup" class="btn-ghost" @click="api.revealSyncBackup()">{{ t('fullSync.backup') }}</button>
          <p class="text-xs leading-relaxed text-muted">{{ t('fullSync.backupHint') }}</p>
        <details class="border-t border-line pt-4">
          <summary class="cursor-pointer text-sm font-medium">{{ t('activity.title') }}</summary>
          <div class="mt-3 space-y-3 text-sm" aria-live="polite">
            <p>{{ t(syncStateKey) }}</p>
            <template v-if="sync.activity?.phase"><p>{{ t('activity.' + sync.activity.phase) }}</p><p class="break-all">{{ sync.activity.path }}</p></template>
            <p>{{ t('activity.completed') }}: {{ sync.activity?.completed || 0 }}</p>
            <p class="text-xs text-muted">{{ t('activity.hint') }}</p>
            <p v-if="sync.last_error" class="break-words text-danger">{{ sync.last_error }}</p>
            <h3 v-if="sync.activity?.errors?.length" class="font-semibold">{{ t('activity.errors') }}</h3>
            <ul class="divide-y divide-line"><li v-for="failure in [...(sync.activity?.errors || [])].reverse()" :key="failure.path" class="space-y-1 py-2">
              <p class="break-all font-medium">{{ failure.path }}</p><p class="break-words text-muted">{{ failure.message }}</p><p class="text-xs text-muted">{{ new Date(failure.time).toLocaleString() }}</p>
            </li></ul>
          </div>
        </details>
        </section>

        <!-- Open at login -->
        <label class="flex cursor-pointer items-center gap-2">
          <input type="checkbox" :checked="settings.openAtLogin" class="accent-accent" @change="toggleOpenAtLogin" />
          <span class="text-sm text-ink">{{ t('settings.openAtLogin') }}</span>
        </label>

        <!-- Start minimized (only meaningful when launched at login) -->
        <label
          class="flex items-center gap-2 pl-6"
          :class="settings.openAtLogin ? 'cursor-pointer' : 'cursor-not-allowed opacity-50'"
        >
          <input
            type="checkbox"
            :checked="settings.startMinimized"
            :disabled="!settings.openAtLogin"
            class="accent-accent"
            @change="toggleStartMinimized"
          />
          <span class="text-sm text-ink">{{ t('settings.startMinimized') }}</span>
        </label>
      </div>
    </div>

    <Dialog :open="confirmSyncDelete" :title="t('fullSync.confirmDelete')" @close="confirmSyncDelete = false">
      <p class="text-sm text-muted">{{ t('fullSync.deleteWarning') }}</p>
      <template #footer>
        <button class="btn-ghost" @click="confirmSyncDelete = false">{{ t('common.cancel') }}</button>
        <button class="btn-accent !bg-danger/15 !text-danger" @click="confirmDeletions">{{ t('fullSync.confirmDelete') }}</button>
      </template>
    </Dialog>

    <Dialog :open="confirmUnpairAnyway" :title="tr('settings.unpairAnywayTitle', 'Sign out anyway?')" @close="confirmUnpairAnyway = false">
      <p class="text-sm text-muted">{{ tr('settings.unpairAnywayBody', "Your open vaults can't be saved to the server. If you sign out anyway, their unsaved changes are kept on this computer, in a recovery folder in your home folder (DiscoDrive Recovery), and not uploaded. Encrypted copies open with the vault's password.") }}</p>
      <template #footer>
        <button class="btn-ghost" :disabled="unpairBusy" @click="confirmUnpairAnyway = false">{{ t('common.cancel') }}</button>
        <button class="btn-accent !bg-danger/15 !text-danger !ring-danger/30" :disabled="unpairBusy" @click="doUnpairAnyway">{{ tr('settings.unpairAnyway', 'Sign out anyway') }}</button>
      </template>
    </Dialog>
    <Dialog :open="!!recovered" :title="tr('settings.unpairRecoveredTitle', 'Signed out')" @close="closeRecovered">
      <p class="text-sm text-muted">{{ tr('settings.unpairRecoveredBody', 'Unsaved vault changes were kept in {dir}. Folders marked NOT ENCRYPTED hold decrypted files: move them somewhere safe or delete them once you no longer need them.', { dir: recovered?.recoveryDir }) }}</p>
      <ul class="mt-2 space-y-1 text-xs text-ink">
        <li v-for="p in recovered?.recovered || []" :key="p" class="break-all font-mono">{{ p }}</li>
      </ul>
      <template #footer>
        <button class="btn-accent" @click="closeRecovered">{{ t('common.close') }}</button>
      </template>
    </Dialog>
    <Dialog :open="confirmUnpair" :title="t('settings.changeServerTitle')" @close="confirmUnpair = false">
      <p class="text-sm text-muted">{{ t('settings.changeServerConfirm') }}</p>
      <template #footer>
        <button class="btn-ghost" @click="confirmUnpair = false">{{ t('common.cancel') }}</button>
        <button class="btn-accent !bg-danger/15 !text-danger !ring-danger/30" @click="doChangeServer">{{ t('settings.changeServer') }}</button>
      </template>
    </Dialog>
  </div>
</template>
