import { describe, expect, it } from 'vitest'
import { niceCeil, stepActive } from './chart-scale'

describe('niceCeil', () => {
  it('rounds up to 1, 2, 5 or 10 times a power of ten', () => {
    expect([0, 0.3, 1, 1.5, 2, 3, 7, 10, 11, 4800].map(niceCeil)).toEqual([
      1, 0.5, 1, 2, 2, 5, 10, 10, 20, 5000
    ])
  })
})

describe('stepActive', () => {
  it('starts from the end the arrow points to and stays in range', () => {
    expect(stepActive(-1, 5, -1)).toBe(4)
    expect(stepActive(-1, 5, 1)).toBe(0)
    expect(stepActive(4, 5, 1)).toBe(4)
    expect(stepActive(0, 5, -1)).toBe(0)
    expect(stepActive(2, 5, 1)).toBe(3)
  })
})
