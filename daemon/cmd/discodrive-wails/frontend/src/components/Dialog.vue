<script setup>
import { watch, nextTick, onUnmounted, ref } from 'vue'
import { X } from 'lucide-vue-next'
import { t } from '../lib/i18n.js'
const props = defineProps({ open: Boolean, title: { type: String, default: '' } })
const emit = defineEmits(['close'])
const panel = ref(null)
let previousFocus
function onKey(e) {
  if (e.key === 'Escape') { e.preventDefault(); emit('close') }
  if (e.key !== 'Tab') return
  const elements = [...(panel.value?.querySelectorAll('button:not(:disabled), input:not(:disabled), select:not(:disabled), [tabindex="0"]') || [])].filter(el => el.getClientRects().length)
  const first = elements[0], last = elements.at(-1)
  if (!first) { e.preventDefault(); panel.value?.focus(); return }
  if (e.shiftKey && (document.activeElement === first || document.activeElement === panel.value)) { e.preventDefault(); last.focus() }
  else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
}
watch(() => props.open, async (open) => {
  if (open) {
    previousFocus = document.activeElement
    window.addEventListener('keydown', onKey)
    await nextTick()
    panel.value?.focus()
  } else {
    window.removeEventListener('keydown', onKey)
    previousFocus?.focus?.()
  }
}, { immediate: true })
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div v-if="open" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" @click.self="emit('close')">
    <div ref="panel" role="dialog" aria-modal="true" :aria-label="title" tabindex="-1" class="card max-h-[90vh] w-full max-w-lg overflow-auto p-4 outline-none">
      <div class="mb-3 flex items-center">
        <div class="text-sm font-semibold text-ink">{{ title }}</div>
        <button class="btn-ghost ml-auto" :aria-label="t('common.close')" @click="emit('close')"><X :size="14" /></button>
      </div>
      <slot />
      <div v-if="$slots.footer" class="mt-4 flex justify-end gap-2"><slot name="footer" /></div>
    </div>
  </div>
</template>
