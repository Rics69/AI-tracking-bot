/**
 * Parses an amount typed by hand: "1 200", "99,9", "1500.50". Returns the
 * number, or a message for the field when it is not a valid amount.
 */
export function parseAmount(input: string, { allowNegative = false } = {}): number | { error: string } {
  const normalized = input.replace(/\s/g, "").replace(",", ".").replace("−", "-");
  if (!normalized) return { error: "Введите сумму" };
  const pattern = allowNegative ? /^-?\d+(\.\d{1,2})?$/ : /^\d+(\.\d{1,2})?$/;
  const amount = Number(normalized);
  if (!pattern.test(normalized) || amount === 0 || (!allowNegative && amount < 0)) {
    return { error: "Положительное число, до 2 знаков после запятой" };
  }
  return amount;
}

/** Keeps only what an amount can contain while typing. */
export function amountInput(value: string): string {
  return value.replace(/[^\d\s,.]/g, "");
}
