<script setup>
import { ref } from 'vue'
import { Link, Loader2, ShieldAlert } from 'lucide-vue-next'
import { api } from '../lib/api.js'
import { t, errorText } from '../lib/i18n.js'
import Dialog from '../components/Dialog.vue'

const emit = defineEmits(['paired'])

const server = ref('https://')
const status = ref('')
const error = ref('')
const busy = ref(false)
const pairing = ref(null) // {userCode, verifyUrl, deviceCode, interval}
// The certificate PairInit fetched when the server is not trusted by the system:
// {host, fingerprint, subject, issuer, notAfter, selfSigned}. The backend keeps the pin;
// trusting only tells it to go ahead.
const cert = ref(null)
let certURL = ''

function isAllowedServerURL(u) {
  let parsed
  try {
    parsed = new URL(u)
  } catch {
    return false
  }
  if (parsed.protocol === 'https:') return true
  const host = parsed.hostname.toLowerCase()
  return parsed.protocol === 'http:' && (host === 'localhost' || host === '127.0.0.1' || host === '::1' || host === '[::1]')
}

async function pair() {
  const url = server.value.trim()
  if (!url || url === 'https://') {
    error.value = t('pairing.needUrl')
    return
  }
  if (!isAllowedServerURL(url)) {
    error.value = t('pairing.httpsRequired')
    return
  }
  busy.value = true
  error.value = ''
  status.value = t('pairing.contacting')
  try {
    const info = await api.pairInit(url)
    if (info.needsTrust) {
      cert.value = info.certificate
      certURL = url
      status.value = ''
      return
    }
    await awaitApproval(url, info)
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

async function awaitApproval(url, info) {
  pairing.value = info
  status.value = t('pairing.approve')
  await api.pairPoll(url, info.deviceCode, info.interval)
  emit('paired')
}

function fail(e) {
  error.value = errorText(e)
  pairing.value = null
  status.value = ''
}

async function trust() {
  cert.value = null
  busy.value = true
  error.value = ''
  status.value = t('pairing.contacting')
  try {
    await awaitApproval(certURL, await api.trustAndPair())
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

function expiry(s) {
  const d = new Date(s)
  return isNaN(d) ? s : d.toLocaleDateString()
}

async function reopenLink() {
  if (!pairing.value) return
  try {
    await api.openPairURL()
  } catch (e) {
    error.value = errorText(e)
  }
}
</script>

<template>
  <div class="card mx-auto mt-16 max-w-md p-6">
    <div class="mb-1 text-base font-semibold text-ink">{{ t('pairing.title') }}</div>
    <p class="mb-4 text-xs text-muted">{{ t('pairing.subtitle') }}</p>

    <label class="mb-1 block text-xs text-muted">{{ t('pairing.serverUrl') }}</label>
    <input
      v-model="server"
      class="w-full rounded border border-line bg-panel2 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent"
      placeholder="https://your-server.example.com"
      :disabled="busy"
      @keyup.enter="pair"
    />

    <button class="btn-accent mt-3 w-full justify-center" :disabled="busy" @click="pair">
      <Loader2 v-if="busy" :size="14" class="animate-spin" />
      {{ busy ? t('pairing.pairing') : t('pairing.pair') }}
    </button>

    <div v-if="pairing" class="mt-5 rounded-lg border border-line bg-panel2/60 p-4 text-center">
      <div class="text-xs text-muted">{{ t('pairing.enterCode') }}</div>
      <div class="my-1 font-mono text-2xl font-bold tracking-widest text-accent">{{ pairing.userCode }}</div>
      <button class="btn-ghost mx-auto mt-1" @click="reopenLink">
        <Link :size="13" /> {{ t('pairing.openLink') }}
      </button>
    </div>

    <p v-if="status" class="mt-4 text-center text-xs text-muted">{{ status }}</p>
    <p v-if="error" class="mt-4 rounded border border-danger/30 bg-danger/10 px-3 py-2 text-center text-xs text-danger">{{ error }}</p>

    <Dialog :open="!!cert" :title="t('pairing.certTitle')" @close="cert = null">
      <div v-if="cert" class="space-y-2 text-xs">
        <div class="flex items-center gap-2 text-ink">
          <ShieldAlert :size="16" class="text-danger" />
          <span class="font-medium">{{ cert.host }}</span>
          <span v-if="cert.selfSigned" class="rounded border border-danger/30 bg-danger/10 px-1.5 py-0.5 text-danger">{{ t('pairing.certSelfSigned') }}</span>
        </div>
        <div>
          <div class="text-muted">{{ t('pairing.certFingerprint') }}</div>
          <div class="select-text break-all rounded bg-panel2 px-2 py-1 font-mono text-ink">{{ cert.fingerprint }}</div>
        </div>
        <div class="grid grid-cols-[auto,1fr] gap-x-3 gap-y-1">
          <span class="text-muted">{{ t('pairing.certSubject') }}</span><span class="select-text break-all text-ink">{{ cert.subject }}</span>
          <span class="text-muted">{{ t('pairing.certIssuer') }}</span><span class="select-text break-all text-ink">{{ cert.issuer }}</span>
          <span class="text-muted">{{ t('pairing.certExpires') }}</span><span class="text-ink">{{ expiry(cert.notAfter) }}</span>
        </div>
        <p class="text-muted">{{ t('pairing.certHint') }}</p>
      </div>
      <template #footer>
        <button class="btn-ghost" @click="cert = null">{{ t('common.cancel') }}</button>
        <button class="btn-accent" @click="trust">{{ t('pairing.certTrust') }}</button>
      </template>
    </Dialog>
  </div>
</template>
