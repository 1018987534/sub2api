import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";

import DomainBrandConfigPanel from "../DomainBrandConfigPanel.vue";

const { getDomainBrandConfig, updateDomainBrandConfig, getGroups } = vi.hoisted(() => ({
  getDomainBrandConfig: vi.fn(),
  updateDomainBrandConfig: vi.fn(),
  getGroups: vi.fn(),
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
  },
}));

describe("DomainBrandConfigPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getGroups.mockResolvedValue([]);
    getDomainBrandConfig.mockResolvedValue({
      domains: [
        {
          domain: "xiaofanqie.org",
          site_name: "xiaofanqie.org",
          site_logo: "",
          site_subtitle: "B2B API Gateway",
          contact_info: "support@xiaofanqie.org",
          api_base_url: "https://xiaofanqie.org/v1",
          allowed_group_ids: [],
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
    expect((contactInput.element as HTMLInputElement).value).toBe("support@xiaofanqie.org");
    expect((apiBaseURLInput.element as HTMLInputElement).value).toBe(
      "https://xiaofanqie.org/v1",
    );

    await contactInput.setValue(" business@xiaofanqie.org ");
    await apiBaseURLInput.setValue(" https://api.xiaofanqie.org/v1 ");
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
          allowed_group_ids: [],
        },
      ],
    });
  });
});
