import assert from 'node:assert/strict'
import test from 'node:test'

import { matrixEventMessageId } from '../../plugins/teamharness/adapters/deepseek-harness/message-id.js'


test('a retry gets a new stable DSH message id', () => {
  const firstAttempt = matrixEventMessageId('$matrix-event', 1)
  const secondAttempt = matrixEventMessageId('$matrix-event', 2)

  assert.equal(matrixEventMessageId('$matrix-event', 1), firstAttempt)
  assert.equal(matrixEventMessageId('$matrix-event', 2), secondAttempt)
  assert.notEqual(secondAttempt, firstAttempt)
})
