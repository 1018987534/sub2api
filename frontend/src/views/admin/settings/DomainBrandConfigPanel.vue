<template>
  <section class="card">
    <header class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
        域名品牌与分组
      </h2>
    </header>

    <div class="space-y-5 p-6">
      <p v-if="loading" class="text-sm text-gray-500 dark:text-gray-400">正在加载域名配置...</p>

      <template v-else>
        <article
          v-for="(profile, index) in profiles"
          :key="`${profile.domain}-${index}`"
          class="domain-profile border-t border-gray-200 pt-5 first:border-t-0 first:pt-0 dark:border-dark-600"
        >
          <div class="flex items-start justify-between gap-4">
            <div>
              <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
                域名配置 {{ index + 1 }}
              </h3>
            </div>
            <button type="button" class="btn btn-ghost btn-sm" @click="removeDomain(index)">
              删除
            </button>
          </div>

          <div class="mt-4">
            <label class="block">
              <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">域名</span>
              <input v-model.trim="profile.domain" class="input" placeholder="example.com" />
            </label>
            <label class="block">
              <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">站点标题</span>
              <input v-model="profile.site_name" class="input" placeholder="留空则沿用全局标题" />
            </label>
            <label class="block">
              <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">站点副标题</span>
              <input v-model="profile.site_subtitle" class="input" placeholder="留空则沿用全局副标题" />
            </label>
            <label class="block">
              <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">客服联系方式</span>
              <input v-model="profile.contact_info" class="input" placeholder="留空则沿用全局客服联系方式" />
            </label>
            <label class="block">
              <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">API 端点地址</span>
              <input v-model="profile.api_base_url" class="input" placeholder="https://example.com/v1" />
            </label>
            <label class="block">
              <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">发件人邮箱</span>
              <input v-model="profile.smtp_from_email" type="email" class="input" placeholder="留空则沿用全局发件人邮箱" />
            </label>
            <label class="block">
              <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">注册邮箱验证</span>
              <select v-model="profile.registration_email_verify_mode" class="input">
                <option value="inherit">沿用全局设置</option>
                <option value="enabled">发送验证码后注册</option>
                <option value="disabled">无需验证码，直接注册成功</option>
              </select>
            </label>
            <label class="block">
              <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">Logo</span>
              <select v-model="profile.logo_mode" class="input">
                <option value="inherit">沿用全局 Logo</option>
                <option value="default">使用系统默认 Logo</option>
              </select>
            </label>
          </div>

          <div class="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2">
            <fieldset>
              <legend class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">可用分组</legend>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <label
                  v-for="group in activeGroups"
                  :key="group.id"
                  class="flex min-h-10 items-center gap-2 border-b border-gray-100 py-2 text-sm text-gray-700 dark:border-dark-700 dark:text-gray-300"
                >
                  <input
                    v-model="profile.allowed_group_ids"
                    type="checkbox"
                    :value="group.id"
                    :disabled="isGroupUsedByAnotherProfile(index, group.id)"
                  />
                  <span>{{ group.name }} · {{ group.platform }} (#{{ group.id }})</span>
                </label>
              </div>
            </fieldset>
            <fieldset>
              <legend class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">展示渠道监控</legend>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <label
                  v-for="monitor in channelMonitors"
                  :key="monitor.id"
                  class="flex min-h-10 items-center gap-2 border-b border-gray-100 py-2 text-sm text-gray-700 dark:border-dark-700 dark:text-gray-300"
                >
                  <input
                    v-model="profile.channel_monitor_ids"
                    type="checkbox"
                    :value="monitor.id"
                    :disabled="isMonitorUsedByAnotherProfile(index, monitor.id)"
                  />
                  <span>{{ monitor.name }} · {{ monitor.group_name || monitor.provider }} (#{{ monitor.id }})</span>
                </label>
              </div>
            </fieldset>
          </div>
        </article>

        <div class="flex flex-wrap items-center gap-3">
          <button type="button" class="btn btn-secondary" @click="addDomain">添加域名</button>
          <button type="button" class="btn btn-primary" :disabled="saving" @click="save">
            {{ saving ? "保存中..." : "保存域名配置" }}
          </button>
          <span v-if="message" :class="messageError ? 'text-sm text-red-600' : 'text-sm text-emerald-600'">{{ message }}</span>
        </div>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import { adminAPI } from "@/api";
import type { AdminGroup } from "@/types";
import type { DomainBrandConfig, DomainBrandProfile } from "@/api/admin/settings";
import type { ChannelMonitor } from "@/api/admin/channelMonitor";

type EditableDomainBrandProfile = Omit<DomainBrandProfile, "site_logo" | "smtp_from_email" | "registration_email_verify_enabled" | "channel_monitor_ids"> & {
  site_name: string | null;
  site_subtitle: string | null;
  contact_info: string | null;
  api_base_url: string | null;
  smtp_from_email: string | null;
  registration_email_verify_mode: "inherit" | "enabled" | "disabled";
  channel_monitor_ids: number[];
  logo_mode: "inherit" | "default";
};

const loading = ref(true);
const saving = ref(false);
const message = ref("");
const messageError = ref(false);
const activeGroups = ref<AdminGroup[]>([]);
const channelMonitors = ref<ChannelMonitor[]>([]);
const profiles = ref<EditableDomainBrandProfile[]>([]);

function toEditable(profile: DomainBrandProfile): EditableDomainBrandProfile {
  return {
    domain: profile.domain,
    site_name: profile.site_name ?? null,
    site_subtitle: profile.site_subtitle ?? null,
    contact_info: profile.contact_info ?? null,
    api_base_url: profile.api_base_url ?? null,
    smtp_from_email: profile.smtp_from_email ?? null,
    registration_email_verify_mode:
      profile.registration_email_verify_enabled == null
        ? "inherit"
        : profile.registration_email_verify_enabled
          ? "enabled"
          : "disabled",
    logo_mode: profile.site_logo === "" ? "default" : "inherit",
    allowed_group_ids: [...(profile.allowed_group_ids || [])],
    channel_monitor_ids: [...(profile.channel_monitor_ids || [])],
  };
}

function addDomain(): void {
  profiles.value.push({
    domain: "",
    site_name: null,
    site_subtitle: null,
    contact_info: null,
    api_base_url: null,
    smtp_from_email: null,
    registration_email_verify_mode: "inherit",
    logo_mode: "inherit",
    allowed_group_ids: [],
    channel_monitor_ids: [],
  });
}

function removeDomain(index: number): void {
  profiles.value.splice(index, 1);
}

function isGroupUsedByAnotherProfile(currentIndex: number, groupID: number): boolean {
  return profiles.value.some(
    (profile, index) => index !== currentIndex && profile.allowed_group_ids.includes(groupID),
  );
}

function isMonitorUsedByAnotherProfile(currentIndex: number, monitorID: number): boolean {
  return profiles.value.some(
    (profile, index) => index !== currentIndex && profile.channel_monitor_ids.includes(monitorID),
  );
}

function toRequest(): DomainBrandConfig {
  return {
    domains: profiles.value.map((profile) => ({
      domain: profile.domain.trim(),
      site_name: profile.site_name?.trim() ? profile.site_name.trim() : null,
      site_logo: profile.logo_mode === "default" ? "" : null,
      site_subtitle: profile.site_subtitle?.trim() ? profile.site_subtitle.trim() : null,
      contact_info: profile.contact_info?.trim() ? profile.contact_info.trim() : null,
      api_base_url: profile.api_base_url?.trim() ? profile.api_base_url.trim() : null,
      smtp_from_email: profile.smtp_from_email?.trim() ? profile.smtp_from_email.trim() : null,
      registration_email_verify_enabled:
        profile.registration_email_verify_mode === "inherit"
          ? null
          : profile.registration_email_verify_mode === "enabled",
      allowed_group_ids: profile.allowed_group_ids.map(Number),
      channel_monitor_ids: profile.channel_monitor_ids.map(Number),
    })),
  };
}

async function loadChannelMonitors(): Promise<ChannelMonitor[]> {
  const firstPage = await adminAPI.channelMonitor.list({ page: 1, page_size: 100 });
  const monitors = [...(firstPage.items || [])];
  const pages = Math.max(1, firstPage.pages || Math.ceil((firstPage.total || 0) / 100));
  if (pages > 1) {
    const remainingPages = await Promise.all(
      Array.from({ length: pages - 1 }, (_, index) =>
        adminAPI.channelMonitor.list({ page: index + 2, page_size: 100 }),
      ),
    );
    for (const page of remainingPages) {
      monitors.push(...(page.items || []));
    }
  }
  return monitors;
}

async function load(): Promise<void> {
  loading.value = true;
  try {
    const [config, groups, monitors] = await Promise.all([
      adminAPI.settings.getDomainBrandConfig(),
      adminAPI.groups.getAll(),
      loadChannelMonitors(),
    ]);
    profiles.value = (config.domains || []).map(toEditable);
    activeGroups.value = groups.filter((group) => group.status === "active");
    channelMonitors.value = monitors;
  } catch (error) {
    message.value = error instanceof Error ? error.message : "加载域名配置失败";
    messageError.value = true;
  } finally {
    loading.value = false;
  }
}

async function save(): Promise<void> {
  saving.value = true;
  message.value = "";
  try {
    const saved = await adminAPI.settings.updateDomainBrandConfig(toRequest());
    profiles.value = (saved.domains || []).map(toEditable);
    message.value = "已保存；前台页面会在下一次请求时按域名更新。";
    messageError.value = false;
  } catch (error) {
    message.value = error instanceof Error ? error.message : "保存域名配置失败";
    messageError.value = true;
  } finally {
    saving.value = false;
  }
}

onMounted(load);
</script>
