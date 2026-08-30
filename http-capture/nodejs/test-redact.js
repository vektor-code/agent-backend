'use strict';

const assert = require('assert');
const { redact } = require('./crnet-http-capture.js');

assert.ok(!redact('{"password":"hunter2","user":"ada"}').includes('hunter2'));
assert.ok(redact('{"password":"hunter2","user":"ada"}').includes('[redacted]'));
assert.strictEqual(redact('user=ada&token=abc&ok=1'), 'user=ada&token=[redacted]&ok=1');
assert.ok(redact('x'.repeat(5000)).includes('truncated'));
console.log('ok');
