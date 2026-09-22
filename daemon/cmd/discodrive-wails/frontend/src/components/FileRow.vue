<script setup>
import { Share2, History, Folder, FileText, Star, Pencil, Trash2, FolderInput, Download, Pin, PinOff, CloudOff, Loader2 } from 'lucide-vue-next'
import { t } from '../lib/i18n.js'
defineProps({
  node: { type: Object, required: true },
  busy: { type: Boolean, default: false },
  selected: { type: Boolean, default: false },
})
const emit = defineEmits(['share', 'history', 'open', 'select', 'pin', 'unpin', 'removeLocal', 'reveal', 'rename', 'del', 'move'])
</script>

<template>
  <div
    tabindex="0" class="group flex select-none items-center gap-2 rounded px-2 py-1.5"
    :class="selected ? 'bg-accent/10 ring-1 ring-accent/30' : 'hover:bg-ink/5'"
    @click="emit('select', node)"
    @dblclick="emit('open', node)"
  >
    <component
      :is="node.isDir ? Folder : FileText"
      :size="16"
      :class="node.isDir ? 'shrink-0 text-accent' : 'shrink-0 text-muted'"
    />
    <span class="min-w-0 flex-1 truncate text-sm text-ink">{{ node.name }}</span>
    <Star v-if="!node.isDir && node.state === 'pinned'" :size="13" class="shrink-0 text-accent2" />
    <span v-else-if="!node.isDir && node.state === 'cached'" class="shrink-0 text-xs text-muted">●</span>

    <Loader2 v-if="busy" :size="15" class="ml-1 shrink-0 animate-spin text-accent" />
    <div v-else class="ml-1 hidden shrink-0 items-center gap-0.5 group-hover:flex group-focus-within:flex">
      <button class="btn-ghost px-1.5 py-1" :title="t('share.title')" :aria-label="t('share.title')" @click.stop="emit('share', node)"><Share2 :size="14" /></button>
      <button v-if="!node.isDir" class="btn-ghost px-1.5 py-1" :title="t('recovery.versions')" :aria-label="t('recovery.versions')" @click.stop="emit('history', node)"><History :size="14" /></button>
      <button v-if="!node.isDir" class="btn-ghost px-1.5 py-1" :title="t('row.download')" @click.stop="emit('reveal', node)"><Download :size="14" /></button>
      <button v-if="!node.isDir && node.state !== 'pinned'" class="btn-ghost px-1.5 py-1" :title="t('row.pin')" @click.stop="emit('pin', node)"><Pin :size="14" /></button>
      <button v-if="!node.isDir && node.state === 'pinned'" class="btn-ghost px-1.5 py-1" :title="t('row.unpin')" @click.stop="emit('unpin', node)"><PinOff :size="14" /></button>
      <button v-if="!node.isDir && node.state" class="btn-ghost px-1.5 py-1" :title="t('row.removeLocal')" @click.stop="emit('removeLocal', node)"><CloudOff :size="14" /></button>
      <button class="btn-ghost px-1.5 py-1" :title="t('row.move')" @click.stop="emit('move', node)"><FolderInput :size="14" /></button>
      <button class="btn-ghost px-1.5 py-1" :title="t('common.rename')" @click.stop="emit('rename', node)"><Pencil :size="14" /></button>
      <button class="btn-ghost px-1.5 py-1" :title="t('common.delete')" @click.stop="emit('del', node)"><Trash2 :size="14" class="text-danger" /></button>
    </div>
  </div>
</template>
