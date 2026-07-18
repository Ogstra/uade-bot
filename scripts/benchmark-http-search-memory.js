import assert from 'node:assert/strict';

assert.equal(process.versions.node.split('.')[0], '24', 'benchmark requires Node 24');
assert.equal(typeof global.gc, 'function', 'benchmark requires --expose-gc');

assert.fail('RSS benchmark scenarios are not implemented yet');
