<script setup>
import { ref, onMounted } from 'vue'
import { ShieldPlus, RefreshCw, Copy } from 'lucide-vue-next'
import { api } from '../lib/api.js'
import { t } from '../lib/i18n.js'
import VaultRow from '../components/VaultRow.vue'
import Dialog from '../components/Dialog.vue'

const vaults = ref([])
const busy = ref(false)
const busyIds = ref(new Set())
const status = ref('') // errors (red)
const notice = ref('') // info/success (neutral)
const dialog = ref(null) // {kind:'create'|'open'|'recovery', ...}
const activeVaultRel = ref('') // last opened vault — target for drag-dropped files

async function reload() {
  vaults.value = (await api.vaults()) || []
}

async function vaultOp(relPath, fn) {
  if (busyIds.value.has(relPath)) return
  busyIds.value.add(relPath)
  status.value = ''
  try {
    await fn()
    await reload()
  } catch (e) {
    status.value = String(e)
  } finally {
    busyIds.value.delete(relPath)
  }
}

function openCreate() { dialog.value = { kind: 'create', name: '', password: '' } }
function onOpen(v) { dialog.value = { kind: 'open', vault: v, password: '', phrase: '', mode: 'password' } }
const onOpenFolder = (v) => { api.openVaultFolder(v.relPath).catch((e) => (status.value = String(e))) }
const onClose = (v) => vaultOp(v.relPath, () => api.closeVault(v.relPath))
async function onAddFiles(v) {
  activeVaultRel.value = v.relPath
  const paths = await api.pickFiles()
  if (paths && paths.length) {
    try { await api.addFilesToVault(v.relPath, paths) } catch (e) { status.value = String(e) }
  }
}

// Called by App when files are dropped while the Vaults view is active: add them to the
// active open vault, falling back to the first open vault.
function dropFiles(paths) {
  if (!paths || !paths.length) return
  const open = vaults.value.filter((x) => x.open)
  const target = open.find((x) => x.relPath === activeVaultRel.value) || open[0]
  if (!target) {
    status.value = t('vaults.openFirst')
    return
  }
  notice.value = t('vaults.adding', { n: paths.length, name: target.name })
  api.addFilesToVault(target.relPath, paths)
    .then(() => { notice.value = t('vaults.added', { n: paths.length, name: target.name }) })
    .catch((e) => { status.value = String(e); notice.value = '' })
}

// The vault that drag-dropped files will be added to (active, else first open).
const dropTarget = () => {
  const open = vaults.value.filter((x) => x.open)
  return open.find((x) => x.relPath === activeVaultRel.value) || open[0] || null
}

async function confirmCreate() {
  const d = dialog.value
  busy.value = true
  status.value = ''
  try {
    const phrase = await api.createVault(d.name.trim(), d.password)
    await reload()
    // Show the recovery phrase — the only way back in if the password is lost.
    dialog.value = phrase ? { kind: 'recovery', phrase } : null
  } catch (e) {
    status.value = String(e)
    dialog.value = null
  } finally {
    busy.value = false
  }
}

function copyPhrase() {
  if (dialog.value?.phrase) {
    api.copyText(dialog.value.phrase)
    notice.value = t('vaults.copied')
  }
}

async function confirmOpen() {
  const d = dialog.value
  const v = d.vault
  const useRecovery = d.mode === 'recovery'
  const pw = d.password
  const phrase = d.phrase
  dialog.value = null
  await vaultOp(v.relPath, () =>
    useRecovery ? api.openVaultWithRecovery(v.relPath, phrase) : api.openVault(v.relPath, pw),
  )
  if (vaults.value.find((x) => x.relPath === v.relPath && x.open)) {
    activeVaultRel.value = v.relPath
  }
}

async function refresh() {
  busy.value = true
  try { await reload() } finally { busy.value = false }
}

// Called by App when an operation outside this view (e.g. closing all vaults on tab
// switch) fails, so the user still sees why.
function showError(msg) { status.value = msg }

