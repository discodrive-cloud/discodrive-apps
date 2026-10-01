<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { RefreshCw, FolderPlus, FolderOpen, Upload, FolderUp } from 'lucide-vue-next'
import { api } from '../lib/api.js'
import { t, errorText, isNodeGone } from '../lib/i18n.js'
import { sortEntries } from '../lib/format.js'
import Breadcrumbs from '../components/Breadcrumbs.vue'
import FileRow from '../components/FileRow.vue'
import Dialog from '../components/Dialog.vue'
import FolderPicker from '../components/FolderPicker.vue'
import Recovery from '../components/Recovery.vue'
import Sharing from '../components/Sharing.vue'
import UploadPanel from '../components/UploadPanel.vue'

const historyNode = ref(null), shareNode = ref(null), resourceBusy = ref(false)
const stack = ref([{ id: '', relPath: '', name: 'root' }])
const entries = ref([])
const busy = ref(false)
const initializing = ref(true)
const loadError = ref(false)
const status = ref('')

const current = () => stack.value[stack.value.length - 1]

async function reload() {
  const target = current()
  const targetId = target.id
  const targetRelPath = target.relPath
  const nodes = await api.list(targetRelPath)
  // Drop stale responses: the user may have navigated to a different folder while this
  // listing was in flight.
  const now = current()
  if (now.id !== targetId || now.relPath !== targetRelPath) return
  entries.value = sortEntries(nodes)
}

// enter is the double-click action: drill into folders, open (download+launch) files.
async function enter(node) {
  if (!node.isDir) {
    await rowOp(node, () => api.openFile(node.id))
    return
  }
  stack.value.push({ id: node.id, relPath: node.relPath, name: node.name })
  selectedId.value = ''
  await reload()
}

const selectedId = ref('')
const onSelect = (n) => { selectedId.value = n.id }

async function goTo(index) {
  stack.value = stack.value.slice(0, index + 1)
  await reload()
}

async function refresh() {
  if (busy.value) return
  busy.value = true
  loadError.value = false
  status.value = ''
  try {
    await api.refresh()
    await reload()
    status.value = ''
  } catch (e) {
    loadError.value = true
    status.value = errorText(e)
  } finally {
    initializing.value = false
    busy.value = false
  }
}

const busyIds = ref(new Set())

// rowOp runs a per-row operation with a busy indicator on that row. Only re-entrant
// clicks on the SAME row are ignored (pin downloads, so it is not instant); other
// rows stay operable concurrently.
async function rowOp(n, fn) {
  if (busyIds.value.has(n.id)) return
  busyIds.value.add(n.id)
  status.value = ''
  try {
    await fn()
    await reload()
  } catch (e) {
    status.value = errorText(e)
    if (isNodeGone(e)) await reload().catch(() => {})
  } finally {
    busyIds.value.delete(n.id)
  }
}
const onPin = (n) => rowOp(n, () => api.pin(n.id))
const onUnpin = (n) => rowOp(n, () => api.unpin(n.id))
const onRemoveLocal = (n) => rowOp(n, () => api.removeLocal(n.id))
const onReveal = (n) => rowOp(n, () => api.reveal(n.id))

// --- mutations (new folder / rename / delete / move) ---
const dialog = ref(null)   // {kind:'newFolder'|'rename'|'delete', node?, name}
const moveNode = ref(null) // node being moved, or null

// After a mutation: reload, then pull from the server and reload again. Debounced
// (trailing, 300ms, single pending timer) so a burst of mutations or upload:done events
// collapses into one refresh instead of firing a request per event.
let afterMutationTimer = null
function afterMutation() {
  clearTimeout(afterMutationTimer)
  afterMutationTimer = setTimeout(() => {
    afterMutationTimer = null
    reload()
    api.refresh().then(reload).catch(() => {})
  }, 300)
}

function openNewFolder() { dialog.value = { kind: 'newFolder', name: '' } }
const onRename = (n) => { dialog.value = { kind: 'rename', node: n, name: n.name } }
const onDelete = (n) => { dialog.value = { kind: 'delete', node: n } }
const onMove = (n) => { moveNode.value = n }

