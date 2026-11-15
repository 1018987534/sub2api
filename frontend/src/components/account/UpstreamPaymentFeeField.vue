<template>
  <div class="mt-3">
    <label class="input-label">
      {{ t('admin.accounts.upstreamBilling.paymentFeePercent') }}
      <input
        :value="modelValue"
        type="number"
        min="0"
        max="100"
        step="0.01"
        class="input mt-1"
        data-testid="upstream-payment-fee-percent"
        @input="emit('update:modelValue', Number(($event.target as HTMLInputElement).value))"
      />
    </label>
    <p class="input-hint">{{ t('admin.accounts.upstreamBilling.paymentFeeHint') }}</p>
    <p class="mt-2 text-sm font-medium text-emerald-600 dark:text-emerald-400" data-testid="upstream-payment-real-rate">
      {{ t('admin.accounts.upstreamBilling.paymentRealRate', { rate: realRate }) }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { applyPaymentFee, isValidPaymentFeePercent } from '@/utils/accountPaymentFee'

const props = defineProps<{ modelValue: number; baseRate: number | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
const { t } = useI18n()
const realRate = computed(() => {
  if (props.baseRate == null || !Number.isFinite(props.baseRate) || props.baseRate < 0 || !isValidPaymentFeePercent(props.modelValue)) return '-'
  return applyPaymentFee(props.baseRate, props.modelValue)
})
</script>
