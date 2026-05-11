<template>
  <div class="flex flex-col gap-0.5">
    <!-- 并发槽位 -->
    <CapacityBadge :color-class="concurrencyClass" :current="currentConcurrency" :max="account.concurrency">
      <svg class="h-2.5 w-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
        <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6zM3.75 15.75A2.25 2.25 0 016 13.5h2.25a2.25 2.25 0 012.25 2.25V18a2.25 2.25 0 01-2.25 2.25H6A2.25 2.25 0 013.75 18v-2.25zM13.5 6a2.25 2.25 0 012.25-2.25H18A2.25 2.25 0 0120.25 6v2.25A2.25 2.25 0 0118 10.5h-2.25a2.25 2.25 0 01-2.25-2.25V6zM13.5 15.75a2.25 2.25 0 012.25-2.25H18a2.25 2.25 0 012.25 2.25V18A2.25 2.25 0 0118 20.25h-2.25A2.25 2.25 0 0113.5 18v-2.25z" />
      </svg>
    </CapacityBadge>

    <!-- 5h窗口费用限制 -->
    <CapacityBadge v-if="showWindowCost" :color-class="windowCostClass" :tooltip="windowCostTooltip" :current="'$' + formatCost(currentWindowCost)" :max="'$' + formatCost(account.window_cost_limit)">
      <svg class="h-2.5 w-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
        <path stroke-linecap="round" stroke-linejoin="round" d="M12 6v12m-3-2.818l.879.659c1.171.879 3.07.879 4.242 0 1.172-.879 1.172-2.303 0-3.182C13.536 12.219 12.768 12 12 12c-.725 0-1.45-.22-2.003-.659-1.106-.879-1.106-2.303 0-3.182s2.9-.879 4.006 0l.415.33M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
    </CapacityBadge>

    <!-- 会话数量限制 -->
    <CapacityBadge v-if="showSessionLimit" :color-class="sessionLimitClass" :tooltip="sessionLimitTooltip" :current="activeSessions" :max="account.max_sessions!">
      <svg class="h-2.5 w-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
        <path stroke-linecap="round" stroke-linejoin="round" d="M15 19.128a9.38 9.38 0 002.625.372 9.337 9.337 0 004.121-.952 4.125 4.125 0 00-7.533-2.493M15 19.128v-.003c0-1.113-.285-2.16-.786-3.07M15 19.128v.106A12.318 12.318 0 018.624 21c-2.331 0-4.512-.645-6.374-1.766l-.001-.109a6.375 6.375 0 0111.964-3.07M12 6.375a3.375 3.375 0 11-6.75 0 3.375 3.375 0 016.75 0zm8.25 2.25a2.625 2.625 0 11-5.25 0 2.625 2.625 0 015.25 0z" />
      </svg>
    </CapacityBadge>

    <!-- RPM 限制 -->
    <CapacityBadge v-if="showRpmLimit" :color-class="rpmClass" :tooltip="rpmTooltip" :current="currentRPM" :max="account.base_rpm!" :suffix="rpmStrategyTag">
      <svg class="h-2.5 w-2.5" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor">
        <path stroke-linecap="round" stroke-linejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />
      </svg>
    </CapacityBadge>

    <!-- API Key 账号配额限制 -->
    <QuotaBadge v-if="showDailyQuota" :used="account.quota_daily_used ?? 0" :limit="account.quota_daily_limit!" label="D" />
    <QuotaBadge v-if="showWeeklyQuota" :used="account.quota_weekly_used ?? 0" :limit="account.quota_weekly_limit!" label="W" />
    <QuotaBadge v-if="showTotalQuota" :used="account.quota_used ?? 0" :limit="account.quota_limit!" />

    <!-- Kiro 配额（来自 extra.kiro_usage_data） -->
    <button
      v-if="showKiroQuota"
      type="button"
      :title="kiroQuotaTitle"
      class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium transition-opacity disabled:opacity-50"
      :class="kiroQuotaClass"
      :disabled="kiroRefreshing"
      @click.stop="handleKiroRefresh"
    >
      <svg v-if="!kiroRefreshing" class="h-2.5 w-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99"/></svg>
      <svg v-else class="h-2.5 w-2.5 animate-spin" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v4a4 4 0 00-4 4H4z"/></svg>
      Kiro {{ kiroCurrentUsage }}/{{ kiroUsageLimit }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import CapacityBadge from '@/components/account/CapacityBadge.vue'
import QuotaBadge from '@/components/account/QuotaBadge.vue'

const props = defineProps<{
  account: Account
}>()

const { t } = useI18n()

// ====== 并发 ======
const currentConcurrency = computed(() => props.account.current_concurrency || 0)

const concurrencyClass = computed(() => {
  const current = currentConcurrency.value
  const max = props.account.concurrency
  if (current >= max) return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
  if (current > 0) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
  return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400'
})

// ====== 窗口费用 ======
const isAnthropicOAuthOrSetupToken = computed(() =>
  props.account.platform === 'anthropic' &&
  (props.account.type === 'oauth' || props.account.type === 'setup-token')
)

const showWindowCost = computed(() =>
  isAnthropicOAuthOrSetupToken.value &&
  props.account.window_cost_limit != null &&
  props.account.window_cost_limit > 0
)

const currentWindowCost = computed(() => props.account.current_window_cost ?? 0)

const windowCostClass = computed(() => {
  if (!showWindowCost.value) return ''
  const current = currentWindowCost.value
  const limit = props.account.window_cost_limit || 0
  const reserve = props.account.window_cost_sticky_reserve || 10
  if (current >= limit + reserve) return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
  if (current >= limit) return 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'
  if (current >= limit * 0.8) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
  return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
})

const windowCostTooltip = computed(() => {
  if (!showWindowCost.value) return ''
  const current = currentWindowCost.value
  const limit = props.account.window_cost_limit || 0
  const reserve = props.account.window_cost_sticky_reserve || 10
  if (current >= limit + reserve) return t('admin.accounts.capacity.windowCost.blocked')
  if (current >= limit) return t('admin.accounts.capacity.windowCost.stickyOnly')
  return t('admin.accounts.capacity.windowCost.normal')
})

// ====== 会话限制 ======
const showSessionLimit = computed(() =>
  isAnthropicOAuthOrSetupToken.value &&
  props.account.max_sessions != null &&
  props.account.max_sessions > 0
)

const activeSessions = computed(() => props.account.active_sessions ?? 0)

const sessionLimitClass = computed(() => {
  if (!showSessionLimit.value) return ''
  const current = activeSessions.value
  const max = props.account.max_sessions || 0
  if (current >= max) return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
  if (current >= max * 0.8) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
  return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
})

const sessionLimitTooltip = computed(() => {
  if (!showSessionLimit.value) return ''
  const current = activeSessions.value
  const max = props.account.max_sessions || 0
  const idle = props.account.session_idle_timeout_minutes || 5
  if (current >= max) return t('admin.accounts.capacity.sessions.full', { idle })
  return t('admin.accounts.capacity.sessions.normal', { idle })
})

// ====== RPM ======
const showRpmLimit = computed(() =>
  isAnthropicOAuthOrSetupToken.value &&
  props.account.base_rpm != null &&
  props.account.base_rpm > 0
)

const currentRPM = computed(() => props.account.current_rpm ?? 0)
const rpmStrategy = computed(() => props.account.rpm_strategy || 'tiered')
const rpmStrategyTag = computed(() => rpmStrategy.value === 'sticky_exempt' ? '[S]' : '[T]')

const rpmBuffer = computed(() => {
  const base = props.account.base_rpm || 0
  return props.account.rpm_sticky_buffer ?? (base > 0 ? Math.max(1, Math.floor(base / 5)) : 0)
})

const rpmClass = computed(() => {
  if (!showRpmLimit.value) return ''
  const current = currentRPM.value
  const base = props.account.base_rpm ?? 0
  const buffer = rpmBuffer.value
  if (rpmStrategy.value === 'tiered') {
    if (current >= base + buffer) return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
    if (current >= base) return 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'
  } else {
    if (current >= base) return 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'
  }
  if (current >= base * 0.8) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
  return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
})

const rpmTooltip = computed(() => {
  if (!showRpmLimit.value) return ''
  const current = currentRPM.value
  const base = props.account.base_rpm ?? 0
  const buffer = rpmBuffer.value
  if (rpmStrategy.value === 'tiered') {
    if (current >= base + buffer) return t('admin.accounts.capacity.rpm.tieredBlocked', { buffer })
    if (current >= base) return t('admin.accounts.capacity.rpm.tieredStickyOnly', { buffer })
    if (current >= base * 0.8) return t('admin.accounts.capacity.rpm.tieredWarning')
    return t('admin.accounts.capacity.rpm.tieredNormal')
  } else {
    if (current >= base) return t('admin.accounts.capacity.rpm.stickyExemptOver')
    if (current >= base * 0.8) return t('admin.accounts.capacity.rpm.stickyExemptWarning')
    return t('admin.accounts.capacity.rpm.stickyExemptNormal')
  }
})

// 格式化费用显示
const formatCost = (value: number | null | undefined) => {
  if (value === null || value === undefined) return '0'
  return value.toFixed(2)
}

// ====== 配额 ======
const isQuotaEligible = computed(() => props.account.type === 'apikey' || props.account.type === 'bedrock')

const showDailyQuota = computed(() =>
  isQuotaEligible.value && props.account.quota_daily_limit != null && props.account.quota_daily_limit > 0
)
const showWeeklyQuota = computed(() =>
  isQuotaEligible.value && props.account.quota_weekly_limit != null && props.account.quota_weekly_limit > 0
)
const showTotalQuota = computed(() =>
  isQuotaEligible.value && props.account.quota_limit != null && props.account.quota_limit > 0
)

// ====== Kiro 配额 ======
const kiroBreakdown = computed(() => {
  const data = (props.account.extra as Record<string, any> | undefined)?.kiro_usage_data
  const list = data?.usageBreakdownList
  if (!Array.isArray(list) || list.length === 0) return null
  const first = list[0] || {}
  const cur = Number(first.currentUsage ?? 0) || 0
  const lim = Number(first.usageLimit ?? 0) || 0
  const overage = !!first?.overageConfiguration?.overageEnabled
  return { cur, lim, overage }
})
const showKiroQuota = computed(() => props.account.platform === 'kiro' && kiroBreakdown.value !== null)
const kiroCurrentUsage = computed(() => kiroBreakdown.value?.cur ?? 0)
const kiroUsageLimit = computed(() => kiroBreakdown.value?.lim ?? 0)
const kiroQuotaClass = computed(() => {
  const b = kiroBreakdown.value
  if (!b || b.lim <= 0) return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400'
  const ratio = b.cur / b.lim
  if (b.cur >= b.lim && !b.overage) return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
  if (ratio >= 0.8) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
  return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
})
const kiroQuotaTitle = computed(() => {
  const b = kiroBreakdown.value
  if (!b) return '点击刷新 Kiro 配额'
  return `Kiro 配额 ${b.cur}/${b.lim}${b.overage ? '（已开启 overage）' : ''}\n点击刷新`
})

const emit = defineEmits<{ (e: 'kiro-refresh', accountId: number): void }>()
const kiroRefreshing = ref(false)
async function handleKiroRefresh() {
  if (kiroRefreshing.value) return
  kiroRefreshing.value = true
  try {
    emit('kiro-refresh', props.account.id)
  } finally {
    // 父组件刷新结束后会更新 account.extra；这里给 800ms 防抖
    setTimeout(() => { kiroRefreshing.value = false }, 800)
  }
}
</script>