async function confirmDialog() {
  const d = dialog.value
  try {
    if (d.kind === 'newFolder') await api.newFolder(current().id, d.name.trim())
    else if (d.kind === 'rename') await api.rename(d.node.id, d.name.trim())
    else if (d.kind === 'delete') await api.del(d.node.id)
    dialog.value = null
    await afterMutation()
  } catch (e) {
    status.value = errorText(e)
    if (isNodeGone(e)) { dialog.value = null; await reload().catch(() => {}) }
  }
}

async function confirmMove(parentId) {
  try {
    await api.move(moveNode.value.id, parentId)
    moveNode.value = null
    await afterMutation()
  } catch (e) {
    status.value = errorText(e)
    if (isNodeGone(e)) { moveNode.value = null; await reload().catch(() => {}) }
  }
}

// --- uploads (chunked, via backend events) ---
const uploads = ref({}) // id -> {id, name, sent, total, state}
const uploadList = computed(() => Object.values(uploads.value))
const dragOver = ref(false)

async function pickAndUpload() {
  const paths = await api.pickFiles()
  if (paths && paths.length) api.uploadPaths(current().id, current().relPath, paths)
}
async function pickFolderAndUpload() {
  const dir = await api.pickFolder()
  if (dir) api.uploadPaths(current().id, current().relPath, [dir])
}
function onDragOver(e) { e.preventDefault(); dragOver.value = true }
function onDragLeave() { dragOver.value = false }
function onDrop(e) { e.preventDefault(); dragOver.value = false } // native OnFileDrop carries the paths

const unsubs = []
onMounted(() => {
  // Cached files stay usable while the initial server refresh is pending.
  reload().catch(() => {}).finally(refresh)
  unsubs.push(api.onEvent('upload:progress', (e) => { uploads.value[e.id] = { ...e, state: 'uploading' } }))
  unsubs.push(api.onEvent('upload:done', (e) => {
    uploads.value[e.id] = { ...(uploads.value[e.id] || e), state: 'done' }
    afterMutation()
    setTimeout(() => { delete uploads.value[e.id] }, 4000)
  }))
  unsubs.push(api.onEvent('upload:error', (e) => { uploads.value[e.id] = { ...e, state: 'error' } }))
})
onUnmounted(() => {
  unsubs.forEach((u) => u && u())
  clearTimeout(afterMutationTimer)
})

// Called by App when files are dropped while the Files view is active.
function dropFiles(paths) {
  if (paths && paths.length) api.uploadPaths(current().id, current().relPath, paths)
}

