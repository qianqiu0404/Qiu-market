import { describe, expect, it } from 'vitest'
import {
  parsePendingTradingWrite,
  pendingTradingWriteResolvedByOrders,
} from './pending-write'

describe('pending trading write', () => {
  it('restores a complete unknown write and rejects malformed state', () => {
    expect(parsePendingTradingWrite(JSON.stringify({
      operation: 'fund',
      account_id: 'github:qianqiu0404',
      request_id: 'fund-stable-id',
      state: 'unknown',
      created_at: 123,
      payload: { asset: 'USDT', amount: '100' },
    }))).toMatchObject({
      operation: 'fund',
      account_id: 'github:qianqiu0404',
      request_id: 'fund-stable-id',
      state: 'unknown',
    })
    expect(parsePendingTradingWrite('{"operation":"cancel"}')).toBeNull()
    expect(parsePendingTradingWrite('not-json')).toBeNull()
  })

  it('uses authoritative order facts to reconcile submit and cancel', () => {
    const orders = [{
      id: 'order-1',
      client_order_id: 'submit-stable-id',
      status: 'canceled',
    }]
    expect(pendingTradingWriteResolvedByOrders({
      operation: 'submit',
      account_id: 'github:qianqiu0404',
      request_id: 'submit-stable-id',
      state: 'unknown',
      created_at: 123,
      payload: {},
    }, orders)).toBe(true)
    expect(pendingTradingWriteResolvedByOrders({
      operation: 'cancel',
      account_id: 'github:qianqiu0404',
      request_id: 'cancel-stable-id',
      order_id: 'order-1',
      state: 'unknown',
      created_at: 123,
      payload: {},
    }, orders)).toBe(true)
  })

  it('never infers virtual funding from a balance snapshot', () => {
    expect(pendingTradingWriteResolvedByOrders({
      operation: 'fund',
      account_id: 'github:qianqiu0404',
      request_id: 'fund-stable-id',
      state: 'unknown',
      created_at: 123,
      payload: { asset: 'USDT', amount: '100' },
    }, [])).toBe(false)
  })
})
