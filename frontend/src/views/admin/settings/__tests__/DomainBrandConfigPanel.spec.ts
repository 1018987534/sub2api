import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";

import DomainBrandConfigPanel from "../DomainBrandConfigPanel.vue";

const { getDomainBrandConfig, updateDomainBrandConfig, getGroups, listChannelMonitors } = vi.hoisted(() => ({
  getDomainBrandConfig: vi.fn(),
  updateDomainBrandConfig: vi.fn(),
  getGroups: vi.fn(),
  listChannelMonitors: vi.fn(),
}));

vi.mock("@/api", () => ({
  adminAPI: {
    settings: {
      getDomainBrandConfig,
      updateDomainBrandConfig,
    },
    groups: {
      getAll: getGroups,
    },
    channelMonitor: {
      list: listChannelMonitors,
    },
  },
}));

describe("DomainBrandConfigPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getGroups.mockResolvedValue([]);
    listChannelMonitors.mockResolvedValue({
      items: [{ id: 12, name: "番茄渠道", group_name: "B2B", provider: "openai" }],
      total: 1,
      page: 1,
      page_size: 100,
      pages: 1,
    });
    getDomainBrandConfig.mockResolvedValue({
      domains: [
        {
          domain: "xiaofanqie.org",
          site_name: "xiaofanqie.org",
          site_logo: "",
          site_subtitle: "B2B API Gateway",
          contact_info: "support@xiaofanqie.org",
          api_base_url: "https://xiaofanqie.org/v1",
          smtp_from_email: "sender@xiaofanqie.org",
          registration_email_verify_enabled: false,
          allowed_group_ids: [],
          channel_monitor_ids: [12],
        },
      ],
    });
    updateDomainBrandConfig.mockImplementation(async (payload) => payload);
  });

  it("loads and saves per-domain contact and API endpoint overrides", async () => {
    const wrapper = mount(DomainBrandConfigPanel);
    await flushPromises();

    const contactInput = wrapper.get(
      'input[placeholder="留空则沿用全局客服联系方式"]',
    );
    const apiBaseURLInput = wrapper.get('input[placeholder="https://example.com/v1"]');
    const senderInput = wrapper.get('input[type="email"]');
    expect((contactInput.element as HTMLInputElement).value).toBe("support@xiaofanqie.org");
    expect((apiBaseURLInput.element as HTMLInputElement).value).toBe(
      "https://xiaofanqie.org/v1",
    );
    expect((senderInput.element as HTMLInputElement).value).toBe("sender@xiaofanqie.org");
    expect((wrapper.findAll("select")[0].element as HTMLSelectElement).value).toBe("disabled");

    await contactInput.setValue(" business@xiaofanqie.org ");
    await apiBaseURLInput.setValue(" https://api.xiaofanqie.org/v1 ");
    await senderInput.setValue(" mail@xiaofanqie.org ");
    await wrapper.findAll("select")[0].setValue("enabled");
    await wrapper.get("button.btn-primary").trigger("click");
    await flushPromises();

    expect(updateDomainBrandConfig).toHaveBeenCalledWith({
      domains: [
        {
          domain: "xiaofanqie.org",
          site_name: "xiaofanqie.org",
          site_logo: "",
          site_subtitle: "B2B API Gateway",
          contact_info: "business@xiaofanqie.org",
          api_base_url: "https://api.xiaofanqie.org/v1",
          smtp_from_email: "mail@xiaofanqie.org",
          registration_email_verify_enabled: true,
          allowed_group_ids: [],
          channel_monitor_ids: [12],
        },
      ],
    });
  });
});