onMounted(reload)
defineExpose({ reload, dropFiles, showError })
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div class="flex items-center gap-2 border-b border-line px-4 py-2">
      <div class="text-sm font-medium text-ink">{{ t('vaults.title') }}</div>
      <button class="btn-ghost ml-auto" @click="openCreate">
        <ShieldPlus :size="14" />
        {{ t('vaults.newVault') }}
      </button>
      <button class="btn-ghost" :disabled="busy" @click="refresh">
        <RefreshCw :size="14" :class="busy ? 'animate-spin' : ''" />
        {{ t('common.refresh') }}
      </button>
    </div>

    <div v-if="status" class="flex items-center gap-2 border-b border-danger/30 bg-danger/10 px-4 py-1.5 text-xs text-danger">
      <span class="min-w-0 flex-1 truncate">{{ status }}</span>
      <button class="shrink-0 text-danger/70 hover:text-danger" @click="status = ''">✕</button>
    </div>
    <div v-else-if="notice" class="flex items-center gap-2 border-b border-accent2/30 bg-accent2/10 px-4 py-1.5 text-xs text-accent2">
      <span class="min-w-0 flex-1 truncate">{{ notice }}</span>
      <button class="shrink-0 text-accent2/70 hover:text-accent2" @click="notice = ''">✕</button>
    </div>
    <div v-else-if="dropTarget()" class="border-b border-line px-4 py-1.5 text-xs text-muted">
      {{ t('vaults.dropHint', { name: dropTarget().name }) }}
    </div>

    <div class="min-h-0 flex-1 overflow-auto p-2">
      <div v-if="!vaults.length" class="mt-20 text-center text-sm text-muted">
        {{ t('vaults.empty') }}
      </div>
      <ul v-else>
        <VaultRow
          v-for="v in vaults"
          :key="v.relPath"
          :vault="v"
          :busy="busyIds.has(v.relPath)"
          @open="onOpen"
          @openFolder="onOpenFolder"
          @addFiles="onAddFiles"
          @close="onClose"
        />
      </ul>
    </div>

    <Dialog :open="!!dialog && dialog.kind === 'create'" :title="t('vaults.createTitle')" @close="dialog = null">
      <div class="space-y-2">
        <input
          v-model="dialog.name"
          class="w-full rounded border border-line bg-panel2 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent"
          :placeholder="t('vaults.vaultName')"
        />
        <input
          v-model="dialog.password"
          type="password"
          class="w-full rounded border border-line bg-panel2 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent"
          :placeholder="t('vaults.password')"
          @keyup.enter="confirmCreate"
        />
      </div>
      <template #footer>
        <button class="btn-ghost" @click="dialog = null">{{ t('common.cancel') }}</button>
        <button class="btn-accent" :disabled="!dialog.name.trim() || !dialog.password || busy" @click="confirmCreate">
          {{ busy ? t('pairing.pairing') : t('common.create') }}
        </button>
      </template>
    </Dialog>

    <Dialog :open="!!dialog && dialog.kind === 'open'" :title="dialog && dialog.kind === 'open' ? t('vaults.openTitle', { name: dialog.vault.name }) : ''" @close="dialog = null">
      <template v-if="dialog && dialog.kind === 'open'">
        <input
          v-if="dialog.mode === 'password'"
          v-model="dialog.password"
          type="password"
          class="w-full rounded border border-line bg-panel2 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent"
          :placeholder="t('vaults.password')"
          @keyup.enter="confirmOpen"
        />
        <textarea
          v-else
          v-model="dialog.phrase"
          rows="3"
          class="w-full resize-none rounded border border-line bg-panel2 px-2 py-1.5 font-mono text-xs text-ink outline-none focus:border-accent"
          :placeholder="t('vaults.phrasePlaceholder')"
        />
        <button
          class="mt-2 text-xs text-accent hover:underline"
          @click="dialog.mode = dialog.mode === 'password' ? 'recovery' : 'password'"
        >
          {{ dialog.mode === 'password' ? t('vaults.forgot') : t('vaults.usePassword') }}
        </button>
      </template>
      <template #footer>
        <button class="btn-ghost" @click="dialog = null">{{ t('common.cancel') }}</button>
        <button
          class="btn-accent"
          :disabled="dialog.mode === 'password' ? !dialog.password : !dialog.phrase.trim()"
          @click="confirmOpen"
        >
          {{ t('common.open') }}
        </button>
      </template>
    </Dialog>

    <Dialog :open="!!dialog && dialog.kind === 'recovery'" :title="t('vaults.recoveryTitle')" @close="dialog = null">
      <p class="mb-2 text-xs text-muted">{{ t('vaults.recoveryWarn') }}</p>
      <textarea
        :value="dialog.phrase"
        readonly
        rows="4"
        class="w-full resize-none rounded border border-line bg-panel2 px-2 py-1.5 font-mono text-xs text-ink outline-none"
        @focus="(e) => e.target.select()"
      />
      <template #footer>
        <button class="btn-ghost" @click="copyPhrase"><Copy :size="13" /> {{ t('common.copy') }}</button>
        <button class="btn-accent" @click="dialog = null">{{ t('vaults.savedIt') }}</button>
      </template>
    </Dialog>
  </div>
</template>
