import assert from 'node:assert/strict';
import test from 'node:test';
import { formatCaseAmount, paymentMoney } from '../src/utils/paymentCurrency.ts';

test('paymentMoney formats USD, GBP, and zero-decimal JPY correctly', () => {
  assert.equal(paymentMoney('999', 'USD'), '9.99 USD');
  assert.equal(paymentMoney('1250', 'GBP'), '12.50 GBP');
  assert.equal(paymentMoney('1500', 'JPY'), '1,500 JPY');
  assert.equal(paymentMoney('-1500', 'JPY'), '−1,500 JPY');
  assert.equal(paymentMoney(null, 'USD'), '—');
});

test('formatCaseAmount handles GBP, JPY, USD, and legacy unknown dispute records', () => {
  assert.equal(
    formatCaseAmount({ kind: 'dispute', amount_minor: null, original_currency: 'GBP', original_amount_minor: '1250' }),
    '12.50 GBP',
  );
  assert.equal(
    formatCaseAmount({ kind: 'dispute', amount_minor: null, original_currency: 'JPY', original_amount_minor: '1500' }),
    '1,500 JPY',
  );
  assert.equal(
    formatCaseAmount({ kind: 'dispute', amount_minor: '999', original_currency: 'USD', original_amount_minor: '999' }),
    '9.99 USD',
  );
  assert.equal(
    formatCaseAmount({ kind: 'dispute', amount_minor: null, original_currency: null, original_amount_minor: null }),
    '未知',
  );
  assert.equal(
    formatCaseAmount({ kind: 'risk_signal', amount_minor: null, original_currency: null, original_amount_minor: null }),
    '—',
  );
});
