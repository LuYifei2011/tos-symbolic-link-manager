import { test } from 'node:test'
import assert from 'node:assert/strict'
import { deleteLinks } from '../src/link-actions.ts'

const items = [{ path: '/share/一', fingerprint: 'first' }, { path: '/share/二', fingerprint: 'second' }, { path: '/share/三', fingerprint: 'third' }]
test('deletes confirmed identities sequentially and reports completed progress', async () => {
 const seen: unknown[] = [], progress: number[] = []
 let active = 0
 const result = await deleteLinks(items, async item => {
  assert.equal(active++, 0)
  await Promise.resolve()
  seen.push(item); active--
 }, n => progress.push(n))
 assert.deepEqual(seen, items)
 assert.deepEqual(progress, [1, 2, 3])
 assert.deepEqual(result, { completed: 3, failed: null, error: null })
})
test('stops after a failed or uncertain deletion without retrying or touching the rest', async () => {
 for (const code of ['LINK_CHANGED', 'REQUEST_TIMEOUT', 'RECOVERY_REQUIRED', 'TOS_WRITE_DENIED']) {
  const failure = new Error(code), seen: string[] = []
  const result = await deleteLinks(items, async item => {
   seen.push(item.path)
   if (item.path === items[1]!.path) throw failure
  })
  assert.deepEqual(seen, [items[0]!.path, items[1]!.path])
  assert.equal(result.completed, 1)
  assert.deepEqual(result.failed, items[1])
  assert.equal(result.error, failure)
 }
})
test('selection changes cannot alter the confirmed batch snapshot', async () => {
 const selection = items.map(item => ({ ...item })), seen: unknown[] = []
 await deleteLinks(selection, async item => {
  seen.push(item)
  selection[1]!.path = '/share/unconfirmed'
  selection[1]!.fingerprint = 'changed'
 })
 assert.deepEqual(seen, items)
})
test('empty selection performs no requests', async () => {
 const result = await deleteLinks([], async () => { assert.fail('unexpected deletion') })
 assert.equal(result.completed, 0)
})
