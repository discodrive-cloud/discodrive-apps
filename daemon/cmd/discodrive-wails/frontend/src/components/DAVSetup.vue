<script setup>
import { ref, onMounted, watch } from 'vue'
import { api } from '../lib/api.js'
import { t } from '../lib/i18n.js'
import Dialog from './Dialog.vue'
const state = ref(null)
const calendars = ref(true)
const contacts = ref(true)
const busy = ref(false)
const error = ref('')
const copied = ref(false)
const automatic = ref(false)
const confirm = ref(false)
watch([calendars, contacts], () => { if (state.value) state.value.url = '' })
function fail(e) { error.value = String(e).includes('404') ? 'dav.updateServer' : 'dav.error' }
async function load() {
  busy.value = true; error.value = ''
  try {
    state.value = await api.getDAVSetup()
    calendars.value = state.value.access.caldav
    contacts.value = state.value.access.carddav
  } catch (e) { fail(e) }
  finally { busy.value = false }
}
onMounted(load)
async function prepare(withoutPassword = false) {
  if (busy.value) return
  busy.value = true; error.value = ''; copied.value = false
  state.value.url = ''
  try {
    const method = withoutPassword ? api.prepareDAVAutomatic : api.prepareDAV
    const result = await method(calendars.value, contacts.value)
    state.value = { ...state.value, ...result, access: state.value.access }
    automatic.value = withoutPassword
    if (withoutPassword) await api.openPairURL(result.url)
  } catch (e) {
    if (withoutPassword && (String(e).includes('404') || String(e).includes('encrypted setup unavailable'))) error.value = 'dav.automaticUnavailable'
    else fail(e)
    // Preparation can fail after saving a credential; keep revocation available.
    try { const current = await api.getDAVSetup(); state.value.credential = current.credential }
    catch { /* Retain the original preparation error. */ }
  } finally { busy.value = false }
}
async function copy() {
  try { await api.copyText(state.value.credential.password); copied.value = true }
  catch (e) { fail(e) }
}
async function revoke() {
  busy.value = true; confirm.value = false; error.value = ''
  try { await api.revokeDAV(); state.value.credential = null; state.value.url = ''; copied.value = false }
  catch (e) { fail(e) }
  finally { busy.value = false }
}
</script>
<template>
  <section v-if="state?.supported || error" class="space-y-3 border-t border-line pt-5" aria-labelledby="dav-title">
    <h2 id="dav-title" class="text-sm font-semibold text-ink">{{ t('dav.title') }}</h2>
    <p class="text-xs leading-relaxed text-muted">{{ t('dav.hint') }}</p>
    <template v-if="state?.supported">
      <label class="flex items-center gap-2 text-sm"><input v-model="calendars" type="checkbox" class="accent-accent" :disabled="busy || !state.access.caldav" />{{ t('dav.calendars') }}</label>
      <label class="flex items-center gap-2 text-sm"><input v-model="contacts" type="checkbox" class="accent-accent" :disabled="busy || !state.access.carddav" />{{ t('dav.contacts') }}</label>
      <p v-if="!state.access.caldav || !state.access.carddav" class="text-xs text-muted">{{ t('dav.disabled') }}</p>
      <button class="btn disabled:opacity-50" :disabled="busy || (!calendars && !contacts)" @click="prepare(false)">{{ t('dav.prepare') }}</button>
      <p class="text-xs leading-relaxed text-muted">{{ t('dav.profileSignatureHint') }}</p>
      <button class="btn disabled:opacity-50" :disabled="busy || (!calendars && !contacts)" @click="prepare(true)">{{ t('dav.automatic') }}</button>
      <p class="text-xs leading-relaxed text-muted">{{ t('dav.automaticHint') }}</p>
      <div v-if="state.credential" class="space-y-3">
        <button v-if="!automatic" class="btn-ghost" :disabled="busy" @click="copy">{{ t(copied ? 'dav.copied' : 'dav.copyPassword') }}</button>
        <button v-if="state.url" class="btn" :disabled="busy" @click="api.openPairURL(state.url)">{{ t('dav.install') }}</button>
        <p v-else class="text-xs text-muted">{{ t('dav.prepareAgain') }}</p>
        <p class="text-xs leading-relaxed text-muted">{{ t(automatic ? 'dav.automaticInstallHint' : 'dav.installHint') }}</p>
        <button class="btn-ghost !text-danger" :disabled="busy" @click="confirm = true">{{ t('dav.revoke') }}</button>
        <p class="text-xs leading-relaxed text-muted">{{ t('dav.independent') }}</p>
      </div>
    </template>
    <p v-if="busy" role="status" class="text-xs text-muted">{{ t('dav.preparing') }}</p>
    <button v-if="error && !state" class="btn-ghost" :disabled="busy" @click="load">{{ t('dav.retry') }}</button>
    <p v-if="error" role="alert" class="text-xs text-danger">{{ t(error) }}</p>
  </section>
  <Dialog :open="confirm" :title="t('dav.revoke')" @close="confirm = false">
    <p class="text-sm text-muted">{{ t('dav.revokeHint') }}</p>
    <div class="mt-5 flex justify-end gap-2"><button class="btn-ghost" @click="confirm = false">{{ t('common.cancel') }}</button><button class="btn !text-danger" @click="revoke">{{ t('dav.revoke') }}</button></div>
  </Dialog>
</template>
