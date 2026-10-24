<script setup lang="ts">
import { computed } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import BeginnerGuide from '@/components/Guide/BeginnerGuide.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
const auth = useAuthStore()
const app = useAppStore()
const guideSiteName = computed(() => app.publicSettingsLoaded ? app.siteName : '小红豆')
const layout = computed(() => auth.isAuthenticated ? AppLayout : 'div')
</script>
<template>
  <component :is="layout" :class="{ 'beginner-public': !auth.isAuthenticated }">
    <div v-if="!auth.isAuthenticated" class="beginner-public-bar"><a href="/home">← 返回{{ guideSiteName }}</a><a href="/login">登录 / 开始使用 ↗</a></div>
    <BeginnerGuide :site-name="guideSiteName" :inherit-theme="auth.isAuthenticated" />
  </component>
</template>
<style scoped>
.beginner-public{background:#fff;min-height:100vh;padding:20px 32px}.beginner-public-bar{max-width:960px;margin:0 auto 24px;display:flex;justify-content:space-between;font-size:12px;color:#6b7682}.beginner-public-bar a{color:inherit;text-decoration:none}@media(max-width:640px){.beginner-public{padding:17px}.beginner-public-bar{margin-bottom:19px;font-size:11px}}
</style>
