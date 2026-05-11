<template>
  <BaseDialog
    :show="show"
    title="导入 Kiro 账号"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="import-kiro-form" class="space-y-4" @submit.prevent="handleImport">
      <div class="text-sm text-gray-600 dark:text-dark-300">
        粘贴 kiro-account-manager 导出的 JSON 数组（可包含 Social / IdC 账号），或选择 .json 文件上传。
      </div>
      <div
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-600 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400"
      >
        IdC 账号需带 idcInfo + refreshToken；Social 账号需带 accessToken/refreshToken/profileArn。导入后请去账号详情绑定到对应 group。
      </div>

      <div>
        <label class="input-label">选择 JSON 文件</label>
        <div
          class="flex items-center justify-between gap-3 rounded-lg border border-dashed border-gray-300 bg-gray-50 px-4 py-3 dark:border-dark-600 dark:bg-dark-800"
        >
          <div class="min-w-0">
            <div class="truncate text-sm text-gray-700 dark:text-dark-200">
              {{ fileName || '未选择文件' }}
            </div>
            <div class="text-xs text-gray-500 dark:text-dark-400">JSON (.json)</div>
          </div>
          <button type="button" class="btn btn-secondary shrink-0" @click="openFilePicker">
            选择文件
          </button>
        </div>
        <input
          ref="fileInput"
          type="file"
          class="hidden"
          accept="application/json,.json"
          @change="handleFileChange"
        />
      </div>

      <div>
        <label class="input-label">或直接粘贴 JSON</label>
        <textarea
          v-model="jsonText"
          rows="6"
          class="input w-full font-mono text-xs"
          placeholder='[{"id":"...","authMethod":"IdC","idcInfo":{...},"refreshToken":"..."}]'
        />
      </div>

      <div
        v-if="result"
        class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="text-sm font-medium text-gray-900 dark:text-white">导入结果</div>
        <div class="text-sm text-gray-700 dark:text-dark-300">
          总计 {{ result.summary.total }}，成功 {{ result.summary.succeeded }}，失败 {{ result.summary.failed }}
        </div>
        <div v-if="errorItems.length" class="mt-2">
          <div class="text-sm font-medium text-red-600 dark:text-red-400">失败项</div>
          <div
            class="mt-2 max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800"
          >
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              #{{ item.index }} {{ item.id || item.email || '-' }} — {{ item.error }}
            </div>
          </div>
        </div>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="importing" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          class="btn btn-primary"
          type="submit"
          form="import-kiro-form"
          :disabled="importing"
        >
          {{ importing ? '导入中...' : '导入' }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { KiroImportResponse } from '@/api/admin/accounts'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const importing = ref(false)
const file = ref<File | null>(null)
const jsonText = ref('')
const result = ref<KiroImportResponse | null>(null)

const fileInput = ref<HTMLInputElement | null>(null)
const fileName = computed(() => file.value?.name || '')

const errorItems = computed(() => result.value?.results.filter((r) => !r.created) || [])

watch(
  () => props.show,
  (open) => {
    if (open) {
      file.value = null
      jsonText.value = ''
      result.value = null
      if (fileInput.value) fileInput.value.value = ''
    }
  }
)

const openFilePicker = () => fileInput.value?.click()

const handleFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement
  file.value = target.files?.[0] || null
}

const handleClose = () => {
  if (importing.value) return
  emit('close')
}

const readFileAsText = async (sourceFile: File): Promise<string> => {
  if (typeof sourceFile.text === 'function') return sourceFile.text()
  if (typeof sourceFile.arrayBuffer === 'function') {
    return new TextDecoder().decode(await sourceFile.arrayBuffer())
  }
  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error || new Error('Failed to read file'))
    reader.readAsText(sourceFile)
  })
}

const handleImport = async () => {
  let text = jsonText.value.trim()
  if (!text && file.value) {
    text = (await readFileAsText(file.value)).trim()
  }
  if (!text) {
    appStore.showError('请选择 JSON 文件或粘贴 JSON 内容')
    return
  }

  importing.value = true
  try {
    const parsed = JSON.parse(text)
    const items: unknown[] = Array.isArray(parsed)
      ? parsed
      : Array.isArray((parsed as { items?: unknown[] })?.items)
        ? ((parsed as { items: unknown[] }).items)
        : []
    if (items.length === 0) {
      appStore.showError('未发现可导入的账号项（应为数组或 {items:[...]}）')
      importing.value = false
      return
    }

    const res = await adminAPI.accounts.importKiro({ items })
    result.value = res

    if (res.summary.failed > 0) {
      appStore.showError(`导入完成：成功 ${res.summary.succeeded}，失败 ${res.summary.failed}`)
    } else {
      appStore.showSuccess(`成功导入 ${res.summary.succeeded} 个 Kiro 账号`)
      emit('imported')
    }
  } catch (error: any) {
    if (error instanceof SyntaxError) {
      appStore.showError('JSON 解析失败，请检查格式')
    } else {
      appStore.showError(error?.message || '导入失败')
    }
  } finally {
    importing.value = false
  }
}
</script>
