import { describe, expect, it } from "vitest"

import {
  normalizeFirstTokenLatencyAutoPauseSettings,
  sanitizeFirstTokenLatencyAutoPauseSettings,
} from "@/api/admin/settings"

describe("first-token latency auto-pause settings", () => {
  it("provides a disabled rule by default", () => {
    expect(normalizeFirstTokenLatencyAutoPauseSettings()).toEqual({
      enabled: false,
      rules: [
        {
          window_minutes: 5,
          threshold_seconds: 10,
          trigger_percent: 50,
          pause_minutes: 10,
        },
      ],
    })
  })

  it("preserves multiple rules and clamps unsafe values", () => {
    expect(sanitizeFirstTokenLatencyAutoPauseSettings({
      enabled: true,
      rules: [
        { window_minutes: 0, threshold_seconds: 0, trigger_percent: 0, pause_minutes: 0 },
        { window_minutes: 2000, threshold_seconds: 900, trigger_percent: 200, pause_minutes: 2000 },
      ],
    })).toEqual({
      enabled: true,
      rules: [
        { window_minutes: 1, threshold_seconds: 0.1, trigger_percent: 0.1, pause_minutes: 1 },
        { window_minutes: 1440, threshold_seconds: 600, trigger_percent: 100, pause_minutes: 1440 },
      ],
    })
  })

  it("normalizes legacy count rules to a conservative 100% share", () => {
    expect(normalizeFirstTokenLatencyAutoPauseSettings({
      enabled: true,
      rules: [
        { window_minutes: 1, threshold_seconds: 120, trigger_count: 2, pause_minutes: 360 },
      ],
    })).toEqual({
      enabled: true,
      rules: [
        { window_minutes: 1, threshold_seconds: 120, trigger_percent: 100, pause_minutes: 360 },
      ],
    })
  })
})
