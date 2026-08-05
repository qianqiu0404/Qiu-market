import { describe, expect, it } from 'vitest'
import {
  parsePendingTradingWrite,
  pendingTradingWriteResolvedByOrders,
  updatePendingTradingWriteState,
} from './pending-write'

describe('pending trading write', () => {
  it('restores a complete unknown write and rejects malformed state', () => {
    expect(parsePendingTradingWrite(JSON.stringify({
      operation_id: 'operation-1',
      operation: 'fund',
      account_id: 'github:qianqiu0404',
      request_id: 'fund-stable-id',
      state: 'submitted',
      created_at: 123,
      updated_at: 124,
      payload: { asset: 'USDT', amount: '100' },
    }))).toMatchObject({
      operation_id: 'operation-1',
      operation: 'fund',
      account_id: 'github:qianqiu0404',
      request_id: 'fund-stable-id',
      state: 'submitted',
      updated_at: 124,
    })
    expect(parsePendingTradingWrite('{"operation":"cancel"}')).toBeNull()
    expect(parsePendingTradingWrite('not-json')).toBeNull()
  })

  it('migrates a v1 unknown write without losing the original request ID', () => {
    expect(parsePendingTradingWrite(JSON.stringify({
      operation: 'submit',
      account_id: 'github:qianqiu0404',
      request_id: 'submit-stable-id',
      state: 'unknown',
      created_at: 123,
      payload: { client_order_id: 'submit-stable-id' },
    }))).toMatchObject({
      operation_id: 'legacy-submit-stable-id',
      request_id: 'submit-stable-id',
      state: 'unknown',
      updated_at: 123,
    })
  })

  it('updates reconcile state without changing operation or request identity', () => {
    const pending = parsePendingTradingWrite(JSON.stringify({
      operation_id: 'operation-1',
      operation: 'cancel',
      account_id: 'github:qianqiu0404',
      request_id: 'cancel-stable-id',
      order_id: 'order-1',
      state: 'unknown',
      created_at: 123,
      updated_at: 123,
      payload: {},
    }))
    expect(pending).not.toBeNull()

    expect(updatePendingTradingWriteState(
      pending!,
      'reconciling',
      456,
    )).toMatchObject({
      operation_id: 'operation-1',
      request_id: 'cancel-stable-id',
      state: 'reconciling',
      updated_at: 456,
    })
  })

  it('uses authoritative order facts to reconcile submit and cancel', () => {
    const orders = [{
      id: 'order-1',
      client_order_id: 'submit-stable-id',
      status: 'canceled',
    }]
    expect(pendingTradingWriteResolvedByOrders({
      operation_id: 'operation-submit',
      operation: 'submit',
      account_id: 'github:qianqiu0404',
      request_id: 'submit-stable-id',
      state: 'unknown',
      created_at: 123,
      updated_at: 123,
      payload: {},
    }, orders)).toBe(true)
    expect(pendingTradingWriteResolvedByOrders({
      operation_id: 'operation-cancel',
      operation: 'cancel',
      account_id: 'github:qianqiu0404',
      request_id: 'cancel-stable-id',
      order_id: 'order-1',
      state: 'unknown',
      created_at: 123,
      updated_at: 123,
      payload: {},
    }, orders)).toBe(true)
  })

  it('never infers virtual funding from a balance snapshot', () => {
    expect(pendingTradingWriteResolvedByOrders({
      operation_id: 'operation-fund',
      operation: 'fund',
      account_id: 'github:qianqiu0404',
      request_id: 'fund-stable-id',
      state: 'unknown',
      created_at: 123,
      updated_at: 123,
      payload: { asset: 'USDT', amount: '100' },
    }, [])).toBe(false)
  })
})
