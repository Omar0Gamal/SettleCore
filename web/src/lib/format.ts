/**
 * Deterministic string-based decimal and telemetry formatters.
 * Avoids IEEE-754 floating-point drift on financial quantities and prices.
 */

const DECIMAL_RE = /^-?\d+(?:\.\d+)?$/;

function splitDecimal(value: string): { sign: -1 | 1; intPart: string; fracPart: string } | null {
  const trimmed = value.trim();
  if (!DECIMAL_RE.test(trimmed)) return null;
  const sign: -1 | 1 = trimmed.startsWith("-") ? -1 : 1;
  const unsigned = sign === -1 ? trimmed.slice(1) : trimmed;
  const [rawInt = "0", rawFrac = ""] = unsigned.split(".");
  const intPart = rawInt.replace(/^0+(?=\d)/, "") || "0";
  return { sign, intPart, fracPart: rawFrac };
}

export function parseDecimalToScaled(value: string, decimals = 8): bigint | null {
  const parts = splitDecimal(value);
  if (!parts) return null;
  const paddedFrac = (parts.fracPart + "0".repeat(decimals)).slice(0, decimals);
  const combined = `${parts.intPart}${paddedFrac}`;
  const magnitude = BigInt(combined);
  return parts.sign === -1 ? -magnitude : magnitude;
}

export function formatScaledToDecimal(
  scaled: bigint,
  decimals = 8,
  displayDecimals = decimals,
  withCommas = false
): string {
  const negative = scaled < 0n;
  const abs = negative ? -scaled : scaled;
  const divisor = 10n ** BigInt(decimals);
  const intBig = abs / divisor;
  const fracBig = abs % divisor;

  let intStr = intBig.toString();
  if (withCommas) {
    intStr = intStr.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  }

  if (displayDecimals <= 0) {
    return `${negative ? "-" : ""}${intStr}`;
  }

  const fullFrac = fracBig.toString().padStart(decimals, "0");
  const slicedFrac =
    displayDecimals <= decimals
      ? fullFrac.slice(0, displayDecimals)
      : fullFrac.padEnd(displayDecimals, "0");

  return `${negative ? "-" : ""}${intStr}.${slicedFrac}`;
}

export function formatPrice(value: string, displayDecimals = 2, withCommas = true): string {
  const scaled = parseDecimalToScaled(value, 8);
  if (scaled === null) return "0." + "0".repeat(displayDecimals);
  return formatScaledToDecimal(scaled, 8, displayDecimals, withCommas);
}

export function formatQuantity(value: string, displayDecimals = 4, withCommas = false): string {
  const scaled = parseDecimalToScaled(value, 8);
  if (scaled === null) return "0." + "0".repeat(displayDecimals);
  return formatScaledToDecimal(scaled, 8, displayDecimals, withCommas);
}

export function formatUnitsString(
  rawAmount: string,
  tokenDecimals: number,
  displayDecimals = 4,
  withCommas = true
): string {
  if (!/^-?\d+$/.test(rawAmount.trim())) {
    return "0." + "0".repeat(displayDecimals);
  }
  return formatScaledToDecimal(BigInt(rawAmount.trim()), tokenDecimals, displayDecimals, withCommas);
}

export function addDecimalStrings(a: string, b: string, scale = 8): string {
  const sa = parseDecimalToScaled(a, scale) ?? 0n;
  const sb = parseDecimalToScaled(b, scale) ?? 0n;
  return formatScaledToDecimal(sa + sb, scale, scale, false);
}

export function subDecimalStrings(a: string, b: string, scale = 8): string {
  const sa = parseDecimalToScaled(a, scale) ?? 0n;
  const sb = parseDecimalToScaled(b, scale) ?? 0n;
  return formatScaledToDecimal(sa - sb, scale, scale, false);
}

export function mulDecimalStrings(a: string, b: string, scale = 8): string {
  const sa = parseDecimalToScaled(a, scale) ?? 0n;
  const sb = parseDecimalToScaled(b, scale) ?? 0n;
  const product = (sa * sb) / 10n ** BigInt(scale);
  return formatScaledToDecimal(product, scale, scale, false);
}

export function compareDecimalStrings(a: string, b: string, scale = 8): -1 | 0 | 1 {
  const sa = parseDecimalToScaled(a, scale) ?? 0n;
  const sb = parseDecimalToScaled(b, scale) ?? 0n;
  if (sa < sb) return -1;
  if (sa > sb) return 1;
  return 0;
}

export function isValidPositiveDecimal(value: string, maxDecimals = 8): boolean {
  const trimmed = value.trim();
  if (!trimmed || trimmed.startsWith("-")) return false;
  if (!DECIMAL_RE.test(trimmed)) return false;
  const parts = trimmed.split(".");
  if (parts[1] && parts[1].length > maxDecimals) return false;
  const scaled = parseDecimalToScaled(trimmed, maxDecimals);
  return scaled !== null && scaled > 0n;
}

export function formatBps(bps: number): string {
  const sign = bps > 0 ? "+" : bps < 0 ? "-" : "";
  const absInt = Math.abs(Math.round(bps));
  const whole = Math.floor(absInt / 100);
  const rem = (absInt % 100).toString().padStart(2, "0");
  return `${sign}${whole}.${rem}%`;
}

export function formatLatencyUs(value: number, unit: "ns" | "us" = "us"): string {
  const us = unit === "ns" ? value / 1000 : value;
  return `${us.toFixed(2)} µs`;
}

export function formatTimestamp(input: string | number): string {
  const date = typeof input === "number" ? new Date(input) : new Date(input);
  if (Number.isNaN(date.getTime())) return "--:--:--";
  const hh = date.getUTCHours().toString().padStart(2, "0");
  const mm = date.getUTCMinutes().toString().padStart(2, "0");
  const ss = date.getUTCSeconds().toString().padStart(2, "0");
  const ms = date.getUTCMilliseconds().toString().padStart(3, "0");
  return `${hh}:${mm}:${ss}.${ms}`;
}

export function truncateHash(hash: string, prefixLen = 8, suffixLen = 6): string {
  if (!hash) return "0x0000...0000";
  if (hash.length <= prefixLen + suffixLen + 3) return hash;
  return `${hash.slice(0, prefixLen)}...${hash.slice(-suffixLen)}`;
}
