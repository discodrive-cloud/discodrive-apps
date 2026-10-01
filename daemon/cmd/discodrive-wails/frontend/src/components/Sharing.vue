<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../lib/api.js'
import { t, errorText, isNodeGone } from '../lib/i18n.js'
const props = defineProps({ node: {type: Object, required: true} })
const emit = defineEmits(['busy', 'changed'])
const byLink = ref(true), email = ref(''), days = ref(7), shares = ref([]), link = ref(''), createdID = ref('')
const busy = ref(false), error = ref('')
async function load() { shares.value = await api.shares(props.node.id) }
async function run(action) {
  if(busy.value) return
  busy.value = true; emit('busy', true); error.value = ''
  try { await action() } catch(e) { error.value = errorText(e); if (isNodeGone(e)) emit('changed') }
  finally { busy.value = false; emit('busy', false) }
}
async function create() {
  await run(async () => {
    const result = await api.createShare(props.node.id, byLink.value ? '' : email.value.trim(), days.value)
    if(result.url) { link.value = result.url; createdID.value = result.share_id }
    await load()
  })
}
async function revoke(id) {
  await run(async () => {
    await api.revokeShare(id)
    if(createdID.value === id) { link.value = ''; createdID.value = '' }
    await load()
  })
}
async function copy() { await run(async () => { await api.copyText(link.value) }) }
onMounted(() => run(load))
</script>

<template>
  <div class="space-y-4 text-sm" :aria-busy="busy">
    <p class="break-words font-semibold">{{ node.name }}</p>
    <form class="space-y-3" @submit.prevent="create">
      <label class="block">{{ t('share.method') }}
        <select v-model="byLink" class="mt-1 w-full rounded border border-line bg-panel2 px-2 py-1.5 text-ink focus:border-accent" :disabled="busy"><option :value="true">{{ t('share.link') }}</option><option :value="false">{{ t('share.user') }}</option></select>
      </label>
      <label v-if="!byLink" class="block">{{ t('share.email') }}<input v-model="email" type="email" required class="mt-1 w-full rounded border border-line bg-panel2 px-2 py-1.5 text-ink focus:border-accent" :disabled="busy" /></label>
      <label class="block">{{ t('share.expiry') }}
        <select v-model="days" class="mt-1 w-full rounded border border-line bg-panel2 px-2 py-1.5 text-ink focus:border-accent" :disabled="busy"><option :value="0">{{ t('share.forever') }}</option><option :value="1">{{ t('share.day') }}</option><option :value="7">{{ t('share.week') }}</option><option :value="30">{{ t('share.month') }}</option></select>
      </label>
      <p class="text-muted">{{ t('share.readOnly') }}</p>
      <button class="btn-accent" :disabled="busy || (!byLink && !email.trim())">{{ t(byLink ? 'share.create' : 'share.grant') }}</button>
    </form>
    <p v-if="error" role="alert" class="break-words text-danger">{{ error }}</p>
    <div v-if="link" class="space-y-2"><p class="select-text break-all">{{ link }}</p><button class="btn-ghost" :disabled="busy" @click="copy">{{ t('share.copy') }}</button></div>
    <h3 class="font-semibold">{{ t('share.existing') }}</h3>
    <p v-if="!shares.length && !busy && !error" class="text-muted">{{ t('share.empty') }}</p>
    <ul class="divide-y divide-line"><li v-for="share in shares" :key="share.share_id" class="flex items-center gap-3 py-2">
      <div class="min-w-0 flex-1"><p class="break-words">{{ share.email || t('share.link') }}</p><p v-if="share.expires_at" class="text-xs text-muted">{{ new Date(share.expires_at).toLocaleString() }}</p></div>
      <button class="btn-ghost text-danger" :disabled="busy" @click="revoke(share.share_id)">{{ t('share.revoke') }}</button>
    </li></ul>
  </div>
</template>
