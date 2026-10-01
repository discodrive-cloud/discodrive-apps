<script setup>
import { ref, onMounted } from 'vue'
import { RefreshCw, Folder, FileText } from 'lucide-vue-next'
import { api } from '../lib/api.js'
import { t, errorText, isNodeGone } from '../lib/i18n.js'
const props = defineProps({ node: { type: Object, default: null } })
const emit = defineEmits(['changed', 'busy'])
const items = ref([]), busy = ref(false), error = ref(''), notice = ref(''), pending = ref(null)
async function load() {
  items.value = props.node ? (await api.versions(props.node.id)).sort((a,b) => b.version-a.version) : await api.trash()
}
async function run(action) {
  if (busy.value) return
  busy.value = true; emit('busy', true); error.value = ''; notice.value = ''
  try { await action() } catch(e) { error.value = errorText(e); if (isNodeGone(e)) emit('changed') }
  finally { busy.value = false; emit('busy', false) }
}
async function restore(item) {
  await run(async () => {
    await api.undelete(item.id)
    notice.value = t('recovery.restored')
    await load(); emit('changed')
  })
}
async function confirm() {
  const action = pending.value
  pending.value = null
  await run(async () => {
    if (props.node) {
      await api.restoreVersion(props.node.id, action.version); notice.value = t('recovery.restored')
      await load(); emit('changed')
      return
    }
    // Relist whatever the outcome: a refused purge (409) may still have removed other items.
    try {
      if (action.all) await api.emptyTrash()
      else await api.purge(action.id)
    } finally {
      await load().catch(() => {}); emit('changed')
    }
  })
}
onMounted(() => run(load))
</script>

<template>
  <section class="flex min-h-0 flex-col gap-4" :aria-busy="busy">
    <div class="flex flex-wrap items-center gap-3">
      <h2 class="min-w-0 flex-1 break-words text-base font-semibold">{{ node?.name || t('recovery.trash') }}</h2>
      <button class="btn-ghost" :disabled="busy" :aria-label="t('common.refresh')" @click="run(load)"><RefreshCw :size="16" :class="{'animate-spin': busy}" /></button>
      <button v-if="!node" class="btn-ghost text-danger" :disabled="busy || !items.length" @click="pending = {all:true}">{{ t('recovery.empty') }}</button>
    </div>
    <p v-if="node" class="text-sm text-muted">{{ t('recovery.versionHint') }}</p>
    <p v-if="error" role="alert" class="break-words text-sm text-danger">{{ error }}</p>
    <p v-if="notice" role="status" class="text-sm text-muted">{{ notice }}</p>
    <div v-if="pending" class="space-y-3 border border-line p-3" role="alert">
      <p class="break-words text-sm font-semibold">{{ pending.name || (node ? `${t('recovery.version')} ${pending.version}` : t('recovery.empty')) }}</p>
      <p class="text-sm">{{ t(node ? 'recovery.versionHint' : 'recovery.purgeWarning') }}</p>
      <div class="flex flex-wrap justify-end gap-2">
        <button class="btn-ghost" :disabled="busy" @click="pending = null">{{ t('common.cancel') }}</button>
        <button class="btn-ghost text-danger" :disabled="busy" @click="confirm">{{ t(node ? 'recovery.restore' : 'recovery.purge') }}</button>
      </div>
    </div>
    <p v-if="!items.length && !busy && !error" class="py-8 text-center text-sm text-muted">{{ t(node ? 'recovery.emptyVersions' : 'recovery.emptyTrash') }}</p>
    <ul class="min-h-0 divide-y divide-line overflow-auto">
      <li v-for="item in items" :key="node ? item.version : item.id" class="flex flex-wrap items-center gap-3 py-3">
        <component v-if="!node" :is="item.is_dir ? Folder : FileText" :size="16" class="shrink-0 text-muted" />
        <div class="min-w-0 flex-1">
          <p class="break-words text-sm">{{ node ? `${t('recovery.version')} ${item.version}` : item.name }}</p>
          <p v-if="item.deleted_at" class="text-xs text-muted">{{ new Date(item.deleted_at).toLocaleString() }}</p>
          <p v-if="item.is_conflict_loser" class="text-xs text-muted">{{ t('recovery.conflict') }}</p>
        </div>
        <button class="btn-ghost" :disabled="busy || !!pending" @click="node ? pending = item : restore(item)">{{ t('recovery.restore') }}</button>
        <button v-if="!node" class="btn-ghost text-danger" :disabled="busy || !!pending" @click="pending = item">{{ t('recovery.purge') }}</button>
      </li>
    </ul>
  </section>
</template>
