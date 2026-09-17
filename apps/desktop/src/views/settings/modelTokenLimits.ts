export type TokenLimitValue = number | undefined;
export type TokenLimitInputValue = string | number | undefined;

export function tokenLimitEditorValue(value?: number | null): TokenLimitValue {
  return isTokenLimit(value) ? value : undefined;
}

export function normalizeTokenLimitInput(value: TokenLimitInputValue): TokenLimitValue {
  if (typeof value === "number") return isTokenLimit(value) ? value : undefined;
  const text = String(value ?? "").trim();
  if (!text) return undefined;
  if (!/^\d+$/.test(text)) return undefined;
  const parsed = Number(text);
  return isTokenLimit(parsed) ? parsed : undefined;
}

function isTokenLimit(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}
