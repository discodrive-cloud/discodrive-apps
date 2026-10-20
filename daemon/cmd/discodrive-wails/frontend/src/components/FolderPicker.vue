<script setup>
import { ref, watch } from 'vue'
import { FolderOpen } from 'lucide-vue-next'
import { api } from '../lib/api.js'
import { t } from '../lib/i18n.js'
import Dialog from './Dialog.vue'
import FolderPickerNode from './FolderPickerNode.vue'

const props = defineProps({
  open: Boolean,
  title: { type: String, default: 'Move to…' },
  excludeId: { type: String, default: '' },
  confirmLabel: { type: String, default: '' }, // '' = "Move here"
})
const emit = defineEmits(['pick', 'close'])

const tree = ref([]) // [{id, relPath, name, expanded, children}]
const selected = ref({ id: '', relPath: '', name: 'root' })

async function foldersOf(relPath) {
  const nodes = await api.list(relPath)
  return (nodes || [])
    .filter((n) => n.isDir && n.id !== props.excludeId)
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }))
    .map((n) => ({ id: n.id, relPath: n.relPath, name: n.name, expanded: false, children: null }))
}

async function toggle(node) {
  node.expanded = !node.expanded
  if (node.expanded && !node.children) node.children = await foldersOf(node.relPath)
}
function select(node) { selected.value = node }

watch(() => props.open, async (v) => {
  if (v) {
    selected.value = { id: '', relPath: '', name: 'root' }
    tree.value = await foldersOf('')
  }
})
</script>

<template>
  <Dialog :open="open" :title="title" @close="emit('close')">
    <div class="max-h-72 overflow-auto rounded border border-line p-1 text-sm">
      <button
        class="flex w-full items-center gap-1.5 rounded px-2 py-1 text-left"
        :class="selected.id === '' ? 'bg-accent/15 text-accent' : 'text-ink hover:bg-ink/5'"
        @click="select({ id: '', relPath: '', name: 'root' })"
      >
        <FolderOpen :size="15" class="shrink-0 text-accent" /> {{ t('picker.root') }}
      </button>
      <FolderPickerNode
        v-for="n in tree"
        :key="n.id"
        :node="n"
        :selected-id="selected.id"
        @toggle="toggle"
        @select="select"
      />
    </div>
    <template #footer>
      <button class="btn-ghost" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button class="btn-accent" @click="emit('pick', selected.id, selected.relPath)">{{ confirmLabel || t('picker.moveHere') }}</button>
    </template>
  </Dialog>
</template>
