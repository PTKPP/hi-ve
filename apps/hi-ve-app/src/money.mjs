const decimal = /^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$/;

function parts(value) {
  if (typeof value !== "string" || value.length > 96 || !decimal.test(value)) throw new Error("Jumlah atau nominal tidak sah");
  const [whole, fraction = ""] = value.split(".");
  return [BigInt(whole + fraction), fraction.length];
}

export function minorUnits(value) {
  const [n, scale] = parts(value);
  if (scale <= 2) return n * 10n ** BigInt(2 - scale);
  const divisor = 10n ** BigInt(scale - 2);
  if (n % divisor) throw new Error("Nominal harus tepat pada satuan uang terkecil");
  return n / divisor;
}

export function lineTotal(quantity, price, discount = "0") {
  const [units, unitsScale] = parts(quantity);
  const [amount, amountScale] = parts(price);
  const [deduction, discountScale] = parts(discount || "0");
  const scale = Math.max(unitsScale + amountScale, discountScale, 2);
  const adjusted = units * amount * 10n ** BigInt(scale - unitsScale - amountScale)
    - deduction * 10n ** BigInt(scale - discountScale);
  const divisor = 10n ** BigInt(scale - 2);
  if (units === 0n || adjusted < 0n || adjusted % divisor) throw new Error("Jumlah, diskon, atau satuan uang tidak sah");
  return adjusted / divisor;
}

export function formatMoney(value) {
  const major = (value / 100n).toLocaleString("id-ID");
  return `Rp ${major},${(value % 100n).toString().padStart(2, "0")}`;
}
