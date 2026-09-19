// The API stores rates as decimals (0.80), while the group form displays
// percentages (80). Keep the conversion aligned with decimal(10,4).
export const cacheRatePercentToDecimal = (
  value: number | string | null | undefined,
): number => {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) return 0;
  return Math.round(parsed * 100) / 10000;
};

export const cacheRateDecimalToPercent = (
  value: number | null | undefined,
): number => {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) return 0;
  return Math.round(parsed * 1e6) / 1e4;
};

export const isValidCacheRatePercent = (
  value: number | string | null | undefined,
): boolean => {
  if (value === "" || value === null || value === undefined) return true;
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) return false;
  return cacheRatePercentToDecimal(parsed) <= 1;
};
