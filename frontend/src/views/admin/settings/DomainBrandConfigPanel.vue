<template>
  <section class="card">
    <header class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
        域名品牌与分组
      </h2>
    </header>

    <div class="space-y-5 p-6">
      <p v-if="loading" class="text-sm text-gray-500 dark:text-gray-400">正在加载域名配置...</p>
      <p v-else-if="loadError" class="text-sm text-red-600">{{ loadError }}</p>

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
              <input
                v-model="profile.smtp_from_email"
                type="email"
                class="input"
                placeholder="name@example.com；留空则沿用全局发件人邮箱"
              />
              <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
                请填写完整邮箱地址，不要填写品牌名称。
              </span>
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
                    @change="profile.channel_monitor_ids_explicit = true"
                  />
                  <span>{{ monitor.name }} · {{ monitor.group_name || monitor.provider }} (#{{ monitor.id }})</span>
                </label>
              </div>
            </fieldset>
          </div>
        </article>

        <div class="flex flex-wrap items-center gap-3">
          <button type="button" class="btn btn-secondary" @click="addDomain">添加域名</button>
          <span class="text-sm text-gray-500 dark:text-gray-400">
            域名品牌配置会随页面底部的“保存设置”统一提交。
          </span>
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
  channel_monitor_ids_explicit: boolean;
  logo_mode: "inherit" | "default";
};

const loading = ref(true);
const loadError = ref("");
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
    channel_monitor_ids_explicit: profile.channel_monitor_ids != null,
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
    channel_monitor_ids_explicit: true,
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
      channel_monitor_ids: profile.channel_monitor_ids_explicit
        ? profile.channel_monitor_ids.map(Number)
        : null,
    })),
  };
}

function isValidSenderEmail(value: string): boolean {
  return /^[^\s<>@]+@[^\s<>@]+$/.test(value);
}

function buildConfigForSave(): DomainBrandConfig {
  if (loading.value) {
    throw new Error("域名品牌配置仍在加载，请稍后再保存。");
  }
  if (loadError.value) {
    throw new Error("域名品牌配置加载失败，请刷新页面后重试。");
  }

  const config = toRequest();
  const seenDomains = new Set<string>();
  config.domains.forEach((profile, index) => {
    const label = `域名配置 ${index + 1}`;
    if (!profile.domain) {
      throw new Error(`${label}：域名不能为空。`);
    }
    const domainKey = profile.domain.toLowerCase().replace(/\.$/, "");
    if (seenDomains.has(domainKey)) {
      throw new Error(`${label}：域名 ${profile.domain} 与其他配置重复。`);
    }
    seenDomains.add(domainKey);
    if (profile.smtp_from_email && !isValidSenderEmail(profile.smtp_from_email)) {
      throw new Error(`${label}：发件人邮箱格式不正确，请填写完整邮箱地址。`);
    }
  });
  return config;
}

function applySavedConfig(config: DomainBrandConfig): void {
  profiles.value = (config.domains || []).map(toEditable);
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
  loadError.value = "";
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
    loadError.value = error instanceof Error ? error.message : "加载域名配置失败";
  } finally {
    loading.value = false;
  }
}

defineExpose({ buildConfigForSave, applySavedConfig });

onMounted(load);
</script>