defineExpose({ refresh, reload, current, dropFiles })
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div class="flex items-center gap-2 border-b border-line px-4 py-2">
      <Breadcrumbs :frames="stack" @go="goTo" />
      <button class="btn-ghost ml-auto" @click="pickAndUpload">
        <Upload :size="14" />
        {{ t('browser.upload') }}
      </button>
      <button class="btn-ghost" :title="t('browser.folder')" @click="pickFolderAndUpload">
        <FolderUp :size="14" />
        {{ t('browser.folder') }}
      </button>
      <button class="btn-ghost" @click="openNewFolder">
        <FolderPlus :size="14" />
        {{ t('browser.newFolder') }}
      </button>
      <button class="btn-ghost" :disabled="busy" @click="refresh">
        <RefreshCw :size="14" :class="busy ? 'animate-spin' : ''" />
        {{ t('common.refresh') }}
      </button>
      <button class="btn-ghost" :title="t('settings.openCache')" @click="api.revealCache()">
        <FolderOpen :size="14" />
        {{ t('browser.downloads') }}
      </button>
    </div>

    <div v-if="status" class="flex items-center gap-2 border-b border-danger/30 bg-danger/10 px-4 py-1.5 text-xs text-danger">
      <span class="min-w-0 flex-1 truncate">{{ status }}</span>
      <button class="shrink-0 text-danger/70 hover:text-danger" @click="status = ''">✕</button>
    </div>

    <div
      class="min-h-0 flex-1 overflow-auto p-2"
      :class="dragOver ? 'ring-2 ring-inset ring-accent/50' : ''"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
    >
      <div v-if="dragOver" class="mt-20 text-center text-sm text-accent">{{ t('browser.dropHere') }}</div>
      <div v-else-if="!entries.length && (initializing || busy)" role="status" aria-live="polite" class="mt-20 flex flex-col items-center gap-3 text-sm text-muted">
        <RefreshCw :size="24" class="animate-spin motion-reduce:animate-none" aria-hidden="true" />
        <span>{{ t('browser.loading') }}</span>
      </div>
      <div v-else-if="!entries.length && loadError" role="status" class="mt-20 flex flex-col items-center gap-3 text-sm text-muted">
        <span>{{ t('browser.loadFailed') }}</span>
        <button class="btn-ghost" @click="refresh">{{ t('common.refresh') }}</button>
      </div>
      <div v-else-if="!entries.length" class="mt-20 text-center text-sm text-muted">
        {{ t('browser.empty') }}
      </div>
      <ul v-else class="select-none">
        <FileRow
          v-for="node in entries"
          :key="node.id"
          :node="node"
          :busy="busyIds.has(node.id)"
          :selected="selectedId === node.id"
          @share="shareNode = $event"
          @history="historyNode = $event"
          @open="enter"
          @select="onSelect"
          @pin="onPin"
          @unpin="onUnpin"
          @removeLocal="onRemoveLocal"
          @reveal="onReveal"
          @rename="onRename"
          @del="onDelete"
          @move="onMove"
        />
      </ul>
    </div>

    <Dialog :open="!!historyNode" :title="t('recovery.versions')" @close="!resourceBusy && (historyNode = null)">
      <Recovery v-if="historyNode" :key="historyNode.id" :node="historyNode" @busy="resourceBusy = $event" @changed="refresh" />
    </Dialog>
    <Dialog :open="!!shareNode" :title="t('share.title')" @close="!resourceBusy && (shareNode = null)">
      <Sharing v-if="shareNode" :key="shareNode.id" :node="shareNode" @busy="resourceBusy = $event" @changed="refresh" />
    </Dialog>
    <Dialog :open="!!dialog && dialog.kind === 'newFolder'" :title="t('dialog.newFolder')" @close="dialog = null">
      <input
        v-model="dialog.name"
        class="w-full rounded border border-line bg-panel2 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent"
        :placeholder="t('browser.folderName')"
        @keyup.enter="confirmDialog"
      />
      <template #footer>
        <button class="btn-ghost" @click="dialog = null">{{ t('common.cancel') }}</button>
        <button class="btn-accent" :disabled="!dialog.name.trim()" @click="confirmDialog">{{ t('common.create') }}</button>
      </template>
    </Dialog>

    <Dialog :open="!!dialog && dialog.kind === 'rename'" :title="t('dialog.rename')" @close="dialog = null">
      <input
        v-model="dialog.name"
        class="w-full rounded border border-line bg-panel2 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent"
        @keyup.enter="confirmDialog"
      />
      <template #footer>
        <button class="btn-ghost" @click="dialog = null">{{ t('common.cancel') }}</button>
        <button class="btn-accent" :disabled="!dialog.name.trim()" @click="confirmDialog">{{ t('common.rename') }}</button>
      </template>
    </Dialog>

    <Dialog :open="!!dialog && dialog.kind === 'delete'" :title="t('dialog.delete')" @close="dialog = null">
      <p class="text-sm text-muted">{{ t('browser.deleteConfirm', { name: dialog.node.name }) }}</p>
      <template #footer>
        <button class="btn-ghost" @click="dialog = null">{{ t('common.cancel') }}</button>
        <button class="btn-accent !bg-danger/15 !text-danger !ring-danger/30" @click="confirmDialog">{{ t('common.delete') }}</button>
      </template>
    </Dialog>

    <FolderPicker
      :open="!!moveNode"
      :title="moveNode ? t('browser.moveTitle', { name: moveNode.name }) : ''"
      :exclude-id="moveNode ? moveNode.id : ''"
      @pick="confirmMove"
      @close="moveNode = null"
    />

    <UploadPanel :uploads="uploadList" @dismiss="(id) => delete uploads[id]" />
  </div>
</template>
