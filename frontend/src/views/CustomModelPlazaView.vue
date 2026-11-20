<template>
  <component :is="auth.isAuthenticated ? AppLayout : 'div'" :class="{ 'min-h-screen bg-gray-50 dark:bg-dark-950': !auth.isAuthenticated }">
    <div :class="{ 'mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:py-8': !auth.isAuthenticated }" class="space-y-8">
      <header v-if="!auth.isAuthenticated" class="flex flex-wrap items-center justify-between gap-3">
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('customModelPlaza.title') }}</h1>
        <RouterLink to="/home" class="text-sm font-medium text-primary-700 dark:text-primary-300">{{ app.siteName }}</RouterLink>
      </header>
      <p class="rounded-2xl border border-gray-200 bg-white px-4 py-4 text-sm leading-relaxed text-gray-700 dark:border-dark-700/60 dark:bg-dark-800/60 dark:text-gray-300 sm:px-5">
        {{ t('customModelPlaza.disclaimer') }}
      </p>
      <p v-if="loading" role="status" class="py-12 text-center text-gray-500">{{ t('customModelPlaza.loading') }}</p>
      <div v-else-if="failed" class="text-center">
        <p role="alert" class="mb-3 text-sm text-red-600 dark:text-red-400">{{ t('customModelPlaza.loadFailed') }}</p>
        <button type="button" class="btn btn-secondary" @click="load">{{ t('customModelPlaza.retry') }}</button>
      </div>
      <p v-else-if="models.length === 0" class="py-12 text-center text-gray-500">{{ t('customModelPlaza.empty') }}</p>
      <template v-else>
        <CustomModelPriceTable v-for="section in sections" :key="section.id" :family="section.name" :models="section.models" />
      </template>
    </div>
  </component>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import CustomModelPriceTable from '@/components/customModelPlaza/CustomModelPriceTable.vue'
import { getCustomModelPlaza, plazaFamilies, type CustomPlazaModel } from '@/api/customModelPlaza'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const app = useAppStore()
const auth = useAuthStore()
const models = ref<CustomPlazaModel[]>([])
const loading = ref(true)
const failed = ref(false)
const sections = computed(() => plazaFamilies.map(family => ({
  ...family, models: models.value.filter(model => model.family === family.id),
})).filter(family => family.models.length > 0))

async function load() {
  loading.value = true
  failed.value = false
  try {
    models.value = await getCustomModelPlaza()
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void app.fetchPublicSettings()
  void load()
})
</script>
