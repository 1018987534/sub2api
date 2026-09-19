import { describe, expect, it } from "vitest";

import {
  cacheRateDecimalToPercent,
  cacheRatePercentToDecimal,
  isValidCacheRatePercent,
} from "../groupsCacheRate";

describe("group cache-rate form helpers", () => {
  it("converts between percent input and decimal storage", () => {
    expect(cacheRatePercentToDecimal(80)).toBe(0.8);
    expect(cacheRatePercentToDecimal(72.34)).toBe(0.7234);
    expect(cacheRateDecimalToPercent(0.8)).toBe(80);
    expect(cacheRateDecimalToPercent(0.7234)).toBe(72.34);
  });

  it("uses zero for an empty or disabled threshold", () => {
    expect(cacheRatePercentToDecimal(0)).toBe(0);
    expect(cacheRatePercentToDecimal("")).toBe(0);
    expect(cacheRateDecimalToPercent(undefined)).toBe(0);
  });

  it("accepts 0..100 percent and rejects values that store above one", () => {
    expect(isValidCacheRatePercent(0)).toBe(true);
    expect(isValidCacheRatePercent(80)).toBe(true);
    expect(isValidCacheRatePercent(100)).toBe(true);
    expect(isValidCacheRatePercent(-1)).toBe(false);
    expect(isValidCacheRatePercent(100.01)).toBe(false);
    expect(isValidCacheRatePercent(Number.NaN)).toBe(false);
  });
});
