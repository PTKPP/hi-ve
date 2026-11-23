import { test } from "node:test";
import assert from "node:assert/strict";
import { formatMoney, lineTotal, minorUnits } from "../src/money.mjs";
import { cashPayload, failureReason, matchesACK } from "../src/sync.mjs";

test("money uses exact minor units and rejects invisible rounding", () => {
  assert.equal(lineTotal("3", "0.04", "0.02"), 10n);
  assert.equal(lineTotal("0.25", "10000000000000000.12"), 250000000000000003n);
  assert.equal(formatMoney(minorUnits("1234.50")), "Rp 1.234,50");
  assert.throws(() => lineTotal("1", "0.001"), /sah/);
  assert.throws(() => lineTotal("1", "1", "2"), /sah/);
  assert.throws(() => minorUnits("0.001"));
});

test("queue scope is verified and only checkout fields are copied", () => {
  const record = { id: "doc", outlet: "outlet-a", operator: "operator-a" };
  const ack = { id: record.id, outlet_id: record.outlet, operator_id: record.operator,
    payment_state: "cash_received", posted_at: "2026-01-01T00:00:00Z" };
  assert.equal(matchesACK(record, ack), true);
  for (const change of [{ id: "different" }, { outlet_id: "different" }, { operator_id: "different" },
    { posted_at: "invalid" }, { payment_state: "pending" }]) {
    assert.equal(matchesACK(record, { ...ack, ...change }), false);
  }
  const body = cashPayload([{ sku_id: "sku", quantity: "1", unit: "pcs", price_id: "price", discount: "0",
    hpp: "sensitive", token: "secret" }], "20");
  assert.deepEqual(body, { method: "cash", tendered: "20",
    items: [{ sku_id: "sku", quantity: "1", unit: "pcs", price_id: "price", discount: "0" }] });
  assert.match(failureReason({ code: "INSUFFICIENT_STOCK" }), /Stok tidak cukup/);
});
